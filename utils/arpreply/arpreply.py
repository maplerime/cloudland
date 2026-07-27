#!/usr/bin/env python3
"""
arpreply — intercept arphole's ARP probes, answer for locally-owned VM IPs,
and drop the probe so it never reaches the VMs.

Counterpart to arphole. arphole floods who-has probes for contended IPs using
a fixed sender IP (192.0.2.100, RFC 5737 TEST-NET-1) and reclaims any IP that
stays silent past its probe timeout. This tool defends the VMs living on THIS
compute node.

Unlike a passive sniffer, arpreply sits INLINE via an nftables `queue` rule in
the bridge datapath. A single rule matches only arphole's who-has
(`arp saddr ip 192.0.2.100`) and hands each such frame to this process:

  * If the queried IP belongs to a local VM, we emit an is-at with the VM's
    real MAC (from the sgas-* anti-spoofing ipset), UNICAST back to arphole,
    then verdict DROP — so the VM never sees the probe and never double-answers.
  * Otherwise verdict ACCEPT — let it flood (another node may own it, or a VM
    can answer for itself).

The nft rule carries `queue ... bypass`, so if this process is down or the
queue is full the probe is ACCEPTed (flooded) and the VM answers itself — a
fail-safe. Because only arphole's probes ever match the rule, VM data traffic
never reaches userspace: zero data-plane cost.

Ownership comes from the `sgas-<vnic>` ipsets (type hash:ip,mac) the
security-group machinery already maintains; each vnic is bridged onto
`br<vlan>`, so the VLAN an IP lives on is that bridge's numeric suffix. The
reply is sent untagged on `br<vlan>`; `v-<vlan>` re-tags it toward the trunk.
The ownership map is rebuilt from ipset every REFRESH_INTERVAL seconds.
"""

import argparse
import logging
import os
import re
import select
import signal
import socket
import struct
import subprocess
import sys
import threading
import time

logger = logging.getLogger("arpreply")

DESCRIPTION = (
    "Inline (nftables NFQUEUE) responder: intercept arphole's who-has probes, "
    "answer for locally-owned VM IPs from the sgas-* ipsets, and drop the probe "
    "so VMs never see it. Fail-safe via `queue ... bypass`."
)

# Only requests whose ARP psrc equals this are queued/answered. Must match
# arphole's probe source (RFC 5737 TEST-NET-1, reserved for documentation so it
# can never be a real host). Overridable via --probe-src / ARPREPLY_PROBE_SRC.
_PROBE_SRC_IP = "192.0.2.100"

# How often the ownership map is rebuilt from ipset.
_REFRESH_INTERVAL = 15.0

# NFQUEUE number the nft rule dispatches to (must be free on the host).
_QUEUE_NUM = 40

# Dedicated nft bridge table we own (decoupled from `bridge cloudland`).
_NFT_TABLE = "arpreply"

# Prefix of the per-vnic anti-spoofing ipsets created by create_sg_chain.sh.
_IPSET_PREFIX = "sgas-"

# ipset member line, e.g. "192.168.1.5,52:54:00:aa:bb:cc".
_MEMBER_RE = re.compile(r"^(\d{1,3}(?:\.\d{1,3}){3}),\s*([0-9A-Fa-f:]{17})")

# Fixed ARP-reply prefix: htype=Ethernet(1), ptype=IPv4(0x0800), hlen=6,
# plen=4, oper=reply(2). Everything after this is address bytes.
_ARP_REPLY_FIXED = struct.pack("!HHBBH", 1, 0x0800, 6, 4, 2)

# ARP who-has opcode, as the 2-byte oper field.
_ARP_OP_REQUEST = b"\x00\x01"


def _run(cmd: list[str]) -> str:
    """Run a command, return stdout (empty string on failure)."""
    try:
        out = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            check=False,
            text=True,
        )
        return out.stdout or ""
    except FileNotFoundError:
        logger.error("command not found: %s", cmd[0])
        return ""


def _nft_load(script: str) -> bool:
    """Apply an nft ruleset script atomically via `nft -f -`. Returns success."""
    try:
        r = subprocess.run(
            ["nft", "-f", "-"],
            input=script,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
    except FileNotFoundError:
        logger.error("nft not found; cannot manage the queue rule")
        return False
    if r.returncode != 0:
        logger.error("nft failed (rc=%d): %s", r.returncode, (r.stderr or "").strip())
        return False
    return True


def _mac_to_bytes(mac: str) -> bytes:
    return bytes.fromhex(mac.replace(":", "").replace("-", ""))


def _ip_to_bytes(ip: str) -> bytes:
    return bytes(int(o) for o in ip.split("."))


def _pack_reply(
    src_mac_b: bytes,
    target_ip_b: bytes,
    vlan_id: int,
    dst_mac_b: bytes,
    req_ip_b: bytes,
) -> bytes:
    """Hand-build an ARP is-at Ethernet frame as raw bytes.

    'target_ip is at src_mac', addressed to the requester (dst_mac / req_ip),
    carrying an 802.1Q tag when vlan_id != 0. Verified byte-identical to the
    equivalent scapy Ether()/Dot1Q()/ARP() serialization; doing it by hand
    keeps the hot path at ~1us/frame.
    """
    if vlan_id:
        l2 = (
            dst_mac_b + src_mac_b
            + b"\x81\x00" + struct.pack("!H", vlan_id & 0x0FFF)
            + b"\x08\x06"
        )
    else:
        l2 = dst_mac_b + src_mac_b + b"\x08\x06"
    # ARP: <fixed> sha=src_mac spa=target_ip tha=req_mac tpa=req_ip
    return l2 + _ARP_REPLY_FIXED + src_mac_b + target_ip_b + dst_mac_b + req_ip_b


def _locate_arp(payload: bytes) -> int | None:
    """Find where the ARP header starts in a queued frame.

    An nft bridge-family `queue` was observed to hand us the BARE ARP header
    (no Ethernet header, offset 0). We still detect the Ethernet-II (offset 14)
    and 802.1Q (offset 18) framings defensively, so the parse never depends on
    the queue's payload convention. Returns the ARP-header offset, or None if
    this isn't a parseable ARP frame.
    """
    n = len(payload)
    if n >= 42 and payload[12:14] == b"\x08\x06":            # Ethernet II + ARP
        return 14
    if n >= 46 and payload[12:14] == b"\x81\x00" and payload[16:18] == b"\x08\x06":
        return 18                                            # 802.1Q + ARP
    if n >= 28 and payload[0:2] == b"\x00\x01":              # bare ARP (htype=1)
        return 0
    return None


def _vnic_vlan(vnic: str) -> int | None:
    """Resolve the VLAN a vnic lives on via its bridge master (br<vlan>).

    Returns the integer VLAN, or None if the vnic/bridge can't be resolved
    or the bridge isn't named br<digits> (e.g. it's already gone).
    """
    try:
        master = os.path.basename(os.readlink("/sys/class/net/%s/master" % vnic))
    except OSError:
        return None
    if master.startswith("br") and master[2:].isdigit():
        return int(master[2:])
    return None


# Map value: (mac_str, mac_bytes, ip_bytes). Byte forms precomputed per refresh.
OwnerEntry = tuple[str, bytes, bytes]
# by-IP projection value: (vlan, mac_str, mac_bytes, ip_bytes).
IpEntry = tuple[int, str, bytes, bytes]


def build_owner_map() -> dict[tuple[int, str], OwnerEntry]:
    """Build a (vlan, ip) -> (mac, mac_bytes, ip_bytes) map from all sgas-*
    ipsets on this host.

    Uses a single `ipset save` (one fork) rather than `ipset list` per set.
    Save emits lines like `add sgas-<vnic> <ip>,<mac>`. An IP whose vnic VLAN
    can't be resolved is skipped: without the VLAN we don't know which bridge
    to answer on.
    """
    owned: dict[tuple[int, str], OwnerEntry] = {}
    vlan_cache: dict[str, int | None] = {}
    for line in _run(["ipset", "save"]).splitlines():
        parts = line.split()
        if len(parts) < 3 or parts[0] != "add":
            continue
        name = parts[1]
        if not name.startswith(_IPSET_PREFIX):
            continue
        m = _MEMBER_RE.match(parts[2])
        if not m:
            continue
        vnic = name[len(_IPSET_PREFIX):]
        if vnic not in vlan_cache:
            vlan_cache[vnic] = _vnic_vlan(vnic)
        vlan = vlan_cache[vnic]
        if vlan is None:
            continue
        ip, mac = m.group(1), m.group(2).lower()
        owned[(vlan, ip)] = (mac, _mac_to_bytes(mac), _ip_to_bytes(ip))
    return owned


class ArpReply:
    def __init__(
        self,
        probe_src: str = _PROBE_SRC_IP,
        queue_num: int = _QUEUE_NUM,
        refresh_interval: float = _REFRESH_INTERVAL,
    ):
        self.probe_src = probe_src
        self.queue_num = queue_num
        self.refresh_interval = refresh_interval
        # Empty probe_src => answer who-has from ANY source (general proxy-ARP
        # for local VM IPs); otherwise only from this one sender IP (arphole's
        # probe source). match_all drives both the nft rule and the spa check.
        self.match_all = not probe_src
        self._probe_src_b = None if self.match_all else _ip_to_bytes(probe_src)
        # ip -> IpEntry, rebuilt periodically. Guarded by _lock. Lookups are by
        # IP because the frame is untagged on the bridge; the stored VLAN says
        # which br<vlan> to answer on.
        self._owned_by_ip: dict[str, IpEntry] = {}
        self._lock = threading.Lock()
        # br<vlan> -> raw AF_PACKET send socket. Reused; reopened on error.
        self._sockets: dict[str, socket.socket] = {}
        self._sock_lock = threading.Lock()
        self._stop = threading.Event()
        self._refresher: threading.Thread | None = None

    # ------------------------------------------------------------ ownership

    def _refresh(self) -> None:
        owned = build_owner_map()
        by_ip = {
            ip: (vlan, mac, mac_b, ip_b)
            for (vlan, ip), (mac, mac_b, ip_b) in owned.items()
        }
        with self._lock:
            self._owned_by_ip = by_ip
        logger.info("owner map refreshed: %d owned IP(s)", len(by_ip))

    def _refresh_loop(self) -> None:
        while not self._stop.is_set():
            try:
                self._refresh()
            except Exception:
                logger.exception("owner map refresh failed")
            self._stop.wait(self.refresh_interval)

    def _lookup_ip(self, ip: str) -> IpEntry | None:
        with self._lock:
            return self._owned_by_ip.get(ip)

    # ---------------------------------------------------------------- send

    def _get_send_socket(self, iface: str) -> socket.socket:
        with self._sock_lock:
            sock = self._sockets.get(iface)
            if sock is not None:
                return sock
            sock = socket.socket(
                socket.AF_PACKET, socket.SOCK_RAW, socket.htons(0x0806)
            )
            sock.bind((iface, 0x0806))
            self._sockets[iface] = sock
            return sock

    def _drop_socket(self, iface: str) -> None:
        with self._sock_lock:
            sock = self._sockets.pop(iface, None)
        if sock is not None:
            try:
                sock.close()
            except OSError:
                pass

    def _send_frame(self, iface: str, frame: bytes, target_ip: str) -> bool:
        try:
            self._get_send_socket(iface).send(frame)
            return True
        except OSError as e:
            # Bridge likely gone (VM/vnic removed); reopen next time.
            self._drop_socket(iface)
            logger.warning("[%s] failed to send reply for %s: %s", iface, target_ip, e)
            return False

    # -------------------------------------------------------------- nftables

    def _install_nft(self) -> bool:
        """Idempotently install our queue rule. flush+re-add avoids stacking."""
        t = _NFT_TABLE
        # Empty probe_src drops the saddr match, so ALL ARP who-has is queued.
        saddr = "" if self.match_all else "arp saddr ip %s " % self.probe_src
        script = (
            "add table bridge {t}\n"
            "add chain bridge {t} forward "
            "{{ type filter hook forward priority -10 ; policy accept ; }}\n"
            "flush chain bridge {t} forward\n"
            "add rule bridge {t} forward ether type 0x0806 arp operation request "
            "{saddr}queue num {q} bypass\n"
        ).format(t=t, saddr=saddr, q=self.queue_num)
        ok = _nft_load(script)
        if ok:
            logger.info(
                "installed nft queue rule: bridge %s forward -> queue %d "
                "(arp request %s)",
                t, self.queue_num,
                "any source" if self.match_all else "saddr %s" % self.probe_src,
            )
        return ok

    def _remove_nft(self) -> None:
        _nft_load("delete table bridge %s\n" % _NFT_TABLE)

    # ---------------------------------------------------------------- queue

    def _on_packet(self, pkt) -> None:
        """NFQUEUE callback: answer + drop if owned, else accept. Never let an
        exception escape without issuing a verdict (that would stall the queue).
        """
        try:
            payload = pkt.get_payload()
            off = _locate_arp(payload)
            if off is None:
                pkt.accept()
                return
            arp = payload[off:off + 28]
            if len(arp) < 28 or arp[6:8] != _ARP_OP_REQUEST:
                pkt.accept()
                return
            sha = arp[8:14]       # requester MAC
            spa = arp[14:18]      # requester IP (== probe source unless match_all)
            tpa = arp[24:28]      # queried target IP (the VM IP)
            if not self.match_all and spa != self._probe_src_b:
                pkt.accept()
                return
            target_ip = "%d.%d.%d.%d" % (tpa[0], tpa[1], tpa[2], tpa[3])
            ent = self._lookup_ip(target_ip)
            if ent is None:
                pkt.accept()
                return
            vlan, mac, mac_b, _ip_b = ent
            frame = _pack_reply(mac_b, tpa, 0, sha, spa)
            if self._send_frame("br%d" % vlan, frame, target_ip):
                pkt.drop()
                if logger.isEnabledFor(logging.INFO):
                    logger.info(
                        "answered+dropped who-has %s -> %s on br%d (asked by %s)",
                        target_ip, mac, vlan,
                        ":".join("%02x" % b for b in sha),
                    )
            else:
                # Couldn't reply — let it through so a VM can answer.
                pkt.accept()
        except Exception:
            logger.exception("error handling queued packet")
            try:
                pkt.accept()
            except Exception:
                pass

    def run(self) -> None:
        try:
            from netfilterqueue import NetfilterQueue
        except ImportError as e:
            raise RuntimeError(
                "python NetfilterQueue is required (pip install NetfilterQueue; "
                "needs libnetfilter-queue)"
            ) from e

        logger.info(
            "arpreply starting (probe-src=%s queue=%d refresh=%.0fs)",
            self.probe_src, self.queue_num, self.refresh_interval,
        )
        # Prime ownership before we install the rule so the first probe is
        # answerable.
        self._refresh()
        if not self._install_nft():
            raise RuntimeError("failed to install nft queue rule")

        # Start the refresh loop once; run() may be re-entered by main()'s
        # crash-retry, and we don't want to pile up refresh threads.
        if self._refresher is None or not self._refresher.is_alive():
            self._refresher = threading.Thread(
                target=self._refresh_loop, name="refresh", daemon=True
            )
            self._refresher.start()

        nfq = NetfilterQueue()
        nfq.bind(self.queue_num, self._on_packet)
        # Drive the queue through a select loop so SIGTERM/SIGINT (which set
        # self._stop) break us out promptly for a clean nft teardown.
        qsock = socket.fromfd(nfq.get_fd(), socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            while not self._stop.is_set():
                try:
                    ready, _, _ = select.select([qsock], [], [], 1.0)
                except InterruptedError:
                    continue
                if ready:
                    nfq.run_socket(qsock)
        finally:
            try:
                nfq.unbind()
            except Exception:
                pass
            qsock.close()
            self._remove_nft()
            logger.info("arpreply stopped; nft rule removed")

    def shutdown(self) -> None:
        self._stop.set()


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=DESCRIPTION)
    p.add_argument(
        "--probe-src",
        default=os.environ.get("ARPREPLY_PROBE_SRC", _PROBE_SRC_IP),
        help="only intercept/answer requests whose ARP sender IP equals this; "
        "set EMPTY to answer who-has from ANY source (general proxy-ARP for "
        "local VM IPs) (env: ARPREPLY_PROBE_SRC, default: %s)" % _PROBE_SRC_IP,
    )
    p.add_argument(
        "--queue-num",
        type=int,
        default=int(os.environ.get("ARPREPLY_QUEUE_NUM", str(_QUEUE_NUM))),
        help="NFQUEUE number the nft rule dispatches to "
        "(env: ARPREPLY_QUEUE_NUM, default: %d)" % _QUEUE_NUM,
    )
    p.add_argument(
        "--refresh-interval",
        type=float,
        default=float(os.environ.get("ARPREPLY_REFRESH", str(_REFRESH_INTERVAL))),
        help="seconds between ipset ownership-map rebuilds "
        "(env: ARPREPLY_REFRESH, default: %.0f)" % _REFRESH_INTERVAL,
    )
    p.add_argument("--log-level", default=os.environ.get("ARPREPLY_LOG", "INFO"))
    return p.parse_args()


def main() -> int:
    args = parse_args()
    logging.basicConfig(
        level=args.log_level.upper(),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )

    responder = ArpReply(
        probe_src=args.probe_src,
        queue_num=args.queue_num,
        refresh_interval=args.refresh_interval,
    )

    def _stop(*_):
        logger.info("shutting down")
        responder.shutdown()

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)

    while not responder._stop.is_set():
        try:
            responder.run()
            return 0
        except RuntimeError as e:
            logger.error("%s", e)
            return 1
        except Exception:
            logger.exception("run loop crashed; retrying in 5s")
            responder._remove_nft()
            responder._stop.wait(5)
    return 0


if __name__ == "__main__":
    sys.exit(main())

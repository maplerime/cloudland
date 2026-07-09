#!/usr/bin/env python3
"""
arpreply — answer ARP probes for VM IPs this host actually owns.

Counterpart to arphole. arphole floods who-has probes for contended IPs using
a fixed sender IP (192.0.2.100, RFC 5737 TEST-NET-1) and reclaims any IP that
stays silent past probe_timeout. This tool defends the VMs living on THIS host:
when a who-has for one of our VM IPs arrives from arphole's probe source, we
answer with the VM's real MAC so arphole sees the IP as occupied and leaves it
alone.

Ownership comes straight from the anti-spoofing ipsets the security-group
machinery already maintains: `sgas-<vnic>` (type hash:ip,mac) holds every
(IP, MAC) pair a VM is allowed to source from. Each vnic is bridged onto
`br<vlan>`, so the VLAN an IP lives on is the numeric suffix of that bridge.

Only requests matching ALL of these get a reply:
  * ARP op=1 (who-has),
  * sender IP == the probe source (default 192.0.2.100),
  * target IP present in one of our sgas-* ipsets,
  * (unless --ignore-vlan) arriving on the VLAN that IP's vnic is bridged to.

The reply is shaped from the OWNED (ip, mac) pair and sent back on the request's
original VLAN tag, over the same interface it came in on. The ownership map is
rebuilt from ipset every REFRESH_INTERVAL seconds so VM churn is picked up.
"""

import argparse
import logging
import os
import re
import signal
import socket
import struct
import subprocess
import sys
import threading
import time

from scapy.all import (
    ARP,
    Dot1Q,
    conf,
    sniff,
)

logger = logging.getLogger("arpreply")

DESCRIPTION = (
    "Answer ARP who-has probes (from arphole's probe source) for VM IPs this "
    "host owns, using the IP/MAC recorded in the sgas-* anti-spoofing ipsets "
    "and replying on the request's original VLAN."
)

# Only requests whose ARP psrc equals this get a reply. Must match arphole's
# _PROBE_SRC_IP (RFC 5737 TEST-NET-1, reserved for documentation so it can
# never be a real host). Overridable via --probe-src / ARPREPLY_PROBE_SRC.
_PROBE_SRC_IP = "192.0.2.100"

# How often the (vlan, ip) -> mac ownership map is rebuilt from ipset.
_REFRESH_INTERVAL = 15.0

# Prefix of the per-vnic anti-spoofing ipsets created by create_sg_chain.sh.
_IPSET_PREFIX = "sgas-"

# ipset member line, e.g. "192.168.1.5,52:54:00:aa:bb:cc" (counters/comments,
# if ever enabled, follow after more whitespace and are ignored).
_MEMBER_RE = re.compile(
    r"^(\d{1,3}(?:\.\d{1,3}){3}),\s*([0-9A-Fa-f:]{17})"
)


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


def _ip_to_u32(ip: str) -> int:
    """Pack a dotted-quad IPv4 string into its 32-bit big-endian integer,
    for use as a numeric literal in a BPF field comparison."""
    parts = ip.split(".")
    if len(parts) != 4:
        raise ValueError("invalid IPv4 address: %r" % ip)
    n = 0
    for p in parts:
        v = int(p)
        if not 0 <= v <= 255:
            raise ValueError("invalid IPv4 address: %r" % ip)
        n = (n << 8) | v
    return n


# Fixed ARP-reply prefix: htype=Ethernet(1), ptype=IPv4(0x0800), hlen=6,
# plen=4, oper=reply(2). Everything after this is address bytes.
_ARP_REPLY_FIXED = struct.pack("!HHBBH", 1, 0x0800, 6, 4, 2)


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
    keeps the hot path at ~1us/frame instead of scapy's ~1ms, so a whole
    host's worth of IPs can be answered well inside arphole's probe timeout.
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


# Map value: (mac_str, mac_bytes, target_ip_bytes). The byte forms are
# precomputed once per refresh so the reply hot path never re-parses them.
OwnerEntry = tuple[str, bytes, bytes]


def build_owner_map() -> dict[tuple[int, str], OwnerEntry]:
    """Build a (vlan, ip) -> (mac, mac_bytes, ip_bytes) map from all sgas-*
    ipsets on this host.

    Uses a single `ipset save` (one fork) rather than `ipset list` per set,
    so a host with hundreds of vnics still rebuilds cheaply. Save emits
    lines like `add sgas-<vnic> <ip>,<mac>`. An IP whose vnic VLAN can't be
    resolved is skipped: without the VLAN we can't decide which segment to
    answer on.
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
        ifaces: list[str],
        probe_src: str = _PROBE_SRC_IP,
        ignore_vlan: bool = False,
        refresh_interval: float = _REFRESH_INTERVAL,
    ):
        self.ifaces = ifaces
        self.probe_src = probe_src
        self.ignore_vlan = ignore_vlan
        self.refresh_interval = refresh_interval
        # Requester IP is always the probe source (the BPF filter guarantees
        # it), so its wire bytes are constant — precompute once.
        self._probe_src_b = _ip_to_bytes(probe_src)
        # (vlan, ip) -> OwnerEntry, rebuilt periodically. Guarded by _lock.
        self._owned: dict[tuple[int, str], OwnerEntry] = {}
        # ip -> OwnerEntry projection used only when --ignore-vlan is set.
        self._owned_by_ip: dict[str, OwnerEntry] = {}
        self._lock = threading.Lock()
        # iface -> raw AF_PACKET send socket. A raw socket + a hand-packed
        # frame (~1us) replaces scapy's sendp (~1ms/frame), so a whole host's
        # worth of IPs answers well inside arphole's probe timeout.
        self._sockets: dict[str, socket.socket] = {}
        self._sock_lock = threading.Lock()

    # ------------------------------------------------------------ ownership

    def _refresh(self) -> None:
        owned = build_owner_map()
        by_ip = {ip: entry for (_vlan, ip), entry in owned.items()}
        with self._lock:
            self._owned = owned
            self._owned_by_ip = by_ip
        logger.info(
            "owner map refreshed: %d (vlan,ip) pair(s) across %d IP(s)",
            len(owned),
            len(by_ip),
        )

    def _refresh_loop(self) -> None:
        while True:
            try:
                self._refresh()
            except Exception:
                logger.exception("owner map refresh failed")
            time.sleep(self.refresh_interval)

    def _lookup(self, ip: str, vlan_id: int) -> OwnerEntry | None:
        with self._lock:
            if self.ignore_vlan:
                return self._owned_by_ip.get(ip)
            return self._owned.get((vlan_id, ip))

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

    def _reply(self, iface: str, entry: OwnerEntry, target_ip: str,
               vlan_id: int, req_mac: str) -> None:
        mac, mac_b, ip_b = entry
        frame = _pack_reply(
            mac_b, ip_b, vlan_id, _mac_to_bytes(req_mac), self._probe_src_b
        )
        try:
            self._get_send_socket(iface).send(frame)
        except OSError:
            # iface likely went down; drop the socket so it reopens next time.
            self._drop_socket(iface)
            logger.exception("[%s] failed to reply for %s", iface, target_ip)
            return
        if logger.isEnabledFor(logging.INFO):
            vlan_info = " vlan=%d" % vlan_id if vlan_id else ""
            logger.info(
                "[%s] answered who-has %s -> %s (asked by %s/%s%s)",
                iface, target_ip, mac, req_mac, self.probe_src, vlan_info,
            )

    # --------------------------------------------------------------- sniff

    def _on_request(self, iface: str, pkt) -> None:
        if ARP not in pkt:
            return
        arp = pkt[ARP]
        if arp.op != 1:
            return
        # Redundant safety net: the BPF filter already restricts to op=1 with
        # this sender IP, but re-check in case sniff ever falls back to an
        # unfiltered capture.
        if arp.psrc != self.probe_src:
            return
        target_ip = arp.pdst
        if not target_ip:
            return
        vlan_id = pkt[Dot1Q].vlan if Dot1Q in pkt else 0
        entry = self._lookup(target_ip, vlan_id)
        if entry is None:
            if logger.isEnabledFor(logging.DEBUG):
                logger.debug(
                    "[%s] not ours: %s vlan=%d (from %s)",
                    iface, target_ip, vlan_id, arp.psrc,
                )
            return
        self._reply(iface, entry, target_ip, vlan_id, arp.hwsrc)

    def _sniff_iface(self, iface: str) -> None:
        # Kernel-filter down to exactly the packets we might answer: ARP
        # who-has (op=1, arp[6:2]) whose ARP sender IP (spa, arp[14:4]) equals
        # our probe source. Matching the sender IP in the kernel — not in
        # Python — means an ARP request flood from other hosts never wakes
        # this process at all; only arphole's own probes get through, so the
        # pcap ring buffer can't overflow and drop the ones we care about.
        # The `vlan` keyword shifts every subsequent arp offset by the 4-byte
        # 802.1Q tag, so tagged probes match at the same logical fields.
        src = _ip_to_u32(self.probe_src)
        match = "arp[6:2] = 1 and arp[14:4] = %d" % src
        bpf = "(%s) or (vlan and %s)" % (match, match)
        logger.info("sniffing requests on %s (filter=%r)", iface, bpf)
        try:
            sniff(
                iface=iface,
                filter=bpf,
                prn=lambda pkt: self._on_request(iface, pkt),
                store=False,
            )
        except Exception:
            logger.exception("[%s] request sniff loop crashed", iface)

    def run(self) -> None:
        logger.info(
            "arpreply starting on %s (probe-src=%s ignore-vlan=%s refresh=%.0fs)",
            ", ".join(self.ifaces),
            self.probe_src,
            self.ignore_vlan,
            self.refresh_interval,
        )
        conf.verb = 0
        # Prime the map before we start answering.
        self._refresh()
        threads = []
        refresher = threading.Thread(
            target=self._refresh_loop, name="refresh", daemon=True
        )
        refresher.start()
        threads.append(refresher)
        for iface in self.ifaces:
            t = threading.Thread(
                target=self._sniff_iface,
                args=(iface,),
                daemon=True,
                name="sniff-%s" % iface,
            )
            t.start()
            threads.append(t)
        for t in threads:
            t.join()


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=DESCRIPTION)
    p.add_argument(
        "--iface",
        nargs="+",
        default=os.environ.get("ARPREPLY_IFACE", "").split(","),
        required=not os.environ.get("ARPREPLY_IFACE"),
        help="one or more interfaces to listen on, e.g. --iface ens5 ens6 "
        "(env: ARPREPLY_IFACE=ens5,ens6)",
    )
    p.add_argument(
        "--probe-src",
        default=os.environ.get("ARPREPLY_PROBE_SRC", _PROBE_SRC_IP),
        help="only answer requests whose ARP sender IP equals this "
        "(env: ARPREPLY_PROBE_SRC, default: %s)" % _PROBE_SRC_IP,
    )
    p.add_argument(
        "--ignore-vlan",
        action="store_true",
        default=os.environ.get("ARPREPLY_IGNORE_VLAN", "").lower()
        in ("1", "true", "yes"),
        help="match owned IPs regardless of VLAN (reply still uses the "
        "request's VLAN). Default: require the request VLAN to match the "
        "IP's vnic bridge (env: ARPREPLY_IGNORE_VLAN)",
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
    ifaces = [i.strip() for i in args.iface if i.strip()]
    if not ifaces:
        print("error: no interfaces specified", file=sys.stderr)
        return 1

    responder = ArpReply(
        ifaces=ifaces,
        probe_src=args.probe_src,
        ignore_vlan=args.ignore_vlan,
        refresh_interval=args.refresh_interval,
    )

    def _stop(*_):
        logger.info("shutting down")
        sys.exit(0)

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)

    while True:
        try:
            responder.run()
        except KeyboardInterrupt:
            return 0
        except Exception:
            logger.exception("run loop crashed; retrying in 5s")
            time.sleep(5)


if __name__ == "__main__":
    sys.exit(main())

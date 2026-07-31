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
  * Otherwise (ipset miss) verdict DROP too — the who-has is suppressed. This
    is only safe when the nft rule is gated (via --vlans / --iface) to the
    v-<vlan> uplinks so it sees trunk-INBOUND probes only; on an interface
    carrying VM-sourced who-has it would blackhole ARP for non-VM destinations
    (gateway, external, other-node VMs).

The nft rule carries `queue ... bypass`, so if this process is down or the
queue is full the probe is ACCEPTed (flooded) and the VM answers itself — a
fail-safe against the tool being absent (distinct from the per-packet DROP
verdict above). Because only matched probes reach the rule, VM data traffic
never enters userspace: zero data-plane cost.

Ownership (the full local-VM IP list) is unioned from two on-host sources,
rebuilt every REFRESH_INTERVAL seconds:
  1. the `sgas-<vnic>` anti-spoofing ipsets (ip,mac incl. secondary IPs); VLAN
     from the vnic's `br<vlan>`, and
  2. the per-VM config-drive ISOs under cache/meta (assigned ip<->mac from
     network_data.json) — the ONLY on-host source for allow_spoofing VMs, which
     have no sgas set. Each ISO entry is kept only if its MAC matches a LIVE tap
     (dropping stale ISOs of deleted VMs), and the VLAN comes from that tap's
     bridge. Reading ISOs needs `isoinfo` (from genisoimage/cdrtools).
The reply is sent untagged on `br<vlan>`; `v-<vlan>` re-tags it toward the
trunk. NOTE: a truly-spoofed IP an allow_spoofing VM uses but was never
assigned is unknowable here — with ipset-miss => DROP it would be blackholed.
"""

import argparse
import json
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

# How often the change detectors (virsh domain set + config-drive ISO dir
# fingerprint) are checked. A rebuild happens only when one of them changed.
_POLL_INTERVAL = 5.0
# Optional backstop: force a full rebuild at least this often regardless of the
# change detectors. 0 = disabled (the ISO-mtime + domain signals cover every
# IP change, since apply_second_ips.sh rebuilds the ISO on secondary-IP adds).
_FULL_REBUILD_INTERVAL = 0.0

# NFQUEUE number the nft rule dispatches to (must be free on the host).
_QUEUE_NUM = 40

# Dedicated nft bridge table we own (decoupled from `bridge cloudland`).
_NFT_TABLE = "arpreply"

# Prefix of the per-vnic anti-spoofing ipsets created by create_sg_chain.sh.
_IPSET_PREFIX = "sgas-"

# Per-VM config-drive ISOs (build_meta.sh writes <vm_ID>.iso here, containing
# openstack/latest/network_data.json with the assigned IP<->MAC — present even
# for allow_spoofing VMs that have no sgas ipset).
_META_DIR = "/opt/cloudland/cache/meta"
_META_NETDATA = "/openstack/latest/network_data.json"

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

# ISO extraction cache: path -> (mtime, [(mac, ip), ...]). Avoids re-forking
# isoinfo for unchanged config drives every refresh.
_iso_cache: dict[str, tuple[float, list[tuple[str, str]]]] = {}


def _virsh_domains() -> frozenset[str] | None:
    """Names of running libvirt domains, or None if virsh is unavailable.

    Used as a cheap change detector: when the set is unchanged we skip the
    ownership rebuild. None (virsh missing/errored) makes the caller fall back
    to time-based rebuilds so a broken virsh never freezes the map.
    """
    try:
        r = subprocess.run(
            ["virsh", "-r", "-q", "list", "--name", "--state-running"],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            check=False,
        )
    except FileNotFoundError:
        return None
    if r.returncode != 0:
        return None
    return frozenset(n.strip() for n in (r.stdout or "").splitlines() if n.strip())


def _meta_signature() -> frozenset[tuple[str, float]] | None:
    """Cheap fingerprint of the config-drive ISO dir: a set of (name, mtime).

    Changes when any ISO is created, removed, or rebuilt — including a
    secondary-IP add, since apply_second_ips.sh regenerates the ISO. Used as
    the ownership-refresh trigger so a rebuild happens only when an ISO (hence
    a VM's IP set) actually changed, not on a blind timer. None if the dir
    can't be listed (caller then rebuilds unconditionally).
    """
    try:
        names = os.listdir(_META_DIR)
    except OSError:
        return None
    sig = []
    for n in names:
        if not n.endswith(".iso"):
            continue
        try:
            sig.append((n, os.path.getmtime(os.path.join(_META_DIR, n))))
        except OSError:
            continue
    return frozenset(sig)


def _mac_tail(mac: str) -> str:
    """MAC minus its first octet (octets 2-6), lowercased. A VM's tap device
    MAC is the VM MAC with the first octet forced to 0xfe, so octets 2-6 are
    shared — we key on them to match a VM MAC to its live tap."""
    mac = mac.lower()
    return mac.split(":", 1)[1] if ":" in mac else mac


def _live_taps() -> dict[str, int]:
    """Map _mac_tail(vm_mac) -> vlan for every bridge-attached interface (VM
    taps live on br<vlan>; the v-<vlan> uplink is skipped). This is the source
    of truth for 'this VM is actually here, on this VLAN' and filters out stale
    config-drive ISOs left behind by incomplete VM deletes."""
    taps: dict[str, int] = {}
    try:
        names = os.listdir("/sys/class/net")
    except OSError:
        return taps
    for name in names:
        if name.startswith("v-"):          # vlan/vxlan uplink, not a VM tap
            continue
        try:
            master = os.path.basename(
                os.readlink("/sys/class/net/%s/master" % name)
            )
        except OSError:
            continue
        if not (master.startswith("br") and master[2:].isdigit()):
            continue
        try:
            with open("/sys/class/net/%s/address" % name) as f:
                mac = f.read().strip()
        except OSError:
            continue
        if mac:
            taps[_mac_tail(mac)] = int(master[2:])
    return taps


def _parse_network_data(raw: str) -> list[tuple[str, str]]:
    """Pull (mac, ipv4) pairs out of an OpenStack network_data.json blob."""
    try:
        data = json.loads(raw)
    except (ValueError, TypeError):
        return []
    link_mac = {}
    for link in data.get("links") or []:
        lid, mac = link.get("id"), (link.get("ethernet_mac_address") or "")
        if lid and mac:
            link_mac[lid] = mac.lower()
    out = []
    for net in data.get("networks") or []:
        ip = net.get("ip_address")
        mac = link_mac.get(net.get("link"))
        if ip and mac and ip.count(".") == 3:   # IPv4 only (ARP)
            out.append((mac, ip))
    return out


def _iso_entries(path: str) -> list[tuple[str, str]]:
    """(mac, ip) pairs from a config-drive ISO's network_data.json, cached by
    mtime so unchanged ISOs cost nothing on subsequent refreshes."""
    try:
        mtime = os.path.getmtime(path)
    except OSError:
        return []
    cached = _iso_cache.get(path)
    if cached and cached[0] == mtime:
        return cached[1]
    raw = _run(["isoinfo", "-i", path, "-R", "-x", _META_NETDATA])
    entries = _parse_network_data(raw)
    _iso_cache[path] = (mtime, entries)
    return entries


def build_owner_map(
    domains: frozenset[str] | None = None,
) -> dict[tuple[int, str], OwnerEntry]:
    """Build the full (vlan, ip) -> (mac, mac_bytes, ip_bytes) map of local VM
    IPs, from two sources unioned:

      1. sgas-* ipsets (anti-spoofing ip,mac pairs, incl. secondary IPs). VLAN
         comes from the vnic's br<vlan>; a set whose vnic is gone self-filters.
      2. per-VM config-drive ISOs under cache/meta (assigned ip<->mac from
         network_data.json) — this is the ONLY on-host source for allow_spoofing
         VMs, which have no sgas set. Each ISO entry is kept only if its MAC
         matches a LIVE tap (so stale ISOs from deleted VMs are dropped), and
         the VLAN is taken from that tap's bridge.

    When `domains` (running libvirt domain names) is given, ISOs whose vm_ID is
    not a running domain are skipped BEFORE extraction — so isoinfo forks track
    live VM count, not the total (possibly stale-heavy) file count. domains=None
    (virsh unavailable) falls back to scanning every ISO (live-tap filter still
    guards correctness). sgas wins on conflict.
    """
    owned: dict[tuple[int, str], OwnerEntry] = {}
    # --- source 1: sgas ipsets ---
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
    # --- source 2: config-drive ISOs, filtered by live taps ---
    taps = _live_taps()
    try:
        isos = os.listdir(_META_DIR)
    except OSError:
        isos = []
    seen_paths = set()
    for fname in isos:
        if not fname.endswith(".iso"):
            continue
        path = os.path.join(_META_DIR, fname)
        seen_paths.add(path)                 # track by file existence for pruning
        if domains is not None:
            vm_id = fname[:-4]               # strip ".iso"
            if vm_id.endswith("-rescue"):
                vm_id = vm_id[:-7]
            if vm_id not in domains:         # not a running VM -> skip extraction
                continue
        for mac, ip in _iso_entries(path):
            vlan = taps.get(_mac_tail(mac))
            if vlan is None:                 # not a live local VM -> skip stale
                continue
            key = (vlan, ip)
            if key not in owned:             # sgas takes precedence
                owned[key] = (mac, _mac_to_bytes(mac), _ip_to_bytes(ip))
    # prune cache entries for ISOs that no longer exist
    for gone in [p for p in _iso_cache if p not in seen_paths]:
        _iso_cache.pop(gone, None)
    return owned


def parse_vlans(spec: str) -> set[int] | None:
    """Parse a VLAN spec. None (= no restriction) if empty.

    Forms (comma-separated, mixable): "25", "25,100", "25-30", "0,25-30,200".
    0 means untagged. Mirrors arphole/bumdetect's --vlans parsing.
    """
    spec = (spec or "").strip()
    if not spec:
        return None
    out: set[int] = set()
    for part in spec.split(","):
        part = part.strip()
        if not part:
            continue
        if "-" in part:
            lo_s, hi_s = part.split("-", 1)
            lo, hi = int(lo_s), int(hi_s)
            if lo > hi:
                lo, hi = hi, lo
            out.update(range(lo, hi + 1))
        else:
            out.add(int(part))
    return out


class ArpReply:
    def __init__(
        self,
        probe_src: str = _PROBE_SRC_IP,
        queue_num: int = _QUEUE_NUM,
        poll_interval: float = _POLL_INTERVAL,
        full_rebuild_interval: float = _FULL_REBUILD_INTERVAL,
        allowed_vlans: set[int] | None = None,
        iifaces: list[str] | None = None,
    ):
        self.probe_src = probe_src
        self.queue_num = queue_num
        # Poll `virsh list` this often; rebuild the map on a domain-set change
        # (or every full_rebuild_interval, to catch IP-only changes).
        self.poll_interval = poll_interval
        self.full_rebuild_interval = full_rebuild_interval
        # Empty probe_src => answer who-has from ANY source (general proxy-ARP
        # for local VM IPs); otherwise only from this one sender IP (arphole's
        # probe source). match_all drives both the nft rule and the spa check.
        self.match_all = not probe_src
        self._probe_src_b = None if self.match_all else _ip_to_bytes(probe_src)
        # None = answer on all VLANs. Otherwise only queue probes ingressing on
        # these VLANs' v-<vlan> uplinks (nft iifname gate), and — belt and
        # suspenders — only answer when the target IP's owned VLAN is in the set.
        self.allowed_vlans = allowed_vlans
        # nft iifname allow-list: the v-<vlan> uplinks for allowed_vlans plus any
        # extra ingress interfaces named explicitly. Empty => match all ifaces.
        ifnames = set(iifaces or [])
        if allowed_vlans is not None:
            ifnames |= {"v-%d" % v for v in allowed_vlans}
        self.allowed_ifnames = sorted(ifnames)
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

    def _refresh(self, domains: frozenset[str] | None = None) -> None:
        owned = build_owner_map(domains)
        by_ip = {
            ip: (vlan, mac, mac_b, ip_b)
            for (vlan, ip), (mac, mac_b, ip_b) in owned.items()
        }
        with self._lock:
            self._owned_by_ip = by_ip
        logger.info("owner map refreshed: %d owned IP(s)", len(by_ip))

    def _refresh_loop(self) -> None:
        """Every poll_interval, rebuild the ownership map only when something
        that affects it changed:
          * the running-domain set (VM start/stop/add/remove), or
          * the config-drive ISO dir fingerprint — name+mtime — which changes
            on a new/removed ISO or a rebuilt one (a secondary-IP add rebuilds
            the ISO via apply_second_ips.sh).
        virsh being unavailable, or the optional backstop interval, also force
        a rebuild. Steady state is two cheap stat-loops per tick, no rebuild.
        """
        last_doms = last_sig = None
        last_full = 0.0
        first = True
        while not self._stop.is_set():
            try:
                doms = _virsh_domains()
                sig = _meta_signature()
                now = time.monotonic()
                backstop = (self.full_rebuild_interval > 0
                            and now - last_full >= self.full_rebuild_interval)
                if (first or doms is None or doms != last_doms
                        or sig != last_sig or backstop):
                    self._refresh(doms)
                    last_doms, last_sig, last_full, first = doms, sig, now, False
            except Exception:
                logger.exception("owner map refresh failed")
            self._stop.wait(self.poll_interval)

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
        # Optional ingress-interface gate: only probes arriving on these
        # interfaces (the v-<vlan> uplinks for allowed VLANs, plus any explicit
        # ones) are queued. Everything else falls through to policy accept.
        iif = ""
        if self.allowed_ifnames:
            iif = "iifname { %s } " % ", ".join(
                '"%s"' % n for n in self.allowed_ifnames
            )
        # Empty probe_src drops the saddr match, so ALL ARP who-has is queued.
        saddr = "" if self.match_all else "arp saddr ip %s " % self.probe_src
        script = (
            "add table bridge {t}\n"
            "add chain bridge {t} forward "
            "{{ type filter hook forward priority -10 ; policy accept ; }}\n"
            "flush chain bridge {t} forward\n"
            "add rule bridge {t} forward {iif}ether type 0x0806 "
            "arp operation request {saddr}queue num {q} bypass\n"
        ).format(t=t, iif=iif, saddr=saddr, q=self.queue_num)
        ok = _nft_load(script)
        if ok:
            logger.info(
                "installed nft queue rule: bridge %s forward -> queue %d "
                "(iif=%s, arp request %s)",
                t, self.queue_num,
                "{%s}" % ",".join(self.allowed_ifnames) if self.allowed_ifnames
                else "any",
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
                # Not a local VM IP (ipset miss) — DROP (suppress the who-has).
                # WARNING: on any interface that carries VM-SOURCED who-has this
                # blackholes ARP for every non-VM destination (gateway, external
                # hosts, other-node VMs). Keep the iifname gate on the v-<vlan>
                # uplinks so only trunk-inbound probes reach here, where the real
                # owner still answers elsewhere and dropping is safe.
                pkt.drop()
                return
            vlan, mac, mac_b, _ip_b = ent
            # VLAN gate (belt-and-suspenders on top of the nft iifname match):
            # only answer for IPs whose owned VLAN is allowed; else release.
            if self.allowed_vlans is not None and vlan not in self.allowed_vlans:
                pkt.accept()
                return
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
            "arpreply starting (probe-src=%s queue=%d poll=%.0fs backstop=%s vlans=%s ifaces=%s)",
            self.probe_src or "(any)", self.queue_num, self.poll_interval,
            ("%.0fs" % self.full_rebuild_interval
             if self.full_rebuild_interval > 0 else "off"),
            "all" if self.allowed_vlans is None
            else ",".join(str(v) for v in sorted(self.allowed_vlans)),
            ",".join(self.allowed_ifnames) or "all",
        )
        # Prime ownership before we install the rule so the first probe is
        # answerable.
        self._refresh(_virsh_domains())
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
        "--poll-interval",
        type=float,
        default=float(os.environ.get("ARPREPLY_POLL", str(_POLL_INTERVAL))),
        help="seconds between `virsh list` checks for VM add/remove "
        "(env: ARPREPLY_POLL, default: %.0f)" % _POLL_INTERVAL,
    )
    p.add_argument(
        "--refresh-interval",
        type=float,
        default=float(os.environ.get("ARPREPLY_REFRESH", str(_FULL_REBUILD_INTERVAL))),
        help="optional backstop: force a full rebuild at least this often "
        "regardless of change detection; 0 = disabled (the map already "
        "rebuilds on any VM or config-drive ISO change) "
        "(env: ARPREPLY_REFRESH, default: %.0f)" % _FULL_REBUILD_INTERVAL,
    )
    p.add_argument(
        "--vlans",
        default=os.environ.get("ARPREPLY_VLANS", ""),
        help="restrict interception to these VLANs — only who-has ingressing on "
        "their v-<vlan> uplinks is queued/answered. Comma list/ranges, e.g. "
        "'25-30' or '25,100'. Empty = all VLANs (env: ARPREPLY_VLANS)",
    )
    p.add_argument(
        "--iface",
        nargs="+",
        default=os.environ.get("ARPREPLY_IFACE", "").replace(",", " ").split(),
        help="extra ingress interface names to intercept on, added to the "
        "v-<vlan> set from --vlans (env: ARPREPLY_IFACE, space/comma-separated)",
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
        poll_interval=args.poll_interval,
        full_rebuild_interval=args.refresh_interval,
        allowed_vlans=parse_vlans(args.vlans),
        iifaces=[i.strip() for i in args.iface if i.strip()],
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

#!/usr/bin/env python3
"""
bumdetect — passive BUM-source detector.

Sniffs one or more interfaces for every frame NOT destined to this host —
i.e. whose Ethernet dst is not the sniffing interface's own MAC:

  * broadcast (dst = ff:ff:ff:ff:ff:ff), including ALL ARP — who-has requests,
    is-at replies, and gratuitous ARP are counted like any other broadcast,
  * multicast (group bit set, not broadcast), and
  * unicast destined to any MAC other than this host's own (unknown-unicast /
    flooded, including traffic bridged toward local VMs).

Frames to or from this host's own interface MAC are ignored.

For each matching frame we read the source MAC (Ethernet src) and best-effort
source IP (ARP psrc / IPv4 / IPv6) and bump TWO independent per-window
counters: one keyed by source MAC, one keyed by source IP. This is a RATE
detector: every WINDOW seconds (default 1s) each counter is evaluated and
reset, and any source that sent MORE than THRESHOLD (default 10) BUM frames in
that window is logged once — a by-mac line names the MAC and the IP(s) seen
behind it; a by-ip line names the IP and the MAC(s) it was seen with. Indexing
both ways makes IP↔MAC mismatches (spoofing, one MAC spraying many IPs, or one
IP behind many MACs) visible. A frame with no L3 source (pure L2) counts only
toward its source MAC.

Device (iface), VLAN allow-list, rate threshold and window are configurable
via CLI flags or env vars. Read-only: bumdetect never injects or drops a frame.
"""

import argparse
import logging
import os
import signal
import sys
import threading
import time

from scapy.all import (
    ARP,
    Dot1Q,
    Ether,
    IP,
    IPv6,
    UDP,
    conf,
    get_if_hwaddr,
    sniff,
)

logger = logging.getLogger("bumdetect")

DESCRIPTION = (
    "Passively sniff one or more interfaces for BUM sources: every frame not "
    "destined to this host's own MAC (broadcast including all ARP, multicast, "
    "and unknown unicast). Rate detector: count per source MAC and per source "
    "IP independently over a WINDOW (default 1s) and log any source that "
    "exceeds THRESHOLD (default 10) frames within a window. Never injects or "
    "drops traffic."
)

# Broadcast destination MAC.
_BROADCAST = "ff:ff:ff:ff:ff:ff"


def parse_vlans(spec):
    """Parse a VLAN spec string. Returns None if spec is empty (= allow all).

    Accepted forms (comma-separated, mixable):
      "0"            -> untagged only
      "25,100"       -> VLAN 25 and 100
      "25-100"       -> VLANs 25 through 100 inclusive
      "0,25-100,200" -> untagged + 25..100 + 200
    0 always means untagged (no Dot1Q header).
    """
    spec = (spec or "").strip()
    if not spec:
        return None
    out = set()
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


def parse_macs(spec):
    """Parse a source-MAC ignore-list. Returns a set of lowercased MACs
    (empty set if spec is empty). Accepts comma- or space-separated MACs, e.g.
    "10:f3:11:56:6a:e1, aa:bb:cc:dd:ee:ff".
    """
    out = set()
    for part in (spec or "").replace(",", " ").split():
        part = part.strip().lower()
        if part:
            out.add(part)
    return out


class BumDetect:
    def __init__(
        self,
        ifaces,
        allowed_vlans=None,
        threshold=10,
        window=1.0,
        ignore_macs=None,
    ):
        self.ifaces = ifaces
        # None = allow all VLANs (and untagged). 0 in set = allow untagged.
        self.allowed_vlans = allowed_vlans
        # Source MACs whose ARP *requests* are ignored: an ARP who-has from a
        # MAC in this set is dropped before counting (e.g. a gateway whose
        # who-has sweeps are expected noise). All other frame types from the
        # same MAC are still reported.
        self.ignore_macs = ignore_macs or set()
        # A source is logged when it sends MORE than `threshold` BUM frames
        # within `window` seconds.
        self.threshold = max(1, threshold)
        self.window = max(0.1, window)
        # Two independent per-window indices, both guarded by _lock. Each maps
        # a key to {"count", "peers" (cross-referenced values seen this
        # window), "last" (iface, kind, dst, vlan, peer) sample}. Both dicts
        # are swapped for fresh empties every window by _flush.
        #   _by_mac: src_mac -> entry, peers = source IPs seen behind the MAC
        #   _by_ip:  src_ip  -> entry, peers = source MACs seen for the IP
        self._by_mac = {}
        self._by_ip = {}
        # Cap how many distinct cross-referenced values a log line prints, so
        # one chatty source (e.g. a gateway ARPing for hundreds of IPs) can't
        # produce an unbounded line. The window count itself is exact.
        self._max_shown = 8
        self._lock = threading.Lock()
        # iface -> own hardware address, lowercased. Frames we transmit carry
        # this src MAC (AF_PACKET loops outgoing frames back to the sniffer);
        # skip them so bumdetect never counts its host's own traffic.
        self._iface_macs = {
            iface: (get_if_hwaddr(iface) or "").lower() for iface in ifaces
        }

    # ------------------------------------------------------------ classify

    @staticmethod
    def _classify_dst(dst):
        """Return 'broadcast', 'multicast', or 'unicast' for a dst MAC.

        The caller has already dropped frames to/from this host's own MAC, so
        every dst reaching here is a non-local destination we report on.
        """
        if dst == _BROADCAST:
            return "broadcast"
        # Group bit (LSB of first octet) set but not broadcast -> multicast.
        if int(dst[0:2], 16) & 1:
            return "multicast"
        return "unicast"

    @staticmethod
    def _src_ip(pkt):
        """Best-effort L3 source address, or '?' for a pure-L2 frame."""
        if ARP in pkt:
            return pkt[ARP].psrc or "?"
        if IP in pkt:
            return pkt[IP].src
        if IPv6 in pkt:
            return pkt[IPv6].src
        return "?"

    @staticmethod
    def _proto(pkt):
        """Short recognizable protocol label for the frame, for the log line.

        ARP is split into request / reply / gratuitous; common broadcast /
        multicast L3 protocols (DHCP, ICMP, IGMP) are named; anything else
        falls back to ipv4 / ipv6 / an ethertype hex / 'l2'.
        """
        if ARP in pkt:
            arp = pkt[ARP]
            if arp.psrc and arp.psrc == arp.pdst:
                return "arp-garp"
            if arp.op == 1:
                return "arp-request"
            if arp.op == 2:
                return "arp-reply"
            return "arp"
        if IP in pkt:
            proto = pkt[IP].proto
            if UDP in pkt:
                ports = (pkt[UDP].sport, pkt[UDP].dport)
                if 67 in ports or 68 in ports:
                    return "dhcp"
                return "udp"
            if proto == 1:
                return "icmp"
            if proto == 2:
                return "igmp"
            if proto == 6:
                return "tcp"
            return "ipv4"
        if IPv6 in pkt:
            return "ipv6"
        etype = pkt[Dot1Q].type if Dot1Q in pkt else getattr(pkt, "type", None)
        if etype and etype >= 0x0600:
            return "0x%04x" % etype
        return "l2"

    # ------------------------------------------------------------- record

    def _fmt_set(self, values):
        """Render a distinct-value set for a log line, capped at _max_shown."""
        if not values:
            return "?"
        shown = values[:self._max_shown]
        out = ",".join(shown)
        if len(values) > len(shown):
            out += " (+%d more)" % (len(values) - len(shown))
        return out

    def _record(self, iface, src_mac, src_ip, dst, kind, proto, vlan_id):
        """Accumulate one frame into the current window. Logging is deferred to
        _flush, which evaluates the per-window rate every `window` seconds.
        """
        have_ip = bool(src_ip) and src_ip != "?"
        with self._lock:
            m = self._by_mac.get(src_mac)
            if m is None:
                m = {"count": 0, "peers": set(), "last": None}
                self._by_mac[src_mac] = m
            m["count"] += 1
            if have_ip:
                m["peers"].add(src_ip)
            m["last"] = (iface, kind, proto, dst, vlan_id, src_ip)
            if have_ip:
                e = self._by_ip.get(src_ip)
                if e is None:
                    e = {"count": 0, "peers": set(), "last": None}
                    self._by_ip[src_ip] = e
                e["count"] += 1
                e["peers"].add(src_mac)
                e["last"] = (iface, kind, proto, dst, vlan_id, src_mac)

    def _flush(self):
        """Evaluate the window: log every source over threshold, then reset.

        The index dicts are swapped for fresh empties under the lock, so the
        sniff threads keep counting the next window with no contention while we
        format log lines from the snapshot.
        """
        with self._lock:
            mac_win = self._by_mac
            ip_win = self._by_ip
            self._by_mac = {}
            self._by_ip = {}
        win = "%gs" % self.window
        for mac, e in mac_win.items():
            if e["count"] <= self.threshold or e["last"] is None:
                continue
            iface, kind, proto, dst, vlan_id, src_ip = e["last"]
            vlan_info = " vlan=%d" % vlan_id if vlan_id else ""
            logger.warning(
                "[%s] BUM rate by-mac mac=%s count=%d/%s (>%d) ips=%s (last: %s proto=%s src_ip=%s dst=%s%s)",
                iface,
                mac,
                e["count"],
                win,
                self.threshold,
                self._fmt_set(sorted(e["peers"])),
                kind,
                proto,
                src_ip,
                dst,
                vlan_info,
            )
        for ip, e in ip_win.items():
            if e["count"] <= self.threshold or e["last"] is None:
                continue
            iface, kind, proto, dst, vlan_id, src_mac = e["last"]
            vlan_info = " vlan=%d" % vlan_id if vlan_id else ""
            logger.warning(
                "[%s] BUM rate by-ip ip=%s count=%d/%s (>%d) macs=%s (last: %s proto=%s src_mac=%s dst=%s%s)",
                iface,
                ip,
                e["count"],
                win,
                self.threshold,
                self._fmt_set(sorted(e["peers"])),
                kind,
                proto,
                src_mac,
                dst,
                vlan_info,
            )

    def _flush_loop(self):
        while True:
            time.sleep(self.window)
            try:
                self._flush()
            except Exception:
                logger.exception("flush failed")

    # -------------------------------------------------------------- sniff

    def _on_packet(self, iface, pkt):
        if Ether not in pkt:
            return
        eth = pkt[Ether]
        dst = (eth.dst or "").lower()
        src_mac = (eth.src or "").lower()
        if not dst or not src_mac:
            return
        own = self._iface_macs.get(iface)
        # Ignore anything to or from this host's own MAC. dst == own means the
        # frame is destined to us (not BUM); src == own means we transmitted it
        # (AF_PACKET loops outgoing frames back to the sniffer).
        if dst == own or src_mac == own:
            return
        # Configured ignore-list only silences ARP *requests* from these source
        # MACs (known-noisy gateways whose who-has sweeps are expected). Every
        # other frame from the same MAC — broadcast, multicast, unicast, ARP
        # reply / gratuitous — is still reported under the normal rules.
        if src_mac in self.ignore_macs and ARP in pkt and pkt[ARP].op == 1:
            return
        kind = self._classify_dst(dst)
        # All ARP is in scope — who-has requests, is-at replies and gratuitous
        # ARP are counted like any other BUM frame (no carve-out).
        # Optional VLAN allow-list. 0 in the set = untagged (no Dot1Q).
        vlan_id = pkt[Dot1Q].vlan if Dot1Q in pkt else 0
        if self.allowed_vlans is not None and vlan_id not in self.allowed_vlans:
            return
        self._record(
            iface, src_mac, self._src_ip(pkt), dst, kind, self._proto(pkt), vlan_id
        )

    @staticmethod
    def _build_filter(mac):
        """BPF pre-filter for one iface: capture every frame not to or from
        this host's own MAC. Broadcast (all ARP included), multicast and
        unknown-unicast all qualify. An empty mac yields an empty filter
        (capture all) and the to/from-us check is skipped.

        The ignore-MAC list is deliberately NOT pushed down here: it only
        silences ARP requests from those MACs, so the decision must be made in
        _on_packet where the frame type is known.
        """
        if not mac:
            return ""
        return "not ether src %s and not ether dst %s" % (mac, mac)

    def _sniff_iface(self, iface):
        mac = self._iface_macs.get(iface) or ""
        bpf = self._build_filter(mac)
        logger.info("sniffing on %s (mac=%s filter=%r)", iface, mac or "?", bpf)
        try:
            sniff(
                iface=iface,
                filter=bpf,
                prn=lambda pkt: self._on_packet(iface, pkt),
                store=False,
            )
        except Exception:
            logger.exception("[%s] sniff loop crashed", iface)

    def run(self):
        vlans_desc = "all" if self.allowed_vlans is None else (
            ",".join(str(v) for v in sorted(self.allowed_vlans))
        )
        ignore_desc = (
            ",".join(sorted(self.ignore_macs)) if self.ignore_macs else "none"
        )
        logger.info(
            "bumdetect starting on %s (threshold=%d/%gs vlans=%s ignore_macs=%s)",
            ", ".join(self.ifaces),
            self.threshold,
            self.window,
            vlans_desc,
            ignore_desc,
        )
        conf.verb = 0
        threads = []
        flusher = threading.Thread(
            target=self._flush_loop, name="flush", daemon=True
        )
        flusher.start()
        threads.append(flusher)
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


def parse_args():
    p = argparse.ArgumentParser(description=DESCRIPTION)
    p.add_argument(
        "--iface",
        nargs="+",
        default=os.environ.get("BUMDETECT_IFACE", "").split(","),
        required=not os.environ.get("BUMDETECT_IFACE"),
        help="one or more interfaces to listen on, e.g. --iface ens5 ens6 "
        "(env: BUMDETECT_IFACE=ens5,ens6)",
    )
    p.add_argument("--log-level", default=os.environ.get("BUMDETECT_LOG", "INFO"))
    p.add_argument(
        "--threshold",
        type=int,
        # BUMDETECT_LOG_INTERVAL is honored as a legacy alias so an existing
        # start.sh keeps working.
        default=int(
            os.environ.get(
                "BUMDETECT_THRESHOLD",
                os.environ.get("BUMDETECT_LOG_INTERVAL", "10"),
            )
        ),
        help="log a source when it sends MORE than this many BUM frames within "
        "one --window (env: BUMDETECT_THRESHOLD, legacy BUMDETECT_LOG_INTERVAL, "
        "default: 10)",
    )
    p.add_argument(
        "--window",
        type=float,
        default=float(os.environ.get("BUMDETECT_WINDOW", "1.0")),
        help="rate evaluation window in seconds; counters reset each window "
        "(env: BUMDETECT_WINDOW, default: 1.0)",
    )
    p.add_argument(
        "--vlans",
        default=os.environ.get("BUMDETECT_VLANS", ""),
        help="comma-separated VLANs/ranges to process; 0 = untagged, "
        "empty = all. e.g. '0,25,100' or '0,25-100' "
        "(env: BUMDETECT_VLANS)",
    )
    p.add_argument(
        "--ignore-macs",
        default=os.environ.get("BUMDETECT_IGNORE_MACS", ""),
        help="comma- or space-separated source MACs whose ARP *requests* are "
        "ignored; all other frame types from these MACs are still reported. "
        "e.g. '10:f3:11:56:6a:e1,aa:bb:cc:dd:ee:ff' "
        "(env: BUMDETECT_IGNORE_MACS)",
    )
    return p.parse_args()


def main():
    args = parse_args()
    logging.basicConfig(
        level=args.log_level.upper(),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    ifaces = [i.strip() for i in args.iface if i.strip()]
    if not ifaces:
        print("error: no interfaces specified", file=sys.stderr)
        return 1

    detector = BumDetect(
        ifaces=ifaces,
        allowed_vlans=parse_vlans(args.vlans),
        threshold=args.threshold,
        window=args.window,
        ignore_macs=parse_macs(args.ignore_macs),
    )

    def _stop(*_):
        logger.info("shutting down")
        sys.exit(0)

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)

    while True:
        try:
            detector.run()
        except KeyboardInterrupt:
            return 0
        except Exception:
            logger.exception("sniff loop crashed; retrying in 5s")
            time.sleep(5)


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""
arphole — producer/consumer ARP hole.

Sniff threads (one per interface) act as producers: they count
per-(iface, IP) ARP requests within a rolling window and, when the
threshold is hit, enqueue a reclaim task. Consumer threads drain the
queue, run the probe (PROBE_COUNT ARP who-has), and only if no reply
is received reclaim the IP with a locally-administered unicast MAC
(fe:55:xx:xx:xx:xx) preserving the original VLAN tag. After a probe or
reclaim the (iface, IP) is silenced for CLAIM_COOLDOWN seconds; the
chosen MAC is cached per (iface, IP) so subsequent reclaims of the same
IP reuse it (no MAC flapping for the requester).
"""

import argparse
import logging
import os
import queue
import random
import signal
import sys
import threading
import time
from typing import NamedTuple

from scapy.all import (
    ARP,
    Dot1Q,
    Ether,
    conf,
    get_if_hwaddr,
    sendp,
    sniff,
    srp,
)

logger = logging.getLogger("arphole")

DESCRIPTION = (
    "Listen for broadcast ARP requests on one or more interfaces; "
    "after THRESHOLD hits within WINDOW send PROBE_COUNT who-has, "
    "and reclaim the IP with a random MAC if no reply is received."
)

# GC interval for _silent_until / _pending / _claimed_macs dicts.
_GC_INTERVAL = 60.0
# Drop cached reclaim MACs that haven't been reused in this many seconds.
_MAC_RETENTION = 3600.0


class ProbeTask(NamedTuple):
    """Pure-field task payload handed from producer to consumer.

    Extracting fields at enqueue time avoids touching scapy Packet
    objects across threads.
    """

    target_ip: str
    sender_mac: str  # ARP hwsrc of the original request
    sender_ip: str   # ARP psrc of the original request
    vlan_id: int     # 0 = untagged, else 802.1Q VLAN ID


def rand_unicast_mac() -> str:
    """Generate a locally-administered unicast MAC (fe:55:rand:rand:rand:rand).

    fe = 11111110: bit 0 = 0 (unicast), bit 1 = 1 (locally administered).
    """
    return "fe:55:%s" % ":".join("%02x" % random.randint(0, 0xFF) for _ in range(4))


def parse_vlans(spec: str) -> set[int] | None:
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


class ArpHole:
    def __init__(
        self,
        ifaces: list[str],
        threshold: int = 6,
        window: float = 15.0,
        claim_cooldown: float = 300.0,
        probe_count: int = 2,
        probe_timeout: float = 1.0,
        workers: int = 4,
        allowed_vlans: set[int] | None = None,
    ):
        self.ifaces = ifaces
        self.threshold = threshold
        self.window = window
        self.claim_cooldown = claim_cooldown
        self.probe_count = probe_count
        self.probe_timeout = probe_timeout
        self.workers = max(1, workers)
        # None = allow all VLANs (and untagged). 0 in set = allow untagged.
        self.allowed_vlans = allowed_vlans
        # Guards _pending, _silent_until, _inflight, _claimed_macs.
        self._lock = threading.Lock()
        # (iface, ip) -> list of monotonic timestamps of recent ARP requests.
        self._pending: dict[tuple[str, str], list[float]] = {}
        # (iface, ip) -> monotonic timestamp until which we stay silent.
        self._silent_until: dict[tuple[str, str], float] = {}
        # (iface, ip) currently queued or being processed by the consumer.
        self._inflight: set[tuple[str, str]] = set()
        # (iface, ip) -> (mac, last_used_monotonic). Reused on subsequent
        # reclaims so requesters don't see MAC flapping.
        self._claimed_macs: dict[tuple[str, str], tuple[str, float]] = {}
        # Producer -> consumer queue. Each item is (iface, ip, ProbeTask).
        self._work_q: "queue.Queue[tuple[str, str, ProbeTask] | None]" = queue.Queue()
        # iface -> hardware address (used to filter out our own probes).
        self._iface_macs: dict[str, str] = {
            iface: (get_if_hwaddr(iface) or "").lower() for iface in ifaces
        }

    # ------------------------------------------------------------------ state

    def _in_silence(self, iface: str, ip: str, now: float) -> bool:
        key = (iface, ip)
        until = self._silent_until.get(key)
        if until is None:
            return False
        if now >= until:
            self._silent_until.pop(key, None)
            return False
        return True

    def _get_or_create_mac(self, iface: str, ip: str) -> str:
        """Return cached reclaim MAC for (iface, ip), creating one on first use.

        Refreshes last_used on hit so the GC retention window is measured
        from the most recent reclaim, not the first.
        """
        key = (iface, ip)
        now = time.monotonic()
        with self._lock:
            entry = self._claimed_macs.get(key)
            if entry is None:
                mac = rand_unicast_mac()
                self._claimed_macs[key] = (mac, now)
                return mac
            mac, _ = entry
            self._claimed_macs[key] = (mac, now)
            return mac

    def _record_request(self, iface: str, ip: str, now: float) -> int:
        """Return count of requests for (iface, ip) within the rolling window."""
        key = (iface, ip)
        timestamps = self._pending.get(key)
        if timestamps is None:
            timestamps = []
            self._pending[key] = timestamps
        cutoff = now - self.window
        timestamps.append(now)
        i = 0
        while i < len(timestamps) and timestamps[i] < cutoff:
            i += 1
        if i:
            del timestamps[:i]
        return len(timestamps)

    def _gc(self) -> None:
        """Drop expired silence entries, stale pending counters, and stale MAC cache."""
        now = time.monotonic()
        cutoff = now - self.window
        mac_cutoff = now - _MAC_RETENTION
        with self._lock:
            expired_silence = [
                k for k, until in self._silent_until.items() if until <= now
            ]
            for k in expired_silence:
                del self._silent_until[k]
            stale_pending = []
            for k, ts in self._pending.items():
                if not ts or ts[-1] < cutoff:
                    stale_pending.append(k)
                    continue
                i = 0
                while i < len(ts) and ts[i] < cutoff:
                    i += 1
                if i:
                    del ts[:i]
                if not ts:
                    stale_pending.append(k)
            for k in stale_pending:
                del self._pending[k]
            stale_macs = [
                k for k, (_, last_used) in self._claimed_macs.items()
                if last_used < mac_cutoff
            ]
            for k in stale_macs:
                del self._claimed_macs[k]
        if expired_silence or stale_pending or stale_macs:
            logger.debug(
                "GC: removed %d silence, %d pending, %d mac entries",
                len(expired_silence),
                len(stale_pending),
                len(stale_macs),
            )

    def _gc_loop(self) -> None:
        while True:
            time.sleep(_GC_INTERVAL)
            try:
                self._gc()
            except Exception:
                logger.exception("GC failed")

    # --------------------------------------------------------------- producer

    def enqueue_if_needed(self, iface: str, task: ProbeTask) -> bool:
        """Producer side: increment counter, enqueue reclaim task if threshold hit.

        Returns True if a task was enqueued, False otherwise. The actual
        probe + reply happens in the consumer thread.
        """
        ip = task.target_ip
        now = time.monotonic()
        with self._lock:
            if self._in_silence(iface, ip, now):
                logger.debug(
                    "[%s] arp request for %s — silent for %.0fs more",
                    iface,
                    ip,
                    self._silent_until.get((iface, ip), now) - now,
                )
                return False
            if (iface, ip) in self._inflight:
                logger.debug(
                    "[%s] arp request for %s — already queued for probe",
                    iface,
                    ip,
                )
                return False
            count = self._record_request(iface, ip, now)
            if count < self.threshold:
                logger.debug(
                    "[%s] arp request for %s, count=%d/%d (window=%.0fs) — under threshold",
                    iface,
                    ip,
                    count,
                    self.threshold,
                    self.window,
                )
                return False
            # Threshold reached: hand off to consumer.
            self._pending.pop((iface, ip), None)
            self._inflight.add((iface, ip))
        self._work_q.put((iface, ip, task))
        return True

    # --------------------------------------------------------------- consumer

    def _build_probe(self, iface: str, task: ProbeTask):
        src_mac = self._iface_macs.get(iface, "")
        arp = ARP(op=1, hwsrc=src_mac, psrc="0.0.0.0", pdst=task.target_ip)
        ether = Ether(src=src_mac, dst="ff:ff:ff:ff:ff:ff")
        if task.vlan_id:
            return ether / Dot1Q(vlan=task.vlan_id) / arp
        return ether / arp

    def _probe_occupied(self, iface: str, task: ProbeTask) -> bool:
        """Send probe_count who-has; return True only if a reply from someone
        claiming to own target_ip is received.

        On error: treat as occupied (don't reclaim).
        """
        target_ip = task.target_ip
        probe = self._build_probe(iface, task)
        try:
            ans, _ = srp(
                [probe] * self.probe_count,
                iface=iface,
                filter="arp or (vlan and arp)",
                timeout=self.probe_timeout,
                inter=0.3,
                verbose=False,
                retry=0,
            )
        except Exception:
            logger.exception(
                "[%s] probe failed for %s; assuming occupied",
                iface,
                target_ip,
            )
            return True
        for _, reply in ans:
            if (
                ARP in reply
                and reply[ARP].op == 2
                and reply[ARP].psrc == target_ip
            ):
                logger.info(
                    "[%s] probe answered by %s for %s",
                    iface,
                    reply[Ether].src if Ether in reply else "?",
                    target_ip,
                )
                return True
        return False

    def _build_reply(self, iface: str, task: ProbeTask, mac: str):
        ether = Ether(src=mac, dst=task.sender_mac)
        arp = ARP(
            op=2,
            hwsrc=mac,
            psrc=task.target_ip,
            hwdst=task.sender_mac,
            pdst=task.sender_ip,
        )
        if task.vlan_id:
            frame = ether / Dot1Q(vlan=task.vlan_id) / arp
        else:
            frame = ether / arp
        return frame

    def _handle_claim(self, iface: str, task: ProbeTask) -> None:
        """Consumer side: probe then reclaim (or just silence if occupied)."""
        ip = task.target_ip
        now = time.monotonic()
        # Silence may have been set by a previous task while we queued.
        with self._lock:
            if self._in_silence(iface, ip, now):
                logger.info("[%s] %s already silent; skipping", iface, ip)
                return
        occupied = self._probe_occupied(iface, task)
        now_after = time.monotonic()
        with self._lock:
            self._silent_until[(iface, ip)] = now_after + self.claim_cooldown
        if occupied:
            logger.info(
                "[%s] %s occupied; silent for %.0fs",
                iface,
                ip,
                self.claim_cooldown,
            )
            return
        mac = self._get_or_create_mac(iface, ip)
        frame = self._build_reply(iface, task, mac)
        try:
            sendp(frame, iface=iface, verbose=False)
        except Exception:
            logger.exception("[%s] failed to send reply for %s", iface, ip)
            return
        vlan_info = " vlan=%d" % task.vlan_id if task.vlan_id else ""
        logger.info(
            "[%s] claimed %s for %s (asked by %s/%s%s)",
            iface,
            ip,
            mac,
            task.sender_mac,
            task.sender_ip,
            vlan_info,
        )

    def consumer_loop(self, worker_id: int = 0) -> None:
        logger.info("consumer[%d] started", worker_id)
        while True:
            task = self._work_q.get()
            iface = ip = None
            try:
                if task is None:
                    logger.info("consumer[%d] got sentinel, exiting", worker_id)
                    return
                iface, ip, probe_task = task
                try:
                    self._handle_claim(iface, probe_task)
                except Exception:
                    logger.exception(
                        "[%s] claim handler crashed for %s", iface, ip
                    )
            finally:
                if task is not None and iface is not None and ip is not None:
                    with self._lock:
                        self._inflight.discard((iface, ip))
                self._work_q.task_done()

    # ----------------------------------------------------------------- sniff

    def on_packet(self, iface: str, pkt) -> None:
        if ARP not in pkt or Ether not in pkt:
            return
        if pkt[ARP].op != 1:
            return
        # Skip our own probes (srp uses the iface MAC as source).
        src_mac = self._iface_macs.get(iface, "")
        if src_mac and pkt[Ether].src.lower() == src_mac:
            return
        dst = pkt[Ether].dst
        if dst and dst.lower() != "ff:ff:ff:ff:ff:ff":
            return
        # Optional VLAN allow-list. 0 in the set = untagged (no Dot1Q).
        vlan_id = pkt[Dot1Q].vlan if Dot1Q in pkt else 0
        if self.allowed_vlans is not None and vlan_id not in self.allowed_vlans:
            return
        target_ip = pkt[ARP].pdst
        if not target_ip:
            return
        # Skip ARP probes used during Duplicate Address Detection (RFC 5227):
        # psrc=0.0.0.0 means the sender hasn't bound the IP yet — claiming
        # here would make the host's DAD report a conflict and abandon it.
        sender_ip = pkt[ARP].psrc
        if sender_ip in ("0.0.0.0", ""):
            return
        # Skip gratuitous ARP (psrc == pdst): host announcing its own
        # address, e.g. on failover. Not a request to claim.
        if sender_ip == target_ip:
            return
        # Extract pure fields; never touch pkt again after this point.
        task = ProbeTask(
            target_ip=target_ip,
            sender_mac=pkt[ARP].hwsrc,
            sender_ip=pkt[ARP].psrc,
            vlan_id=vlan_id,
        )
        self.enqueue_if_needed(iface, task)

    def _sniff_iface(self, iface: str) -> None:
        logger.info("sniffing on %s (mac=%s)", iface, self._iface_macs.get(iface))
        try:
            sniff(
                iface=iface,
                filter="arp or (vlan and arp)",
                prn=lambda pkt: self.on_packet(iface, pkt),
                store=False,
            )
        except Exception:
            logger.exception("[%s] sniff loop crashed", iface)

    def run(self) -> None:
        vlans_desc = "all" if self.allowed_vlans is None else (
            ",".join(str(v) for v in sorted(self.allowed_vlans))
        )
        logger.info(
            "arphole starting on %s (threshold=%d window=%.1fs probe=%dx%.1fs cooldown=%.0fs workers=%d vlans=%s)",
            ", ".join(self.ifaces),
            self.threshold,
            self.window,
            self.probe_count,
            self.probe_timeout,
            self.claim_cooldown,
            self.workers,
            vlans_desc,
        )
        conf.verb = 0
        threads = []
        gc = threading.Thread(target=self._gc_loop, name="gc", daemon=True)
        gc.start()
        threads.append(gc)
        for i in range(self.workers):
            t = threading.Thread(
                target=self.consumer_loop,
                args=(i,),
                daemon=True,
                name=f"consumer-{i}",
            )
            t.start()
            threads.append(t)
        for iface in self.ifaces:
            t = threading.Thread(
                target=self._sniff_iface,
                args=(iface,),
                daemon=True,
                name=f"sniff-{iface}",
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
        default=os.environ.get("ARPHOLE_IFACE", "").split(","),
        required=not os.environ.get("ARPHOLE_IFACE"),
        help="one or more interfaces to listen on, e.g. --iface ens5 ens6 "
        "(env: ARPHOLE_IFACE=ens5,ens6)",
    )
    p.add_argument("--log-level", default=os.environ.get("ARPHOLE_LOG", "INFO"))
    p.add_argument(
        "--threshold",
        type=int,
        default=int(os.environ.get("ARPHOLE_THRESHOLD", "6")),
        help="number of same-target ARP requests before probing/reclaiming "
        "(env: ARPHOLE_THRESHOLD, default: 6)",
    )
    p.add_argument(
        "--window",
        type=float,
        default=float(os.environ.get("ARPHOLE_WINDOW", "15")),
        help="rolling window in seconds within which the threshold must be hit "
        "(env: ARPHOLE_WINDOW, default: 15)",
    )
    p.add_argument(
        "--claim-cooldown",
        type=float,
        default=float(os.environ.get("ARPHOLE_CLAIM_COOLDOWN", "300")),
        help="seconds to silence (iface, IP) after a probe or reclaim "
        "(env: ARPHOLE_CLAIM_COOLDOWN, default: 300 = 5 min)",
    )
    p.add_argument(
        "--probe-count",
        type=int,
        default=int(os.environ.get("ARPHOLE_PROBE_COUNT", "2")),
        help="number of who-has probes to send before reclaiming "
        "(env: ARPHOLE_PROBE_COUNT, default: 2)",
    )
    p.add_argument(
        "--probe-timeout",
        type=float,
        default=float(os.environ.get("ARPHOLE_PROBE_TIMEOUT", "1")),
        help="seconds to wait for an answer to each probe "
        "(env: ARPHOLE_PROBE_TIMEOUT, default: 1)",
    )
    p.add_argument(
        "--workers",
        type=int,
        default=int(os.environ.get("ARPHOLE_WORKERS", "4")),
        help="number of consumer threads for probe+reclaim "
        "(env: ARPHOLE_WORKERS, default: 4)",
    )
    p.add_argument(
        "--vlans",
        default=os.environ.get("ARPHOLE_VLANS", ""),
        help="comma-separated VLANs/ranges to process; 0 = untagged, "
        "empty = all. e.g. '0,25,100' or '0,25-100' "
        "(env: ARPHOLE_VLANS)",
    )
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

    hole = ArpHole(
        ifaces=ifaces,
        threshold=args.threshold,
        window=args.window,
        claim_cooldown=args.claim_cooldown,
        probe_count=args.probe_count,
        probe_timeout=args.probe_timeout,
        workers=args.workers,
        allowed_vlans=parse_vlans(args.vlans),
    )

    def _stop(*_):
        logger.info("shutting down")
        sys.exit(0)

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)

    while True:
        try:
            hole.run()
        except KeyboardInterrupt:
            return 0
        except Exception:
            logger.exception("sniff loop crashed; retrying in 5s")
            time.sleep(5)


if __name__ == "__main__":
    sys.exit(main())

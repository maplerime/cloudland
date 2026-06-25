#!/usr/bin/env python3
"""
arphole — async ARP hole with decoupled send / sniff-reply / sweep-reclaim.

Five thread roles:

  * Request sniff (per iface): watches broadcast ARP who-has. A per-(iface, IP)
    counter over a rolling WINDOW triggers an enqueue when THRESHOLD is hit.
  * Reply sniff (per iface): watches ARP is-at replies addressed to our iface
    MAC. If the reply's psrc matches an in-flight probe, the IP is marked
    occupied and silenced.
  * Sender (single): drains the queue, registers the (iface, IP) in _probing
    with a send timestamp, and fires PROBE_COUNT who-has via sendp() — no
    waiting.
  * Sweeper (single): every _SWEEP_INTERVAL, reclaims any (iface, IP) still in
    _probing past PROBE_TIMEOUT — emitting an is-at with a cached locally-
    administered unicast MAC (fe:55:xx:xx:xx:xx) preserving the VLAN tag.
  * GC (single): periodic cleanup of stale dicts.

Decoupling send from wait means throughput is bounded by sendp() rate (~us
per packet), not by probe_timeout. After a probe-answered or reclaim, the
(iface, IP) is silenced for CLAIM_COOLDOWN seconds; the reclaim MAC is cached
per (iface, IP) so subsequent reclaims reuse it (no flapping for requesters).
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
)

logger = logging.getLogger("arphole")

DESCRIPTION = (
    "Listen for broadcast ARP requests on one or more interfaces; "
    "after THRESHOLD hits within WINDOW enqueue a probe task. A single "
    "sender fires PROBE_COUNT who-has; a reply sniffer cancels reclaim on "
    "occupied IPs; a sweeper reclaims IPs still unanswered after "
    "PROBE_TIMEOUT."
)

# GC interval for _silent_until / _pending / _claimed_macs dicts.
_GC_INTERVAL = 60.0
# Drop cached reclaim MACs that haven't been reused in this many seconds.
_MAC_RETENTION = 3600.0
# How often the reclaim sweeper wakes.
_SWEEP_INTERVAL = 0.1
# Source IP used in outgoing who-has probes. RFC 5737 TEST-NET-1 (192.0.2.0/24)
# is reserved for documentation, so this address cannot collide with a real
# host. Using a non-zero psrc avoids being mistaken for an RFC 5227 DAD probe
# (which would make Windows abandon the address it is probing). _on_request
# also filters incoming requests carrying this psrc so multiple arphole
# instances do not amplify each other's probes.
_PROBE_SRC_IP = "192.0.2.100"


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
        probe_timeout: float = 2.0,
        allowed_vlans: set[int] | None = None,
    ):
        self.ifaces = ifaces
        self.threshold = threshold
        self.window = window
        self.claim_cooldown = claim_cooldown
        self.probe_count = probe_count
        self.probe_timeout = probe_timeout
        # None = allow all VLANs (and untagged). 0 in set = allow untagged.
        self.allowed_vlans = allowed_vlans
        # Guards _pending, _silent_until, _inflight, _claimed_macs, _probing.
        # All keyed by (iface, ip, vlan_id) so the same IP on different VLANs
        # doesn't cross-interfere.
        self._lock = threading.Lock()
        # (iface, ip, vlan) -> list of monotonic timestamps of recent ARP requests.
        self._pending: dict[tuple[str, str, int], list[float]] = {}
        # (iface, ip, vlan) -> monotonic timestamp until which we stay silent.
        self._silent_until: dict[tuple[str, str, int], float] = {}
        # (iface, ip, vlan) currently queued, probing, or pending reclaim.
        self._inflight: set[tuple[str, str, int]] = set()
        # (iface, ip, vlan) -> (mac, last_used_monotonic). Reused on subsequent
        # reclaims so requesters don't see MAC flapping.
        self._claimed_macs: dict[tuple[str, str, int], tuple[str, float]] = {}
        # (iface, ip, vlan) -> (send_ts, ProbeTask). Set by sender BEFORE sendp,
        # cleared by reply sniffer (occupied) or sweeper (reclaim).
        self._probing: dict[tuple[str, str, int], tuple[float, ProbeTask]] = {}
        # Producer -> sender queue. Each item is (iface, ip, ProbeTask).
        self._work_q: "queue.Queue[tuple[str, str, ProbeTask] | None]" = queue.Queue()
        # iface -> hardware address (used to filter out our own probes
        # and to match replies addressed to us).
        self._iface_macs: dict[str, str] = {
            iface: (get_if_hwaddr(iface) or "").lower() for iface in ifaces
        }
        # iface -> cached L2Socket. Opening an AF_PACKET socket takes ~40ms
        # on this host (kernel capability/path walk on every open); reusing
        # one drops per-send cost from ~50ms to ~2ms, raising sender
        # throughput from ~20 to ~500 tasks/sec.
        self._sockets: dict[str, object] = {}
        self._sock_lock = threading.Lock()

    # ------------------------------------------------------------------ state

    def _in_silence(self, iface: str, ip: str, vlan_id: int, now: float) -> bool:
        key = (iface, ip, vlan_id)
        until = self._silent_until.get(key)
        if until is None:
            return False
        if now >= until:
            self._silent_until.pop(key, None)
            return False
        return True

    def _get_or_create_mac(self, iface: str, ip: str, vlan_id: int) -> str:
        """Return cached reclaim MAC for (iface, ip, vlan), creating one on first use.

        Refreshes last_used on hit so the GC retention window is measured
        from the most recent reclaim, not the first.
        """
        key = (iface, ip, vlan_id)
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

    def _record_request(self, iface: str, ip: str, vlan_id: int, now: float) -> int:
        """Return count of requests for (iface, ip, vlan) within the rolling window."""
        key = (iface, ip, vlan_id)
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
        vlan_id = task.vlan_id
        now = time.monotonic()
        with self._lock:
            if self._in_silence(iface, ip, vlan_id, now):
                logger.debug(
                    "[%s] arp request for %s vlan=%d — silent for %.0fs more",
                    iface,
                    ip,
                    vlan_id,
                    self._silent_until.get((iface, ip, vlan_id), now) - now,
                )
                return False
            if (iface, ip, vlan_id) in self._inflight:
                logger.debug(
                    "[%s] arp request for %s vlan=%d — already queued for probe",
                    iface,
                    ip,
                    vlan_id,
                )
                return False
            count = self._record_request(iface, ip, vlan_id, now)
            if count < self.threshold:
                logger.debug(
                    "[%s] arp request for %s vlan=%d, count=%d/%d (window=%.0fs) — under threshold",
                    iface,
                    ip,
                    vlan_id,
                    count,
                    self.threshold,
                    self.window,
                )
                return False
            # Threshold reached: hand off to consumer.
            self._pending.pop((iface, ip, vlan_id), None)
            self._inflight.add((iface, ip, vlan_id))
        logger.info(
            "[%s] threshold reached for %s vlan=%d (count=%d/%d) — enqueued probe task",
            iface,
            ip,
            vlan_id,
            count,
            self.threshold,
        )
        self._work_q.put((iface, ip, task))
        return True

    # ---------------------------------------------------------------- sender

    def _get_send_socket(self, iface: str):
        """Return a cached L2Socket for iface, opening one if needed.
        Re-opens if the existing socket has errored (e.g., iface went down).
        """
        with self._sock_lock:
            sock = self._sockets.get(iface)
            if sock is not None and not getattr(sock, "closed", False):
                return sock
            sock = conf.L2socket(iface=iface)
            self._sockets[iface] = sock
            return sock

    def _build_probe(self, iface: str, task: ProbeTask):
        src_mac = self._iface_macs.get(iface, "")
        arp = ARP(op=1, hwsrc=src_mac, psrc=_PROBE_SRC_IP, pdst=task.target_ip)
        ether = Ether(src=src_mac, dst="ff:ff:ff:ff:ff:ff")
        if task.vlan_id:
            return ether / Dot1Q(vlan=task.vlan_id) / arp
        return ether / arp

    def _send_probes(self, iface: str, task: ProbeTask) -> None:
        """Fire probe_count who-has probes. No waiting — replies arrive via
        the reply sniff thread; if none arrive within probe_timeout, the
        sweeper reclaims.

        Note: scapy's sendp(pkt, count=N) silently ignores count for a
        single Packet (only applies to list/generator), so we build an
        explicit list of copies.
        """
        probe = self._build_probe(iface, task)
        logger.info(
            "[%s] sending %d probe(s) for %s vlan=%d (psrc=%s pdst=%s)",
            iface,
            self.probe_count,
            task.target_ip,
            task.vlan_id,
            _PROBE_SRC_IP,
            task.target_ip,
        )
        sock = self._get_send_socket(iface)
        sendp(
            [probe.copy() for _ in range(self.probe_count)],
            iface=iface,
            socket=sock,
            verbose=False,
        )

    def _sender_loop(self) -> None:
        logger.info("sender started")
        while True:
            item = self._work_q.get()
            try:
                if item is None:
                    logger.info("sender got sentinel, exiting")
                    return
                iface, ip, task = item
                vlan_id = task.vlan_id
                # Register in _probing BEFORE sendp so a reply that lands
                # mid-send can still match.
                with self._lock:
                    self._probing[(iface, ip, vlan_id)] = (time.monotonic(), task)
                try:
                    self._send_probes(iface, task)
                except Exception:
                    logger.exception(
                        "[%s] probe send failed for %s vlan=%d", iface, ip, vlan_id
                    )
                    with self._lock:
                        self._probing.pop((iface, ip, vlan_id), None)
                        self._inflight.discard((iface, ip, vlan_id))
            finally:
                self._work_q.task_done()

    # --------------------------------------------------------------- reclaim

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

    def _do_reclaim(self, iface: str, task: ProbeTask) -> None:
        """Send reclaim is-at, cache MAC, silence. Called by sweeper."""
        ip = task.target_ip
        vlan_id = task.vlan_id
        mac = self._get_or_create_mac(iface, ip, vlan_id)
        frame = self._build_reply(iface, task, mac)
        try:
            sock = self._get_send_socket(iface)
            sendp(frame, iface=iface, socket=sock, verbose=False)
        except Exception:
            logger.exception("[%s] failed to send reply for %s", iface, ip)
            return
        now = time.monotonic()
        with self._lock:
            self._silent_until[(iface, ip, vlan_id)] = now + self.claim_cooldown
        vlan_info = " vlan=%d" % vlan_id if vlan_id else ""
        logger.info(
            "[%s] claimed %s for %s (asked by %s/%s%s)",
            iface,
            ip,
            mac,
            task.sender_mac,
            task.sender_ip,
            vlan_info,
        )

    def _sweep(self) -> None:
        """Reclaim probing entries older than probe_timeout."""
        now = time.monotonic()
        cutoff = now - self.probe_timeout
        expired: list[tuple[str, ProbeTask]] = []
        with self._lock:
            for key, (send_ts, task) in list(self._probing.items()):
                if send_ts <= cutoff:
                    del self._probing[key]
                    expired.append((key[0], task))
        for iface, task in expired:
            ip = task.target_ip
            vlan_id = task.vlan_id
            try:
                self._do_reclaim(iface, task)
            except Exception:
                logger.exception(
                    "[%s] reclaim failed for %s vlan=%d", iface, ip, vlan_id
                )
            finally:
                with self._lock:
                    self._inflight.discard((iface, ip, vlan_id))

    def _sweep_loop(self) -> None:
        logger.info(
            "sweeper started (interval=%.2fs timeout=%.1fs)",
            _SWEEP_INTERVAL,
            self.probe_timeout,
        )
        while True:
            time.sleep(_SWEEP_INTERVAL)
            try:
                self._sweep()
            except Exception:
                logger.exception("sweep failed")

    # ------------------------------------------------------ reply matching

    def _handle_probe_reply(
        self, iface: str, ip: str, vlan_id: int, reply_mac: str
    ) -> None:
        """Mark (iface, ip, vlan) occupied: drop from _probing, silence, clear inflight.

        Called by the reply sniff thread when an is-at for one of our probing
        IPs arrives. No-op if the entry was already claimed by the sweeper
        (race resolved under _lock by key-then-delete).
        """
        now = time.monotonic()
        with self._lock:
            if (iface, ip, vlan_id) not in self._probing:
                return
            del self._probing[(iface, ip, vlan_id)]
            self._silent_until[(iface, ip, vlan_id)] = now + self.claim_cooldown
            self._inflight.discard((iface, ip, vlan_id))
        vlan_info = " vlan=%d" % vlan_id if vlan_id else ""
        logger.info(
            "[%s] probe answered by %s for %s%s; silent for %.0fs",
            iface,
            reply_mac,
            ip,
            vlan_info,
            self.claim_cooldown,
        )

    # ----------------------------------------------------------------- sniff

    def _on_request(self, iface: str, pkt) -> None:
        """Request sniff handler: count, enqueue if threshold hit."""
        if ARP not in pkt or Ether not in pkt:
            return
        if pkt[ARP].op != 1:
            return
        # Skip our own probes (sendp uses the iface MAC as source).
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
        # Skip probes from other arphole instances so they do not amplify
        # each other into a feedback loop.
        if sender_ip == _PROBE_SRC_IP:
            return
        # Skip gratuitous ARP (psrc == pdst): host announcing its own
        # address, e.g. on failover. Not a request to claim.
        if sender_ip == target_ip:
            return
        # Extract pure fields; never touch pkt again after this point.
        task = ProbeTask(
            target_ip=target_ip,
            sender_mac=pkt[ARP].hwsrc,
            sender_ip=sender_ip,
            vlan_id=vlan_id,
        )
        self.enqueue_if_needed(iface, task)

    def _on_reply(self, iface: str, pkt) -> None:
        """Reply sniff handler: if it answers one of our probes, mark occupied.

        Match is purely on (iface, psrc, vlan_id) — no MAC check against the
        iface. Any ARP is-at for an IP we're currently probing is treated as
        evidence the IP is occupied, regardless of whom the reply was
        addressed to. Safer failure mode: false-positive "occupied" (we skip
        a reclaim) is much less harmful than false-negative (we reclaim a
        live host's IP). Also sidesteps bridge / bond / VLAN sub-interface
        MAC mismatch where Ether.dst wouldn't equal get_if_hwaddr(iface).

        AF_PACKET on Linux captures our own outgoing frames (PACKET_OUTGOING)
        on the sniff socket, so without an explicit filter the reply sniff
        would catch our own reclaim is-at (src=fe:55:*) and falsely mark
        any in-flight probe for the same (iface, psrc, vlan) as 'answered'.
        Skip frames whose source MAC is in our locally-administered block.
        """
        if ARP not in pkt or Ether not in pkt:
            return
        if pkt[ARP].op != 2:
            return
        # Skip our own reclaim is-at (AF_PACKET loops outgoing frames back).
        src_mac = pkt[Ether].src.lower()
        if src_mac.startswith("fe:55:"):
            return
        src_ip = pkt[ARP].psrc
        if not src_ip:
            return
        # VLAN tag must match what we probed; 0 = untagged.
        vlan_id = pkt[Dot1Q].vlan if Dot1Q in pkt else 0
        # Cheap lock-free pre-check; the handler re-checks under the lock.
        if (iface, src_ip, vlan_id) not in self._probing:
            return
        self._handle_probe_reply(iface, src_ip, vlan_id, pkt[Ether].src)

    def _sniff_iface(self, iface: str, role: str) -> None:
        """role: 'request' (op=1 producer) or 'reply' (op=2 response matcher)."""
        handler = self._on_request if role == "request" else self._on_reply
        logger.info(
            "sniffing %s on %s (mac=%s)",
            role,
            iface,
            self._iface_macs.get(iface),
        )
        try:
            sniff(
                iface=iface,
                filter="arp or (vlan and arp)",
                prn=lambda pkt: handler(iface, pkt),
                store=False,
            )
        except Exception:
            logger.exception("[%s] %s sniff loop crashed", iface, role)

    def run(self) -> None:
        vlans_desc = "all" if self.allowed_vlans is None else (
            ",".join(str(v) for v in sorted(self.allowed_vlans))
        )
        logger.info(
            "arphole starting on %s (threshold=%d window=%.1fs probe=%dx inter=0.1s sweep=%.1fs cooldown=%.0fs vlans=%s)",
            ", ".join(self.ifaces),
            self.threshold,
            self.window,
            self.probe_count,
            self.probe_timeout,
            self.claim_cooldown,
            vlans_desc,
        )
        conf.verb = 0
        threads = []
        gc = threading.Thread(target=self._gc_loop, name="gc", daemon=True)
        gc.start()
        threads.append(gc)
        sender = threading.Thread(target=self._sender_loop, name="sender", daemon=True)
        sender.start()
        threads.append(sender)
        sweeper = threading.Thread(target=self._sweep_loop, name="sweeper", daemon=True)
        sweeper.start()
        threads.append(sweeper)
        for iface in self.ifaces:
            for role in ("request", "reply"):
                t = threading.Thread(
                    target=self._sniff_iface,
                    args=(iface, role),
                    daemon=True,
                    name=f"sniff-{iface}-{role}",
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
        default=float(os.environ.get("ARPHOLE_PROBE_TIMEOUT", "2")),
        help="seconds after probe send with no reply before sweeper reclaims "
        "(env: ARPHOLE_PROBE_TIMEOUT, default: 2)",
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

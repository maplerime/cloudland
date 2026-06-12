#!/usr/bin/env python3
"""
arphole — listen for broadcast ARP requests on multiple interfaces and
reply to every target IP after seeing it requested THRESHOLD times
within a rolling window.

Each reply claims the IP with a freshly generated locally-administered
unicast MAC (0x02:xx:xx:xx:xx:xx) and preserves the VLAN tag (if any)
from the original request. No database lookup is performed.
"""

import argparse
import logging
import os
import random
import signal
import sys
import threading
import time

from scapy.all import ARP, Dot1Q, Ether, conf, sendp, sniff

logger = logging.getLogger("arphole")


def rand_unicast_mac() -> str:
    """Generate a locally-administered unicast MAC (0x02:rand:rand:rand:rand:rand).

    Bit 0 of the first octet = 0 -> unicast.
    Bit 1 of the first octet = 1 -> locally administered.
    """
    return "02:%s" % ":".join("%02x" % random.randint(0, 0xFF) for _ in range(5))


class ArpHole:
    def __init__(
        self,
        ifaces: list[str],
        threshold: int = 9,
        window: float = 15.0,
    ):
        self.ifaces = ifaces
        self.threshold = threshold
        self.window = window
        self._lock = threading.Lock()
        # (iface, ip) -> list of monotonic timestamps of recent ARP requests.
        self._pending: dict[tuple[str, str], list[float]] = {}

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

    def should_claim(self, iface: str, ip: str, now: float) -> bool:
        """Return True once the per-(iface, IP) threshold within the window is hit.

        Resets the counter after firing so the next batch of THRESHOLD
        requests triggers another reply.
        """
        with self._lock:
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
            self._pending.pop((iface, ip), None)
        return True

    def reply(self, iface: str, req) -> None:
        rand_mac = rand_unicast_mac()
        ether = Ether(src=rand_mac, dst=req[Ether].src)
        arp = ARP(
            op=2,
            hwsrc=rand_mac,
            psrc=req[ARP].pdst,
            hwdst=req[ARP].hwsrc,
            pdst=req[ARP].psrc,
        )
        if Dot1Q in req:
            frame = ether / Dot1Q(vlan=req[Dot1Q].vlan) / arp
        else:
            frame = ether / arp
        sendp(frame, iface=iface, verbose=False)
        vlan_info = " vlan=%d" % req[Dot1Q].vlan if Dot1Q in req else ""
        logger.info(
            "[%s] claimed %s for %s (asked by %s/%s%s)",
            iface,
            req[ARP].pdst,
            rand_mac,
            req[ARP].hwsrc,
            req[ARP].psrc,
            vlan_info,
        )

    def on_packet(self, iface: str, pkt) -> None:
        if ARP not in pkt or Ether not in pkt:
            return
        if pkt[ARP].op != 1:
            return
        dst = pkt[Ether].dst
        if dst and dst.lower() != "ff:ff:ff:ff:ff:ff":
            return
        target_ip = pkt[ARP].pdst
        if not target_ip:
            return
        if not self.should_claim(iface, target_ip, time.monotonic()):
            return
        self.reply(iface, pkt)

    def _sniff_iface(self, iface: str) -> None:
        logger.info("sniffing on %s", iface)
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
        logger.info(
            "arphole starting on %s (threshold=%d window=%.1fs)",
            ", ".join(self.ifaces),
            self.threshold,
            self.window,
        )
        conf.verb = 0
        threads = []
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
    p = argparse.ArgumentParser(description=__doc__.splitlines()[1].strip())
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
        default=int(os.environ.get("ARPHOLE_THRESHOLD", "9")),
        help="number of same-target ARP requests before replying "
        "(env: ARPHOLE_THRESHOLD, default: 9)",
    )
    p.add_argument(
        "--window",
        type=float,
        default=float(os.environ.get("ARPHOLE_WINDOW", "15")),
        help="rolling window in seconds within which the threshold must be hit "
        "(env: ARPHOLE_WINDOW, default: 15)",
    )
    return p.parse_args()


def main() -> int:
    args = parse_args()
    logging.basicConfig(
        level=args.log_level.upper(),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    # Filter out empty strings from env var parsing.
    ifaces = [i.strip() for i in args.iface if i.strip()]
    if not ifaces:
        print("error: no interfaces specified", file=sys.stderr)
        return 1

    hole = ArpHole(
        ifaces=ifaces,
        threshold=args.threshold,
        window=args.window,
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

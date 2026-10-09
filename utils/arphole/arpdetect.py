#!/usr/bin/env python3
"""Send concurrent outstanding ARP probes and collect replies on one VLAN."""

import argparse
import ipaddress
import math
import socket
import struct
import sys
import threading
import time

from scapy.all import ARP, AsyncSniffer, Dot1Q, Ether, conf, get_if_hwaddr


# Accept both on-wire tags and frames whose tag was stripped into AUXDATA.
# VLAN/source validation still happens after Scapy restores the VLAN header.
REPLY_FILTER = "arp[6:2] = 2 or (vlan and arp[6:2] = 2)"


def packet_socket_stats(receiver):
    """Read Linux AF_PACKET queue statistics once (the read resets them)."""
    raw_socket = getattr(receiver, "ins", None)
    if (not sys.platform.startswith("linux")
            or getattr(raw_socket, "family", None) != socket.AF_PACKET):
        return None
    try:
        # SOL_PACKET = 263, PACKET_STATISTICS = 6; native unsigned ints.
        data = raw_socket.getsockopt(getattr(socket, "SOL_PACKET", 263), 6, 8)
        return struct.unpack("=II", data)
    except (OSError, struct.error):
        return None


def parse_targets(specs, max_targets=65536):
    """Expand IPv4 specs, merge overlapping intervals, and sort numerically."""
    intervals = []
    for spec in specs:
        for part in spec.split(","):
            part = part.strip()
            if not part:
                raise ValueError("empty IP specification")
            if "-" in part:
                start, end = part.split("-", 1)
                lo = int(ipaddress.IPv4Address(start.strip()))
                hi = int(ipaddress.IPv4Address(end.strip()))
                if lo > hi:
                    raise ValueError(f"reversed IP range: {part}")
            elif "/" in part:
                network = ipaddress.IPv4Network(part, strict=False)
                lo, hi = int(network.network_address), int(network.broadcast_address)
                if network.prefixlen < 31:
                    lo, hi = lo + 1, hi - 1
            else:
                lo = hi = int(ipaddress.IPv4Address(part))
            intervals.append((lo, hi))

    merged = []
    for lo, hi in sorted(intervals):
        if merged and lo <= merged[-1][1] + 1:
            merged[-1] = (merged[-1][0], max(hi, merged[-1][1]))
        else:
            merged.append((lo, hi))
    count = sum(hi - lo + 1 for lo, hi in merged)
    if not count:
        raise ValueError("at least one target IP is required")
    if count > max_targets:
        raise ValueError(f"{count} targets exceed --max-targets {max_targets}")
    return [str(ipaddress.IPv4Address(ip)) for lo, hi in merged for ip in range(lo, hi + 1)]


def make_probe(mac, source_ip, target_ip, vlan):
    frame = Ether(src=mac, dst="ff:ff:ff:ff:ff:ff")
    if vlan:
        frame /= Dot1Q(vlan=vlan)
    return frame / ARP(op=1, hwsrc=mac, psrc=source_ip,
                       hwdst="00:00:00:00:00:00", pdst=target_ip)


def detect(device, vlan, targets, source_ip, timeout=3.0, count=1, interval=0.0,
           debug=False):
    """Collect replies from probed targets on the selected VLAN."""
    mac = get_if_hwaddr(device).lower()
    sent = set()
    replies = {}
    lock = threading.Lock()
    ready = threading.Event()
    target_set = set(targets)
    received = 0
    target_arp = 0

    def trace(message):
        if debug:
            print(f"arpdetect debug: {message}", file=sys.stderr)

    def on_reply(packet):
        nonlocal received, target_arp
        received += 1
        if ARP not in packet or Ether not in packet:
            return
        arp = packet[ARP]
        tag = packet.getlayer(Dot1Q)
        is_target = str(arp.psrc) in target_set
        if is_target:
            target_arp += 1

        def reject(reason):
            if is_target:
                trace(f"ignore {packet.summary()}: {reason}")

        if vlan:
            if tag is None or int(tag.vlan) != vlan or isinstance(tag.payload, Dot1Q):
                reject(f"VLAN mismatch (expected {vlan}, "
                       f"received {tag.vlan if tag is not None else 'untagged'}; "
                       "stacked tags are excluded)")
                return
        elif tag is not None:
            reject("tagged frame on an untagged scan")
            return
        if (int(arp.op) != 2 or int(arp.ptype) != 0x0800
                or int(arp.hwtype) != 1 or int(arp.plen) != 4 or int(arp.hwlen) != 6):
            reject(f"not an Ethernet/IPv4 ARP reply (op={arp.op}, "
                   f"hwtype={arp.hwtype}, ptype={arp.ptype}, "
                   f"hwlen={arp.hwlen}, plen={arp.plen})")
            return
        if str(arp.hwsrc).lower() == mac:
            reject(f"sender MAC equals interface MAC {mac}")
            return
        if str(packet[Ether].src).lower() != str(arp.hwsrc).lower():
            reject("Ethernet source differs from ARP sender MAC")
            return
        sender_mac = str(arp.hwsrc).lower()
        if sender_mac == "00:00:00:00:00:00" or int(sender_mac.split(":")[0], 16) & 1:
            reject(f"invalid sender MAC {sender_mac}")
            return
        # Match replies by VLAN and sender IP. The reply need not be addressed
        # to our MAC/source IP, but requests never count as replies.
        with lock:
            if str(arp.psrc) in sent:
                replies.setdefault(str(arp.psrc), set()).add(sender_mac)
                if is_target:
                    trace(f"accept {packet.summary()}")
            elif is_target:
                reject("target has not been probed yet")

    # Filter before queueing/parsing: unrelated traffic and ARP requests can
    # otherwise fill this socket's queue even when tcpdump receives the reply.
    receiver = conf.L2listen(iface=device, filter=REPLY_FILTER)
    sender = None
    sniffer = None
    try:
        trace(f"interface={device} mac={mac} receiver={type(receiver).__name__} "
              f"auxdata={getattr(receiver, 'auxdata_available', 'unknown')} "
              f"filter={REPLY_FILTER!r}")
        sender = conf.L2socket(iface=device)
        sniffer = AsyncSniffer(opened_socket=receiver, store=False,
                               prn=on_reply, started_callback=ready.set)
        sniffer.start()
        if not ready.wait(5.0):
            raise RuntimeError("ARP receiver did not start within 5 seconds")
        for _ in range(count):
            for target in targets:
                with lock:
                    sent.add(target)
                sender.send(make_probe(mac, source_ip, target, vlan))
                trace(f"sent who-has {target} VLAN {vlan}")
                if interval:
                    time.sleep(interval)
        time.sleep(timeout)
        sniffer.stop()
        stats = packet_socket_stats(receiver)
        if stats is not None:
            packets, drops = stats
            trace(f"packet socket: packets={packets}, drops={drops}")
            if drops:
                print(f"arpdetect: receiver dropped {drops} packets; "
                      "NO_REPLY results may be incomplete", file=sys.stderr)
        elif debug:
            trace("packet socket statistics unavailable")
        trace(f"captured {received} frames, {target_arp} ARP frames from targets, "
              f"{len(replies)} targets accepted")
        return replies
    finally:
        try:
            if sniffer is not None and sniffer.running:
                sniffer.stop()
        finally:
            try:
                if sender is not None:
                    sender.close()
            finally:
                receiver.close()


def positive_int(value):
    number = int(value)
    if number < 1:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return number


def nonnegative_float(value):
    number = float(value)
    if not math.isfinite(number) or number < 0:
        raise argparse.ArgumentTypeError("must be a finite, nonnegative number")
    return number


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("device", help="interface, e.g. eth0")
    parser.add_argument("vlan", type=int,
                        help="0 = send/receive directly on device without a tag; 1..4094 = 802.1Q VLAN")
    parser.add_argument("ips", nargs="+", help="IPv4, CIDR or inclusive start-end; comma/space separated")
    parser.add_argument("--source-ip", type=ipaddress.IPv4Address, default="192.0.2.100",
                        help="ARP sender IP (default: 192.0.2.100)")
    parser.add_argument("--timeout", type=nonnegative_float, default=3.0,
                        help="reply wait after the final probe, in seconds (default: 3)")
    parser.add_argument("--count", type=positive_int, default=1,
                        help="probe rounds (default: 1)")
    parser.add_argument("--interval", type=nonnegative_float, default=0.0,
                        help="seconds between sends (default: 0)")
    parser.add_argument("--max-targets", type=positive_int, default=65536,
                        help="maximum distinct targets (default: 65536)")
    parser.add_argument("--debug", action="store_true",
                        help="log target ARP packets and rejection reasons to stderr")
    args = parser.parse_args(argv)
    if not 0 <= args.vlan <= 4094:
        parser.error("vlan must be in 0..4094")
    try:
        targets = parse_targets(args.ips, args.max_targets)
    except ValueError as exc:
        parser.error(str(exc))
    try:
        source_ip = str(args.source_ip)
        print(f"Probing {len(targets)} IPs on {args.device}, VLAN {args.vlan}, "
              f"source {source_ip}", file=sys.stderr)
        replies = detect(args.device, args.vlan, targets, source_ip,
                         args.timeout, args.count, args.interval, debug=args.debug)
    except KeyboardInterrupt:
        print("Interrupted", file=sys.stderr)
        return 130
    except Exception as exc:
        print(f"arpdetect: {exc}", file=sys.stderr)
        return 1
    print("IP\tMAC")
    for target in targets:
        print(f"{target}\t{','.join(sorted(replies[target])) if target in replies else 'NO_REPLY'}")
    print(f"{len(replies)}/{len(targets)} IPs replied", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())

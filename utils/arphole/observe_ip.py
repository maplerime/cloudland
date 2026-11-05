#!/usr/bin/env python3
"""Record unique ARP request/reply source IPs and probe each new IP's /24."""

import argparse
import ipaddress
import math
import queue
import sys
import threading

from scapy.all import ARP, Dot1Q, Ether, conf, get_if_addr, get_if_hwaddr, sniff


# Also accept untagged frames here: Linux may restore an offloaded VLAN tag
# from PACKET_AUXDATA after the kernel filter has run.
ARP_FILTER = "arp or (vlan and arp)"


def arp_source(packet, vlan):
    """Return ARP.psrc for an IPv4 request or reply on exactly one VLAN."""
    if Ether not in packet or ARP not in packet:
        return None
    tag = packet.getlayer(Dot1Q)
    if vlan == 0:
        if tag is not None:
            return None
    elif (tag is None or int(tag.vlan) != vlan
          or isinstance(tag.payload, Dot1Q)):
        return None
    arp = packet[ARP]
    if (int(arp.op) not in (1, 2) or int(arp.hwtype) != 1 or int(arp.ptype) != 0x0800
            or int(arp.hwlen) != 6 or int(arp.plen) != 4):
        return None
    return str(arp.psrc)


def scan_subnet(sender, mac, vlan, source_ip, discovered_ip, interval, stop):
    """Broadcast one request per usable /24 address, without claiming its IP."""
    network = ipaddress.IPv4Network(f"{discovered_ip}/24", strict=False)
    print(f"Scanning {network} (trigger: {discovered_ip})", file=sys.stderr)
    for target in network.hosts():
        if stop.is_set():
            return
        packet = Ether(src=mac, dst="ff:ff:ff:ff:ff:ff")
        if vlan:
            packet /= Dot1Q(vlan=vlan)
        packet /= ARP(op=1, hwsrc=mac, psrc=source_ip,
                      hwdst="00:00:00:00:00:00", pdst=str(target))
        sender.send(packet)
        if stop.wait(interval):
            return


class SubnetScanner:
    """Serialize scans in a worker so capture callbacks never wait on sends."""

    def __init__(self, interface, vlan, source_ip, interval):
        self.mac = get_if_hwaddr(interface).lower()
        self.vlan = vlan
        self.source_ip = source_ip
        self.interval = interval
        self.pending = queue.Queue()
        self.stop = threading.Event()
        self.sender = conf.L2socket(iface=interface)
        self.worker = threading.Thread(target=self.run, name="arp-subnet-scanner")

    def run(self):
        while not self.stop.is_set():
            try:
                source = self.pending.get(timeout=0.2)
            except queue.Empty:
                continue
            try:
                scan_subnet(self.sender, self.mac, self.vlan, self.source_ip,
                            source, self.interval, self.stop)
            except Exception as exc:
                print(f"observe_ip: scan triggered by {source} failed: {exc}",
                      file=sys.stderr)
            finally:
                self.pending.task_done()

    def close(self):
        self.stop.set()
        if self.worker.ident is not None:
            self.worker.join()
        self.sender.close()


def observe(interface, vlan, output, source_ip="0.0.0.0", interval=0.01):
    """Append each new IP once, including deduplication across restarts."""
    with open(output, "a+", encoding="utf-8", buffering=1) as stream:
        stream.seek(0)
        seen = set()
        last_line = ""
        for last_line in stream:
            if last_line.strip():
                seen.add(last_line.strip())
        stream.seek(0, 2)
        needs_newline = bool(last_line and not last_line.endswith("\n"))
        errors = []
        scanner = SubnetScanner(interface, vlan, source_ip, interval)

        def record_ip(source, scan=True):
            nonlocal needs_newline
            if source == "0.0.0.0" or source in seen or errors:
                return
            try:
                stream.write(("\n" if needs_newline else "") + source + "\n")
                stream.flush()
            except OSError as exc:
                # Scapy catches callback exceptions. Stop and report the error
                # outside the callback instead of silently losing records.
                errors.append(exc)
                return
            needs_newline = False
            seen.add(source)
            print(source, flush=True)
            address = ipaddress.IPv4Address(source)
            if address.is_unspecified or address.is_multicast or address.is_loopback:
                return
            if int(address) >> 24 == 255:
                return
            if scan:
                scanner.pending.put(source)

        def on_packet(packet):
            source = arp_source(packet, vlan)
            if source is None or errors:
                return
            # Ignore our outgoing probes to avoid discovering our probe source IP.
            if str(packet[Ether].src).lower() == scanner.mac:
                return
            record_ip(source)

        print(f"Listening on {interface}, VLAN {vlan}; output: {output}; "
              f"{len(seen)} existing IPs. Ctrl+C to stop.", file=sys.stderr)
        try:
            local_ip = get_if_addr(interface) if vlan == 0 else None
            if local_ip == "0.0.0.0":
                print(f"observe_ip: {interface} has no IPv4 address; "
                      "skipping initial /24 scan", file=sys.stderr)
                local_ip = None
            if local_ip is not None:
                record_ip(local_ip, scan=False)
                if errors:
                    raise errors[0]

            def on_started():
                # Capture must be ready before probes can elicit fast replies.
                # Seed the local subnet on every startup, even for an existing IP.
                if local_ip is not None:
                    scanner.pending.put(local_ip)

            scanner.worker.start()
            sniff(iface=interface, filter=ARP_FILTER, store=False,
                  prn=on_packet, stop_filter=lambda packet: bool(errors),
                  started_callback=on_started)
        finally:
            scanner.close()
        if errors:
            raise errors[0]


def vlan_id(value):
    try:
        vlan = int(value)
    except ValueError:
        raise argparse.ArgumentTypeError("VLAN must be an integer from 0 to 4094")
    if not 0 <= vlan <= 4094:
        raise argparse.ArgumentTypeError("VLAN must be from 0 to 4094 (0 = untagged)")
    return vlan


def scan_interval(value):
    interval = float(value)
    if not math.isfinite(interval) or interval < 0:
        raise argparse.ArgumentTypeError("interval must be finite and nonnegative")
    return interval


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("-i", "--interface", required=True, help="interface to listen on")
    parser.add_argument("-v", "--vlan", required=True, type=vlan_id,
                        help="VLAN ID, 0 = untagged")
    parser.add_argument("-o", "--output",
                        help="output file (default: observed_ips_<interface>_vlan<VLAN>.txt)")
    parser.add_argument("--source-ip", type=ipaddress.IPv4Address, default="0.0.0.0",
                        help="ARP probe sender IP (default: 0.0.0.0)")
    parser.add_argument("--interval", type=scan_interval, default=0.01,
                        help="seconds between probe packets (default: 0.01)")
    args = parser.parse_args(argv)
    output = args.output or f"observed_ips_{args.interface}_vlan{args.vlan}.txt"
    try:
        observe(args.interface, args.vlan, output, str(args.source_ip), args.interval)
    except KeyboardInterrupt:
        return 0
    except Exception as exc:
        print(f"observe_ip: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())

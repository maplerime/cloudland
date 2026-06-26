#!/usr/bin/env python3

#from scapy.all import *
import sys
from scapy.layers.l2 import ARP, Ether
from scapy.sendrecv import sendp

def send_spoofed_arp(iface, src_ips, src_mac):
    pkts = []
    for src_ip in src_ips:
        arp_packet = ARP(op=2, pdst=src_ip, psrc=src_ip, hwdst="ff:ff:ff:ff:ff:ff", hwsrc=src_mac)
        pkts.append(Ether(dst="ff:ff:ff:ff:ff:ff", src=src_mac)/arp_packet)
    try:
        # Send all packets in a single batch over one socket
        sendp(pkts, iface=iface, verbose=False)
        print(f"Sent {len(pkts)} spoofed gratuitous ARP(s) ({src_mac}) via interface {iface}")
    except Exception as e:
        print(f"Error sending gratuitous ARP packet: {e}")


if __name__ == "__main__":
    if len(sys.argv) < 4:
        print("Usage: send_spoof_arp.py <interface> <source_mac> <source_ip> [source_ip ...]")
        print("   or: send_spoof_arp.py <interface> <source_ip> <source_mac>  (legacy, single ip)")
        sys.exit(1)
    iface = sys.argv[1]
    # Legacy 3-arg form: <interface> <source_ip> <source_mac>
    if len(sys.argv) == 4 and ":" in sys.argv[3] and ":" not in sys.argv[2]:
        src_ips = [sys.argv[2]]
        src_mac = sys.argv[3]
    else:
        # New form: <interface> <source_mac> <source_ip> [source_ip ...]
        src_mac = sys.argv[2]
        src_ips = sys.argv[3:]
    send_spoofed_arp(iface, src_ips, src_mac)

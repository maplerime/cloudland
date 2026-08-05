#!/usr/bin/env python3

#from scapy.all import *
import os
import sys
from scapy.layers.l2 import ARP, Ether
from scapy.sendrecv import sendp

def resolve_iface(iface):
    # Send the gratuitous ARP out the VLAN uplink (v-<vlan>) instead of the
    # bridge (br<vlan>). Injecting from the bridge floods the frame back to all
    # local taps, so the VM receives a GARP claiming its own IP - which can make
    # Windows report an address conflict during DAD and fail network setup.
    # Sending from the uplink port reaches the external network / other
    # hypervisors without looping the frame back into the local VMs.
    if iface.startswith("br"):
        iface = "v-" + iface[2:]
    return iface

def send_spoofed_arp(iface, src_ips, src_mac):
    iface = resolve_iface(iface)
    if not os.path.exists("/sys/class/net/" + iface):
        print(f"Error: interface {iface} does not exist, cannot send gratuitous ARP")
        sys.exit(1)
    pkts = []
    for src_ip in src_ips:
        arp_packet = ARP(op=2, pdst=src_ip, psrc=src_ip, hwdst="ff:ff:ff:ff:ff:ff", hwsrc=src_mac)
        pkts.append(Ether(dst="ff:ff:ff:ff:ff:ff", src=src_mac)/arp_packet)
    try:
        # Send all packets in a single batch over one socket
        sendp(pkts, iface=iface, verbose=False)
        print(f"Sent {len(pkts)} spoofed gratuitous ARP(s) ({src_mac}) via interface {iface}")
    except Exception as e:
        # Exit non-zero so the caller (cloudlet backHandler -> WEXITSTATUS) reports
        # an error message instead of silently treating a failed send as success.
        print(f"Error sending gratuitous ARP packet: {e}")
        sys.exit(1)


def usage():
    print("Usage: send_spoof_arp.py <interface> <source_mac> <source_ip> [source_ip ...]")


if __name__ == "__main__":
    if len(sys.argv) < 4:
        usage()
        sys.exit(1)
    iface = sys.argv[1]
    src_mac = sys.argv[2]
    src_ips = sys.argv[3:]
    # Fail loudly on the old <interface> <source_ip> <source_mac> ordering instead of
    # silently sending garbage: a MAC always contains ':', an IPv4 address never does.
    if ":" not in src_mac:
        print(f"Error: '{src_mac}' is not a MAC address; argument order is <interface> <mac> <ip>...")
        usage()
        sys.exit(1)
    send_spoofed_arp(iface, src_ips, src_mac)

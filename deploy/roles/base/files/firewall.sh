#!/bin/bash

nft flush ruleset
iptables -D INPUT -m state --state RELATED,ESTABLISHED -j ACCEPT
iptables -I INPUT -m state --state RELATED,ESTABLISHED -j ACCEPT
iptables -D INPUT -p icmp -j ACCEPT
iptables -A INPUT -p icmp -j ACCEPT
iptables -D INPUT -i lo -j ACCEPT
iptables -A INPUT -i lo -j ACCEPT
iptables -D INPUT -p tcp -m state --state NEW -m tcp --dport 22 -j ACCEPT
iptables -A INPUT -p tcp -m state --state NEW -m tcp --dport 22 -j ACCEPT
iptables -D INPUT -s 172.16.0.0/12 -j ACCEPT
iptables -I INPUT -s 172.16.0.0/12 -j ACCEPT
iptables -D INPUT -s 192.168.0.0/16 -j ACCEPT
iptables -I INPUT -s 192.168.0.0/16 -j ACCEPT
iptables -D INPUT -s 10.0.0.0/8 -j ACCEPT
iptables -I INPUT -s 10.0.0.0/8 -j ACCEPT
iptables -P INPUT ACCEPT
iptables -P FORWARD ACCEPT
iptables -P OUTPUT ACCEPT

/sbin/iptables-save -c > /etc/iptables.rules

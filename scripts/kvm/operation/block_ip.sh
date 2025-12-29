#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -ne 1 ] && echo "Usage: $0 <ip_address>" && exit 1

LOG_DIR="/opt/cloudland/log"
LOG_FILE="$LOG_DIR/black_list.log"

# Create log directory if not exists
[ ! -d "$LOG_DIR" ] && mkdir -p "$LOG_DIR"

# Log function
log() {
    local timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    echo "[$timestamp] $*" | tee -a "$LOG_FILE"
}

ip=$1
IPSET_NAME="blacklist"
CHAIN_NAME="BLACKLIST"

# Create ipset if not exists
if ! ipset list "$IPSET_NAME" &>/dev/null; then
    ipset create "$IPSET_NAME" hash:ip
fi

# Create iptables chain if not exists
if ! iptables -L "$CHAIN_NAME" &>/dev/null; then
    iptables -N "$CHAIN_NAME"
    iptables -A "$CHAIN_NAME" -j RETURN
fi

# Ensure BLACKLIST chain is at position 2 in FORWARD chain
check_chain_position() {
    iptables -L FORWARD --line-numbers -n | grep -E "^[0-9]+[[:space:]]+$CHAIN_NAME" | awk '{print $1}'
}

if ! iptables -C FORWARD -j "$CHAIN_NAME" &>/dev/null; then
    # Chain not referenced, insert at position 2
    iptables -I FORWARD 2 -j "$CHAIN_NAME"
else
    # Chain exists, check if at position 2
    pos=$(check_chain_position)
    if [ "$pos" != "2" ]; then
        iptables -D FORWARD -j "$CHAIN_NAME"
        iptables -I FORWARD 2 -j "$CHAIN_NAME"
    fi
fi

# Validate IP format
if [[ ! "$ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] && \
   [[ ! "$ip" =~ ^([0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}$ ]]; then
    log "ERROR: Invalid IP address: $ip"
    exit 1
fi

# Check if IP already in set
if ipset test "$IPSET_NAME" "$ip" &>/dev/null; then
    log "INFO: IP $ip already in blacklist"
    exit 0
fi

ipset add "$IPSET_NAME" "$ip"
log "ACTION: Added $ip to blacklist"

# Ensure iptables rule exists
if ! iptables -C "$CHAIN_NAME" -m set --match-set "$IPSET_NAME" src -j DROP &>/dev/null; then
    iptables -I "$CHAIN_NAME" 1 -m set --match-set "$IPSET_NAME" src -j DROP
fi

log "INFO: Blacklist update completed"

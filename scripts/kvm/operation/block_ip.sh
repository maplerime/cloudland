#!/bin/bash

cd `dirname $0`
source ../../cloudrc

if [ $# -lt 1 ] || [ $# -gt 3 ]; then
    echo "Usage: $0 <ip_address> [info_ipset_name] [timeout_seconds]"
    exit 1
fi

LOG_DIR="${log_dir:-/opt/cloudland/log}"
LOG_FILE="$LOG_DIR/black_list.log"

# Create log directory if not exists
[ ! -d "$LOG_DIR" ] && mkdir -p "$LOG_DIR"

# Log function
log() {
    local timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    echo "[$timestamp] $*" | tee -a "$LOG_FILE"
}

ip=$1
info_ipset=$2
blocking_timeout=${3:-${blocking_timeout:-3600}}
if [[ ! "$blocking_timeout" =~ ^[0-9]+$ ]] || [ ${#blocking_timeout} -gt 7 ] || [ "$blocking_timeout" -gt 2147483 ]; then
    echo "timeout must be an integer between 0 and 2147483 seconds (0 means permanent)" >&2
    exit 1
fi
IPSET_NAME="blacklist"
CHAIN_NAME="BLACKLIST"

# Create ipset if not exists
if ! ipset list "$IPSET_NAME" &>/dev/null; then
    ipset create "$IPSET_NAME" hash:ip timeout 0
fi

if [ -n "$info_ipset" ]; then
    if ! ipset list "$info_ipset" &>/dev/null; then
        ipset create "$info_ipset" hash:ip timeout 0
    fi
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

ipset add "$IPSET_NAME" "$ip" timeout "$blocking_timeout" -exist || exit 1
[ -z "$info_timeout" ] && info_timeout=300
[ -n "$info_ipset" ] && ipset add "$info_ipset" "$ip" timeout "$info_timeout" -exist
log "ACTION: Added $ip to blacklist (timeout: $blocking_timeout seconds)"

# Ensure iptables rule exists
if ! iptables -C "$CHAIN_NAME" -m set --match-set "$IPSET_NAME" src -j DROP &>/dev/null; then
    iptables -I "$CHAIN_NAME" 1 -m set --match-set "$IPSET_NAME" src -j DROP
fi
if ! iptables -C "$CHAIN_NAME" -m set --match-set "$IPSET_NAME" dst -j DROP &>/dev/null; then
    iptables -I "$CHAIN_NAME" 1 -m set --match-set "$IPSET_NAME" dst -j DROP
fi

log "INFO: Blacklist update completed"

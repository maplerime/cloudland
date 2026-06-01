#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -ne 3 ] && echo "Usage: $0 <threshold-src-dst> <threshold-src> <threshold-dst>" && exit 1

LOG_DIR="/opt/cloudland/log"
LOG_FILE="$LOG_DIR/black_list.log"

# Create log directory if not exists
[ ! -d "$LOG_DIR" ] && mkdir -p "$LOG_DIR"

# Log function
log() {
    local timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    echo "[$timestamp] $*" | tee -a "$LOG_FILE"
}

THRESHOLD_SRC_DST=$1
THRESHOLD_SRC=$2
THRESHOLD_DST=$3

BLOCK_SCRIPT="./block_ip.sh"
WHITELIST_FILE="/opt/cloudland/conf/ip_whitelist.json"

get_effective_threshold() {
    local check_ip="$1"
    local default_threshold="$2"
    local threshold="$default_threshold"

    if [ -f "$WHITELIST_FILE" ]; then
        local custom_threshold
        custom_threshold=$(jq -r --arg ip "$check_ip" '.whitelist[] | select(.ip == $ip) | .threshold // empty' "$WHITELIST_FILE" 2>/dev/null | head -1)
        if [[ "$custom_threshold" =~ ^-?[0-9]+$ ]]; then
            threshold="$custom_threshold"
        fi
    fi

    if ! [[ "$threshold" =~ ^-?[0-9]+$ ]]; then
        threshold="$default_threshold"
    fi
    if [ "$threshold" -lt 1 ]; then
        threshold=1
    fi
    echo "$threshold"
}

# Get half-open connections, extract src/dst IPs, count and sort
conn_rest=$(conntrack -L 2>/dev/null)
result=$(echo "$conn_rest" | grep -E 'SYN_SENT.*UNREPLIED' | awk '{print $5, $6}' | sed 's/src=//g; s/dst=//g' | sort | uniq -c | sort -rn | head -20)
if [ -n "$result" ]; then
    blocked_count=0
    echo "$result" | while read count src dst; do
        effective_threshold=$(get_effective_threshold "$src" "$THRESHOLD_SRC_DST")
        if [ "$count" -gt "$effective_threshold" ]; then
            log "CRITICAL: Blocking syn attack from src $src to dst $dst (count: $count, threshold: $effective_threshold)"
            $BLOCK_SCRIPT "$src" "block_src"
            ((blocked_count++))
        fi
    done
fi

# Get half-open connections, extract src IPs, count and sort
result=$(echo "$conn_rest" | grep -E 'SYN_SENT.*UNREPLIED' | awk '{print $5}' | sed 's/src=//g' | sort | uniq -c | sort -rn | head -20)
if [ -n "$result" ]; then
    blocked_count=0
    echo "$result" | while read count src; do
        effective_threshold=$(get_effective_threshold "$src" "$THRESHOLD_SRC")
        if [ "$count" -gt "$effective_threshold" ]; then
            log "CRITICAL: Blocking syn attack from src $src (count: $count, threshold: $effective_threshold)"
            $BLOCK_SCRIPT "$src" "block_src"
            ((blocked_count++))
        fi
    done
fi

# Get half-open connections, extract dst IPs, count and sort
result=$(echo "$conn_rest" | grep -E 'SYN_SENT.*UNREPLIED' | awk '{print $6}' | sed 's/dst=//g' | sort | uniq -c | sort -rn | head -20)
if [ -n "$result" ]; then
    blocked_count=0
    echo "$result" | while read count dst; do
        effective_threshold=$(get_effective_threshold "$dst" "$THRESHOLD_DST")
        if [ "$count" -gt "$effective_threshold" ]; then
            log "CRITICAL: Blocking syn attack to dst $dst (count: $count, threshold: $effective_threshold)"
            $BLOCK_SCRIPT "$dst" "block_dst"
            ((blocked_count++))
        fi
    done
fi

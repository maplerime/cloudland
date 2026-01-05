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

# Get half-open connections, extract src/dst IPs, count and sort
conn_rest=$(conntrack -L 2>/dev/null)
result=$(echo "$conn_rest" | grep -E 'SYN_SENT.*UNREPLIED' | awk '{print $5, $6}' | sed 's/src=//g; s/dst=//g' | sort | uniq -c | sort -rn | head -20)
if [ -n "$result" ]; then
    log "INFO: Half-open src-dst connections found"
    blocked_count=0
    echo "$result" | while read count src dst; do
        if [ "$count" -gt "$THRESHOLD_SRC_DST" ]; then
            log "CRITICAL: Blocking syn attack from src $src to dst $dst (count: $count)"
            $BLOCK_SCRIPT "$src" "block_src"
            ((blocked_count++))
        fi
    done
fi

# Get half-open connections, extract src IPs, count and sort
result=$(echo "$conn_rest" | grep -E 'SYN_SENT.*UNREPLIED' | awk '{print $5}' | sed 's/src=//g' | sort | uniq -c | sort -rn | head -20)
if [ -n "$result" ]; then
    log "INFO: Half-open src connections found"
    blocked_count=0
    echo "$result" | while read count src; do
        if [ "$count" -gt "$THRESHOLD_SRC" ]; then
            log "CRITICAL: Blocking syn attack from src $src (count: $count)"
            $BLOCK_SCRIPT "$src" "block_src"
            ((blocked_count++))
        fi
    done
fi

# Get half-open connections, extract dst IPs, count and sort
result=$(echo "$conn_rest" | grep -E 'SYN_SENT.*UNREPLIED' | awk '{print $6}' | sed 's/dst=//g' | sort | uniq -c | sort -rn | head -20)
if [ -n "$result" ]; then
    log "INFO: Half-open dst connections found"
    blocked_count=0
    echo "$result" | while read count dst; do
        if [ "$count" -gt "$THRESHOLD_DST" ]; then
            log "CRITICAL: Blocking syn attack to dst $dst (count: $count)"
            $BLOCK_SCRIPT "$dst" "block_dst"
            ((blocked_count++))
        fi
    done
fi

#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -ne 1 ] && echo "Usage: $0 <threshold>" && exit 1

LOG_DIR="/opt/cloudland/log"
LOG_FILE="$LOG_DIR/black_list.log"

# Create log directory if not exists
[ ! -d "$LOG_DIR" ] && mkdir -p "$LOG_DIR"

# Log function
log() {
    local timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    echo "[$timestamp] $*" | tee -a "$LOG_FILE"
}

THRESHOLD=$1
BLOCK_SCRIPT="./block_ip.sh"

# Get half-open connections, extract src/dst IPs, count and sort
result=$(conntrack -L 2>/dev/null | grep -E 'SYN_SENT.*UNREPLIED' | awk '{print $5, $6}' | sed 's/src=//g; s/dst=//g' | sort | uniq -c | sort -rn | head -20)

if [ -z "$result" ]; then
    log "INFO: No half-open connections found"
    exit 0
fi

# Block IPs exceeding threshold
blocked_count=0
echo "$result" | while read count src dst; do
    if [ "$count" -gt "$THRESHOLD" ]; then
        log "CRITICAL: Blocking syn attck from src $src to dst $dst (count: $count)"
        $BLOCK_SCRIPT "$src"
        ((blocked_count++))
    fi
done

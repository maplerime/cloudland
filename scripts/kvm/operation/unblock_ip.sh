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

# Check if ipset exists
if ! ipset list "$IPSET_NAME" &>/dev/null; then
    log "ERROR: Ipset '$IPSET_NAME' does not exist"
    exit 1
fi

# Validate IP format
if [[ ! "$ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] && \
   [[ ! "$ip" =~ ^([0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}$ ]]; then
    log "ERROR: Invalid IP address: $ip"
    exit 1
fi

# Check if IP in set
if ! ipset test "$IPSET_NAME" "$ip" &>/dev/null; then
    log "INFO: IP $ip not in blacklist"
    exit 0
fi

ipset del "$IPSET_NAME" "$ip"
log "ACTION: Removed $ip from blacklist"

log "INFO: Unblock completed"

#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -ne 1 ] && echo "Usage: $0 <ip_address>" && exit 1

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
# Validate IP format
if [[ ! "$ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] && \
   [[ ! "$ip" =~ ^([0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}$ ]]; then
    log "ERROR: Invalid IP address: $ip"
    exit 1
fi

# Nodes with no blacklist or no matching IP need no changes.
sets=$(ipset list -name) || exit 1
for set in blacklist block_src block_dst; do
    echo "$sets" | grep -Fxq -- "$set" || continue
    if ipset test "$set" "$ip" &>/dev/null; then
        ipset del "$set" "$ip" -exist || exit 1
        log "ACTION: Removed $ip from $set"
    fi
done

log "INFO: Unblock completed"

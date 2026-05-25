#!/bin/bash
#
# manage_ip_whitelist.sh - Manage IP whitelist on compute nodes
#
# Usage:
#   manage_ip_whitelist.sh refresh <base64_encoded_json>
#
# The JSON format is:
#   {"whitelist": [{"instance_uuid": "...", "ip": "...", "reason": "..."}, ...]}
#
# On refresh:
#   - Writes the new whitelist JSON to WHITELIST_FILE
#   - For any IP in the old blacklist that now appears in the whitelist,
#     calls unblock_ip.sh to remove it from the ipset.

cd "$(dirname "$0")"
source ../../cloudrc

WHITELIST_FILE="/opt/cloudland/conf/ip_whitelist.json"
UNBLOCK_SCRIPT="./unblock_ip.sh"

LOG_DIR="/opt/cloudland/log"
LOG_FILE="$LOG_DIR/ip_whitelist.log"

[ ! -d "$LOG_DIR" ] && mkdir -p "$LOG_DIR"
[ ! -d "$(dirname "$WHITELIST_FILE")" ] && mkdir -p "$(dirname "$WHITELIST_FILE")"

log() {
    local timestamp
    timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    echo "[$timestamp] $*" | tee -a "$LOG_FILE"
}

[ $# -lt 1 ] && echo "Usage: $0 <refresh> [base64_data]" && exit 1

ACTION=$1

case "$ACTION" in
    refresh)
        [ $# -lt 2 ] && echo "Usage: $0 refresh <base64_encoded_json>" && exit 1
        ENCODED="$2"
        NEW_JSON=$(echo "$ENCODED" | base64 -d 2>/dev/null)
        if [ $? -ne 0 ] || [ -z "$NEW_JSON" ]; then
            log "ERROR: Failed to decode base64 whitelist data"
            exit 1
        fi

        # Validate JSON
        if ! echo "$NEW_JSON" | jq empty > /dev/null 2>&1; then
            log "ERROR: Decoded data is not valid JSON"
            exit 1
        fi

        # If ipset blacklist exists, unblock any IPs that are now whitelisted
        if ipset list blacklist &>/dev/null 2>&1; then
            NEW_IPS=$(echo "$NEW_JSON" | jq -r '.whitelist[].ip' 2>/dev/null)
            for wl_ip in $NEW_IPS; do
                if ipset test blacklist "$wl_ip" > /dev/null 2>&1; then
                    log "INFO: IP $wl_ip is now whitelisted, removing from blacklist"
                    if [ -x "$UNBLOCK_SCRIPT" ]; then
                        "$UNBLOCK_SCRIPT" "$wl_ip"
                    else
                        ipset del blacklist "$wl_ip" 2>/dev/null || true
                    fi
                fi
            done
        fi

        echo "$NEW_JSON" > "$WHITELIST_FILE"
        ENTRY_COUNT=$(echo "$NEW_JSON" | jq '.whitelist | length' 2>/dev/null || echo "?")
        log "INFO: Whitelist refreshed with $ENTRY_COUNT entries"
        ;;
    *)
        echo "Unknown action: $ACTION"
        echo "Usage: $0 refresh <base64_encoded_json>"
        exit 1
        ;;
esac

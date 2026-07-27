#!/bin/bash
# bumdetect launcher. Edit the variables below or override via environment.
set -euo pipefail

cd "$(dirname "$0")"

# --- configuration (override with env vars if desired) -----------------------
: "${BUMDETECT_IFACE:=ens5}"
: "${BUMDETECT_LOG:=INFO}"
: "${BUMDETECT_THRESHOLD:=10}"   # log a source exceeding this many frames per window
: "${BUMDETECT_WINDOW:=1.0}"     # rate window in seconds
: "${BUMDETECT_VLANS:=25-30}"    # e.g. "0,25-100" or empty for all
: "${BUMDETECT_IGNORE_MACS:=}"   # source MACs to ignore, e.g. "aa:bb:cc:dd:ee:ff,..."

export BUMDETECT_IFACE BUMDETECT_LOG
export BUMDETECT_THRESHOLD BUMDETECT_WINDOW
export BUMDETECT_VLANS BUMDETECT_IGNORE_MACS

# --- python interpreter ------------------------------------------------------
if [[ -x "./.venv/bin/python" ]]; then
    PY="./.venv/bin/python"
elif [[ -x "/opt/cloudland/utils/bumdetect/.venv/bin/python" ]]; then
    PY="/opt/cloudland/utils/bumdetect/.venv/bin/python"
else
    PY="$(command -v python3)"
fi

# --- root / CAP_NET_RAW check -----------------------------------------------
if [[ $EUID -ne 0 ]] && ! capsh --print 2>/dev/null | grep -q cap_net_raw; then
    echo "WARNING: not running as root and CAP_NET_RAW not present." >&2
    echo "         scapy needs raw socket access to sniff." >&2
fi

echo "bumdetect: iface=$BUMDETECT_IFACE threshold=$BUMDETECT_THRESHOLD window=${BUMDETECT_WINDOW}s vlans=${BUMDETECT_VLANS:-all} ignore_macs=${BUMDETECT_IGNORE_MACS:-none}"
exec "$PY" ./bumdetect.py "$@"

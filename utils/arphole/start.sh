#!/bin/bash
# arphole launcher. Edit the variables below or override via environment.
set -euo pipefail

cd "$(dirname "$0")"

# --- configuration (override with env vars if desired) -----------------------
: "${ARPHOLE_IFACE:=ens5}"
: "${ARPHOLE_LOG:=INFO}"
: "${ARPHOLE_THRESHOLD:=6}"
: "${ARPHOLE_WINDOW:=15}"
: "${ARPHOLE_CLAIM_COOLDOWN:=300}"
: "${ARPHOLE_PROBE_COUNT:=3}"
: "${ARPHOLE_PROBE_TIMEOUT:=2}"
: "${ARPHOLE_WORKERS:=4}"
: "${ARPHOLE_VLANS:=25-30}"   # e.g. "0,25-100" or empty for all

export ARPHOLE_IFACE ARPHOLE_LOG
export ARPHOLE_THRESHOLD ARPHOLE_WINDOW
export ARPHOLE_CLAIM_COOLDOWN ARPHOLE_PROBE_COUNT ARPHOLE_PROBE_TIMEOUT
export ARPHOLE_WORKERS ARPHOLE_VLANS

# --- python interpreter ------------------------------------------------------
if [[ -x "./.venv/bin/python" ]]; then
    PY="./.venv/bin/python"
elif [[ -x "/opt/cloudland/utils/arphole/.venv/bin/python" ]]; then
    PY="/opt/cloudland/utils/arphole/.venv/bin/python"
else
    PY="$(command -v python3)"
fi

# --- root / CAP_NET_RAW check -----------------------------------------------
if [[ $EUID -ne 0 ]] && ! capsh --print 2>/dev/null | grep -q cap_net_raw; then
    echo "WARNING: not running as root and CAP_NET_RAW not present." >&2
    echo "         scapy needs raw socket access to sniff/send ARP." >&2
fi

echo "arphole: iface=$ARPHOLE_IFACE threshold=$ARPHOLE_THRESHOLD window=${ARPHOLE_WINDOW}s cooldown=${ARPHOLE_CLAIM_COOLDOWN}s probe=${ARPHOLE_PROBE_COUNT}x${ARPHOLE_PROBE_TIMEOUT}s workers=$ARPHOLE_WORKERS vlans=${ARPHOLE_VLANS:-all}"
exec "$PY" ./arphole.py "$@"

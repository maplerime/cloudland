#!/bin/bash
# arpreply launcher. Edit the variables below or override via environment.
set -euo pipefail

cd "$(dirname "$0")"

# --- configuration (override with env vars if desired) -----------------------
: "${ARPREPLY_IFACE:=ens5}"
: "${ARPREPLY_LOG:=INFO}"
: "${ARPREPLY_PROBE_SRC:=192.0.2.100}"   # match arphole's probe source IP
: "${ARPREPLY_IGNORE_VLAN:=}"            # non-empty/true = match IP on any vlan
: "${ARPREPLY_REFRESH:=15}"              # ipset ownership rebuild interval (s)

export ARPREPLY_IFACE ARPREPLY_LOG ARPREPLY_PROBE_SRC
export ARPREPLY_IGNORE_VLAN ARPREPLY_REFRESH

# --- python interpreter ------------------------------------------------------
if [[ -x "./.venv/bin/python" ]]; then
    PY="./.venv/bin/python"
elif [[ -x "/opt/cloudland/utils/arpreply/.venv/bin/python" ]]; then
    PY="/opt/cloudland/utils/arpreply/.venv/bin/python"
else
    PY="$(command -v python3)"
fi

# --- root / CAP_NET_RAW check -----------------------------------------------
if [[ $EUID -ne 0 ]] && ! capsh --print 2>/dev/null | grep -q cap_net_raw; then
    echo "WARNING: not running as root and CAP_NET_RAW not present." >&2
    echo "         scapy needs raw socket access to sniff/send ARP," >&2
    echo "         and reading the sgas-* ipsets needs root too." >&2
fi

echo "arpreply: iface=$ARPREPLY_IFACE probe-src=$ARPREPLY_PROBE_SRC ignore-vlan=${ARPREPLY_IGNORE_VLAN:-false} refresh=${ARPREPLY_REFRESH}s"
exec "$PY" ./arpreply.py "$@"

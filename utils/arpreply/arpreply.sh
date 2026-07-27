#!/bin/bash
# arpreply launcher. Edit the variables below or override via environment.
#
# arpreply is INLINE: it installs an nftables `queue` rule in the bridge
# datapath (table `bridge arpreply`) that hands only arphole's who-has probes
# (arp saddr ip = $ARPREPLY_PROBE_SRC) to this process, which answers for
# locally-owned VM IPs and DROPs the probe. The rule uses `bypass`, so if this
# process is down the probe flows normally (fail-safe). Needs CAP_NET_ADMIN
# (nft + NFQUEUE) and CAP_NET_RAW (raw reply send) — i.e. run as root.
#
# System deps: nftables, and libnetfilter-queue for the python NetfilterQueue
# module (see requirements.txt).
set -euo pipefail

cd "$(dirname "$0")"

# --- configuration (override with env vars if desired) -----------------------
: "${ARPREPLY_LOG:=INFO}"
: "${ARPREPLY_PROBE_SRC:=}"   # match arphole's probe source IP
: "${ARPREPLY_QUEUE_NUM:=40}"            # NFQUEUE number the nft rule uses
: "${ARPREPLY_REFRESH:=15}"              # ipset ownership rebuild interval (s)

export ARPREPLY_LOG ARPREPLY_PROBE_SRC ARPREPLY_QUEUE_NUM ARPREPLY_REFRESH

# --- python interpreter ------------------------------------------------------
if [[ -x "./.venv/bin/python" ]]; then
    PY="./.venv/bin/python"
elif [[ -x "/opt/cloudland/utils/arpreply/.venv/bin/python" ]]; then
    PY="/opt/cloudland/utils/arpreply/.venv/bin/python"
else
    PY="$(command -v python3)"
fi

# --- root / capability check -------------------------------------------------
if [[ $EUID -ne 0 ]]; then
    have_admin=$(capsh --print 2>/dev/null | grep -c cap_net_admin || true)
    have_raw=$(capsh --print 2>/dev/null | grep -c cap_net_raw || true)
    if [[ "$have_admin" -eq 0 || "$have_raw" -eq 0 ]]; then
        echo "WARNING: not root and missing CAP_NET_ADMIN/CAP_NET_RAW." >&2
        echo "         arpreply needs them for nft + NFQUEUE + raw reply send." >&2
    fi
fi

echo "arpreply: probe-src=$ARPREPLY_PROBE_SRC queue=$ARPREPLY_QUEUE_NUM refresh=${ARPREPLY_REFRESH}s"
exec "$PY" ./arpreply.py "$@"

#!/bin/bash
set -o pipefail
source "${CLOUDRC:-/opt/cloudland/scripts/cloudrc}"
# Preserve the PoC WDS query; Python integers avoid the original 64-bit mask limit.
if [ -z "${wds_address:-}" ]; then exit 0; fi
wds_curl GET 'api/v2/wds/uss' | python3 -c '
import json,sys
host=sys.argv[1]
data=json.load(sys.stdin)
gateways=data.get("uss_gateways")
if not isinstance(gateways,list): raise SystemExit("invalid WDS gateway response")
matched=[g for g in gateways if g.get("server_name")==host]
if not matched: raise SystemExit("no confirmed WDS binding for this node")
for g in matched:
    raw=g.get("core")
    if isinstance(raw,bool) or raw is None: raise SystemExit("missing WDS CPU mask")
    mask=int(raw,0) if isinstance(raw,str) else int(raw)
    if mask<=0: raise SystemExit("invalid or empty WDS CPU mask")
    out={k:g.get(k) for k in ("name","server_name","server_id","core","status")}
    out["core_cpus"]=",".join(str(i) for i in range(mask.bit_length()) if mask & (1<<i))
    print(json.dumps(out))
' "${1:-$(hostname -s)}"

#!/bin/bash
set -o pipefail
common_dir=$(cd "$(dirname "$0")" && pwd) || exit 1
# Reuse the node storage configuration; no WDS address means local storage.
source "${CLOUDRC:-/opt/cloudland/scripts/cloudrc}"
# Binding data is refreshed immediately before isolation; stale reports are not used.
timeout -k 2 10 bash "$common_dir/check_uss_core_bind.sh" | python3 -c '
import glob,json,os,re,sys
from pathlib import Path
def cpus(text):
    result=set()
    for item in text.strip().split(","):
        if not item: continue
        if not re.fullmatch(r"[0-9]+(?:-[0-9]+)?",item): raise ValueError("invalid CPU list")
        bounds=[int(n) for n in item.split("-")]
        result.update(range(bounds[0],bounds[-1]+1))
    return result
rows=[json.loads(line) for line in sys.stdin if line.strip()]
if not rows and sys.argv[2]: raise SystemExit("WDS bindings could not be confirmed")
blocked=set()
for row in rows: blocked.update(cpus(row["core_cpus"]))
for cpu in list(blocked):
    path=Path("/sys/devices/system/cpu/cpu%d/topology/thread_siblings_list" % cpu)
    if not path.exists(): raise SystemExit("WDS SMT topology unavailable")
    blocked.update(cpus(path.read_text()))
online=cpus(Path("/sys/devices/system/cpu/online").read_text())
allowed=online & os.sched_getaffinity(0)
# QEMU and cloudlet can have different cgroup parents; intersect actual QEMU thread masks.
domain=sys.argv[1]
found=False
for filename in glob.glob("/run/libvirt/qemu/*.pid")+glob.glob("/var/run/libvirt/qemu/*.pid"):
    if Path(filename).stem!=domain: continue
    pid=int(Path(filename).read_text().strip())
    cmdline=Path("/proc/%d/cmdline" % pid).read_bytes().replace(b"\0",b" ")
    if b"qemu" not in cmdline: raise SystemExit("invalid QEMU PID file")
    found=True
    # cgroup effective cpusets bound legal pinning without reusing an existing vCPU pin.
    entries=Path("/proc/%d/cgroup" % pid).read_text().splitlines()
    for entry in entries:
        _,controllers,relative=entry.split(":",2)
        if controllers=="":
            path=Path("/sys/fs/cgroup")/relative.lstrip("/")/"cpuset.cpus.effective"
        elif "cpuset" in controllers.split(","):
            path=Path("/sys/fs/cgroup/cpuset")/relative.lstrip("/")/"cpuset.effective_cpus"
        else: continue
        if not path.exists(): raise SystemExit("QEMU effective cpuset unavailable")
        allowed &= cpus(path.read_text())
if not found: raise SystemExit("QEMU cgroup could not be confirmed")
candidates=sorted(allowed-blocked)
if not candidates: raise SystemExit("no CPU available outside WDS bindings")
print(candidates[0])
' "$1" "${wds_address:-}"

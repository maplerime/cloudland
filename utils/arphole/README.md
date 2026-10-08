# arphole

Sniffs broadcast ARP "who-has" requests on one or more interfaces and,
after the same (iface, target IP) is requested THRESHOLD times within a
rolling WINDOW, fires probes and reclaims the IP with a locally-
administered unicast MAC if no owner replies within PROBE_TIMEOUT.
Replies preserve the original 802.1Q VLAN tag.

## How it works

Five thread roles — probe send and reply wait are decoupled so throughput
is bounded by `sendp()` rate, not by `PROBE_TIMEOUT`.

- **Request sniff** (one per iface): counts broadcast ARP `who-has` per
  (iface, IP) over a rolling WINDOW; at THRESHOLD enqueues a probe task
  and marks the (iface, IP) inflight so duplicates coalesce while the
  task is pending.
- **Reply sniff** (one per iface): watches ARP `is-at` replies addressed
  to our iface MAC. If `ARP.psrc` matches an in-flight probe, that IP is
  marked occupied and silenced — never reclaimed.
- **Sender** (single): drains the queue, registers `(iface, IP)` in
  `_probing` with a send timestamp, then fires PROBE_COUNT `who-has`
  (`ARP op=1`, `hwsrc` = iface MAC, `psrc=192.0.2.100`) via `sendp()`. No
  waiting — replies are matched by the reply sniff thread.
- **Sweeper** (single): every 100 ms reclaims any `(iface, IP)` still in
  `_probing` past PROBE_TIMEOUT — emitting an `is-at` (`op=2`) with a
  cached `fe:55:xx:xx:xx:xx` MAC addressed to the original requester's
  MAC, preserving the VLAN tag. The MAC is cached per (iface, IP) so
  subsequent reclaims reuse it (no flapping).
- **Silence**: after either path (probe answered or reclaim), the
  (iface, IP) is silenced for `CLAIM_COOLDOWN` seconds.
- **GC** (single): every 60 s drops expired silence entries, stale
  pending counters, and MAC cache entries not reused in 1 h.

## Configuration

All knobs are available as CLI flags (`--threshold`, `--probe-timeout`,
…) or matching env vars. Defaults:

| Env var                  | Default | Meaning                                              |
| ------------------------ | ------- | ---------------------------------------------------- |
| `ARPHOLE_IFACE`          | —       | Comma- or space-separated interfaces to listen on.   |
| `ARPHOLE_LOG`            | INFO    | Log level.                                           |
| `ARPHOLE_THRESHOLD`      | 6       | Same-target ARP requests before probing/reclaiming.  |
| `ARPHOLE_WINDOW`         | 15      | Rolling window in seconds.                           |
| `ARPHOLE_CLAIM_COOLDOWN` | 300     | Seconds to silence (iface, IP) after probe/reclaim.  |
| `ARPHOLE_PROBE_COUNT`    | 2       | Number of who-has probes fired per task.             |
| `ARPHOLE_PROBE_TIMEOUT`  | 5       | Seconds after probe send with no reply before sweep-reclaim. |
| `ARPHOLE_VLANS`          | (all)   | VLAN allow-list, see below.                          |

## Run

`sudo` / `CAP_NET_RAW` is required — scapy uses `AF_PACKET` for raw
sockets.

Directly:

```bash
pip install -r requirements.txt
sudo ARPHOLE_IFACE=eth0 \
     ARPHOLE_THRESHOLD=6 \
     ARPHOLE_WINDOW=15 \
     python3 arphole.py
```

Via the launcher (edit `start.sh` or override env vars):

```bash
sudo ./start.sh
```

Via systemd (path in `arphole.service` points at `/opt/arphole/start.sh`):

```bash
sudo cp arphole.service /etc/systemd/system/
sudo systemctl enable --now arphole
```

Individual probe/reclaim send failures and GC/sweep iteration errors are logged;
other tasks and subsequent iterations continue. If a worker exits because of
an uncaught exception or returns unexpectedly, the entire process terminates
with exit status 1 rather than running with missing workers. This also covers
capture errors that Scapy catches internally before returning, and service
startup failures. systemd's `Restart=on-failure` starts a fresh process.
SIGINT/SIGTERM remain normal stops with exit status 0.

## VLAN handling

All sniff threads use the BPF filter `arp or (vlan and arp)` to capture
tagged and untagged frames. Reply frames re-tag with the same VLAN ID
as the request.

`ARPHOLE_VLANS` is an optional allow-list; IPs outside the listed VLANs
are ignored. Empty value = all VLANs (and untagged). Examples:

- `0` — untagged only
- `25,100` — VLAN 25 and 100
- `25-100` — VLANs 25 through 100 inclusive
- `0,25-100,200` — untagged + 25..100 + 200

## Random MAC policy

`rand_unicast_mac()` always emits `fe:55:xx:xx:xx:xx` — first octet
`0xfe` = `11111110`: unicast (LSB = 0) + locally administered (bit 1 = 1).

## ARP detection tool

`arpdetect.py <device> <vlan> <ip1, ...>` sends ARP requests for all targets
without waiting for each IP individually, while a background receiver collects
ARP replies (`op=2`) from the targets on the selected VLAN. It waits once
after the final request (3 seconds by default).
Use VLAN `0` for untagged frames, or `1..4094` for an 802.1Q tag.
With VLAN `0`, both sending and receiving use the specified device directly:
no VLAN header is added and tagged replies are excluded.

```bash
sudo python3 arpdetect.py eth0 25 10.10.10.5
sudo python3 arpdetect.py eth0 25 10.10.10.0/24
sudo python3 arpdetect.py eth0 25 10.10.10.5-10.10.10.10
sudo python3 arpdetect.py eth0 25 10.10.10.5,10.10.11.0/24 10.10.12.5-10.10.12.10 --timeout 5 --count 2
```

Targets can be comma- or space-separated and are deduplicated and sorted.
CIDRs use host addresses (excluding network/broadcast except for `/31` and
`/32`); ranges include both endpoints. IPv4 only. Expansion is limited to
65,536 distinct targets by default; override with `--max-targets`.
`--interval` sets the delay between sends (default: 0).

The ARP sender IP defaults to `192.0.2.100` on all devices and VLANs.
Use `--source-ip` to override it, including an explicit `0.0.0.0`.
Run on the underlying interface when the tool should add a VLAN tag itself;
use VLAN `0` on an existing VLAN subinterface.

Standard output is a tab-separated `IP` / `MAC` table, including `NO_REPLY`
for unanswered targets and comma-separated MACs when multiple hosts reply
for the same IP. Only ARP replies (`op=2`) whose sender IP matches a probed
target are counted on the selected VLAN; locally sent frames are excluded.
Replies need not be addressed to the probing MAC or source IP. ARP requests
(`op=1`), including gratuitous requests, never count as replies.
Progress and the response count go to standard error.
The receiver uses a kernel BPF filter for untagged and VLAN-tagged ARP replies
before Scapy parses them. This excludes unrelated traffic and ARP request floods
from its receive queue. The libpcap runtime library is required to compile the
filter (in addition to Scapy).
Use `--debug` to log the interface MAC, capture backend/filter, target ARP replies,
acceptance/rejection reasons, capture counts, and Linux packet socket drop counts
when available, to standard error. A nonzero drop count produces a warning even
without `--debug`; unanswered results may be incomplete. For example:

```bash
sudo ./arpdetect.py bond0 25 137.175.71.236 --count 2 --timeout 5 --debug
```

Run tcpdump concurrently with the scan when comparing captures. A reply seen
after the scan has finished cannot be counted by that scan.
`NO_REPLY` means no matching reply was received during this scan; it does not
prove an IP is free. This tool collects replies and does not claim addresses.
The same Scapy dependency and `sudo` / `CAP_NET_RAW` requirement apply.

Offline verification (no network packets sent):

```bash
python3 -m unittest -v test_arpdetect
```

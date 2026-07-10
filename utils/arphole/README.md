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

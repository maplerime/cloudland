# arphole

Sniffs broadcast ARP "who-has" requests on one or more interfaces and,
after the same (iface, target IP) is requested THRESHOLD times within a
rolling WINDOW, probes the IP and reclaims it with a freshly generated
locally-administered unicast MAC if no owner replies. Replies preserve
the original 802.1Q VLAN tag.

## How it works

Producer/consumer design so sniffing is never blocked by probe I/O.

- **Sniff threads** (one per interface): for every broadcast ARP
  `who-has`, increment a per-(iface, IP) counter over a rolling window.
  When the counter reaches THRESHOLD, enqueue a reclaim task and mark
  the (iface, IP) as inflight (so duplicate requests are coalesced
  while the task is pending).
- **Consumer threads** (`--workers`): drain the queue, then:
  1. **Probe** — send PROBE_COUNT ARP `who-has` packets (ARP `op=1`,
     `hwsrc` = iface MAC, `psrc=0.0.0.0`) and wait PROBE_TIMEOUT for
     a reply. A reply counts as "occupied" only if `ARP.op == 2` and
     `ARP.psrc == target_ip` (avoids false positives from unrelated
     ARP replies).
  2. **Reclaim** — if no reply, emit an `is-at` (`op=2`) with a
     `fe:55:xx:xx:xx:xx` MAC, addressed to the original requester's
     MAC, preserving the VLAN tag. The MAC is cached per (iface, IP)
     so subsequent reclaims of the same IP reuse it (no flapping).
  3. **Silence** — either way, the (iface, IP) is silenced for
     `CLAIM_COOLDOWN` seconds after a probe (occupied) or a reclaim.
- **GC thread**: every 60 s drops expired silence entries and stale
  pending counters so the dicts stay bounded over long runs.

## Configuration

All knobs are available as CLI flags (`--threshold`, `--workers`, …) or
matching env vars. Defaults:

| Env var                  | Default | Meaning                                              |
| ------------------------ | ------- | ---------------------------------------------------- |
| `ARPHOLE_IFACE`          | —       | Comma- or space-separated interfaces to listen on.   |
| `ARPHOLE_LOG`            | INFO    | Log level.                                           |
| `ARPHOLE_THRESHOLD`      | 6       | Same-target ARP requests before probing/reclaiming.  |
| `ARPHOLE_WINDOW`         | 15      | Rolling window in seconds.                           |
| `ARPHOLE_CLAIM_COOLDOWN` | 300     | Seconds to silence (iface, IP) after probe/reclaim.  |
| `ARPHOLE_PROBE_COUNT`    | 3       | Number of who-has probes before reclaiming.          |
| `ARPHOLE_PROBE_TIMEOUT`  | 2       | Seconds to wait for an answer to each probe batch.   |
| `ARPHOLE_WORKERS`        | 4       | Consumer threads doing probe + reclaim.              |
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

Both sniff (`sniff`) and probe (`srp`) use the BPF filter
`arp or (vlan and arp)` to capture tagged and untagged frames. Reply
frames re-tag with the same VLAN ID as the request.

`ARPHOLE_VLANS` is an optional allow-list; IPs outside the listed VLANs
are ignored. Empty value = all VLANs (and untagged). Examples:

- `0` — untagged only
- `25,100` — VLAN 25 and 100
- `25-100` — VLANs 25 through 100 inclusive
- `0,25-100,200` — untagged + 25..100 + 200

## Random MAC policy

`rand_unicast_mac()` always emits `fe:55:xx:xx:xx:xx` — first octet
`0xfe` = `11111110`: unicast (LSB = 0) + locally administered (bit 1 = 1).

# bumdetect

Passively sniffs one or more interfaces and reports the sources of BUM
(broadcast / unknown-unicast / multicast) flooding on a segment. It reports
every frame **not destined to this host's own interface MAC**:

- **broadcast** (`ether dst ff:ff:ff:ff:ff:ff`), including **all ARP** —
  who-has requests, is-at replies and gratuitous ARP are all counted;
- **multicast** (group bit set, not broadcast); and
- **unicast** destined to any MAC other than this host's own — unknown-unicast
  / flooded, including traffic bridged toward local VMs.

Frames to or from this host's own interface MAC are ignored.

For each matching frame it reads the source MAC (`Ether.src`) and best-effort
source IP (ARP `psrc` / IPv4 / IPv6) and increments **two independent
per-window counters** — one keyed by source MAC, one keyed by source IP. This
is a **rate detector**: every `WINDOW` seconds (default 1s) each counter is
evaluated and reset, and any source that sent **more than `THRESHOLD`**
(default 10) BUM frames in that window is logged once — a `by-mac` line names
the MAC and the IP(s) seen behind it; a `by-ip` line names the IP and the
MAC(s) it was seen with. Indexing both ways surfaces IP↔MAC mismatches
(spoofing, one MAC spraying many IPs, one IP behind many MACs). A pure-L2 frame
(no source IP) counts only toward its source MAC.

bumdetect is **read-only**: it never injects or drops a frame.

## Example log lines

```
2026-07-26 14:51:22,954 WARNING bumdetect [ens5] BUM rate by-mac mac=10:f3:11:56:6a:e1 count=1152/1s (>5) ips=156.236.252.254,156.236.253.254 (+9 more) (last: broadcast proto=arp-request src_ip=156.236.252.254 dst=ff:ff:ff:ff:ff:ff vlan=25)
2026-07-26 14:51:22,955 WARNING bumdetect [ens5] BUM rate by-ip ip=156.245.68.254 count=182/1s (>5) macs=10:f3:11:56:6a:e1 (last: broadcast proto=arp-request src_mac=10:f3:11:56:6a:e1 dst=ff:ff:ff:ff:ff:ff vlan=25)
```

`count=1152/1s (>5)` means the source sent 1152 in-scope frames in a 1-second
window, exceeding the threshold of 5.

## Configuration

All knobs are CLI flags (`--iface`, `--threshold`, `--window`, …) or env vars:

| Env var                   | Default | Meaning                                                             |
| ------------------------- | ------- | ------------------------------------------------------------------- |
| `BUMDETECT_IFACE`         | —       | Comma- or space-separated interfaces to listen on.                  |
| `BUMDETECT_LOG`           | INFO    | Log level.                                                          |
| `BUMDETECT_THRESHOLD`     | 10      | Log a source that sends MORE than this many frames within a window. |
| `BUMDETECT_WINDOW`        | 1.0     | Rate evaluation window in seconds; counters reset each window.       |
| `BUMDETECT_VLANS`         | (all)   | VLAN allow-list, see below.                                         |
| `BUMDETECT_IGNORE_MACS`   | (none)  | Source MACs whose ARP requests are ignored, see below.              |

`BUMDETECT_LOG_INTERVAL` is still honored as a legacy alias for
`BUMDETECT_THRESHOLD`.

## Run

`sudo` / `CAP_NET_RAW` is required — scapy uses `AF_PACKET` for raw sockets.

Directly:

```bash
pip install -r requirements.txt
sudo BUMDETECT_IFACE=ens5 python3 bumdetect.py
```

Via the launcher (edit `start.sh` or override env vars):

```bash
sudo ./start.sh
```

Via systemd (path in `bumdetect.service` points at `/opt/bumdetect/start.sh`):

```bash
sudo cp bumdetect.service /etc/systemd/system/
sudo systemctl enable --now bumdetect
```

## VLAN handling

The per-iface BPF filter is `not ether src <own-mac> and not ether dst
<own-mac>`. The `ether src`/`ether dst` primitives index the outer Ethernet
header, which precedes any 802.1Q tag, so the filter matches tagged and
untagged frames alike with no `vlan` clause. `BUMDETECT_VLANS` is an optional
allow-list; frames outside the listed VLANs are ignored. Empty value = all
VLANs (and untagged). Examples:

- `0` — untagged only
- `25,100` — VLAN 25 and 100
- `25-100` — VLANs 25 through 100 inclusive
- `0,25-100,200` — untagged + 25..100 + 200

## Ignoring ARP requests from specific source MACs

`BUMDETECT_IGNORE_MACS` (or `--ignore-macs`) is an optional list of source MAC
addresses whose **ARP requests only** are ignored — an ARP who-has from a listed
MAC is dropped before counting, in neither the by-mac nor the by-ip index. Every
other frame type from the same MAC (broadcast, multicast, unknown-unicast, and
ARP reply / gratuitous) is still reported under the normal rules. Use it to
silence a gateway whose who-has sweeps are expected noise without going blind to
its other traffic. The list is comma- or space-separated and case-insensitive:

```
BUMDETECT_IGNORE_MACS=10:f3:11:56:6a:e1,aa:bb:cc:dd:ee:ff
```

Because the exemption is frame-type-specific, it is applied in Python (not in
the BPF filter) — the kernel still delivers these frames so their non-request
traffic can be evaluated.

## What counts as "this host's own MAC"

Only the **sniffing interface's own hardware address** (`get_if_hwaddr(iface)`).
There is no VM-MAC awareness: unicast bridged toward a local VM is reported as
unknown-unicast like any other non-local destination. If `get_if_hwaddr` can't
resolve a MAC, the BPF filter is empty (capture all) and the to/from-us check
is skipped for that interface.

# arpreply

Defends locally-owned VM IPs against [arphole](../arphole)'s reclaim probes.
arphole broadcasts ARP `who-has` for contended IPs with a fixed sender IP
(`192.0.2.100`, RFC 5737 TEST-NET-1) and reclaims any IP that stays silent past
its probe timeout. `arpreply` runs on each **compute node** and answers those
probes for the VMs living there — with the VM's real MAC — so the IPs are seen
as occupied and never reclaimed.

**It is not a sniffer.** There is no libpcap / `AF_PACKET` capture and no
promiscuous mode. `arpreply` runs as a systemd service that, at startup,
installs a single nftables `queue` rule in the bridge datapath and then
processes packets the kernel hands it via **NFQUEUE**. Only arphole's who-has
(ARP request with sender IP `192.0.2.100`) ever reaches the process; it replies
on the VM's behalf and **drops the probe** — the VM never sees it and never
double-answers.

## How it works

- **nft interception rule** (installed at startup into a dedicated
  `table bridge arpreply`):

  ```
  ether type 0x0806 arp operation request arp saddr ip 192.0.2.100 \
      queue num 40 bypass
  ```

  Only arphole's who-has ever matches (`192.0.2.100` is reserved, so nothing
  legitimate sources ARP from it). All other traffic — including every VM data
  packet — is dispatched by the kernel without touching this process: **zero
  data-plane cost**. The rule is installed idempotently (`flush chain` +
  re-add) and removed on shutdown.

- **NFQUEUE callback** (one process, plus a refresh thread): for each queued
  probe it locates the ARP header, reads `sha` (arphole's MAC), `spa`
  (=`192.0.2.100`) and `tpa` (the queried VM IP), then:
  - **owned** → hand-pack an `is-at` (`op=2`, `hwsrc` = VM MAC, `psrc` = VM IP)
    **unicast to arphole**, send it untagged on `br<vlan>` over a raw socket,
    and verdict **DROP**. The bridge already learned arphole's MAC on
    `v-<vlan>` at ingress, so the reply egresses the uplink (re-tagged) toward
    arphole; VM taps never see it.
  - **not owned** (ipset miss) → verdict **DROP** — the who-has is suppressed.
    ⚠️ Only safe when the rule is gated (`--vlans` / `--iface`) to the
    `v-<vlan>` uplinks so it sees **trunk-inbound** probes only. On an interface
    carrying VM-sourced who-has this blackholes ARP for every non-VM
    destination (gateway, external hosts, other-node VMs). See VLAN gating
    below.

- **Fail-safe**: the `bypass` flag means if this process is down **or the queue
  is full**, matched probes are ACCEPTed (flooded) and the VM answers itself.
  Stopping arpreply degrades gracefully to the pre-existing behavior.

- **Ownership refresh** (change-driven): a background thread checks two cheap
  signals every `ARPREPLY_POLL` s and rebuilds the map **only when one changed**:
  the running-domain set (`virsh list` — VM start/stop/add/remove) and a
  fingerprint of the config-drive ISO dir (each ISO's name + **mtime**). An ISO
  is rebuilt whenever a VM's IPs change — including a **secondary-IP add**
  (`apply_second_ips.sh` regenerates the ISO) — so its mtime is a complete
  IP-change signal; no blind periodic rescan is needed (`ARPREPLY_REFRESH` is an
  optional backstop, default off). Steady state is two stat-loops per tick with
  no rebuild; the ~3 ms/ISO extraction is paid only for changed VMs. The map
  unions two on-host sources:
  1. **`sgas-<vnic>` ipsets** (`hash:ip,mac`, incl. secondary IPs) — one
     `ipset save` fork; VLAN from the vnic's `br<vlan>`.
  2. **Per-VM config-drive ISOs** under `cache/meta/<vm_ID>.iso` — the assigned
     `ip↔mac` from `openstack/latest/network_data.json` (via `isoinfo`, cached
     by mtime). This is the **only on-host source for `allow_spoofing` VMs**,
     which have no sgas set. ISOs whose `vm_ID` is **not a running `virsh`
     domain are skipped before extraction**, so `isoinfo` forks track live-VM
     count, not the total (often stale-heavy) file count — a full cold scan of
     300 live VMs is ~0.8 s regardless of how many dead-VM ISOs linger. Each
     surviving entry is still kept only if its MAC matches a **live tap** (which
     also supplies the VLAN from that tap's bridge).

  The reply frame is hand-packed (no scapy) — byte-identical to the scapy
  serialization, at ~1 µs/frame.

  > A truly-spoofed IP that an `allow_spoofing` VM uses but was never assigned
  > is unknowable on the host — with ipset-miss → DROP it would be blackholed.
  > Needs `isoinfo` (genisoimage/cdrtools); without it the ISO source is empty
  > and only sgas is used.

## Performance impact

Negligible on the node's data plane, and strictly cheaper than a sniffer:

- **VM data traffic** is never queued and never enters userspace. For each
  frame on the bridge `forward` path, our rule short-circuits on the first
  `ether type 0x0806` test — a single ethertype comparison in the nft VM (a few
  ns). That path is already traversed for the security-group
  `table bridge cloudland`, so we add one gated rule, not a new datapath.
- **No per-packet userspace cost, no packet copies, no promiscuous mode** —
  unlike the old `ETH_P_ALL` sniff, which ran a BPF program on every trunk
  frame in softirq.
- **arphole's probes** (the only packets queued) take an inline userspace
  round-trip (tens of µs). They are low-rate (arphole's sender is ~hundreds/s)
  and are control packets, not throughput. `queue ... bypass` means if arpreply
  is slow, dies, or the queue fills, probes are ACCEPTed rather than stalling
  the datapath.
- **Dropping the probe reduces load**: because the who-has never floods to the
  VM taps, each VM is spared processing it and arphole receives one is-at
  (arpreply's) instead of two (arpreply + VM).
- **Userspace CPU** scales with probe rate only; the refresh thread just checks
  two cheap signals per `ARPREPLY_POLL` (the `virsh` domain set + the ISO dir's
  name/mtime fingerprint) and rebuilds the map only when one changed (`isoinfo`
  then runs just for the changed VMs, mtime-cached).

### Proxy-ARP mode (empty `ARPREPLY_PROBE_SRC`)

With an empty probe source the nft rule drops its `arp saddr ip` match, so
**every** ARP who-has on the gated interfaces is queued — arpreply answers (and
drops) any who-has for a local VM IP, and **drops the rest too** (ipset miss →
DROP). This turns it into a general proxy-ARP / ARP-suppression agent, and makes
the `--vlans` / `--iface` gate essential (see below): without it, ARP for the
gateway and any off-node destination is blackholed.

Efficiency changes only for **ARP**, not data:

- **Non-ARP (all VM data)**: unchanged — still the single `ether type 0x0806`
  ethertype gate; nothing extra is queued.
- **ARP who-has**: now *all* of it takes an inline NFQUEUE round-trip
  (kernel→userspace→verdict, ~µs–tens of µs each, dominated by the context
  switch, not the O(1) dict lookup). Cost scales with the segment's total ARP
  request rate, which is far higher than arphole's ~hundreds/s probe stream —
  and arpreply is now on the **critical path of all ARP resolution** (even
  non-owned who-has pays the detour before being accepted).
- **Overload / floods**: `queue ... bypass` still applies — if the queue fills
  or arpreply is absent, excess who-has is ACCEPTed (floods normally), so ARP
  resolution and the data plane never stall.
- **Upside**: owned-IP who-has is answered authoritatively and dropped before
  reaching VM taps, cutting ARP broadcast flooding toward VMs.

Rule of thumb: keep the default `192.0.2.100` unless you specifically want
node-wide ARP suppression; empty mode trades a small, ARP-rate-proportional
CPU/latency cost for that behavior.

## VLAN / interface gating

Restrict which who-has is intercepted so the tool only touches the VLANs you
mean it to. This is done in the nft rule via an ingress-interface (`iifname`)
match, because the frame is untagged at the bridge `forward` hook — the VLAN is
identified by its `v-<vlan>` uplink name.

- `--vlans 25-30` / `ARPREPLY_VLANS` — only queue who-has arriving on the
  `v-25`…`v-30` uplinks (trunk-inbound). Empty = all VLANs.
- `--iface v-100 …` / `ARPREPLY_IFACE` — extra ingress interface names to
  intercept on, unioned with the `v-<vlan>` set above.

A userspace check also confirms the target IP's owned VLAN is in the allowed
set before answering. **Gating to the `v-<vlan>` uplinks is what makes the
ipset-miss → DROP behavior safe**: only trunk-inbound probes reach userspace, so
a dropped miss just isn't flooded to local taps (the real owner still answers
elsewhere). Do **not** add tap interfaces (VM-sourced ARP) to `--iface` unless
you accept that unknown destinations from those VMs will be blackholed.

## Configuration

All knobs are CLI flags (`--probe-src`, `--queue-num`, …) or matching env vars:

| Env var               | Default     | Meaning                                        |
| --------------------- | ----------- | ---------------------------------------------- |
| `ARPREPLY_PROBE_SRC`  | 192.0.2.100 | Only intercept/answer who-has from this ARP sender IP (must match arphole). **Set empty to answer who-has from ANY source** — general proxy-ARP for local VM IPs (see note below). |
| `ARPREPLY_QUEUE_NUM`  | 40          | NFQUEUE number the nft rule dispatches to.     |
| `ARPREPLY_VLANS`      | (all)       | Only intercept who-has on these VLANs' `v-<vlan>` uplinks, e.g. `25-30`. See VLAN / interface gating. |
| `ARPREPLY_IFACE`      | (none)      | Extra ingress interface names to intercept on, unioned with the `v-<vlan>` set. |
| `ARPREPLY_POLL`       | 5           | Change-check interval; the map rebuilds when the running-domain set or an ISO's mtime changes. |
| `ARPREPLY_REFRESH`    | 0           | Optional backstop full-rebuild interval; 0 = disabled (refresh is change-driven). |
| `ARPREPLY_LOG`        | INFO        | Log level.                                     |

## Run

Requires **root** (or `CAP_NET_ADMIN` for nft + NFQUEUE and `CAP_NET_RAW` for
the raw reply send). System deps: `nftables`; `libnetfilter-queue` for the
python `NetfilterQueue` module; `isoinfo` (genisoimage/cdrtools) to read the
config-drive ISOs; and `virsh` (libvirt-clients) for the VM poll. On a cloudland
compute node the last two are already present (`mkisofs`, libvirt).

```bash
# Debian/Ubuntu build/runtime deps:
sudo apt install nftables libnetfilter-queue1 libnetfilter-queue-dev \
    libnfnetlink-dev genisoimage libvirt-clients
pip install -r requirements.txt
```

Directly:

```bash
sudo python3 arpreply.py
```

Via the launcher (edit `arpreply.sh` or override env vars):

```bash
sudo ./arpreply.sh
```

Via systemd (`arpreply.service` points at `/opt/arpreply/arpreply.sh`):

```bash
sudo cp arpreply.service /etc/systemd/system/
sudo systemctl enable --now arpreply
```

## Notes

- The reply is sent **untagged** on `br<vlan>`; the `v-<vlan>` VLAN/VXLAN
  uplink re-tags it toward the physical trunk. The VLAN is derived from the
  owned IP's vnic bridge, not the (untagged) queued frame.
- The nft `table bridge arpreply` is separate from the security-group
  `table bridge cloudland`; arpreply owns and cleans up only its own table, and
  `create_sg_chain.sh` / `clear_sg_chain.sh` are untouched.
- Because interception is per-node and drops arphole's probe locally, only
  arpreply (not the VM) answers — this also halves the `is-at` load on arphole
  and removes any dependence on the VM's own ARP path (e.g. per-vnic ARP rate
  limits).

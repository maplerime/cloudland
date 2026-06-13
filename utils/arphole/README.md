# arphole

Listens for broadcast ARP "who-has" requests on a layer-2 network and
replies with a freshly generated locally-administered unicast MAC after
the same target IP has been requested THRESHOLD times within a rolling
WINDOW.

## Match logic

Pure rate-gated claiming — no database lookup. For each ARP request the
target IP counter is incremented within a rolling window (default
15 s). When the counter reaches the threshold (default 9) the program
emits an `is-at` reply with a random `fe:55:xx:xx:xx:xx` MAC, then
resets the counter for that IP; the next batch of THRESHOLD requests
triggers another reply.

## Run

```bash
pip install -r requirements.txt
sudo ARPHOLE_IFACE=eth0 \
     ARPHOLE_LOG=INFO \
     ARPHOLE_THRESHOLD=9 \
     ARPHOLE_WINDOW=15 \
     python3 arphole.py
```

`sudo` / `CAP_NET_RAW` is required (scapy uses AF_PACKET).

For production use, install `arphole.service` and edit the
`Environment=` lines.

## Random MAC policy

`rand_unicast_mac()` always emits `fe:55:xx:xx:xx:xx` — first-octet
0xfe = 11111110: unicast (LSB=0) + locally administered (bit 1=1).

#!/bin/bash
#
# Script to collect conntrack unreplied SYN connections and export as Prometheus metrics
# Output format: conntrack_unreplied_syn_flows{source_ip="x.x.x.x",target_ip="y.y.y.y",proto="tcp",state="SYN_SENT"} count
#

# Configuration
OUTPUT_DIR="${NODE_EXPORTER_TEXTFILE_DIR:-/var/lib/node_exporter}"
OUTPUT_FILE="${OUTPUT_DIR}/conntrack_unreplied_syn_flows.prom"
HOSTNAME_VAL=$(hostname -f 2>/dev/null || hostname)

# Ensure output directory exists
mkdir -p "$OUTPUT_DIR"

# Temporary file for processing
TMP_FILE=$(mktemp /tmp/conntrack_unreplied_syn.XXXXXX)
trap "rm -f $TMP_FILE" EXIT

# Collect conntrack data and process - emit source_ip/target_ip
conntrack -L 2>/dev/null \
  | grep SYN_SENT | grep UNREPLIED \
  | awk '{
      proto=$1;      # Protocol field, e.g., tcp
      state=$4;      # State field, e.g., SYN_SENT
      src=""; dst="";
      # Scan all fields to extract src=... and dst=...
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^src=/) src=$i;
        else if ($i ~ /^dst=/) dst=$i;
      }
      if (src != "" && dst != "")
        print proto, state, src, dst;
    }' \
  | sort | uniq -c | sort -nr | head -20 \
  | awk -v hostname="$HOSTNAME_VAL" '{
      count=$1;
      proto=$2;
      state=$3;
      src=$4;
      dst=$5;
      sub(/^src=/,"",src);
      sub(/^dst=/,"",dst);
      printf "conntrack_unreplied_syn_flows{source_ip=\"%s\",target_ip=\"%s\",proto=\"%s\",state=\"%s\",hostname=\"%s\"} %d\n",
             src, dst, proto, state, hostname, count;
    }' > "$TMP_FILE"

# Write to output file atomically - always overwrite to ensure fresh data
# This ensures the metrics file is refreshed on every execution
if [ -s "$TMP_FILE" ]; then
  mv "$TMP_FILE" "$OUTPUT_FILE"
else
  # If no data, create empty file to clear old metrics
  > "$OUTPUT_FILE"
fi

# Set proper permissions
chmod 644 "$OUTPUT_FILE"
chown prometheus:prometheus "$OUTPUT_FILE" 2>/dev/null || true

exit 0


#!/bin/bash
#
# Script to export packet drop and processing failure indicators from dmesg to Prometheus metrics
# Output format: packet_drop_indicators{type="drop|conntrack_full|network_error",hostname="node"} count
#

# Configuration
OUTPUT_DIR="${NODE_EXPORTER_TEXTFILE_DIR:-/var/lib/node_exporter}"
OUTPUT_FILE="${OUTPUT_DIR}/packet_drop_indicators.prom"
HOSTNAME_VAL=$(hostname -f 2>/dev/null || hostname)

# Ensure output directory exists
mkdir -p "$OUTPUT_DIR"

# Temporary file for processing
TMP_FILE=$(mktemp /tmp/packet_drop_indicators.XXXXXX)
trap "rm -f $TMP_FILE" EXIT

# Write metrics header
cat > "$TMP_FILE" << EOF
# HELP packet_drop_indicators Packet drop and processing failure indicators from recent dmesg logs
# TYPE packet_drop_indicators gauge
EOF

# Function to categorize and count packet drop indicators
process_packet_drops() {
    local cutoff_time=$(( $(awk '{print int($1)}' /proc/uptime) - 300 ))

    # Run dmesg command and process results
    dmesg | awk -v cutoff="$cutoff_time" '
    /^\[([0-9.]+)\]/ {
        ts=$1;
        gsub(/\[|\]/,"",ts);
        if(ts >= cutoff) {
            # Store the line for pattern matching
            line = $0;
            # Remove timestamp from line for cleaner processing
            sub(/^\[[0-9.]+\] /, "", line);

            # Check ONLY for nf_conntrack table full errors
            if (line ~ /nf_conntrack.*table full/) {
                print "conntrack_full " line;
            }
        }
    }' | sort | uniq -c | sort -nr | head -20
}

# Process packet drops and generate metrics
process_packet_drops | while read -r line; do
    if [[ -n "$line" ]]; then
        count=$(echo "$line" | awk '{print $1}')
        type_and_message=$(echo "$line" | cut -d' ' -f2-)

        # Determine metric type based on content
        if echo "$type_and_message" | grep -q "^conntrack_full"; then
            metric_type="conntrack_full"
        else
            metric_type="unknown"
        fi

        # Extract a short description for the label (first 50 chars)
        short_desc=$(echo "$type_and_message" | sed 's/^[a-z_]* //' | cut -c1-50 | sed 's/[^a-zA-Z0-9 _-]//g' | sed 's/ /_/g')

        printf "packet_drop_indicators{type=\"%s\",hostname=\"%s\",description=\"%s\"} %d\n" \
               "$metric_type" "$HOSTNAME_VAL" "$short_desc" "$count" >> "$TMP_FILE"
    fi
done

# If no packet drop indicators found, add a zero metric to indicate healthy state
if ! grep -q "packet_drop_indicators" "$TMP_FILE"; then
    printf "packet_drop_indicators{type=\"healthy\",hostname=\"%s\",description=\"no_packet_drops_detected\"} 0\n" \
           "$HOSTNAME_VAL" >> "$TMP_FILE"
fi

# Write to output file atomically - always overwrite to ensure fresh data
mv "$TMP_FILE" "$OUTPUT_FILE"

# Set proper permissions
chmod 644 "$OUTPUT_FILE"
chown prometheus:prometheus "$OUTPUT_FILE" 2>/dev/null || true

exit 0

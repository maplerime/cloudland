#!/bin/bash
#
# Script to export currently blocked IPs from ipset to Prometheus metrics
# Output format: ipset_blocked_ips{ip="x.x.x.x"} 1
#

# Configuration
OUTPUT_DIR="${NODE_EXPORTER_TEXTFILE_DIR:-/var/lib/node_exporter}"
OUTPUT_FILE="${OUTPUT_DIR}/ipset_blocked_ips.prom"
HOSTNAME_VAL=$(hostname -f 2>/dev/null || hostname)

# Ensure output directory exists
mkdir -p "$OUTPUT_DIR"

# Temporary file for processing
TMP_FILE=$(mktemp /tmp/ipset_blocked_ips.XXXXXX)
trap "rm -f $TMP_FILE" EXIT

# Write metrics header
cat > "$TMP_FILE" << EOF
# HELP ipset_blocked_ips Currently blocked IP addresses in ipset blacklist
# TYPE ipset_blocked_ips gauge
EOF

# Check if blacklist set exists and extract blocked IPs
if ipset list blacklist >/dev/null 2>&1; then
    ipset list blacklist | awk -v hostname="$HOSTNAME_VAL" '
    /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/ {
        # Extract IP address from ipset output
        # Format: "1.2.3.4 timeout 3600" or just "1.2.3.4"
        ip = $1
        printf "ipset_blocked_ips{ip=\"%s\",hostname=\"%s\"} 1\n", ip, hostname
    }
    ' >> "$TMP_FILE"
else
    # No blacklist set exists, just add a comment
    echo "# No blacklist ipset found - no IPs currently blocked" >> "$TMP_FILE"
fi

# Write to output file atomically - always overwrite to ensure fresh data
mv "$TMP_FILE" "$OUTPUT_FILE"

# Set proper permissions
chmod 644 "$OUTPUT_FILE"
chown prometheus:prometheus "$OUTPUT_FILE" 2>/dev/null || true

exit 0

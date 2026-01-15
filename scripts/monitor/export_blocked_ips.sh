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

# Export function: append members from a given ipset set
# Args: $1 = set name, $2 = block_type label (dst/src)
export_set() {
    local SET_NAME="$1"
    local BLOCK_TYPE="$2"

    if ipset list "$SET_NAME" >/dev/null 2>&1; then
        ipset list "$SET_NAME" | awk -v hostname="$HOSTNAME_VAL" -v block_type="$BLOCK_TYPE" '
        /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/ {
            # Extract IP address from ipset output
            # Format: "1.2.3.4 timeout 3600" or just "1.2.3.4"
            ip = $1
            printf "ipset_blocked_ips{ip=\"%s\",hostname=\"%s\",block_type=\"%s\"} 1\n", ip, hostname, block_type
        }
        ' >> "$TMP_FILE"
    else
        echo "# No ${SET_NAME} ipset found - no IPs currently blocked for ${BLOCK_TYPE}" >> "$TMP_FILE"
    fi
}

# Export both sets into the same metric file
export_set "block_dst" "dst"
export_set "block_src" "src"


# Write to output file atomically - always overwrite to ensure fresh data
mv "$TMP_FILE" "$OUTPUT_FILE"

# Set proper permissions
chmod 644 "$OUTPUT_FILE"
chown prometheus:prometheus "$OUTPUT_FILE" 2>/dev/null || true

exit 0

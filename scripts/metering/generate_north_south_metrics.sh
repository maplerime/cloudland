#!/bin/bash
set -e

# Optimized version: fetch metrics once and cache to temporary file
# This avoids repeated HTTP requests and significantly improves performance

# Load async_job_dir from cloudrc
CLOUDRC="/opt/cloudland/scripts/cloudrc"
if [ -f "$CLOUDRC" ]; then
    source "$CLOUDRC"
fi
ASYNC_JOB_DIR="${async_job_dir:-/var/lib/cloudland/meter}"

OUTPUT="/var/lib/node_exporter/north_south_metrics.prom"
METRICS_URL="http://localhost:9177/metrics"
> "$OUTPUT"

# Key optimization: fetch metrics once and save to temporary file
METRICS_CACHE="/tmp/metrics_cache_$$.txt"
curl -s "$METRICS_URL" > "$METRICS_CACHE"

# Cleanup function: ensure temporary file is deleted
cleanup() {
    rm -f "$METRICS_CACHE"
}
trap cleanup EXIT

# Read interface information from cache file instead of repeated curl
grep '^libvirt_domain_interface_meta{' "$METRICS_CACHE" | while read -r line; do
    # Parse domain, source_bridge, target_device
    domain=$(echo "$line" | grep -o 'domain="[^"]*"' | cut -d'"' -f2)
    source_bridge=$(echo "$line" | grep -o 'source_bridge="[^"]*"' | cut -d'"' -f2)
    target_device=$(echo "$line" | grep -o 'target_device="[^"]*"' | cut -d'"' -f2)

    # Skip if target_device is empty
    [ -z "$target_device" ] && continue

    # Read async_job_dir/$target_device
    meta_file="$ASYNC_JOB_DIR/$target_device"
    [ -f "$meta_file" ] || continue

    # Default values
    vm_ip=""; floating_ip=""; vm_br=""; router=""

    # Parse key-value pairs from the meta file
    for kv in $(cat "$meta_file"); do
        key="${kv%%=*}"
        val="${kv#*=}"
        case "$key" in
            vm_ip)
                vm_ip="$val"
                vm_ip="${vm_ip%%/*}"   # Keep IP only
                ;;
            floating_ip) floating_ip="$val" ;;
            vm_br) vm_br="$val" ;;
            router) router="$val" ;;
        esac
    done

    # Enhanced validation: vm_ip must be present and non-empty
    [ -z "$vm_ip" ] && continue

    # If router is empty, treat it as "0"
    [ -z "$router" ] && router="0"

    # Use different processing strategies based on router value
    if [ "$router" = "0" ]; then
        # router=0 case: use libvirt to query north-south metrics
        echo "# Processing router=0 case for domain=$domain, target_device=$target_device" >> "$OUTPUT"

        # Key optimization: grep from cache file instead of repeated curl
        # Query received bytes (inbound)
        inbound=$(grep "^libvirt_domain_interface_stats_receive_bytes_total{.*domain=\"$domain\".*target_device=\"$target_device\"" "$METRICS_CACHE" | awk '{printf "%.0f", $2}')

        # Query transmitted bytes (outbound)
        outbound=$(grep "^libvirt_domain_interface_stats_transmit_bytes_total{.*domain=\"$domain\".*target_device=\"$target_device\"" "$METRICS_CACHE" | awk '{printf "%.0f", $2}')

        # Set to 0 if query result is empty
        [ -z "$inbound" ] && inbound=0
        [ -z "$outbound" ] && outbound=0

    else
        # router≠0 case: use existing iptables query method
        echo "# Processing router=$router case for domain=$domain, target_device=$target_device" >> "$OUTPUT"

        # Query iptables for inbound/outbound (sum all lines)
        inbound=$(ip netns exec "router-$router" iptables -t nat -L PREROUTING -v -n -x 2>/dev/null | grep "$vm_ip" | awk '{sum+=$2} END{print sum+0}')
        outbound=$(ip netns exec "router-$router" iptables -t nat -L POSTROUTING -v -n -x 2>/dev/null | grep "$vm_ip" | awk '{sum+=$2} END{print sum+0}')

        # Ensure result is not empty
        [ -z "$inbound" ] && inbound=0
        [ -z "$outbound" ] && outbound=0
    fi

    # Output Prometheus metrics
    echo "domain_north_south_inbound_bytes_total{domain=\"$domain\",source_bridge=\"$source_bridge\",target_device=\"$target_device\",vm_ip=\"$vm_ip\",floating_ip=\"$floating_ip\",vm_br=\"$vm_br\",router=\"$router\"} $inbound" >> "$OUTPUT"
    echo "domain_north_south_outbound_bytes_total{domain=\"$domain\",source_bridge=\"$source_bridge\",target_device=\"$target_device\",vm_ip=\"$vm_ip\",floating_ip=\"$floating_ip\",vm_br=\"$vm_br\",router=\"$router\"} $outbound" >> "$OUTPUT"
done

# fix owner, ensure node exporter can read
chown prometheus:prometheus "$OUTPUT"

#!/bin/bash
set -e

# Load async_job_dir from cloudrc
CLOUDRC="/opt/cloudland/scripts/cloudrc"
if [ -f "$CLOUDRC" ]; then
    source "$CLOUDRC"
fi
ASYNC_JOB_DIR="${async_job_dir:-/var/lib/cloudland/meter}"

OUTPUT="/var/lib/node_exporter/north_south_metrics.prom"
METRICS_URL="http://localhost:9177/metrics"
> "$OUTPUT"

# Get all libvirt_domain_interface_meta lines
curl -s "$METRICS_URL" | grep '^libvirt_domain_interface_meta{' | while read -r line; do
    # Parse domain, source_bridge, target_device
    domain=$(echo "$line" | grep -o 'domain="[^"]*"' | cut -d'"' -f2)
    source_bridge=$(echo "$line" | grep -o 'source_bridge="[^"]*"' | cut -d'"' -f2)
    target_device=$(echo "$line" | grep -o 'target_device="[^"]*"' | cut -d'"' -f2)
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
                vm_ip="${vm_ip%%/*}"   # 只保留 IP
                ;;
            floating_ip) floating_ip="$val" ;;
            vm_br) vm_br="$val" ;;
            router) router="$val" ;;
        esac
    done

    [ -z "$vm_ip" ] || [ -z "$router" ] && continue

    # Query iptables for inbound/outbound (sum all lines)
    inbound=$(ip netns exec "$router" iptables -t nat -L PREROUTING -v -n -x 2>/dev/null | grep "$vm_ip" | awk '{sum+=$2} END{print sum+0}')
    outbound=$(ip netns exec "$router" iptables -t nat -L POSTROUTING -v -n -x 2>/dev/null | grep "$vm_ip" | awk '{sum+=$2} END{print sum+0}')

    # Output Prometheus metrics
    echo "domain_north_south_inbound_bytes_total{domain=\"$domain\",source_bridge=\"$source_bridge\",target_device=\"$target_device\",vm_ip=\"$vm_ip\",floating_ip=\"$floating_ip\",vm_br=\"$vm_br\",router=\"$router\"} $inbound" >> "$OUTPUT"
    echo "domain_north_south_outbound_bytes_total{domain=\"$domain\",source_bridge=\"$source_bridge\",target_device=\"$target_device\",vm_ip=\"$vm_ip\",floating_ip=\"$floating_ip\",vm_br=\"$vm_br\",router=\"$router\"} $outbound" >> "$OUTPUT"
done

# fix owner, ensure node exporter can read
chown prometheus:prometheus "$OUTPUT"

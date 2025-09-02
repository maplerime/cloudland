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

    # Enhanced validation: both vm_ip and router must be present and non-empty
    [ -z "$vm_ip" ] && continue
    [ -z "$router" ] && continue

    # Use different processing strategies based on router value
    if [ "$router" = "0" ]; then
        # router=0 case: use libvirt to query north-south metrics
        echo "# Processing router=0 case for domain=$domain, target_device=$target_device" >> "$OUTPUT"

        # Query rx/tx bytes for this device from libvirt metrics
        # Query received bytes (inbound)
        inbound=$(curl -s "$METRICS_URL" | grep "^libvirt_domain_interface_stats_receive_bytes_total{.*domain=\"$domain\".*target_device=\"$target_device\"" | awk '{printf "%.0f", $2}')

        # Query transmitted bytes (outbound)
        outbound=$(curl -s "$METRICS_URL" | grep "^libvirt_domain_interface_stats_transmit_bytes_total{.*domain=\"$domain\".*target_device=\"$target_device\"" | awk '{printf "%.0f", $2}')

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

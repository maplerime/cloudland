#!/bin/bash

source /opt/cloudland/scripts/cloudrc

domain=$1
limit_percent=$2

if [ -z "$domain" ] || [ -z "$limit_percent" ]; then
    echo "Usage: $0 <domain_name> <limit_percent>"
    echo "  limit_percent: CPU limit percentage (0-100), or 'restore' to remove limits"
    exit 1
fi

# Check if the virtual machine exists
if ! virsh dominfo "$domain" >/dev/null 2>&1; then
    echo "Domain $domain does not exist"
    exit 1
fi

# If restore operation, remove CPU limits
if [ "$limit_percent" = "restore" ]; then
    echo "Restoring CPU resources for $domain (removing limits)"

    # Get CPU count and period
    vcpu_count=$(virsh dominfo "$domain" | grep "CPU(s)" | awk '{print $2}')
    current_period=$(virsh schedinfo "$domain" | grep vcpu_period | awk '{print $3}')

    if [ -z "$current_period" ] || [ "$current_period" -eq 0 ]; then
        current_period=100000
    fi

    # Set quota to cores * period, equivalent to full speed (100% × cores)
    # full_speed_quota=$((current_period * vcpu_count))

    # Restore to truly unlimited state (KVM unlimited special value)
    full_speed_quota=17592186044415

    echo "Setting full speed quota: $full_speed_quota (${current_period} * ${vcpu_count} cores = 100% per core)"

    # Set runtime configuration
    virsh schedinfo "$domain" --set vcpu_quota=$full_speed_quota --live
    live_result=$?

    # Set persistent configuration
    virsh schedinfo "$domain" --set vcpu_quota=$full_speed_quota --config
    config_result=$?

    if [ $live_result -eq 0 ] && [ $config_result -eq 0 ]; then
        echo "Successfully restored CPU resources for $domain (full speed)"
        # Verify result
        echo "Current configuration after restore:"
        virsh schedinfo "$domain" | grep -E 'vcpu_quota|vcpu_period'
        exit 0
    else
        echo "Failed to restore CPU resources for $domain"
        [ $live_result -ne 0 ] && echo "  - Live restore failed"
        [ $config_result -ne 0 ] && echo "  - Persistent restore failed"
        exit 1
    fi
fi

# Validate limit percentage range
if [ "$limit_percent" -lt 1 ] || [ "$limit_percent" -gt 100 ]; then
    echo "Invalid limit percentage: $limit_percent (must be 1-100)"
    exit 1
fi

# Get CPU count
vcpu_count=$(virsh dominfo "$domain" | grep "CPU(s)" | awk '{print $2}')
if [ -z "$vcpu_count" ] || [ "$vcpu_count" -eq 0 ]; then
    echo "Error: Cannot get CPU count for domain $domain"
    exit 1
fi
echo "Domain $domain has $vcpu_count vCPU(s)"

# Get current vcpu_period value
current_period=$(virsh schedinfo "$domain" | grep vcpu_period | awk '{print $3}')
if [ -z "$current_period" ] || [ "$current_period" -eq 0 ]; then
    # If not available or 0, use default value 100000
    current_period=100000
    echo "Using default vcpu_period: $current_period"
else
    echo "Current vcpu_period: $current_period"
fi

# Calculate quota value (period * cores * percentage)
# For multi-core CPUs, total quota should be period * cores * percentage
quota_value=$((current_period * vcpu_count * limit_percent / 100))
echo "Calculated vcpu_quota: $quota_value (${limit_percent}% of ${current_period} * ${vcpu_count} cores)"

# Set both live and config configurations
echo "Setting CPU limit for $domain to ${limit_percent}%..."

# Set runtime configuration (takes effect immediately)
virsh schedinfo "$domain" --set vcpu_quota=$quota_value --live
live_result=$?

# Set persistent configuration (takes effect after restart)
virsh schedinfo "$domain" --set vcpu_quota=$quota_value --config
config_result=$?

# Check execution result
if [ $live_result -eq 0 ] && [ $config_result -eq 0 ]; then
    echo "Successfully set CPU limit for $domain to ${limit_percent}%"

    # Verify configuration result
    echo "Current live configuration:"
    virsh schedinfo "$domain" | grep vcpu_quota

    echo "Persistent configuration:"
    virsh dumpxml "$domain" --inactive | sed -n '/<cputune>/,/<\/cputune>/p'

    exit 0
else
    echo "Failed to set CPU limit for $domain"
    [ $live_result -ne 0 ] && echo "  - Live configuration failed"
    [ $config_result -ne 0 ] && echo "  - Persistent configuration failed"
    exit 1
fi

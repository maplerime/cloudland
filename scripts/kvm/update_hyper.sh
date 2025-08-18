#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 6 ] && echo "$0 <hyper_status> <zone_name> <restart_clet> <cpu_over_ratio> <mem_over_ratio> <disk_over_ratio>"

hyper_status=$1
zone_name=$2
restart_clet=$3
new_cpu_over_ratio=$4
new_mem_over_ratio=$5
new_disk_over_ratio=$6

RCLOCAL="/opt/cloudland/scripts/cloudrc.local"
CLOUDLET_CONF="/etc/sysconfig/cloudlet"

if [ $hyper_status -eq 0 ]; then
    # disble hypervisor
    if [ -f "$run_dir/disabled" ]; then
        log_debug "$SCI_CLIENT_ID" "Hypervisor is already disabled"
    else
        touch "$run_dir/disabled"
    fi
else
    # enable hypervisor
    if [ -f "$run_dir/disabled" ]; then
        rm -f "$run_dir/disabled"
    fi
fi
# restart_clet is 1 means the zone name in cloudlet config file needs to be updated
# then restart cloudlet service at the end of this script
if [ $restart_clet -eq 1 ]; then
    if [ -z "$zone_name" ]; then
        log_debug "$SCI_CLIENT_ID" "Zone name is empty, not updating hypervisor zone"
        restart_clet=0
    else
        # update hypervisor zone
        # replace the line that starts with "ZONE_NAME=" in the cloudlet config file
        if [ -f "$CLOUDLET_CONF" ]; then
            sed -i "s/^ZONE_NAME=.*/ZONE_NAME=\"$zone_name\"/" "$CLOUDLET_CONF"
            log_debug "$SCI_CLIENT_ID" "Updated hypervisor zone to $zone_name"
        else
            log_debug "$SCI_CLIENT_ID" "Cloudlet config file not found, cannot update zone name"
            restart_clet=0
        fi
    fi
fi

# update hypervisor resource over ratio in /opt/cloudland/scripts/cloudrc.local
if [ -f $RCLOCAL ]; then
    [ -n "$new_cpu_over_ratio" ] && [ "$cpu_over_ratio" != "$new_cpu_over_ratio" ] && sed -i "s/^cpu_over_ratio=.*/cpu_over_ratio=$new_cpu_over_ratio/" $RCLOCAL
    [ -n "$new_mem_over_ratio" ] && [ "$mem_over_ratio" != "$new_mem_over_ratio" ] && sed -i "s/^mem_over_ratio=.*/mem_over_ratio=$new_mem_over_ratio/" $RCLOCAL
    [ -n "$new_disk_over_ratio" ] && [ "$disk_over_ratio" != "$new_disk_over_ratio" ] && sed -i "s/^disk_over_ratio=.*/disk_over_ratio=$new_disk_over_ratio/" $RCLOCAL
fi

# restart cloudlet service if needed
if [ $restart_clet -eq 1 ]; then
    log_debug "$SCI_CLIENT_ID" "Restarting cloudlet service"
    systemctl restart cloudlet
else
    log_debug "$SCI_CLIENT_ID" "No need to restart cloudlet service"
fi
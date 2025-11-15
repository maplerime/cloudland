#!/bin/bash

cd `dirname $0`
source ../cloudrc

# Support unidirectional bandwidth setting (via optional parameter)
# Usage:
# $0 <vm_ID> <nic_name> <inbound> <outbound>                    # Set bidirectional bandwidth (original functionality)
# $0 <vm_ID> <nic_name> <inbound> <outbound> --inbound-only    # Set inbound bandwidth only
# $0 <vm_ID> <nic_name> <inbound> <outbound> --outbound-only   # Set outbound bandwidth only

[ $# -lt 4 ] && echo "$0 <vm_ID> <nic_name> <inbound> <outbound> [--inbound-only|--outbound-only]" && exit -1

vm_ID=inst-$1
nic_name=$2
inbound=$3
outbound=$4
mode=${5:-""}  # Optional fifth parameter with default empty string

# Check if it's unidirectional setting mode
if [ "$mode" = "--inbound-only" ]; then
    # Set inbound bandwidth only
    inbound_rate=$(( $inbound * 125 )) # in kilobytes per second
    inbound_peak=$(( $inbound_rate * 2 ))
    inbound_burst=$inbound_rate
    virsh domiftune $vm_ID $nic_name --inbound $inbound_rate,$inbound_peak,$inbound_burst --config --live
elif [ "$mode" = "--outbound-only" ]; then
    # Set outbound bandwidth only
    outbound_rate=$(( $outbound * 125 )) # in kilobytes per second
    outbound_peak=$(( $outbound_rate * 2 ))
    outbound_burst=$outbound_rate
    virsh domiftune $vm_ID $nic_name --outbound $outbound_rate,$outbound_peak,$outbound_burst --config --live
else
    # Default mode: set bidirectional bandwidth (keep original functionality unchanged)
inbound_burst=$(( $inbound / 8 ))
inbound_rate=$(( $inbound * 125 )) # in kilobytes per second
inbound_peak=$(( $inbound_rate * 2 ))
inbound_burst=$inbound_rate
outbound_rate=$(( $outbound * 125 )) # in kilobytes per second
outbound_peak=$(( $outbound_rate * 2 ))
outbound_burst=$outbound_rate
virsh domiftune $vm_ID $nic_name --inbound $inbound_rate,$inbound_peak,$inbound_burst --outbound $outbound_rate,$outbound_peak,$outbound_burst --config --live

    # Update bandwidth configuration metrics (only for bidirectional mode)
    /opt/cloudland/scripts/kvm/update_vm_interface_bandwidth.sh add "$vm_ID" "$nic_name" "$inbound" "$outbound" 2>&1 | logger -t set_nic_speed || true
fi

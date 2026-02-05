#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 3 ] && die "$0 <vm_ID> <cpu> <memory>"

ID=$1
vm_ID=inst-$1
vm_cpu=$2
vm_mem=$3
state=error

./action_vm.sh $ID hard_stop
let vm_mem=${vm_mem%[m|M]}*1024

# backup vm xml
vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
mv $vm_xml $vm_xml-$(date +'%s.%N')
virsh dumpxml $vm_ID >$vm_xml

# Try to undefine the domain, handle NVRAM case
virsh undefine $vm_ID 2>/dev/null
if [ $? -ne 0 ]; then
    # If normal undefine fails, try with --nvram (for UEFI VMs)
    virsh undefine --nvram $vm_ID
    if [ $? -ne 0 ]; then
        echo "Warning: Failed to undefine domain $vm_ID, continuing anyway..."
    fi
fi

# edit vm xml
sed_cmd="s#>.*</memory>#>$vm_mem</memory>#g; s#>.*</currentMemory>#>$vm_mem</currentMemory>#g; s#>.*</vcpu>#>$vm_cpu</vcpu>#g; /<topology/s/cores='[0-9]*'/cores='$vm_cpu'/"
sed -i "$sed_cmd" $vm_xml
virsh define $vm_xml
virsh autostart $vm_ID
virsh start $vm_ID
[ $? -eq 0 ] && state=running
echo "|:-COMMAND-:| inst_status.sh '$SCI_CLIENT_ID' '$ID $state'"

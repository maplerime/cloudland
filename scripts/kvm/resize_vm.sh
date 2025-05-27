#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 6 ] && die "$0 <vm_ID> <cpu> <memory>"

ID=$1
vm_ID=inst-$1
vm_cpu=$2
vm_mem=$3

./action_vm.sh $ID hard_stop
let vm_mem=${vm_mem%[m|M]}*1024
virsh setmaxmem $vm_ID $vm_mem --config
virsh setmem $vm_ID $vm_mem --config
virsh setvcpus $vm_ID --count $vm_cpu --config
virsh setvcpus $vm_ID --count $vm_cpu --config --maximum
virsh setvcpus $vm_ID --count $vm_cpu --config
virsh start $vm_ID
[ $? -eq 0 ] && virsh dumpxml $vm_ID > $xml_dir/$vm_ID/${vm_ID}.xml
echo "|:-COMMAND-:| inst_status.sh '$SCI_CLIENT_ID' '$ID running'"
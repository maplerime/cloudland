#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 4 ] && echo "$0 <vm_ID> <mac> <os_code> <update_meta> [primary_ip] [gateway]" && exit -1

ID=$1
vm_ID=inst-$ID
mac=$2
os_code=$3
update_meta=$4
primary_ip=$5
gateway=${6%/*}
more_addresses=$(cat)
naddrs=$(jq length <<< "$more_addresses")

# Windows uses family-aware netsh commands through the guest agent.
if [ "$os_code" = windows ]; then
    wait_qemu_ping "$ID" 10
    python3 ../windows_network.py "$vm_ID" "$primary_ip" "$gateway" "${more_addresses:-[]}"
fi

# Rebuild the metadata ISO for both Windows and Linux when update_meta is set,
# so the address config is persisted for the next boot. Linux additionally
# re-runs cloud-init to apply it live.
if [ "$update_meta" = "true" ]; then
    tmp_mnt=/tmp/mnt-$vm_ID
    working_dir=/tmp/$vm_ID
    latest_dir=$working_dir/openstack/latest
    mkdir -p $tmp_mnt $working_dir
    mount ${cache_dir}/meta/${vm_ID}.iso $tmp_mnt
    cp -r $tmp_mnt/* $working_dir
    net_json=$(cat $latest_dir/network_data.json)
    new_net_json=$(python3 ../update_network_data.py "$primary_ip" "$gateway" "${more_addresses:-[]}" <<< "$net_json") || { umount "$tmp_mnt"; exit 1; }
    printf '%s\n' "$new_net_json" > "$latest_dir/network_data.json"
    umount $tmp_mnt
    mkisofs -quiet -R -J -V config-2 -o ${cache_dir}/meta/${vm_ID}.iso $working_dir &> /dev/null
    rm -rf $working_dir
    if [ "$os_code" = "linux" ]; then
        virsh qemu-agent-command "$vm_ID" '{"execute": "guest-exec", "arguments": {"path": "/usr/bin/cloud-init", "arg": ["clean", "--reboot"]}}'
    fi
fi

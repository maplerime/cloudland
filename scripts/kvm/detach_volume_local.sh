#!/bin/bash

cd $(dirname $0)
source ../cloudrc

[ $# -lt 3 ] && echo "$0 <vm_ID> <volume_ID> <volume_UUID>" && exit -1

ID=$1
vm_ID=inst-$1
vol_ID=$2
vol_UUID=$3
vol_xml=$xml_dir/$vm_ID/disk-${vol_ID}.xml
vol_path=$(xmllint --xpath "string(//disk/source/@file)" "$vol_xml" 2>/dev/null)

if [ "$(virsh domstate $vm_ID 2>/dev/null | sed 's/shut off/shut_off/g')" = "running" ]; then
    # detach-device may return before QEMU finishes unplugging the live device.
    # Only report success after the live XML no longer contains this disk.
    log_debug $ID "detach volume($vol_ID): detach live device"
    virsh detach-device "$vm_ID" "$vol_xml" --live
    if [ $? -ne 0 ]; then
        log_debug $ID "detach volume($vol_ID): failed to detach live device"
        echo "|:-COMMAND-:| $(basename $0) '$ID' '$vol_ID' 'attached'"
        exit -1
    fi

    log_debug $ID "detach volume($vol_ID): wait for live XML removal within 30s"
    xml_removed=false
    i=0
    while [ $i -lt 30 ]; do
        live_xml=$(virsh dumpxml "$vm_ID" 2>/dev/null)
        device_exists=$(echo "$live_xml" | xmllint --xpath "boolean(//disk[source/@file='$vol_path'])" - 2>/dev/null)
        if [ $? -eq 0 ] && [ "$device_exists" = "false" ]; then
            xml_removed=true
            break
        fi

        sleep 1
        let i=i+1
    done
    if [ "$xml_removed" != "true" ]; then
        log_debug $ID "detach volume($vol_ID): live XML still contains $vol_path after 30s"
        echo "|:-COMMAND-:| $(basename $0) '$ID' '$vol_ID' 'attached'"
        exit -1
    fi

    # After live unplug is confirmed, remove the persistent config entry.
    log_debug $ID "detach volume($vol_ID): detach config device"
    virsh detach-device "$vm_ID" "$vol_xml" --config
    if [ $? -ne 0 ]; then
        log_debug $ID "detach volume($vol_ID): failed to detach config device"
        echo "|:-COMMAND-:| $(basename $0) '$ID' '$vol_ID' 'attached'"
        exit -1
    fi
else
    log_debug $ID "detach volume($vol_ID): VM is not running, detach config device"
    virsh detach-device "$vm_ID" "$vol_xml" --config --persistent
    if [ $? -ne 0 ]; then
        log_debug $ID "detach volume($vol_ID): failed to detach config device"
        echo "|:-COMMAND-:| $(basename $0) '$ID' '$vol_ID' 'attached'"
        exit -1
    fi
fi

vm_xml=$xml_dir/$vm_ID/$vm_ID.xml
log_debug $ID "detach volume($vol_ID): refresh VM XML cache"
virsh dumpxml --security-info $vm_ID 2>/dev/null | sed "s/autoport='yes'/autoport='no'/g" > $vm_xml.dump && mv -f $vm_xml.dump $vm_xml
echo "|:-COMMAND-:| $(basename $0) '$ID' '$vol_ID' 'available'"

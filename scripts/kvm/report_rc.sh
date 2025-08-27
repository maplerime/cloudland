#!/bin/bash

cd `dirname $0`
source ../cloudrc

exec <&-

cpu=0
total_cpu=$(cat /proc/cpuinfo | grep -c processor)
memory=0
[ -z "$system_reserved_memory" ] && system_reserved_memory=4000000
total_memory=$(( $(free | grep 'Mem:' | awk '{print $2}') - $system_reserved_memory ))
disk=0
disk_info=$(df -B 1 $image_dir | tail -1)
total_disk=$(echo $disk_info | awk '{print $2}')
mount_point=$(echo $disk_info | awk '{print $6}')
network=0
total_network=0
load=$(w | head -1 | cut -d',' -f5 | cut -d'.' -f1 | xargs)
total_load=0
vtep_ip=$(ifconfig $vxlan_interface | grep 'inet ' | awk '{print $2}')

function probe_arp()
{
    cd /opt/cloudland/cache/router
    for router in *; do
        ID=${router##router-}
        ext_ips=$(sudo ip netns exec $router ip addr show te-$ID | grep 'inet ' | awk '{print $2}')
        ext_mac=$(sudo ip netns exec $router ip -o link show te-$ID | awk '{print $17}')
        for ip in $ext_ips; do
            sudo ip netns exec $router arping -c 1 -I te-$ID ${ip%%/*}
        done
    done
    cd -
}

function daily_job()
{
    daily_state_file=$run_dir/daily_state_file
    current_date=$(date +%Y%m%d)
    if [ -f "$daily_state_file" ]; then
        last_run_date=$(cat $daily_state_file)
    fi
    if [ "$last_run_date" != "$current_date" ]; then
        sudo ./operation/cleanup_outdated_iptables.sh
        echo "$current_date" >$daily_state_file
    fi
}

function halfday_job()
{
    local state_file="$run_dir/halfday_state_file"
    local current_halfday=$(date +"%Y%m%d-%p")  # e.g., 20250807-AM or 20250807-PM

    if [[ -f "$state_file" ]]; then
        local last_halfday=$(< "$state_file")
        [[ "$last_halfday" == "$current_halfday" ]] && return
    fi

    ./generate_vm_instance_map.sh full
    echo "$current_halfday" > "$state_file"
}

function inst_status()
{
    old_inst_list=$(cat $image_dir/old_inst_list 2>/dev/null)
    all_inst_list=$(sudo virsh list --all | tail -n +3 | cut -d' ' -f3-)
    shutoff_list=$(sudo virsh list --all | grep 'shut off' | awk '{print $2}')
    for inst in $shutoff_list; do
        echo "$all_inst_list" | grep -q $inst-rescue
	[ $? -eq 0 ] && all_inst_list=$(echo "$all_inst_list" | grep -v $inst-rescue | sed "s/$inst.*shut off/$inst rescuing/")
    done
    n=0
    export inst_list=""
    while read line; do
        inst_stat=$(echo $line | sed 's/inst-//g;s/shut off/shut_off/')
        inst_list="$inst_stat $inst_list"
	if [ $n -eq 10 ]; then
            n=0
	    echo "|:-COMMAND-:| inst_status.sh '$SCI_CLIENT_ID' '$inst_list'"
            inst_list=""
        fi
        let n=$n+1
    done <<<$all_inst_list
    [ -n "$inst_list" ] && echo "|:-COMMAND-:| inst_status.sh '$SCI_CLIENT_ID' '$inst_list'"
}

function vlan_status()
{
    cd /opt/cloudland/cache/dnsmasq
    old_vlan_list=$(cat old_vlan_list 2>/dev/null)
    vlan_list=$(ls | grep vlan | grep -v old_vlan_list | xargs | sed 's/vlan//g')
    [ "$vlan_list" = "$old_vlan_list" ] && return
    vlan_arr=($vlan_list)
    nlist=$(ip netns list | grep vlan | cut -d' ' -f1 | xargs | sed 's/vlan//g')
    vlan_status_list=""
    for var in ${vlan_arr[*]}; do
        status="INACTIVE"
        [[ $nlist =~ $var ]] && status="ACTIVE"
        first=""
        [[ -d "vlan$var" ]] && [[ -f "vlan$var/vlan$var.FIRST" ]] && first="FIRST"
        second=""
        [[ -d "vlan$var" ]] && [[ -f "vlan$var/vlan$var.SECOND" ]] && second="SECOND"
        vlan_status_list="$vlan_status_list $var:$status:$first:$second"
    done
    vlan_status_list=$(echo $vlan_status_list | sed -e 's/^[ ]*//g')
    [ -n "$vlan_status_list" ] && echo "|:-COMMAND-:| vlan_status.sh '$SCI_CLIENT_ID' '$vlan_status_list'"
    echo "$vlan_list" >old_vlan_list
}

function router_status()
{
    cd /opt/cloudland/cache/router
    old_router_list=$(cat old_router_list 2>/dev/null)
    router_list=$(ls router* 2>/dev/null)
    router_list=$(echo "$router_list $(sudo ip netns list | grep router | cut -d' ' -f1)" | xargs | sed 's/router-//g')
    [ "$router_list" = "$old_router_list" ] && return
    [ -n "$router_list" ] && echo "|:-COMMAND-:| router_status.sh '$SCI_CLIENT_ID' '$router_list'"
    echo "$router_list" >old_router_list
}

function sync_instance()
{
    flag_file=$run_dir/need_to_sync
    boot_file=/proc/sys/kernel/random/boot_id
    diff $flag_file $boot_file
    [ $? -eq 0 ] && return
    sudo iptables-restore </etc/iptables.rules
    bridges=$(cat /proc/net/dev | grep br | awk -F: '{print $1}')
    sudo iptables -N secgroup-chain && sudo iptables -A secgroup-chain -j ACCEPT
    for bridge in $bridges; do
	sudo iptables -C FORWARD -i $bridge -o $bridge -j ACCEPT
	[ $? -ne 0 ] && sudo iptables -I FORWARD 2 -i $bridge -o $bridge -j ACCEPT
    done
    insts=$(ls $xml_dir)
    for inst in $insts; do
	inst_id=${inst/inst-/}
        for i in {1..10}; do
            ls /var/run/wds/instance-${inst_id}*
            [ $? -eq 0 ] && break
            sleep 2
        done
        sudo virsh start inst-$inst_id
        echo "|:-COMMAND-:| launch_vm.sh '$inst_id' 'running' '$SCI_CLIENT_ID' 'sync'"
    done
    sudo cp $boot_file $flag_file
}

function sync_delayed_job()
{
    for f in $(ls $async_job_dir/*.done); do
        cat $f
	sudo rm -f $f
    done
}

function calc_resource()
{
    virtual_cpu=0
    virtual_memory=0
    virtual_disk=0
    for xml in $(ls $xml_dir/*/*.xml 2>/dev/null); do
        vcpu=$(xmllint --xpath 'string(/domain/vcpu)' $xml)
        vmem=$(xmllint --xpath 'string(/domain/memory)' $xml)
        [ -n "$vcpu" ] && let virtual_cpu=$virtual_cpu+$vcpu
        [ -n "$vmem" ] && let virtual_memory=$virtual_memory+$vmem
    done
    disk=10000000000000
    total_disk=10000000000000
    if [ -z "$wds_address" ]; then
        used_disk=$(sudo du -s $image_dir | awk '{print $1}')
        for disk in $(ls $image_dir/* 2>/dev/null); do
            if [[ "$disk" = "/opt/cloudland/cache/instance/old_inst_list" ]]; then
                continue
            fi
            vdisk=$(qemu-img info --force-share $disk | grep 'virtual size:' | cut -d' ' -f3 | tr -d '(')
            [ -z "$vdisk" ] && continue
            let virtual_disk=$virtual_disk+$vdisk
        done
        let virtual_disk=virtual_disk*1024*1024*1024
        total_used_disk=$(sudo du -s $mount_point | awk '{print $1}')
        total_disk=$(echo "($total_disk-$total_used_disk+$used_disk)*$disk_over_ratio" | bc)
        total_disk=${total_disk%.*}
        disk=$(echo "$total_disk-$virtual_disk" | bc)
        disk=${disk%.*}
        [ $disk -lt 0 ] && disk=0
    fi
    total_cpu=$(echo "$total_cpu*$cpu_over_ratio" | bc)
    total_cpu=${total_cpu%.*}
    cpu=$(echo "$total_cpu-$virtual_cpu" | bc)
    cpu=${cpu%.*}
    [ $cpu -lt 0 ] && cpu=0
    total_memory=$(echo "$total_memory*$mem_over_ratio" | bc)
    total_memory=${total_memory%.*}
    memory=$(echo "$total_memory-$virtual_memory" | bc)
    memory=${memory%.*}
    [ $memory -lt 0 ] && memory=0
    if [ $(( $(date +"%s") % 10 )) -gt 7 ]; then
	rm -f $run_dir/old_resource_list
    fi
    state=1
    if [ -f "$run_dir/disabled" ]; then
        echo "cpu=0/$total_cpu memory=0/$total_memory disk=0/$total_disk network=$network/$total_network load=$load/$total_load"
        state=0
    else
        echo "cpu=$cpu/$total_cpu memory=$memory/$total_memory disk=$disk/$total_disk network=$network/$total_network load=$load/$total_load"
    fi
    cd /opt/cloudland/run
    let disk=$disk/1000*1000
    let total_disk=$total_disk/1000*1000
    old_resource_list=$(cat old_resource_list 2>/dev/null)
    resource_list="'$cpu' '$total_cpu' '$memory' '$total_memory' '$disk' '$total_disk' '$state'"
    echo "'$cpu' '$total_cpu' '$memory' '$total_memory' '$disk' '$total_disk' '$state'" >/opt/cloudland/run/old_resource_list
    [ "$resource_list" = "$old_resource_list" ] && return
    echo "|:-COMMAND-:| hyper_status.sh '$SCI_CLIENT_ID' '$HOSTNAME' '$cpu' '$total_cpu' '$memory' '$total_memory' '$disk' '$total_disk' '$state' '$vtep_ip' '$ZONE_NAME' '$cpu_over_ratio' '$mem_over_ratio' '$disk_over_ratio'"
}

calc_resource
sync_instance
sync_delayed_job
#probe_arp >/dev/null 2>&1
inst_status
daily_job
halfday_job
#vlan_status
#router_status

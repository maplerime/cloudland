#!/bin/bash

base_dir=$(dirname $0)
cd $base_dir
source ../cloudrc

exec <&-

cpu=0
total_cpu=$(cat /proc/cpuinfo | grep -c processor)
memory=0
if [ -z "$system_reserved_memory" ]; then
    let system_reserved_memory=$(cat /proc/meminfo | grep MemTotal | awk '{print $2}')/4
    [ $system_reserved_memory -gt 32000000 ] && system_reserved_memory=32000000
fi
total_memory=$(( $(free | grep 'Mem:' | awk '{print $2}') - $system_reserved_memory ))
disk=0
disk_info=$(df -B 1 $image_dir | tail -1)
total_disk=$(echo $disk_info | awk '{print $2}')
mount_point=$(echo $disk_info | awk '{print $6}')
network=0
total_network=0
load_1m=$(awk '{print $1}' /proc/loadavg)
load_5m=$(awk '{print $2}' /proc/loadavg)
load_15m=$(awk '{print $3}' /proc/loadavg)
load=$(echo "$load_5m" | cut -d'.' -f1)
[ -z "$load" ] && load=0
total_load=0
# Hugepage collection
hp_2m_free=$(cat /sys/kernel/mm/hugepages/hugepages-2048kB/free_hugepages 2>/dev/null || echo 0)
hp_2m_total=$(cat /sys/kernel/mm/hugepages/hugepages-2048kB/nr_hugepages 2>/dev/null || echo 0)
hp_1g_free=$(cat /sys/kernel/mm/hugepages/hugepages-1048576kB/free_hugepages 2>/dev/null || echo 0)
hp_1g_total=$(cat /sys/kernel/mm/hugepages/hugepages-1048576kB/nr_hugepages 2>/dev/null || echo 0)
hp_size_kb=0
[ "$hp_2m_total" -gt 0 ] 2>/dev/null && hp_size_kb=2048
[ "$hp_1g_total" -gt 0 ] 2>/dev/null && hp_size_kb=1048576
# CPU idle percentage
cpu_idle=$(top -bn1 2>/dev/null | grep 'Cpu(s)' | awk '{for(i=1;i<=NF;i++) if($i~/id/) print $(i-1)}' | tr -d '%,' || echo 100)
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
        sudo $base_dir/operation/cleanup_outdated_iptables.sh >>$log_dir/iptables_cleanup.log 2>&1
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
    inst_list_file=$image_dir/old_inst_list
    old_state_time=$(stat -c %W $inst_list_file)
    [ -z "$old_state_time" ] && old_state_time=0
    current_time=$(date +"%s")
    [ $(( $current_time - $old_state_time )) -gt 600 ] && rm -f $inst_list_file
    old_inst_list=$(cat $inst_list_file 2>/dev/null)
    all_inst_list=$(sudo virsh list --all | tail -n +3 | cut -d' ' -f3-)
    shutoff_list=$(sudo virsh list --all | grep 'shut off' | awk '{print $2}')
    for inst in $shutoff_list; do
        echo "$all_inst_list" | grep -q $inst-rescue
	[ $? -eq 0 ] && all_inst_list=$(echo "$all_inst_list" | grep -v $inst-rescue | sed "s/$inst.*shut off/$inst rescuing/")
    done
    n=0
    export inst_list=""
    all_inst_list=$(echo "$all_inst_list" | sed 's/inst-//g;s/-rescue//g;s/shut off/shut_off/g')
    while read line; do
        grep -q "$line" <<<$old_inst_list
        [ $? -eq 0 ] && continue
        inst_list="$line $inst_list"
        if [ $n -eq 10 ]; then
            n=0
            inst_list=$(echo $inst_list)
            echo "|:-COMMAND-:| inst_status.sh '$SCI_CLIENT_ID' '$inst_list'"
            inst_list=""
        fi
        let n=$n+1
    done <<<$all_inst_list
    echo "$all_inst_list" >$inst_list_file
    inst_list=$(echo $inst_list)
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

function check_system_router()
{
    sudo ip netns exec router-0 ip r | grep default
    if [ $? -ne 0 ]; then
        sudo -E bash -c "echo '|:-COMMAND-:|' system_router.sh \'$SCI_CLIENT_ID\' \'$HOSTNAME\' >$async_job_dir/system_router.done"
    fi
}

function check_conntrack()
{
    inst_list_file=$image_dir/old_inst_list
    [ -z "$syn_threshold_src_dst" ] && syn_threshold_src_dst=1500
    [ -z "$syn_threshold_src" ] && syn_threshold_src=3000
    [ -z "$syn_threshold_dst" ] && syn_threshold_dst=5000
    [ -z "$base_conn_num" ] && base_conn_num=1000000
    inst_num=$(wc -l <$inst_list_file)
    if [ "$inst_num" -gt 0 ]; then
        syn_threshold_dst=$(($base_conn_num/$inst_num))
        [ "$syn_threshold_dst" -lt 4000 ] && syn_threshold_dst=4000
        [ "$syn_threshold_dst" -gt 20000 ] && syn_threshold_dst=20000
        syn_threshold_src=$(($syn_threshold_dst * 3 / 5))
        syn_threshold_src_dst=$(($syn_threshold_src/2))
    fi
    sudo $base_dir/operation/check_halfopen_connections.sh $syn_threshold_src_dst $syn_threshold_src $syn_threshold_dst
}

function check_sync_flag()
{
    flag_file=$run_dir/need_to_sync
    boot_file=/proc/sys/kernel/random/boot_id
    diff $flag_file $boot_file
    return $?
}

function recover_loadbalancer()
{
    check_sync_flag
    [ $? -eq 0 ] && return
    echo "|:-COMMAND-:| recover_loadbalancer.sh '$SCI_CLIENT_ID'"
    sudo cp $boot_file $flag_file
}

function sync_instance()
{
    check_sync_flag
    [ $? -eq 0 ] && return
    sudo iptables-restore </etc/iptables.rules
    bridges=$(cat /proc/net/dev | grep br | awk -F: '{print $1}')
    sudo iptables -N secgroup-chain && sudo iptables -A secgroup-chain -j ACCEPT
    for bridge in $bridges; do
	sudo iptables -C FORWARD -i $bridge -o $bridge -j ACCEPT
	[ $? -ne 0 ] && sudo iptables -I FORWARD 2 -i $bridge -o $bridge -j ACCEPT
    done
    sudo iptables -D FORWARD -j REJECT --reject-with icmp-host-prohibited
    sudo iptables -A FORWARD -j REJECT --reject-with icmp-host-prohibited
    insts=$(ls $xml_dir)
    for inst in $insts; do
	inst_id=${inst/inst-/}
        for i in {1..100}; do
            ls /var/run/wds/instance-${inst_id}*
            [ $? -eq 0 ] && break
            sleep 2
        done
        sudo virsh start inst-$inst_id
        echo "|:-COMMAND-:| launch_vm.sh '$inst_id' 'running' '$SCI_CLIENT_ID' 'sync'"
    done
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
    free_mem=$(cat /proc/meminfo | grep -i MemFree | awk '{print $2}')
    [ $memory -lt $free_mem ] && memory=$free_mem
    if [ $(( $(date +"%s") % 10 )) -gt 7 ]; then
	rm -f $run_dir/old_resource_list
    fi
    state=1
    if [ -f "$run_dir/disabled" ]; then
        echo "cpu=0/$total_cpu memory=0/$total_memory disk=0/$total_disk network=$network/$total_network hugepages_2m=$hp_2m_free/$hp_2m_total hugepages_1g=$hp_1g_free/$hp_1g_total hugepage_size_kb=$hp_size_kb load=$load_1m/$load_5m/$load_15m cpu_idle=$cpu_idle"
        state=0
    else
        echo "cpu=$cpu/$total_cpu memory=$memory/$total_memory disk=$disk/$total_disk network=$network/$total_network hugepages_2m=$hp_2m_free/$hp_2m_total hugepages_1g=$hp_1g_free/$hp_1g_total hugepage_size_kb=$hp_size_kb load=$load_1m/$load_5m/$load_15m cpu_idle=$cpu_idle"
    fi
    cd /opt/cloudland/run
    let disk=$disk/1000*1000
    let total_disk=$total_disk/1000*1000
    old_resource_list=$(cat old_resource_list 2>/dev/null)
    # Include hugepage and cpu_idle in dedup check so changes trigger hyper_status update
    cpu_idle_int=${cpu_idle%.*}
    resource_list="'$cpu' '$total_cpu' '$memory' '$total_memory' '$disk' '$total_disk' '$state' '$hp_2m_free' '$hp_1g_free' '$hp_size_kb' '$cpu_idle_int'"
    echo "$resource_list" >/opt/cloudland/run/old_resource_list
    [ "$resource_list" = "$old_resource_list" ] && return
    cpu_model=$(lscpu | grep 'Model name:' | cut -d: -f2 | xargs)
    echo "|:-COMMAND-:| hyper_status.sh '$SCI_CLIENT_ID' '$HOSTNAME' '$cpu' '$total_cpu' '$memory' '$total_memory' '$disk' '$total_disk' '$state' '$vtep_ip' '$ZONE_NAME' '$cpu_over_ratio' '$mem_over_ratio' '$disk_over_ratio' '$cpu_model' '$hp_2m_free' '$hp_1g_free' '$hp_size_kb' '$load_1m' '$load_5m' '$load_15m' '$cpu_idle'"
}

calc_resource
sync_instance
recover_loadbalancer
sync_delayed_job
check_system_router
#probe_arp >/dev/null 2>&1
inst_status
check_conntrack
daily_job
halfday_job
#vlan_status
#router_status

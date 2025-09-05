#!/bin/bash

cd $(dirname $0)
source ../../cloudrc

[ $# -lt 3 ] && die "$0 <migrate_ID> <task_ID> <vm_ID> [prometheus_host] [prometheus_port]"

migrate_ID=$1
task_ID=$2
ID=$3
prometheus_host=${4:-""}
prometheus_port=${5:-""}
vm_ID=inst-$ID
state="error"

for i in {1..1800}; do
    vm_state=$(virsh domstate $vm_ID)
    if [ "$vm_state" = "running" ]; then
        echo
        state="completed"
        vm_xml=$xml_dir/$vm_ID/${vm_ID}.xml
        virsh define $vm_xml
        virsh autostart $vm_ID

        # Migrate VM custom metrics after successful migration
        echo "=== Starting VM Metrics Migration ==="
        if [ -n "$prometheus_host" ] && [ -n "$prometheus_port" ] && [ -f "../query_and_migrate_vm_metrics.sh" ]; then
            echo "Migrating custom metrics for VM: $vm_ID to Prometheus: $prometheus_host:$prometheus_port"
            ../query_and_migrate_vm_metrics.sh --domain "$vm_ID" --prometheus-host "$prometheus_host" --prometheus-port "$prometheus_port" || {
                echo "Warning: VM metrics migration failed, but VM migration completed successfully"
            }
        else
            if [ -z "$prometheus_host" ] || [ -z "$prometheus_port" ]; then
                echo "Info: No Prometheus configuration provided, skipping metrics migration"
            else
                echo "Warning: VM metrics migration script not found, skipping metrics migration"
            fi
        fi
        echo "=== VM Metrics Migration Process Completed ==="

        echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"
        exit 0
    fi
    sleep 1
done

state="timeout"
rm -f ${cache_dir}/meta/${vm_ID}.iso
rm -rf $xml_dir/$vm_ID
echo "|:-COMMAND-:| migrate_vm.sh '$migrate_ID' '$task_ID' '$ID' '$SCI_CLIENT_ID' '$state'"

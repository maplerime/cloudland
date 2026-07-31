#!/bin/bash
set -euo pipefail

# Generates the vm_traffic_billing_map textfile-collector metric, structured
# exactly like generate_vm_instance_map.sh. Unlike that metric, this one is
# NOT full-VM-coverage: a domain only appears here if clapi's traffic-billing
# API was explicitly called for it (add).
#
# Actions: gc | add | remove | sync
#
# There is deliberately NO "full" action here, even though
# generate_vm_instance_map.sh has one. That script's "full" rescans XML_DIR and
# genuinely rebuilds from ground truth; doing the same here would wrongly mark
# every VM on this node as traffic-billing, since this metric is opt-in per VM.
# What this script offers instead is "gc": re-validate the domains already
# present in the .prom file against local XML and drop any whose VM is no
# longer here. It can only ever shrink the set -- it never adds a domain that
# is not already in the file. The name is intentionally NOT "full" so that it
# cannot be mistaken for a rebuild.
#
# Only "sync" can rebuild from nothing, and it needs the authoritative domain
# list pushed from clapi (TrafficBillingAdmin.BroadcastSync).
#
# Consequently, if this node's .prom file is lost (reinstall, /var/lib cleanup,
# node_exporter dir recreated), nothing on this node restores it by itself.
# That is an ACCEPTED trade-off, not an oversight: recovery is operator-driven
# (UI Refresh button, or POST /api/v1/traffic-billing/sync -- both broadcast
# TrafficBillingAdmin.BroadcastSync to every node). A scheduled cluster-wide
# broadcast was deliberately rejected -- the events that can desync this metric
# (VM migration, node rebuild) happen on a months-scale cadence, and paying a
# fleet-wide fan-out every N hours to cover them is not a proportionate cost.
# Do NOT "fix" this by adding a periodic broadcast; see the matching note on
# BroadcastSync in web/src/routes/traffic_billing.go.

XML_DIR="/opt/cloudland/cache/xml"
PROM_DIR="/var/lib/node_exporter"
TMP_FILE="${PROM_DIR}/vm_traffic_billing_map.prom.tmp"
FINAL_FILE="${PROM_DIR}/vm_traffic_billing_map.prom"

# Command line arguments
ACTION=${1:-"gc"}    # Default action is the garbage-collection pass (see header)
DOMAIN=${2:-""}      # Domain name for add/remove actions

# Ensure output directory exists with proper permissions
mkdir -p "$PROM_DIR"
chmod 755 "$PROM_DIR"  # Ensure directory has sufficient read permissions

# Get hostname for hypervisor label
hypervisor=$(hostname)

# Function to extract instance_id from XML file
extract_instance_id() {
    local xml_file=$1
    local instance_id=""

    # 使用grep直接提取cloudland:instance_id的值
    instance_id=$(grep -o "<cloudland:instance_id>.*</cloudland:instance_id>" "$xml_file" | sed -e 's/<cloudland:instance_id>\(.*\)<\/cloudland:instance_id>/\1/')

    # 如果读取失败，返回空字符串，不要使用uuid
    echo "$instance_id"
}

# Function to add a VM to metrics
add_vm_to_metrics() {
    local domain=$1
    local output_file=$2
    local found=false

    main_xml="$XML_DIR/$domain/$domain.xml"
    if [ -f "$main_xml" ]; then
        instance_id=$(extract_instance_id "$main_xml")
        if [[ -n "$domain" && -n "$instance_id" ]]; then
            echo "vm_traffic_billing_map{domain=\"$domain\",instance_id=\"$instance_id\",hypervisor=\"$hypervisor\"} 1" >> "$output_file"
            found=true
        fi
    fi

    echo "$found"
}

# Function to remove a VM from metrics
remove_vm_from_metrics() {
    local domain=$1

    # Create a temporary file for the filtered content
    local filtered_file="${PROM_DIR}/vm_traffic_billing_map.filtered.tmp"

    # Filter out lines containing the domain
    if [ -f "$FINAL_FILE" ]; then
        grep -v "domain=\"$domain\"" "$FINAL_FILE" > "$filtered_file" || true
        mv "$filtered_file" "$FINAL_FILE"
        # Set permissions for Prometheus to read
        chmod 644 "$FINAL_FILE"
        if getent passwd prometheus > /dev/null; then
            chown prometheus:prometheus "$FINAL_FILE"
        fi
        echo "Removed domain $domain from traffic billing metrics file"
    else
        echo "Traffic billing metrics file does not exist, nothing to remove"
    fi
}

# Main logic based on action
case "$ACTION" in
    "gc")
        # Clear temporary file
        > "$TMP_FILE"

        # Add metric help information and type
        echo "# HELP vm_traffic_billing_map Mapping between traffic-billing VM domain and instance_id" >> "$TMP_FILE"
        echo "# TYPE vm_traffic_billing_map gauge" >> "$TMP_FILE"

        # Re-validate domains already tracked in the current file; drop any
        # whose VM is no longer on this node. Never adds domains that were not
        # already present -- this is a garbage-collection pass, not a rebuild,
        # which is why the action is called "gc" and not "full" (see header).
        if [ -f "$FINAL_FILE" ]; then
            existing_domains=$(grep -o 'domain="[^"]*"' "$FINAL_FILE" | sed -e 's/domain="\(.*\)"/\1/' | sort -u || true)
            for domain in $existing_domains; do
                add_vm_to_metrics "$domain" "$TMP_FILE" > /dev/null
            done
        fi

        # Atomic file replacement
        mv "$TMP_FILE" "$FINAL_FILE"

        # Set permissions for Prometheus to read
        chmod 644 "$FINAL_FILE"
        if getent passwd prometheus > /dev/null; then
            chown prometheus:prometheus "$FINAL_FILE"
        fi

        echo "Garbage-collected traffic billing metrics at $FINAL_FILE (stale domains dropped; none added)"
        ;;

    "add")
        if [ -z "$DOMAIN" ]; then
            echo "Error: Domain name is required for add action"
            exit 1
        fi

        # Create temporary file if final file doesn't exist
        if [ ! -f "$FINAL_FILE" ]; then
            echo "# HELP vm_traffic_billing_map Mapping between traffic-billing VM domain and instance_id" > "$FINAL_FILE"
            echo "# TYPE vm_traffic_billing_map gauge" >> "$FINAL_FILE"
            # Set permissions for Prometheus to read
            chmod 644 "$FINAL_FILE"
            if getent passwd prometheus > /dev/null; then
                chown prometheus:prometheus "$FINAL_FILE"
            fi
        fi

        # Remove existing entries for this domain first
        remove_vm_from_metrics "$DOMAIN" > /dev/null

        # Add the VM to metrics
        found=$(add_vm_to_metrics "$DOMAIN" "$FINAL_FILE")

        if [ "$found" = "true" ]; then
            echo "Added domain $DOMAIN to traffic billing metrics file"
        else
            echo "No valid XML found for domain $DOMAIN"
        fi
        ;;

    "remove")
        if [ -z "$DOMAIN" ]; then
            echo "Error: Domain name is required for remove action"
            exit 1
        fi

        remove_vm_from_metrics "$DOMAIN"
        ;;

    "sync")
        # Full reconciliation against the authoritative DB list, broadcast
        # (HyperExecute "toall=") to every compute node from clapi -- mirrors
        # manage_ip_whitelist.sh's "refresh". $DOMAIN here carries a
        # base64-encoded JSON payload: {"mappings":[{"domain":"inst-N",...}]}.
        # Rebuilding from scratch, keeping only domains present in the
        # payload, handles both directions on this node in one pass: a domain
        # no longer in the payload (DB mark removed) is simply not written
        # back, and a domain in the payload still gets added only if its XML
        # actually exists here -- a node with neither does nothing.
        if [ -z "$DOMAIN" ]; then
            echo "Error: base64 encoded JSON payload is required for sync action"
            exit 1
        fi

        PAYLOAD_JSON=$(echo "$DOMAIN" | base64 -d 2>/dev/null) || {
            echo "Error: failed to decode base64 sync payload"
            exit 1
        }
        if [ -z "$PAYLOAD_JSON" ] || ! echo "$PAYLOAD_JSON" | jq empty > /dev/null 2>&1; then
            echo "Error: decoded sync payload is not valid JSON"
            exit 1
        fi

        > "$TMP_FILE"
        echo "# HELP vm_traffic_billing_map Mapping between traffic-billing VM domain and instance_id" >> "$TMP_FILE"
        echo "# TYPE vm_traffic_billing_map gauge" >> "$TMP_FILE"

        desired_domains=$(echo "$PAYLOAD_JSON" | jq -r '.mappings[].domain')
        for domain in $desired_domains; do
            add_vm_to_metrics "$domain" "$TMP_FILE" > /dev/null
        done

        mv "$TMP_FILE" "$FINAL_FILE"
        chmod 644 "$FINAL_FILE"
        if getent passwd prometheus > /dev/null; then
            chown prometheus:prometheus "$FINAL_FILE"
        fi

        domain_count=$(echo "$desired_domains" | grep -c . || true)
        echo "Sync complete: rebuilt vm_traffic_billing_map.prom from $domain_count domain(s) in the authoritative payload"
        ;;

    *)
        echo "Error: Invalid action. Use 'gc', 'add', 'remove', or 'sync'"
        exit 1
        ;;
esac

exit 0

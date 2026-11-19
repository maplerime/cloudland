#!/bin/bash
# Runtime configuration lives only in namespaces, addresses, routes and rules.
# The server supplies every assignment again after migration or reboot.

JOOL_BIN=${CLOUDLAND_JOOL_BIN:-jool}

f6_ids() {
    [[ $1 =~ ^[1-9][0-9]*$ && $2 =~ ^[1-9][0-9]*$ ]] || return 1
    router_id=$1
    fip_id=$2
    router=router-$router_id
    translator=fip6-$router_id-$fip_id
    tag=fip6-$fip_id
    local value=$((10#$fip_id)) encoded= digit alphabet=0123456789abcdefghijklmnopqrstuvwxyz
    (( value > 0 )) || return 1
    while (( value )); do
        digit=$((value % 36)); encoded=${alphabet:digit:1}$encoded; value=$((value / 36))
    done
    rdev=6r$encoded
    tdev=6t$encoded
    (( ${#rdev} <= 15 )) || return 1
    local hex
    printf -v hex '%016x' "$fip_id"
    prefix_base=fd64:${hex:0:4}:${hex:4:4}:${hex:8:4}:${hex:12:4}:0
    prefix=$prefix_base::/96
}

f6_lock() {
    local namespace=/var/run/netns/$router
    if [[ ${CLOUDLAND_FLOATING_NS_LOCK_FD:-} == 9 ]]; then
        [[ $(stat -Lc %i /proc/$$/fd/9) == $(stat -Lc %i "$namespace") ]] || return 1
    else
        exec 9<"$namespace" || return 1
    fi
    flock -x 9 || return 1
    export CLOUDLAND_FLOATING_NS_LOCK_FD=9
}

r() { ip netns exec "$router" "$@"; }
t() { ip netns exec "$translator" "$@"; }

f6_has_device() {
    local listing
    listing=$(r ip -j link show) || return 2
    jq -e --arg dev "$1" 'any(.[]; .ifname == $dev)' <<<"$listing" >/dev/null
}

f6_ipv4_number() {
    local a b c d
    IFS=. read -r a b c d <<<"$1"
    [[ $a =~ ^[0-9]+$ && $b =~ ^[0-9]+$ && $c =~ ^[0-9]+$ && $d =~ ^[0-9]+$ ]] || return 1
    (( 10#$a < 256 && 10#$b < 256 && 10#$c < 256 && 10#$d < 256 )) || return 1
    echo $(((10#$a << 24) | (10#$b << 16) | (10#$c << 8) | 10#$d))
}

# Compare addresses by value: iproute2 compresses and lowercases IPv6 output.
f6_ipv6_key() {
    local address=${1,,} left right part number tail zeros index
    local words=() lhs=() rhs=()
    if [[ $address == *.* ]]; then
        tail=${address##*:}
        number=$(f6_ipv4_number "$tail") || return 1
        printf -v tail '%x:%x' "$((number >> 16))" "$((number & 65535))"
        address=${address%:*}:$tail
    fi
    if [[ $address == *::* ]]; then
        left=${address%%::*}; right=${address#*::}
        [[ $right != *::* ]] || return 1
        IFS=: read -r -a lhs <<<"$left"
        IFS=: read -r -a rhs <<<"$right"
        zeros=$((8 - ${#lhs[@]} - ${#rhs[@]}))
        (( zeros > 0 )) || return 1
        words=("${lhs[@]}")
        for ((index=0; index<zeros; index++)); do words+=(0); done
        words+=("${rhs[@]}")
    else
        IFS=: read -r -a words <<<"$address"
    fi
    (( ${#words[@]} == 8 )) || return 1
    for part in "${words[@]}"; do
        [[ $part =~ ^[0-9a-f]{1,4}$ ]] || return 1
        printf '%04x' "$((16#$part))"
    done
}

f6_pool() {
    local route_json address_json tables occupied item start bits mask end candidate attempt base=3323068416
    route_json=$(r ip -4 -j route show table all) || return 1
    address_json=$(r ip -4 -j addr show) || return 1
    occupied=$(jq -r '.[] | select(.dst != null and .dst != "default") | .dst' <<<"$route_json") || return 1
    occupied+=$'\n'$(jq -r '.[].addr_info[] | select(.family == "inet") | "\(.local)/\(.prefixlen)"' <<<"$address_json")
    # A failed cleanup can retain only its namespace metadata. Reserve that
    # transit pair until retry succeeds, even if its routes are already gone.
    local namespaces name rest metadata saved_pool
    namespaces=$(ip netns list) || return 1
    while read -r name rest; do
        [[ $name == "fip6-$router_id-"* ]] || continue
        metadata=$(ip netns exec "$name" ip -j link show dev lo | jq -r '.[0].ifalias // empty') || return 1
        [[ $metadata == 'cloudland-fip6 '* ]] || continue
        saved_pool=${metadata##* }
        occupied+=$'\n'$saved_pool
    done <<<"$namespaces"
    tables=$(r ip -6 -j route show table all | jq -r '.[].table // empty') || return 1
    tables+=$'\n'$(r ip -6 -j rule show | jq -r '.[].table // empty')
    local starts=() ends=()
    while IFS= read -r item; do
        [[ -n $item ]] || continue
        bits=32; [[ $item == */* ]] && bits=${item##*/}
        start=$(f6_ipv4_number "${item%/*}") || return 1
        mask=$(((0xffffffff << (32 - bits)) & 0xffffffff))
        starts+=("$((start & mask))"); ends+=("$(((start & mask) | (0xffffffff ^ mask)))")
    done <<<"$occupied"
    for ((attempt=0; attempt<65536; attempt++)); do
        slot=$(((fip_id + attempt) % 65536)); candidate=$((base + slot * 2)); table=$((20000000 + slot))
        [[ $'\n'$tables$'\n' != *$'\n'"$table"$'\n'* ]] || continue
        local collision=0 index
        for index in "${!starts[@]}"; do
            if (( candidate <= ends[index] && candidate + 1 >= starts[index] )); then collision=1; break; fi
        done
        (( collision == 0 )) || continue
        raddr=198.$((18 + slot / 32768)).$(((slot % 32768) / 128)).$(((slot % 128) * 2))
        taddr=198.$((18 + slot / 32768)).$(((slot % 32768) / 128)).$(((slot % 128) * 2 + 1))
        pool=$raddr/31
        return 0
    done
    echo "No free NAT64 transit address pair in this router" >&2
    return 1
}

f6_ensure_rule() {
    local binary=$1 rule_table=$2 chain=$3 status
    shift 3
    if r "$binary" -w -t "$rule_table" -C "$chain" "$@" 2>/dev/null; then return 0; else status=$?; fi
    (( status == 1 )) || return "$status"
    r "$binary" -w -t "$rule_table" -I "$chain" 1 "$@"
}

f6_remove_rules() {
    local binary rule_table listing line part index parts
    for binary in iptables ip6tables; do
        for rule_table in nat filter; do
            listing=$(r "$binary-save" -t "$rule_table") || return 1
            while IFS= read -r line; do
                [[ $line == '-A '* ]] || continue
                read -r -a parts <<<"$line"
                local ours=0
                for index in "${!parts[@]}"; do
                    if [[ ${parts[index]} == --comment && ${parts[index+1]//\"/} == "$tag" ]]; then ours=1; fi
                    part=${parts[index]}; parts[index]=${part//\"/}
                done
                (( ours )) || continue
                parts[0]=-D
                r "$binary" -w -t "$rule_table" "${parts[@]}" || return 1
                parts[0]=-C
                if r "$binary" -w -t "$rule_table" "${parts[@]}" 2>/dev/null; then
                    echo "Owned firewall rule remains after deletion" >&2; return 1
                else
                    [[ $? == 1 ]] || return 1
                fi
            done <<<"$listing"
        done
    done
}

f6_clear_one() {
    # This namespace's lo alias holds the parameters applied to the kernel.
    # It survives partial cleanup and is rebuilt from server arguments.
    local record= marker alias_pool listing status saved_ext saved_int
    if [[ ! -e /var/run/netns/$translator ]]; then return 0; fi
    listing=$(t ip -j link show dev lo) || return 1
    record=$(jq -r '.[0].ifalias // empty' <<<"$listing") || return 1
    if [[ -z $record ]]; then
        listing=$(t ip -j link show) || return 1
        [[ $(jq length <<<"$listing") == 1 ]] || { echo "Unidentified NAT64 namespace; refusing deletion" >&2; return 1; }
        ip netns del "$translator"
        return
    fi
    read -r marker saved_ext ext_vlan saved_int int_vlan alias_pool <<<"$record"
    [[ $marker == cloudland-fip6 && $ext_vlan =~ ^[1-9][0-9]*$ && $int_vlan =~ ^[1-9][0-9]*$ && $alias_pool == 198.1[89].*/31 ]] || return 1
    ext_cidr=$saved_ext; int_cidr=$saved_int; pool=$alias_pool
    ext_dev=te-$router_id-$ext_vlan
    local number
    number=$(f6_ipv4_number "${pool%/*}") || return 1
    slot=$(((number - 3323068416) / 2)); table=$((20000000 + slot))
    (( slot >= 0 && slot < 65536 )) || return 1
    # Disable translation first. Failure retains the namespace for retry.
    local instance_status
    instance_status=$(t "$JOOL_BIN" -i floating instance status) || return 1
    case $instance_status in
        Running*) t "$JOOL_BIN" instance remove floating || return 1 ;;
        Dead*) ;;
        *) echo "Unexpected NAT64 instance status" >&2; return 1 ;;
    esac
    f6_remove_rules || return 1
    listing=$(r ip -4 -j rule show) || return 1
    while jq -e --arg dst "${pool%/*}" 'any(.[]; .priority == 100 and (.table == "main" or .table == 254) and (.dst == $dst or .dst == ($dst + "/31")) and (.dstlen // 31) == 31)' <<<"$listing" >/dev/null; do
        r ip -4 rule del priority 100 to "$pool" lookup main || return 1
        listing=$(r ip -4 -j rule show) || return 1
    done
    local sets
    sets=$(r ipset list -name) || return 1
    if [[ $'\n'$sets$'\n' == *$'\n'nonat$'\n'* ]]; then
        if r ipset test nonat "$pool" 2>/dev/null; then r ipset del nonat "$pool" || return 1; else [[ $? == 1 ]] || return 1; fi
    fi
    # Keep the private link and namespace until every external cleanup succeeds.
    if f6_has_device "$ext_dev"; then
        listing=$(r ip -6 -j addr show dev "$ext_dev") || return 1
        local expected actual addresses found=
        expected=$(f6_ipv6_key "${ext_cidr%/*}") || return 1
        addresses=$(jq -r '.[].addr_info[] | select(.family == "inet6") | "\(.local)/\(.prefixlen)"' <<<"$listing") || return 1
        while IFS= read -r actual; do
            [[ -n $actual ]] || continue
            if [[ $(f6_ipv6_key "${actual%/*}") == "$expected" ]]; then found=$actual; break; fi
        done <<<"$addresses"
        [[ -z $found ]] || r ip -6 addr del "$found" dev "$ext_dev" || return 1
        listing=$(r ip -j addr show dev "$ext_dev") || return 1
        local remaining neighbours
        remaining=$(jq '[.[].addr_info[] | select(.family == "inet" or .scope == "global")] | length' <<<"$listing") || return 1
        neighbours=$(r ip -6 -j neigh show proxy dev "$ext_dev") || return 1
        if [[ $remaining == 0 && $(jq length <<<"$neighbours") == 0 ]]; then r ip link del "$ext_dev" || return 1; fi
    else
        status=$?; (( status == 1 )) || return "$status"
        # A half-created external veth may still be entirely on the host.
        local host_dev=ext-$router_id-$ext_vlan
        listing=$(ip -j link show) || return 1
        if jq -e --arg d "$host_dev" 'any(.[]; .ifname == $d)' <<<"$listing" >/dev/null; then ip link del "$host_dev" || return 1; fi
    fi
    listing=$(r ip -6 -j rule show) || return 1
    while jq -e --argjson rt "$table" 'any(.[]; .priority == 1000 and (.table | tostring) == ($rt | tostring))' <<<"$listing" >/dev/null; do
        r ip -6 rule del priority 1000 lookup "$table" || return 1
        listing=$(r ip -6 -j rule show) || return 1
    done
    listing=$(r ip -6 -j route show table all) || return 1
    if jq -e --argjson rt "$table" 'any(.[]; (.table // "main" | tostring) == ($rt | tostring))' <<<"$listing" >/dev/null; then
        r ip -6 route flush table "$table" || return 1
    fi
    listing=$(r ip -6 -j route show table main) || return 1
    if jq -e --arg dev "$rdev" 'any(.[]; .dev == $dev)' <<<"$listing" >/dev/null; then
        r ip -6 route flush dev "$rdev" table main || return 1
    fi
    # Keep the named namespace as a retry handle until every owned link is gone.
    if f6_has_device "$rdev"; then
        r ip link del "$rdev" || return 1
    else
        status=$?; (( status == 1 )) || return "$status"
    fi
    local dev
    for dev in "$rdev" "$tdev"; do
        listing=$(ip -j link show) || return 1
        if jq -e --arg dev "$dev" 'any(.[]; .ifname == $dev)' <<<"$listing" >/dev/null; then ip link del "$dev" || return 1; fi
    done
    ip netns del "$translator" || return 1
}

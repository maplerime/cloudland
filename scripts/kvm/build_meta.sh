#!/bin/bash
cd `dirname $0`
source ../cloudrc
[ $# -lt 2 ] && echo "$0 <vm_ID> <vm_name> <rescue>" && exit -1

vm_ID=$1
vm_name=$2
rescue=$3
[ "${vm_name%%.*}" = "$vm_name" ] && vm_name=${vm_name}.$cloud_domain
working_dir=/tmp/$vm_ID
latest_dir=$working_dir/openstack/latest
mkdir -p $latest_dir
rm -f ${latest_dir}/*

vm_meta=$(cat | base64 -d)
userdata=$(jq -r .userdata <<<$vm_meta)
userdata_type=$(jq -r .userdata_type <<<$vm_meta)
if [ -n "$userdata" ]; then
   if [ "$userdata_type" = "base64" ]; then
      echo "$userdata" | base64 -d > $latest_dir/user_data
   else
      echo "$userdata" > $latest_dir/user_data
   fi
fi

root_passwd=$(jq -r '.root_passwd' <<< $vm_meta)
os_code=$(jq -r '.os_code' <<< $vm_meta)
dns=$(jq -r '.dns' <<< $vm_meta)
login_port=$(jq -r '.login_port' <<< $vm_meta)
pub_keys=$(jq -r '.keys' <<< $vm_meta)
admin_pass=`openssl rand -base64 12`
random_seed=`cat /dev/urandom | head -c 512 | base64 -w 0`
(
    echo '{'
    echo '  "name": "'${vm_name}'",'
    if [ -n "${pub_keys}" ]; then
        echo -n '  "public_keys": {'
        i=0
        n=$(jq length <<< $pub_keys)
        while [ $i -lt $n ]; do
            key=$(jq -r .[$i] <<< $pub_keys)
            [ $i -ne 0 ] && echo -n ','
            echo -n '"key'$i'": "'$key'\n"'
            let i=$i+1
        done
        echo '},'
    fi
    echo '  "launch_index": 0,'
    echo '  "hostname": "'${vm_name}'",'
    echo '  "availability_zone": "cloudland",'
    echo '  "uuid": "'${vm_ID}'",'
    if [ -n "${root_passwd}" ] && [ "${os_code}" = "windows" ]; then
        echo '  "admin_pass": "'${root_passwd}'",'
    else
        echo '  "admin_pass": "'${admin_pass}'",'
    fi
    echo '  "random_seed": "'${random_seed}'"'
    echo '}'
) > $latest_dir/meta_data.json

ssh_pwauth="false"
if [ -n "${root_passwd}" ] && [ "${os_code}" != "windows" ]; then
    ssh_pwauth="true"
fi

vendor_scripts="#!/bin/bash\n"

cloud_config_txt=$(
    cat <<EOF
#cloud-config

merge_how:
  - name: list
    settings: [append]
  - name: dict
    settings: [no_replace, recurse_list]
  - name: str
    settings: [append]

ssh_pwauth: '${ssh_pwauth}'

disable_root: false\n

EOF
)

if [ -n "${root_passwd}" ] && [ "${os_code}" != "windows" ]; then
    cloud_config_txt+=$(
        cat <<EOF

chpasswd:
  expire: false
  users:
    - name: root
      password: '${root_passwd}'
  list: |
    root:${root_passwd}

EOF
    )
    vendor_scripts+="echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config.d/allow_root.conf\n"
    vendor_scripts+="echo 'PasswordAuthentication yes' >> /etc/ssh/sshd_config.d/allow_root.conf\n"
fi

# change qemu-guest-agent config
if [ "${os_code}" = "linux" ]; then
        vendor_scripts+=$(cat <<EOF

# change qemu-guest-agent config
if [ -f /etc/sysconfig/qemu-ga ]; then
    sed -i 's/--allow-rpcs=/--allow-rpcs=guest-exec,/;/BLACKLIST_RPC/d' /etc/sysconfig/qemu-ga
elif [ -f /lib/systemd/system/qemu-guest-agent.service ]; then
    sed -i "s#/usr/bin/qemu-ga#/usr/bin/qemu-ga -b ''#" /lib/systemd/system/qemu-guest-agent.service
    sed -i "s#/usr/sbin/qemu-ga#/usr/sbin/qemu-ga -b ''#" /lib/systemd/system/qemu-guest-agent.service
    systemctl daemon-reload
fi
systemctl restart qemu-guest-agent.service

EOF
    )
# use runcmd to change the port value of /etc/ssh/sshd_config
# and restart the ssh service
    if [ -n "${login_port}" ] && [ "${login_port}" != "22" ] && [ ${login_port} -gt 0 ]; then
        vendor_scripts+=$(cat <<EOF

# change ssh port
sed -i 's/^#Port .*/Port ${login_port}/' /etc/ssh/sshd_config
sed -i 's/^Port .*/Port ${login_port}/' /etc/ssh/sshd_config
systemctl daemon-reload
systemctl restart ssh.socket
systemctl restart sshd || systemctl restart ssh

EOF
        )
    fi
fi

write_mime_multipart_args=""
# write to vendor_data.json
if [ "${os_code}" != "windows" ]; then
    echo -e "$cloud_config_txt" > $latest_dir/cloud_config.txt
    write_mime_multipart_args+="cloud_config.txt:text/cloud-config "

    echo -e "$vendor_scripts" > $latest_dir/vendor_script.sh
    write_mime_multipart_args+="vendor_script.sh:text/x-shellscript "

    # insert fixed vendor scripts in /opt/cloudland/scripts/kvm/vendor_scripts
    for i in $(cd ./vendor_scripts; ls *.sh); do 
        cat $i > $latest_dir/$i
        write_mime_multipart_args+="$i:text/x-shellscript "
    end

    # insert customized vendor data from api
    custom_vendordata=""
    vendordata_type=$(jq -r .vendordata_type <<<$vm_meta)
    vendordata=$(jq -r .vendordata <<<$vm_meta)
    if [ -n "$vendordata" ]; then
        if [ "$vendordata_type" = "base64" ]; then
            custom_vendordata+=$(echo "$vendordata" | base64 -d)
        else
            custom_vendordata+="$vendordata"
        fi
        echo -e "$custom_vendordata" > $latest_dir/custom_vendor_script.sh
        write_mime_multipart_args+="custom_vendor_script.sh:text/x-shellscript "
    fi
    cd $latest_dir
    write-mime-multipart -o vendor_data.txt $write_mime_multipart_args
    jq -n --arg data "$(cat vendor_data.txt)" '{"cloud-init": $data}' > vendor_data.json
    cd -
    rm -f $latest_dir/cloud_config.txt $latest_dir/custom_vendor_script.sh $latest_dir/vendor_data.txt $latest_dir/*.sh
fi

[ -z "$dns" ] && dns=$dns_server
net_json=$(jq 'del(.userdata) | del(.userdata_type) | del(.vendordata) | del(.vendordata_type) | del(.vlans) | del(.keys) | del(.security) | del(.login_port) | del(.root_passwd) | del(.dns)' <<< $vm_meta | jq --arg dns $dns '.services[0].type = "dns" | .services[0].address |= .+$dns')
let mtu=$(cat /sys/class/net/$vxlan_interface/mtu)-50
if [ "$mtu" -lt 1450 ]; then
    net_json=$(sed "s/\"mtu\": 1450/\"mtu\": $mtu/g" <<<$net_json)
fi
echo "$net_json" > $latest_dir/network_data.json

iso_name=$vm_ID
[ "$rescue" = "true" ] && iso_name=$vm_ID-rescue
mkisofs -quiet -R -J -V config-2 -o ${cache_dir}/meta/${iso_name}.iso $working_dir &> /dev/null
rm -rf $working_dir

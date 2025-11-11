#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 2 ] && die "$0 <router> <lb_ID>"

router=$1
lb_ID=$2
[ "${router/router-/}" = "$router" ] && router=router-$1
src_vrrp_ip=$(grep unicast_src_ip $router_dir/$router/keepalived.conf | awk '{print $2}')
lb_dir=$router_dir/$router/lb-$lb_ID
[ ! -d "$lb_dir" ] && mkdir -p $lb_dir
content=$(cat)
cat >$lb_dir/haproxy.conf <<EOF
global
    log /dev/log local0 info
    log /dev/log local0 notice
    chroot $lb_dir
    pidfile $lb_dir/haproxy.pid
    maxconn 4000
    user haproxy
    group haproxy
    daemon
    stats socket $lb_dir/admin.sock mode 660 level admin expose-fd listeners
    stats timeout 30s

defaults
    log global
    mode http
    option httplog
    option dontlognull
    option http-server-close
    option forwardfor except 127.0.0.0/8
    option redispatch
    retries 3
    timeout http-request 10s
    timeout queue 1m
    timeout connect 10s
    timeout client 1m
    timeout server 1m
    timeout http-keep-alive 10s
    timeout check 10s
    maxconn 3000

listen stats
    bind 127.0.0.1:8080
    mode http
    stats enable
    stats uri /haproxy-stats
    stats realm Haproxy\ Statistics
    stats auth admin:password
    stats hide-version
    stats refresh 30s
EOF

listeners=$(jq -r .listeners <<< $content)
nlistener=$(jq length <<< $listeners)
floating_ips=$(jq -r .floating_ips <<< $content)
nfloating_ip=$(jq length <<< $floating_ips)
i=0
while [ $i -lt $nlistener ]; do
    listener=$(jq -r .[$i] <<< $listeners)
    echo $listener
    read -d'\n' -r name mode port key cert< <(jq -r ".name, .mode, .port, .key, .cert" <<<$listener)
    if [ -n "$key" -a -n "$cert" ]; then
        base64 -d <<<"$key" >$lb_dir/$name.pem
	echo >>$lb_dir/$name.pem
        base64 -d <<<"$cert" >>$lb_dir/$name.pem
	echo >>$lb_dir/$name.pem
        ssl_config="ssl crt $lb_dir/$name.pem"
    fi
    cat >>$lb_dir/haproxy.conf <<EOF

frontend ${name}_front
    bind *:$port $ssl_config
    mode $mode
    default_backend ${name}_back

backend ${name}_back
    balance roundrobin
    source $src_vrrp_ip
EOF
    backends=$(jq -r .backends <<< $listener)
    nbackend=$(jq length <<< $backends)
    j=0
    while [ $j -lt $nbackend ]; do
        backend=$(jq -r .[$j] <<< $backends)
        read -d'\n' -r backend < <(jq -r ".backend_url" <<<$backend)
        cat >>$lb_dir/haproxy.conf <<EOF
    server ${name}$j $backend check weight 100 maxconn 1000
EOF
        let j=$j+1
    done
    let i=$i+1
done

haproxy_pid=$(cat $lb_dir/haproxy.pid)
[ $haproxy_pid -gt 0 ] && kill -HUP $haproxy_pid
[ $? -ne 0 ] && ip netns exec $router haproxy -D -p $lb_dir/haproxy.pid -f $lb_dir/haproxy.conf

#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 2 ] && die "$0 <router> <lb_ID>"

router=$1
lb_ID=$2
[ "${router/router-/}" = "$router" ] && router=router-$1
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
i=0
while [ $i -lt $nlistener ]; do
    listener=$(jq -r .[$i] <<< $listeners)
    echo $listener
    read -d'\n' -r name mode port< <(jq -r ".name, .mode, .port" <<<$listener)
    cat >>$lb_dir/haproxy.conf <<EOF

frontend ${name}_front
    bind *:$port
    mode $mode
    default_backend ${name}_back

backend ${name}_back
    balance roundrobin
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

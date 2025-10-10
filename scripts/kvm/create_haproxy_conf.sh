#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 2 ] && die "$0 <router> <lb_ID>"

router=$1
[ "${router/router-/}" = "$router" ] && router=router-$1
lb_dir=$router_dir/$router/lb-$lb_ID
[ ! -d "$lb_dir" ] && mkdir -p $lb_dir
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

frontend http_front
    bind *:80
    # redirect scheme https code 301 if !{ ssl_fc }
    default_backend http_back

frontend https_front
    bind *:443 ssl crt $lb_dir/certs/
    mode http
    default_backend http_back

backend http_back
    balance roundrobin
    option httpchk GET /health
    http-check expect status 200

    server web1 192.168.1.101:80 check weight 100 maxconn 1000
    server web2 192.168.1.102:80 check weight 100 maxconn 1000
    # server web3 192.168.1.103:80 check weight 100 maxconn 1000 backup

frontend tcp_front
    bind *:3306
    mode tcp
    default_backend tcp_back

backend tcp_back
    mode tcp
    balance source
    option tcp-check
    server db1 192.168.1.201:3306 check inter 10s fall 3 rise 2
    server db2 192.168.1.202:3306 check inter 10s fall 3 rise 2
EOF

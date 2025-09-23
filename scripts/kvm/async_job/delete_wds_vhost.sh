#!/bin/bash

cd `dirname $0`
source ../../cloudrc

[ $# -lt 3 ] && die "$0 <ID> <vhost_ID> <uss_ID>"

ID=$1
vhost_ID=$2
uss_ID=$3

delete_vhost $ID $vhost_ID $uss_ID

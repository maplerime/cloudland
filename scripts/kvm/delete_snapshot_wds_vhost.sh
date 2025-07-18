#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 1 ] && echo "$0 <WDS_SNAPSHOT_ID>" && exit -1

wds_snapshot_ID=$1

get_wds_token
wds_curl DELETE "api/v2/sync/block/snaps/$wds_snapshot_ID?force=false"

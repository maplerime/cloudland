#!/bin/bash

cd `dirname $0`
source ../cloudrc

[ $# -lt 1 ] && echo "$0 <WDS_SNAPSHOT_ID>" && exit -1

wds_snapshot_ID=$1

get_wds_token
wds_curl DELETE "api/v2/sync/block/snaps/$wds_snapshot_ID?force=false"
# Also try to delete the volume with same uuid since the snapshot may be cloned to the different pool
wds_curl DELETE "api/v2/sync/block/volumes/$wds_snapshot_ID?force=false"

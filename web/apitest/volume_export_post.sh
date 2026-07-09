#!/bin/bash

source tokenrc

volume_id=${1:?"usage: $0 <volume_uuid>"}

curl -k -XPOST -H "Authorization: bearer $token" "$endpoint/api/v1/volumes/$volume_id/export" | jq .

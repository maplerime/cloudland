#!/bin/bash

source tokenrc

image_id=${1:?"usage: $0 <image_uuid> <storage_id>"}
storage_id=${2:?"usage: $0 <image_uuid> <storage_id>"}

curl -k -XPOST \
  -H "Authorization: bearer $token" \
  -H "Content-Type: application/json" \
  -d "{\"storage_id\": $storage_id}" \
  "$endpoint/api/v1/images/$image_id/export" | jq .

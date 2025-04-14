#!/bin/bash

source tokenrc

cat >tmp.json <<EOF
{
  "name": "secgroup-new-$RANDOM",
  "is_default": false
}
EOF

#curl -k -XPATCH -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/security_groups/382fb8d9-44ac-46e7-9660-867e289e182c" -d@./tmp.json | jq .
curl -k -XPATCH -H "Authorization: bearer $token" "$endpoint/api/v1/security_groups/382fb8d9-44ac-46e7-9660-867e289e182c" -d@./tmp.json | jq .

#!/bin/bash

source tokenrc

cat >tmp.json <<EOF
{
  "hostname": "cathy-win",
  "primary_interface": {
    "public_addresses": [
      {"id": "d5df4f5a-1464-4322-a03c-7b4d1b85106c"},
      {"id": "7b4f1fb8-7595-41f1-b9f4-edfce7e93a82"}
    ],
    "inbound": 100,
    "outbound": 100
  },
  "flavor": "XLarge-8C16G",
  "root_passwd": "Wish-Y0u-Happy",
  "image": {
    "id": "10cc52a1-ca87-4815-86f8-d63784ae5924"
  },
  "hypervisor": 1,
  "zone": "zone0"
}
EOF

curl -k -XPOST -H "Authorization: bearer $token" "$endpoint/api/v1/instances" -d @./tmp.json | jq .

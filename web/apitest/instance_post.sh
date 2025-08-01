#!/bin/bash

source tokenrc

cat >tmp.json <<EOF
{
  "hostname": "cathy-perf4",
  "primary_interface": {
    "subnets": [{
      "id": "a0e2514d-3964-4e23-a2ff-fb5e66003fae"
    }],
    "inbound": 100,
    "outbound": 100
  },
  "flavor": "XLarge-8C16G",
  "image": {
    "id": "67e5608a-4d4d-4ef3-af86-6953c13233a6"
  },
  "hypervisor": 1,
  "keys": [
    {
      "id": "689f82db-cd87-46b3-8808-1eec15a6d13c"
    }
  ],
  "zone": "zone0"
}
EOF

curl -k -XPOST -H "Authorization: bearer $token" "$endpoint/api/v1/instances" -d @./tmp.json | jq .

#!/bin/bash

source tokenrc

cat >tmp.json <<EOF
{
  "hostname": "test",
  "primary_interface": {
    "subnets": [{
      "id": "8bc206c8-0ced-49f8-ba9b-4b9717fbacc5"
    }],
    "inbound": 100,
    "outbound": 100
  },
  "flavor": "small",
  "image": {
    "id": "1655a434-d726-49c2-8286-0866135d2475"
  },
  "keys": [
    {
      "id": "506d75da-1e3f-47a2-8f98-8ff7deefa0f0"
    }
  ],
  "zone": "zone0"
}
EOF

curl -k -XPOST -H "Authorization: bearer $token" "$endpoint/api/v1/instances" -d @./tmp.json | jq .

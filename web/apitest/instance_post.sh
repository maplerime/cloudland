#!/bin/bash

source tokenrc

cat >tmp.json <<EOF
{
  "hostname": "test-$RANDOM",
  "primary_interface": {
    "subnet": {
      "id": "9ce50975-d635-4ae3-b434-66ec04c6396d"
    }
  },
  "flavor": "small",
  "image": {
    "id": "ab1d61bd-0d4b-4bb3-8f2c-adc61d84f8d6"
  },
  "keys": [
    {
      "name": "cathy"
    },
    {
      "id": "8a1443df-c2e5-4932-8e60-3fb8af33a1eb"
    }
  ],
  "zone": "zone0"
}
EOF

curl -k -XPOST -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/instances" -d @./tmp.json | jq .

#!/bin/bash

source tokenrc

cat >tmp.json <<EOF
{
  "hostname": "test",
  "primary_interface": {
    "subnet": {
      "id": "69e7af0d-2f0d-4fdc-a874-a69a99012e55"
    },
    "inbound": 100,
    "outbound": 100
  },
  "flavor": "small",
  "image": {
    "id": "3c0cca59-df4f-4daa-bfa1-04d771e1a17c"
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

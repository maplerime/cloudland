#!/bin/bash

source tokenrc

cat >tmp.json <<EOF
{
  "instance": {
    "id": "83ac585a-6779-454f-b71a-76ca20db87c6"
  },
  "inbound": 1000,
  "outbound": 1000
}
EOF

curl -k -XPATCH -H "Authorization: bearer $token" "$endpoint/api/v1/floating_ips/a2e08225-f29c-4186-8ba5-6fa6d94dee14" -d @./tmp.json | jq .

#!/bin/bash

source tokenrc

cat >tmp.json <<EOF
{"site_subnets": [{"id": ""}]}
EOF

curl -k -v -XPATCH -H "Authorization: bearer $token" "$endpoint/api/v1/instances/b4b1302e-5cf6-46aa-a175-b34700514744/interfaces/b2f47aa2-3137-41a3-a67b-bd844596a0b3" -d@./tmp.json

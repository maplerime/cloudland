#!/bin/bash

source tokenrc

cat >tmp.json <<EOF
{"primary_address": {"id": "319a509b-73f8-4675-8d89-65d5d125c65d"}}
EOF

curl -k -v -XPATCH -H "Authorization: bearer $token" "$endpoint/api/v1/instances/65971708-275a-45a6-9f30-38887f75345c/interfaces/437af77f-45e1-43bf-ad4b-5cbcb52edb6f" -d@./tmp.json

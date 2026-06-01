#!/bin/bash

source tokenrc

#cat >tmp.json <<EOF
#{"site_subnets": [{"id": "cd02c4d3-f00c-4111-89a3-e080a96cbd9f"}]}
#EOF
cat >tmp.json <<EOF
{"site_subnets": []}
EOF

curl -k -XPATCH -H "Authorization: bearer $token" "$endpoint/api/v1/instances/73d7bea4-c6e6-4189-a8d6-d85289ba99b7/interfaces/a1ef2f5b-fbd6-4b77-938a-f7de9e830bbf" -d@./tmp.json | jq .

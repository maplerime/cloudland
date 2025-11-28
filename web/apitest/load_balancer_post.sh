#!/bin/bash

source tokenrc

cat >tmp.json <<EOF
{
  "name": "vpc-$RANDOM"
}
EOF
vpc_id=$(curl -k -XPOST -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/vpcs" -d @./tmp.json | jq -r .id)

cat >tmp.json <<EOF
{
  "name": "loadbalancer-$RANDOM",
  "vpc": {
    "id": "$vpc_id"
  }
}
EOF
curl -k -XPOST -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/load_balancers" -d @./tmp.json | jq .

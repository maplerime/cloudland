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
loadbalancer_id=$(curl -k -XPOST -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/load_balancers" -d @./tmp.json | jq -r .id)

cat >tmp.json <<EOF
{
  "name": "listener-$RANDOM",
  "mode": "http",
  "port": 80
}
EOF
listener_id=$(curl -k -XPOST -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/load_balancers/$loadbalancer_id/listeners" -d @./tmp.json | jq -r .id)
cat >tmp.json <<EOF
{
  "name": "backend-$RANDOM",
  "endpoint": "10.10.200.2:80"
}
EOF
sleep 5
curl -k -XPOST -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/load_balancers/$loadbalancer_id/listeners/$listener_id/backends" -d @./tmp.json | jq .

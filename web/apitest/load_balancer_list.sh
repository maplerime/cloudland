#!/bin/bash

source tokenrc

curl -k -XGET -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/load_balancers" | jq .

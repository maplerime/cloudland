#!/bin/bash

source tokenrc

loadbalancers=$(curl -k -XGET -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/load_balancers")
length=$(jq '.load_balancers | length' <<<$loadbalancers)
i=0
while [ $i -lt $length ]; do
	loadbalancer_id=$(jq -r .load_balancers[$i].id <<<$loadbalancers)
	curl -k -XGET -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/load_balancers/$loadbalancer_id/listeners" | jq .
	let i=$i+1
done

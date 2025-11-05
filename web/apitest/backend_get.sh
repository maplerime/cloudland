#!/bin/bash

source tokenrc

loadbalancers=$(curl -k -XGET -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/load_balancers")
length=$(jq '.load_balancers | length' <<<$loadbalancers)
i=0
while [ $i -lt $length ]; do
	loadbalancer_id=$(jq -r .load_balancers[$i].id <<<$loadbalancers)
	listeners=$(curl -k -XGET -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/load_balancers/$loadbalancer_id/listeners")
	listeners_length=$(jq '.listeners | length' <<<$listeners)
	j=0
	while [ $j -lt $listeners_length ]; do
		listener_id=$(jq -r .listeners[$i].id <<<$listeners)
		curl -k -XGET -H "Authorization: bearer $token" -H "X-Resource-User: cathy" -H "X-Resource-Org: cathy" "$endpoint/api/v1/load_balancers/$loadbalancer_id/listeners/$listener_id/backends" | jq .
		let j=$j+1
	done
	let i=$i+1
done

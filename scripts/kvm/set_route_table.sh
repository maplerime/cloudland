#!/bin/bash

cd `dirname $0`
source ../cloudrc

routes_file=$ROUTES_FILE
[ ! -f $routes_file ] && exit 0

for i in {1..150}; do
    while read line; do
        if eval $line; then
       	    pass="true"
	    break
	fi
    done <$routes_file
    [ "$pass" = "true" ] && break
    sleep 2
done

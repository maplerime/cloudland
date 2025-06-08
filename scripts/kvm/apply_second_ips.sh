#!/bin/bash

cd `dirname $0`
source ../cloudrc

./async_job/$(basename $0) $*

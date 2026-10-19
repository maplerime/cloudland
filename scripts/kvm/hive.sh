#!/bin/bash
cd "$(dirname "$0")" || exit 1
[ "$#" -eq 1 ] && [ "$1" = sync ] || exit 1
# The existing SCI command channel supplies the configuration on stdin.
IFS= read -r payload || exit 1
source ../cloudrc
async_exec ./async_job/hive.sh sync "$payload"

#!/bin/bash
cd "$(dirname "$0")" || exit 1
[ "$#" -eq 1 ] && [ "$1" = sync ] || exit 1
# The existing SSH action sends configuration on stdin, never in command arguments.
IFS= read -r payload || exit 1
source ../cloudrc
async_exec ./async_job/hive.sh sync "$payload"

#!/bin/bash
cd "$(dirname "$0")" || exit 1
[ "$#" -eq 4 ] || exit 1
source ../cloudrc
async_exec ./async_job/inspect_vm.sh "$@"

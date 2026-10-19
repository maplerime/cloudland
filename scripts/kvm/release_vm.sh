#!/bin/bash
cd "$(dirname "$0")" || exit 1
[ "$#" -eq 3 ] || exit 1
source ../cloudrc
async_exec ./async_job/release_vm.sh "$@"

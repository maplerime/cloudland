#!/bin/bash
cd "$(dirname "$0")" || exit 1
[ "$#" -eq 1 ] || exit 1
source ../cloudrc
async_exec ./async_job/quarantine_vm.sh "$@"

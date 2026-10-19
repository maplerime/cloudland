#!/bin/bash
cd "$(dirname "$0")/.." || exit 1
source ../cloudrc
# This action must not enable shell tracing or print configuration arguments.
python3 ./vm_inspection/common/hive_client.py "$@"

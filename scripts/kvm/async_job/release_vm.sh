#!/bin/bash
exec bash "$(dirname "$0")/vm_isolation.sh" release "$@"

#!/bin/bash
# One scan uses the snapshot copied at its start, even if a refresh finishes later.
[ "$#" -eq 2 ] && [ -s "$1" ] && [ -n "$2" ] || exit 1
python3 "$(dirname "$0")/match_inspection.py" evaluate "$1" "$2"

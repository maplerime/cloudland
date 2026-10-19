#!/bin/bash
# Every completed CPU-triggered scan publishes one immutable notification snapshot.
inspection_metrics_script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/metrics.py"
export_infection_metrics() {
    python3 "$inspection_metrics_script" export "$inspection_run" "$1" "$2" "$3"
}
mark_inspection_metrics_collected() {
    python3 "$inspection_metrics_script" collected "$inspection_run" "$1" "$2"
}
cleanup_collected_inspection_metrics() {
    python3 "$inspection_metrics_script" cleanup "$inspection_run"
}

#!/bin/bash
#
# Classifies why the previous boot ended (clean shutdown vs abnormal) and
# exposes the result via node_exporter's textfile collector. Runs once per
# boot via boot-reason-detector.service (systemd oneshot) on compute and
# control-plane nodes.
#

OUTPUT_DIR="${NODE_EXPORTER_TEXTFILE_DIR:-/var/lib/node_exporter}"
OUTPUT_FILE="${OUTPUT_DIR}/boot_reason.prom"
TMP_FILE="${OUTPUT_FILE}.$$.tmp"

mkdir -p "$OUTPUT_DIR"

if [ ! -d /var/log/journal ] || [ -z "$(find /var/log/journal -mindepth 1 -maxdepth 1 2>/dev/null)" ]; then
  {
    echo '# HELP node_reboot_detection_error Boot-reason detection cannot run because a precondition is not met'
    echo '# TYPE node_reboot_detection_error gauge'
    echo 'node_reboot_detection_error{error="journal_not_persistent"} 1'
  } > "$TMP_FILE"
  mv "$TMP_FILE" "$OUTPUT_FILE"
  exit 0
fi

BOOT_COUNT=$(journalctl --utc --list-boots --no-pager 2>/dev/null | wc -l)
if [ "$BOOT_COUNT" -lt 2 ]; then
  {
    echo '# HELP node_last_boot_reason Classification of the most recent reboot'
    echo '# TYPE node_last_boot_reason gauge'
    echo 'node_last_boot_reason{reason="no_reboot_yet"} 1'
  } > "$TMP_FILE"
  mv "$TMP_FILE" "$OUTPUT_FILE"
  exit 0
fi

PREV_BOOT_LINE=$(journalctl --utc --list-boots --no-pager 2>/dev/null | awk '$1 == "-1"')
PREV_BOOT_END_STR=$(echo "$PREV_BOOT_LINE" | awk '{print $(NF-2), $(NF-1), $NF}')
PREV_BOOT_END_EPOCH=$(date -u -d "${PREV_BOOT_END_STR}" +%s 2>/dev/null)

REASON=""

# Prefer IPMI SEL: some hardware-fatal errors reset the machine instantly,
# before the OS (user-space or kernel) has any chance to log anything.
if command -v ipmitool >/dev/null 2>&1 && [ -n "$PREV_BOOT_END_EPOCH" ]; then
  SEL_HIT=$(ipmitool sel elist 2>/dev/null | grep -iE "Fatal Err|Bus Fatal Error|Critical Interrupt|Uncorrectable" | while IFS="|" read -r _ d t rest; do
    d="${d//[[:space:]]/}"; t="${t//[[:space:]]/}"
    ts=$(date -u -d "${d} ${t}" +%s 2>/dev/null)
    [ -z "$ts" ] && continue
    diff=$(( ts - PREV_BOOT_END_EPOCH )); diff=${diff#-}
    if [ "$diff" -le 300 ]; then echo "${d} ${t}${rest}"; break; fi
  done)
  if [ -n "$SEL_HIT" ]; then
    REASON="hardware_fatal_error"
    logger -t boot-reason-detector "abnormal reboot classified as hardware_fatal_error, matching IPMI SEL entry: ${SEL_HIT}"
  fi
fi

if [ -z "$REASON" ]; then
  # Only the tail of the previous boot's log matters: the direct trigger for a
  # reboot always shows up right before it happened, not somewhere in the
  # middle of a multi-day uptime.
  PREV_BOOT_LOG=$(journalctl --utc -b -1 -n 2000 --no-pager 2>/dev/null)
  if [ -z "$PREV_BOOT_LOG" ]; then
    {
      echo '# HELP node_reboot_detection_error Boot-reason detection cannot run because a precondition is not met'
      echo '# TYPE node_reboot_detection_error gauge'
      echo 'node_reboot_detection_error{error="boot_log_missing_despite_history"} 1'
    } > "$TMP_FILE"
    mv "$TMP_FILE" "$OUTPUT_FILE"
    exit 0
  fi

  if echo "$PREV_BOOT_LOG" | grep -qiE "Kernel panic|Oops:"; then
    REASON="kernel_panic"
  elif echo "$PREV_BOOT_LOG" | grep -qiE "Out of memory: Killed process|oom-kill|oom_reaper"; then
    REASON="oom_kill"
  elif echo "$PREV_BOOT_LOG" | grep -qiE "soft lockup|hard LOCKUP"; then
    REASON="hardware_watchdog"
  elif echo "$PREV_BOOT_LOG" | grep -qiE "Reached target (Reboot|Power-Off|Shutdown)|Stopped target Multi-User System|Rebooting\.|Powering off\."; then
    REASON="clean_shutdown"
  else
    REASON="unknown"
  fi
fi

{
  echo '# HELP node_last_boot_reason Classification of the most recent reboot'
  echo '# TYPE node_last_boot_reason gauge'
  echo "node_last_boot_reason{reason=\"${REASON}\"} 1"
} > "$TMP_FILE"
mv "$TMP_FILE" "$OUTPUT_FILE"

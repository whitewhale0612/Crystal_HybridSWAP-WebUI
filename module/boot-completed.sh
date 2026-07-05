#!/system/bin/sh

MODDIR=${0%/*}
CTL="$MODDIR/bin/chswapctl"
LOG_DIR="$MODDIR/data/logs"
LOG="$LOG_DIR/boot-completed.log"

mkdir -p "$LOG_DIR" 2>/dev/null

run_bounded() {
  seconds="$1"
  shift
  grace="${CHSWAP_TIMEOUT_GRACE_SECONDS:-2}"
  case "$grace" in
    "" | *[!0-9]*) grace=2 ;;
  esac
  timeout_hit=0
  parent_pid=$$
  trap 'timeout_hit=1' 14
  "$@" &
  pid=$!
  timeout_flag="$LOG_DIR/.timeout.$parent_pid.$pid"
  rm -f "$timeout_flag" 2>/dev/null
  (
    sleep "$seconds"
    if kill -0 "$pid" 2>/dev/null; then
      : >"$timeout_flag" 2>/dev/null
      kill "$pid" 2>/dev/null
      sleep "$grace"
      kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null
      kill -14 "$parent_pid" 2>/dev/null
    fi
  ) &
  watchdog=$!
  wait "$pid"
  rc=$?
  kill "$watchdog" 2>/dev/null
  wait "$watchdog" 2>/dev/null
  trap - 14
  if [ "$timeout_hit" -ne 0 ] || [ -f "$timeout_flag" ]; then
    rm -f "$timeout_flag" 2>/dev/null
    return 124
  fi
  rm -f "$timeout_flag" 2>/dev/null
  return "$rc"
}

if [ ! -x "$CTL" ]; then
  echo "boot-completed: chswapctl missing" >>"$LOG"
  exit 0
fi

echo "boot-completed: start $(date +%s 2>/dev/null)" >>"$LOG"
run_bounded 40 "$CTL" api POST /v1/boot/apply \
  --json '{"phase":"boot-completed","timeout_ms":30000}' >>"$LOG" 2>&1 || true
run_bounded 8 "$CTL" api GET /v1/status >>"$LOG" 2>&1 || true
echo "boot-completed: end $(date +%s 2>/dev/null)" >>"$LOG"
exit 0

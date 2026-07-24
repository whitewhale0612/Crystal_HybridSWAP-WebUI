#!/system/bin/sh

MODDIR=${0%/*}
CTL="$MODDIR/bin/chswapctl"
LOG_DIR="$MODDIR/data/logs"
LOG="$LOG_DIR/post-fs-data.log"

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
  echo "post-fs-data: chswapctl missing" >>"$LOG"
  exit 0
fi

echo "post-fs-data: start $(date +%s 2>/dev/null)" >>"$LOG"

if [ -f "$MODDIR/oplus-policy.sh" ]; then
  . "$MODDIR/oplus-policy.sh"
  chswap_mount_oplus_policy "$MODDIR" "$LOG"
fi

run_bounded 35 "$CTL" api POST /v1/boot/apply \
  --json '{"phase":"post-fs-data"}' >>"$LOG" 2>&1 || true

echo "post-fs-data: end $(date +%s 2>/dev/null)" >>"$LOG"
exit 0

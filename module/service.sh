#!/system/bin/sh

MODDIR=${0%/*}
CTL="$MODDIR/bin/chswapctl"
LOG_DIR="$MODDIR/data/logs"
STATE_DIR="$MODDIR/data/state"
LOG="$LOG_DIR/service.log"
SUPERVISOR_PIDFILE="$STATE_DIR/daemon-supervisor.pid"

mkdir -p "$LOG_DIR" "$STATE_DIR" 2>/dev/null

read_pid_file() {
  [ -f "$1" ] || return 1
  IFS= read -r pid <"$1" || return 1
  case "$pid" in
    "" | *[!0-9]*) return 1 ;;
  esac
  printf '%s\n' "$pid"
}

pid_is_supervisor() {
  pid="$1"
  [ -n "$pid" ] || return 1
  [ -d "/proc/$pid" ] || return 1
  cmdline="$(tr '\000' ' ' <"/proc/$pid/cmdline" 2>/dev/null || true)"
  case "$cmdline" in
    *"$MODDIR/daemon.sh"*) return 0 ;;
  esac
  return 1
}

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
  echo "service: chswapctl missing" >>"$LOG"
  exit 0
fi

echo "service: start $(date +%s 2>/dev/null)" >>"$LOG"

status_tmp="$STATE_DIR/status.$$.json"
if run_bounded 8 "$CTL" api GET /v1/status >"$status_tmp" 2>>"$LOG" &&
  grep -q '"mode":"diagnostic"' "$status_tmp"; then
  rm -f "$status_tmp" 2>/dev/null
  echo "service: diagnostic profile; skip watchdog and daemon" >>"$LOG"
  exit 0
fi
rm -f "$status_tmp" 2>/dev/null

# Initial boot reconcile (service phase): one-shot apply after core services.
run_bounded 40 "$CTL" api POST /v1/boot/apply \
  --json '{"phase":"service","timeout_ms":30000}' >>"$LOG" 2>&1
rc=$?
if [ "$rc" -ne 0 ]; then
  echo "service: initial reconcile failed rc=$rc" >>"$LOG"
  echo "service: launch daemon anyway for later takeover attempts" >>"$LOG"
fi

# Launch the standing WebUI owner: a persistent daemon that reconciles the
# profile onto the kernel whenever an external actor drifts a managed node.
# Fully detached so it survives the launching shell; the supervisor respawns it
# and stops on disable.
old_supervisor_pid="$(read_pid_file "$SUPERVISOR_PIDFILE" 2>/dev/null || true)"
if pid_is_supervisor "$old_supervisor_pid"; then
  echo "service: daemon supervisor already running pid=$old_supervisor_pid" >>"$LOG"
  exit 0
fi
rm -f "$SUPERVISOR_PIDFILE" 2>/dev/null

chmod 0755 "$MODDIR/daemon.sh" 2>/dev/null
nohup "$MODDIR/daemon.sh" </dev/null >/dev/null 2>&1 &
supervisor_pid=$!
printf '%s\n' "$supervisor_pid" >"$SUPERVISOR_PIDFILE" 2>/dev/null

sleep 1
if pid_is_supervisor "$supervisor_pid"; then
  echo "service: daemon launched pid=$supervisor_pid" >>"$LOG"
else
  rm -f "$SUPERVISOR_PIDFILE" 2>/dev/null
  echo "service: daemon launch failed pid=$supervisor_pid" >>"$LOG"
fi
exit 0

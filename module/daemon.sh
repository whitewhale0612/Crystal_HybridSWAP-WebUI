#!/system/bin/sh
# Supervisor for the standing WebUI-owner daemon. Kept separate from service.sh
# so it can be launched fully detached (nohup) and respawn the daemon on crash.

MODDIR=${0%/*}
CTL="$MODDIR/bin/chswapctl"
LOG_DIR="$MODDIR/data/logs"
STATE_DIR="$MODDIR/data/state"
SUPERVISOR_PIDFILE="$STATE_DIR/daemon-supervisor.pid"
child_pid=
stop_requested=0

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

cleanup_supervisor_pid() {
  old_pid="$(read_pid_file "$SUPERVISOR_PIDFILE" 2>/dev/null || true)"
  [ "$old_pid" = "$$" ] && rm -f "$SUPERVISOR_PIDFILE" 2>/dev/null
}

terminate_child() {
  target="$1"
  grace="${CHSWAP_TIMEOUT_GRACE_SECONDS:-2}"
  case "$grace" in
    "" | *[!0-9]*) grace=2 ;;
  esac
  [ -n "$target" ] || return 0
  kill "$target" 2>/dev/null || return 0
  sleep "$grace"
  kill -0 "$target" 2>/dev/null && kill -9 "$target" 2>/dev/null
}

stop_supervisor() {
  stop_requested=1
  terminate_child "$child_pid"
  exit 0
}

trap 'stop_supervisor' TERM INT
trap 'cleanup_supervisor_pid' 0

if [ ! -x "$CTL" ]; then
  echo "daemon supervisor: chswapctl missing" >>"$LOG_DIR/daemon.log"
  exit 0
fi

existing_pid="$(read_pid_file "$SUPERVISOR_PIDFILE" 2>/dev/null || true)"
if [ -n "$existing_pid" ] && [ "$existing_pid" != "$$" ] &&
  pid_is_supervisor "$existing_pid"; then
  echo "daemon supervisor: already running pid=$existing_pid" >>"$LOG_DIR/daemon.log"
  exit 0
fi
printf '%s\n' "$$" >"$SUPERVISOR_PIDFILE" 2>/dev/null

while [ ! -f "$MODDIR/disable" ] && [ ! -f "$MODDIR/remove" ]; do
  "$CTL" daemon >>"$LOG_DIR/daemon.log" 2>&1 &
  child_pid=$!
  wait "$child_pid"
  rc=$?
  child_pid=
  [ "$stop_requested" -ne 0 ] && break
  [ -f "$MODDIR/disable" ] && break
  [ -f "$MODDIR/remove" ] && break
  [ "$rc" -eq 0 ] && break
  sleep 5
done

echo "daemon supervisor: exit $(date +%s 2>/dev/null)" >>"$LOG_DIR/daemon.log"

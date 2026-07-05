#!/system/bin/sh

MODDIR=${0%/*}
CTL="$MODDIR/bin/chswapctl"
LOG_DIR="$MODDIR/data/logs"
STATE_DIR="$MODDIR/data/state"
LOG="$LOG_DIR/uninstall.log"

mkdir -p "$LOG_DIR" "$STATE_DIR" 2>/dev/null
if ! touch "$MODDIR/remove" 2>/dev/null; then
  echo "uninstall: failed to create remove marker" >>"$LOG"
fi

read_pid_file() {
  [ -f "$1" ] || return 1
  IFS= read -r pid <"$1" || return 1
  case "$pid" in
    "" | *[!0-9]*) return 1 ;;
  esac
  printf '%s\n' "$pid"
}

proc_cmdline() {
  tr '\000' ' ' <"/proc/$1/cmdline" 2>/dev/null || true
}

proc_exe() {
  readlink "/proc/$1/exe" 2>/dev/null || true
}

pid_belongs_to_module() {
  pid="$1"
  kind="$2"
  [ -n "$pid" ] || return 1
  [ -d "/proc/$pid" ] || return 1

  cmdline="$(proc_cmdline "$pid")"
  exe="$(proc_exe "$pid")"

  case "$kind" in
    daemon)
      case "$exe" in
        "$CTL" | "$CTL (deleted)") return 0 ;;
      esac
      case "$cmdline" in
        *"$CTL daemon"*) return 0 ;;
      esac
      ;;
    supervisor)
      case "$cmdline" in
        *"$MODDIR/daemon.sh"*) return 0 ;;
      esac
      ;;
  esac
  return 1
}

terminate_pid() {
  pid="$1"
  kind="$2"
  label="$3"
  grace="${CHSWAP_TIMEOUT_GRACE_SECONDS:-2}"
  case "$grace" in
    "" | *[!0-9]*) grace=2 ;;
  esac

  if ! pid_belongs_to_module "$pid" "$kind"; then
    echo "uninstall: skip stale or unowned $label pid=$pid" >>"$LOG"
    return 0
  fi

  echo "uninstall: stop $label pid=$pid" >>"$LOG"
  kill "$pid" 2>/dev/null || true
  sleep "$grace"
  if pid_belongs_to_module "$pid" "$kind"; then
    kill -9 "$pid" 2>/dev/null || true
  fi
}

terminate_pidfile() {
  pidfile="$1"
  kind="$2"
  label="$3"
  pid="$(read_pid_file "$pidfile" 2>/dev/null || true)"
  if [ -n "$pid" ]; then
    terminate_pid "$pid" "$kind" "$label"
  fi
  rm -f "$pidfile" 2>/dev/null
}

terminate_pidfile "$STATE_DIR/daemon.pid" daemon daemon
terminate_pidfile "$STATE_DIR/daemon-supervisor.pid" supervisor supervisor

for proc in /proc/[0-9]*; do
  [ -d "$proc" ] || continue
  pid="${proc#/proc/}"
  pid_belongs_to_module "$pid" daemon && terminate_pid "$pid" daemon daemon
  pid_belongs_to_module "$pid" supervisor && terminate_pid "$pid" supervisor supervisor
done

exit 0

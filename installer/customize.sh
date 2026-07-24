#!/system/bin/sh

if ! command -v ui_print >/dev/null 2>&1; then
  ui_print() {
    echo "$1"
  }
fi

chswap_fail() {
  ui_print "! $1"
  exit 1
}

ui_print "- Preparing Crystal Hybridswap WebUI"

[ -n "${MODPATH:-}" ] || chswap_fail "MODPATH is not set"

arch="${ARCH:-$(uname -m 2>/dev/null || echo unknown)}"
case "$arch" in
  arm64 | arm64-v8a | aarch64)
    ;;
  *)
    chswap_fail "Only arm64 devices are supported"
    ;;
esac

[ -f "$MODPATH/bin/chswapctl" ] ||
  chswap_fail "bin/chswapctl is missing"

[ -f "$MODPATH/oplus-policy.sh" ] ||
  chswap_fail "oplus-policy.sh is missing"

# Build device-specific XML overlays from the installed ColorOS files. Missing
# files mean this is not a supported Oplus layout and are intentionally skipped.
. "$MODPATH/oplus-policy.sh"
if ! chswap_prepare_oplus_policy "${CHSWAP_OPLUS_ROOT:-/my_stock}" "$MODPATH"; then
  ui_print "! Failed to prepare Oplus background policy; continuing without it"
fi

chmod 0755 \
  "$MODPATH/post-fs-data.sh" \
  "$MODPATH/service.sh" \
  "$MODPATH/boot-completed.sh" \
  "$MODPATH/daemon.sh" \
  "$MODPATH/uninstall.sh" \
  "$MODPATH/bin/chswapctl" 2>/dev/null ||
  chswap_fail "failed to set executable permissions"
chmod 0644 "$MODPATH/oplus-policy.sh" 2>/dev/null ||
  chswap_fail "failed to set Oplus policy helper permissions"

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE_DIR="$ROOT_DIR/module"
INSTALLER_DIR="$ROOT_DIR/installer"
DIST_DIR="${CHSWAP_DIST_DIR:-$ROOT_DIR/dist}"
PROP="$MODULE_DIR/module.prop"

die() {
  echo "error: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

read_prop() {
  local key="$1"
  awk -F= -v key="$key" '$1 == key {
    sub(/^[^=]*=/, "")
    print
    exit
  }' "$PROP"
}

need zip
need unzip

[ -f "$PROP" ] || die "missing module/module.prop"

ID="$(read_prop id)"
VERSION="$(read_prop version)"
VERSION_CODE="$(read_prop versionCode)"

[ -n "$ID" ] || die "module id is empty"
[ -n "$VERSION" ] || die "module version is empty"
[ -n "$VERSION_CODE" ] || die "module versionCode is empty"

case "$ID" in
  *[!A-Za-z0-9._-]*)
    die "module id contains unsafe characters: $ID"
    ;;
esac

OUT="${1:-$DIST_DIR/${ID}-v${VERSION}.zip}"
case "$OUT" in
  /*) ;;
  *) OUT="$ROOT_DIR/$OUT" ;;
esac

module_files=(
  module.prop
  launcher.png
  skip_mount
  oplus-policy.sh
  post-fs-data.sh
  service.sh
  boot-completed.sh
  daemon.sh
  uninstall.sh
  bin/chswapctl
)

no_slash_tail() {
  local value="$1"
  [ -n "$value" ] || return 1
  case "$value" in
    */*) return 1 ;;
  esac
  return 0
}

is_listed_module_file() {
  local candidate="$1"
  local path
  for path in "${module_files[@]}"; do
    [ "$candidate" = "$path" ] && return 0
  done
  return 1
}

is_runtime_webroot_file() {
  local rel="$1"
  local tail
  case "$rel" in
    index.html | config.json | launcher.png | vendor/preact.mjs)
      return 0
      ;;
    css/*.css)
      tail="${rel#css/}"
      no_slash_tail "$tail"
      return $?
      ;;
    js/components/*.js)
      tail="${rel#js/components/}"
      no_slash_tail "$tail"
      return $?
      ;;
    js/pages/*.js)
      tail="${rel#js/pages/}"
      no_slash_tail "$tail"
      return $?
      ;;
    js/*.js)
      tail="${rel#js/}"
      no_slash_tail "$tail"
      return $?
      ;;
  esac
  return 1
}

is_dev_webroot_file() {
  local rel="$1"
  local tail
  case "$rel" in
    README.md)
      return 0
      ;;
    mock/*.json)
      tail="${rel#mock/}"
      no_slash_tail "$tail"
      return $?
      ;;
  esac
  return 1
}

is_allowed_module_source_file() {
  local rel="$1"
  local webrel
  is_listed_module_file "$rel" && return 0
  [ "$rel" = "README.md" ] && return 0
  case "$rel" in
    webroot/*)
      webrel="${rel#webroot/}"
      is_runtime_webroot_file "$webrel" && return 0
      is_dev_webroot_file "$webrel" && return 0
      ;;
  esac
  return 1
}

is_allowed_installer_source_file() {
  case "$1" in
    customize.sh | META-INF/com/google/android/update-binary | \
      META-INF/com/google/android/updater-script)
      return 0
      ;;
  esac
  return 1
}

is_allowed_zip_entry() {
  local entry="$1"
  local webrel
  is_listed_module_file "$entry" && return 0
  case "$entry" in
    customize.sh | META-INF/com/google/android/update-binary | \
      META-INF/com/google/android/updater-script)
      return 0
      ;;
    webroot/*)
      webrel="${entry#webroot/}"
      is_runtime_webroot_file "$webrel" && return 0
      if [ "${CHSWAP_PACKAGE_INCLUDE_DEV_ASSETS:-0}" = "1" ] &&
        is_dev_webroot_file "$webrel"; then
        return 0
      fi
      ;;
  esac
  return 1
}

for path in "${module_files[@]}"; do
  [ -f "$MODULE_DIR/$path" ] || die "missing module/$path"
  [ ! -L "$MODULE_DIR/$path" ] || die "module/$path must not be a symlink"
done

[ ! -L "$MODULE_DIR" ] || die "module must not be a symlink"
if find "$MODULE_DIR" -type l -print -quit | grep -q .; then
  die "module contains symlinks"
fi

while IFS= read -r rel; do
  is_allowed_module_source_file "$rel" ||
    die "module contains unapproved file: $rel"
done < <(cd "$MODULE_DIR" && find . -type f -printf '%P\n' | LC_ALL=C sort)

[ -d "$MODULE_DIR/webroot" ] || die "missing module/webroot"

[ -f "$INSTALLER_DIR/customize.sh" ] || die "missing installer/customize.sh"
[ -f "$INSTALLER_DIR/META-INF/com/google/android/update-binary" ] ||
  die "missing installer update-binary"
[ -f "$INSTALLER_DIR/META-INF/com/google/android/updater-script" ] ||
  die "missing installer updater-script"
[ ! -L "$INSTALLER_DIR" ] || die "installer must not be a symlink"
if find "$INSTALLER_DIR" -type l -print -quit | grep -q .; then
  die "installer contains symlinks"
fi
while IFS= read -r rel; do
  is_allowed_installer_source_file "$rel" ||
    die "installer contains unapproved file: $rel"
done < <(cd "$INSTALLER_DIR" && find . -type f -printf '%P\n' | LC_ALL=C sort)

[ -x "$MODULE_DIR/bin/chswapctl" ] || die "module/bin/chswapctl is not executable"

newer_backend_source="$(
  find "$ROOT_DIR/backend/chswapctl" "$ROOT_DIR/backend/go.mod" \
    "$ROOT_DIR/backend/build.sh" \
    -type f \( -name '*.go' -o -name 'go.mod' -o -name 'build.sh' \) \
    -newer "$MODULE_DIR/bin/chswapctl" -print -quit
)"
if [ -n "$newer_backend_source" ] &&
  [ "${CHSWAP_SKIP_STALE_BUILD_CHECK:-0}" != "1" ]; then
  die "module/bin/chswapctl is older than backend source; run ./backend/build.sh"
fi

if command -v file >/dev/null 2>&1; then
  desc="$(file -b "$MODULE_DIR/bin/chswapctl")"
  case "$desc" in
    *"ARM aarch64"* | *"ARM64"*) ;;
    *) die "module/bin/chswapctl is not an arm64 ELF: $desc" ;;
  esac
  case "$desc" in
    *"statically linked"*) ;;
    *) die "module/bin/chswapctl is not statically linked: $desc" ;;
  esac
fi

mkdir -p "$DIST_DIR" "$(dirname "$OUT")"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
stage="$tmp_dir/stage"
mkdir -p "$stage"

copy_file() {
  local src="$1"
  local rel="$2"
  [ -f "$src" ] || die "missing file for package: $src"
  [ ! -L "$src" ] || die "refusing symlink in package: $src"
  mkdir -p "$stage/$(dirname "$rel")"
  cp -p "$src" "$stage/$rel"
}

for path in "${module_files[@]}"; do
  copy_file "$MODULE_DIR/$path" "$path"
done

while IFS= read -r rel; do
  if is_runtime_webroot_file "$rel"; then
    copy_file "$MODULE_DIR/webroot/$rel" "webroot/$rel"
  elif is_dev_webroot_file "$rel"; then
    if [ "${CHSWAP_PACKAGE_INCLUDE_DEV_ASSETS:-0}" = "1" ]; then
      copy_file "$MODULE_DIR/webroot/$rel" "webroot/$rel"
    fi
  else
    die "module/webroot contains unapproved file: $rel"
  fi
done < <(cd "$MODULE_DIR/webroot" && find . -type f -printf '%P\n' | LC_ALL=C sort)

copy_file "$INSTALLER_DIR/customize.sh" "customize.sh"
copy_file "$INSTALLER_DIR/META-INF/com/google/android/update-binary" \
  "META-INF/com/google/android/update-binary"
copy_file "$INSTALLER_DIR/META-INF/com/google/android/updater-script" \
  "META-INF/com/google/android/updater-script"

chmod 0755 \
  "$stage/customize.sh" \
  "$stage/META-INF/com/google/android/update-binary" \
  "$stage/post-fs-data.sh" \
  "$stage/service.sh" \
  "$stage/boot-completed.sh" \
  "$stage/daemon.sh" \
  "$stage/uninstall.sh" \
  "$stage/bin/chswapctl"
chmod 0644 "$stage/oplus-policy.sh"
chmod 0644 "$stage/META-INF/com/google/android/updater-script"

find "$stage" -type f \( -name ".DS_Store" -o -name "*~" \) -delete
rm -rf "$stage/data" "$stage/logs" "$stage/diagnostics" "$stage/state"

if printf '%s\n' "$(
  find "$stage/webroot" -type f -printf '%P\n'
)" | grep -Eq '(^|/)(\.env|.*\.map|.*~|.*\.swp|.*\.swo)$'; then
  die "webroot contains development-only files"
fi

manifest_expected="$tmp_dir/zip-manifest.expected"
manifest_actual="$tmp_dir/zip-manifest.actual"
(
  cd "$stage"
  find . -type f -printf '%P\n' | LC_ALL=C sort
) >"$manifest_expected"
while IFS= read -r entry; do
  [ -n "$entry" ] || continue
  is_allowed_zip_entry "$entry" || die "stage contains unapproved file: $entry"
done <"$manifest_expected"

rm -f "$OUT"
(
  cd "$stage"
  zip -9 -X -q "$OUT" -@ <"$manifest_expected"
)

unzip -tq "$OUT"

entries="$(unzip -Z1 "$OUT")"
printf '%s\n' "$entries" | LC_ALL=C sort >"$manifest_actual"
if ! cmp -s "$manifest_expected" "$manifest_actual"; then
  command -v diff >/dev/null 2>&1 &&
    diff -u "$manifest_expected" "$manifest_actual" >&2 || true
  die "zip manifest differs from staged manifest"
fi
printf '%s\n' "$entries" | grep -qx 'module.prop' ||
  die "zip does not contain module.prop at root"
printf '%s\n' "$entries" | grep -qx 'bin/chswapctl' ||
  die "zip does not contain bin/chswapctl"
printf '%s\n' "$entries" |
  grep -qx 'META-INF/com/google/android/update-binary' ||
  die "zip does not contain update-binary"
printf '%s\n' "$entries" |
  grep -qx 'META-INF/com/google/android/updater-script' ||
  die "zip does not contain updater-script"
printf '%s\n' "$entries" | grep -qx 'customize.sh' ||
  die "zip does not contain customize.sh"

if printf '%s\n' "$entries" | grep -Eq '^(data|logs|diagnostics|state)/'; then
  die "zip contains runtime state"
fi

if printf '%s\n' "$entries" |
  grep -Eiq '(^|/)(docs|contracts|backend|dist|\.git)(/|$)'; then
  die "zip contains source-only files"
fi

if [ "${CHSWAP_PACKAGE_INCLUDE_DEV_ASSETS:-0}" != "1" ] &&
  printf '%s\n' "$entries" |
    grep -Eq '(^|/)README([^/]*)$|^webroot/mock/'; then
  die "zip contains development WebUI assets"
fi

echo "created: $OUT"
unzip -l "$OUT" | tail -n 3

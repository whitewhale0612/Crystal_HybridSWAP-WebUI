#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROP="$ROOT_DIR/module/module.prop"
DIST_DIR="${CHSWAP_DIST_DIR:-$ROOT_DIR/dist}"

die() {
  echo "error: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

log() {
  printf '\n==> %s\n' "$*"
}

read_prop() {
  local key="$1"
  awk -F= -v key="$key" '$1 == key {
    sub(/^[^=]*=/, "")
    print
    exit
  }' "$PROP"
}

no_slash_tail() {
  local value="$1"
  [[ -n "$value" && "$value" != */* ]]
}

is_release_zip_entry() {
  local entry="$1"
  local tail
  case "$entry" in
    module.prop | launcher.png | skip_mount | post-fs-data.sh | service.sh | \
      boot-completed.sh | daemon.sh | uninstall.sh | \
      bin/chswapctl | customize.sh | \
      META-INF/com/google/android/update-binary | \
      META-INF/com/google/android/updater-script | \
      webroot/index.html | webroot/config.json | webroot/launcher.png | \
      webroot/vendor/preact.mjs)
      return 0
      ;;
    webroot/css/*.css)
      tail="${entry#webroot/css/}"
      no_slash_tail "$tail"
      return $?
      ;;
    webroot/js/components/*.js)
      tail="${entry#webroot/js/components/}"
      no_slash_tail "$tail"
      return $?
      ;;
    webroot/js/pages/*.js)
      tail="${entry#webroot/js/pages/}"
      no_slash_tail "$tail"
      return $?
      ;;
    webroot/js/*.js)
      tail="${entry#webroot/js/}"
      no_slash_tail "$tail"
      return $?
      ;;
  esac
  return 1
}

check_zip_manifest() {
  local zip="$1"
  local entry
  local required

  while IFS= read -r entry; do
    [ -n "$entry" ] || continue
    is_release_zip_entry "$entry" ||
      die "release zip contains unapproved entry: $entry"
  done < <(unzip -Z1 "$zip" | LC_ALL=C sort)

  for required in \
    module.prop \
    launcher.png \
    bin/chswapctl \
    webroot/config.json \
    webroot/launcher.png \
    customize.sh \
    META-INF/com/google/android/update-binary \
    META-INF/com/google/android/updater-script \
    post-fs-data.sh \
    service.sh \
    boot-completed.sh \
    daemon.sh \
    uninstall.sh; do
    unzip -Z1 "$zip" | grep -qx "$required" ||
      die "release zip is missing required entry: $required"
  done

  if unzip -Z1 "$zip" |
    grep -Eiq '(^|/)(docs|contracts|backend|dist|\.git)(/|$)'; then
    die "release zip contains source-only entries"
  fi
  if unzip -Z1 "$zip" | grep -Eq '(^|/)README([^/]*)$|^webroot/mock/'; then
    die "release zip contains development WebUI assets"
  fi
}

case "${1:-}" in
  -h | --help)
    cat <<'EOF'
Usage: scripts/release.sh [output.zip]

Runs release validation, rebuilds the arm64 backend, creates a flashable module
zip, and writes output.zip.sha256 next to it. Set CHSWAP_RELEASE_SKIP_TESTS=1
only for local smoke packaging.
EOF
    exit 0
    ;;
esac

need bash
need sh
need go
need jq
need node
need python3
need zip
need unzip

if ! command -v sha256sum >/dev/null 2>&1 &&
  ! command -v shasum >/dev/null 2>&1; then
  die "sha256sum or shasum is required"
fi

[ -f "$PROP" ] || die "missing module/module.prop"

ID="$(read_prop id)"
VERSION="$(read_prop version)"
VERSION_CODE="$(read_prop versionCode)"

[ -n "$ID" ] || die "module id is empty"
[ -n "$VERSION" ] || die "module version is empty"
[ -n "$VERSION_CODE" ] || die "module versionCode is empty"

ZIP_OUT="${1:-$DIST_DIR/${ID}-v${VERSION}.zip}"
case "$ZIP_OUT" in
  /*) ;;
  *) ZIP_OUT="$ROOT_DIR/$ZIP_OUT" ;;
esac

log "Checking script syntax"
bash -n \
  "$ROOT_DIR/backend/build.sh" \
  "$ROOT_DIR/backend/run-tests.sh" \
  "$ROOT_DIR/scripts/package-module.sh" \
  "$ROOT_DIR/scripts/release.sh"
sh -n \
  "$ROOT_DIR/installer/customize.sh" \
  "$ROOT_DIR/installer/META-INF/com/google/android/update-binary" \
  "$ROOT_DIR/module/post-fs-data.sh" \
  "$ROOT_DIR/module/service.sh" \
  "$ROOT_DIR/module/boot-completed.sh" \
  "$ROOT_DIR/module/daemon.sh" \
  "$ROOT_DIR/module/uninstall.sh"

log "Checking frontend modules"
while IFS= read -r -d '' file; do
  node --input-type=module --check <"$file" >/dev/null
done < <(find "$ROOT_DIR/module/webroot/js" -name '*.js' -print0 | sort -z)

log "Checking JSON and YAML fixtures"
python3 - "$ROOT_DIR" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
examples = root / "contracts/examples"
mocks = root / "module/webroot/mock"
spec_path = root / "contracts/chswap-api.v1.yaml"

try:
    import yaml
except ImportError:
    raise SystemExit("PyYAML is required to parse contracts/chswap-api.v1.yaml")

def load_json(path):
    with path.open(encoding="utf-8") as fh:
        return json.load(fh)

def json_name_set(path):
    return {p.name for p in path.glob("*.json")}

example_json = {p.name: load_json(p) for p in sorted(examples.glob("*.json"))}
mock_json = {p.name: load_json(p) for p in sorted(mocks.glob("*.json"))}

expected_mocks = set(example_json) - {"profile.default.json"}
expected_mocks.add("profile.envelope.json")
if json_name_set(mocks) != expected_mocks:
    missing = sorted(expected_mocks - set(mock_json))
    extra = sorted(set(mock_json) - expected_mocks)
    raise SystemExit(f"mock fixture set drift; missing={missing} extra={extra}")

drift = []
for name in sorted(expected_mocks - {"profile.envelope.json"}):
    if example_json[name] != mock_json[name]:
        drift.append(name)
if mock_json["profile.envelope.json"].get("data") != example_json["profile.default.json"]:
    drift.append("profile.envelope.json:data != profile.default.json")
if drift:
    raise SystemExit("fixture drift: " + ", ".join(drift))

spec = yaml.safe_load(spec_path.open(encoding="utf-8"))

def resolve_ref(ref):
    if not isinstance(ref, str):
        return "$ref is not a string"
    if not ref.startswith("#/"):
        return f"external $ref is not vendored: {ref}"
    node = spec
    for raw in ref[2:].split("/"):
        key = raw.replace("~1", "/").replace("~0", "~")
        if isinstance(node, dict) and key in node:
            node = node[key]
        elif isinstance(node, list) and key.isdigit() and int(key) < len(node):
            node = node[int(key)]
        else:
            return f"unresolved $ref: {ref}"
    return None

ref_errors = []

def walk(node, path):
    if isinstance(node, dict):
        if "$ref" in node:
            error = resolve_ref(node["$ref"])
            if error:
                ref_errors.append(".".join(path + ["$ref"]) + ": " + error)
        for key, value in node.items():
            walk(value, path + [str(key)])
    elif isinstance(node, list):
        for index, value in enumerate(node):
            walk(value, path + [str(index)])

walk(spec, [])
if ref_errors:
    raise SystemExit("OpenAPI ref errors:\n" + "\n".join(ref_errors))
PY

if [ "${CHSWAP_RELEASE_SKIP_TESTS:-0}" != "1" ]; then
  log "Running backend tests"
  "$ROOT_DIR/backend/run-tests.sh"

  log "Running go vet"
  (cd "$ROOT_DIR/backend" && go vet ./...)
fi

log "Building arm64 backend"
"$ROOT_DIR/backend/build.sh"

log "Packaging module"
"$ROOT_DIR/scripts/package-module.sh" "$ZIP_OUT"

log "Checking release zip manifest"
check_zip_manifest "$ZIP_OUT"

log "Writing checksum"
zip_dir="$(dirname "$ZIP_OUT")"
zip_base="$(basename "$ZIP_OUT")"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$zip_dir" && sha256sum "$zip_base" >"$zip_base.sha256")
else
  (cd "$zip_dir" && shasum -a 256 "$zip_base" >"$zip_base.sha256")
fi

echo "release: $ZIP_OUT"
echo "sha256: $ZIP_OUT.sha256"

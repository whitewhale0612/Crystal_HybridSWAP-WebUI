#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

cd "$ROOT_DIR/backend"
go test ./...

HOST_BIN="$TMP_DIR/chswapctl"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags='-s -w -extldflags "-static"' \
  -o "$HOST_BIN" \
  ./chswapctl

make_zram_tree() {
  local root="$1"
  mkdir -p "$root/sys/block/zram0" "$root/sys/class/zram-control" \
    "$root/proc" "$root/dev/block" "$root/dev/memcg"
  : > "$root/dev/block/zram0"
  cat > "$root/proc/swaps" <<'EOF'
Filename				Type		Size		Used		Priority
/dev/block/zram0                        partition	12582912	1024		32767
EOF
  printf '1\n' > "$root/sys/block/zram0/initstate"
  printf '12884901888\n' > "$root/sys/block/zram0/disksize"
  printf '0\n' > "$root/sys/block/zram0/reset"
  printf '0\n' > "$root/sys/block/zram0/mem_limit"
  printf 'lzo [lz4]\n' > "$root/sys/block/zram0/comp_algorithm"
  printf 'none\n' > "$root/sys/block/zram0/backing_dev"
  printf 'none\n' > "$root/sys/block/zram0/hybridswap_loop_device"
  printf 'Crystal Hybridswap stage-3\n' > "$root/sys/block/zram0/hybridswap_report"
  cat > "$root/sys/block/zram0/hybridswap_crystal_stat" <<'EOF'
dev_life_level 0
writeback_quota_limit_bytes 2147483648
writeback_quota_used_bytes 0
writeback_quota_remaining_bytes 2147483648
writeback_quota_used_pages 0
writeback_quota_remaining_pages 0
zram_bd_physical_write_pages 0
zram_bd_physical_write_bytes 0
zram_bd_physical_write_ios_count 0
zram_bd_physical_write_failed_pages 0
zram_bd_physical_read_pages 0
zram_bd_physical_read_ios_count 0
zram_bd_physical_read_failed_pages 0
EOF
  printf '128 16 64\n' > "$root/sys/block/zram0/bd_stat"
  printf '145540224 146486320 173502464 0 0 0 0 0 0\n' \
    > "$root/sys/block/zram0/mm_stat"
  printf 'enabled: 1\nused_blocks: 42359\nstored_bytes: 145540224\n' \
    > "$root/sys/block/zram0/zms_stat"
  printf 'packed_bytes: 146486320\n' >> "$root/sys/block/zram0/zms_stat"
  printf 'physical_read_pages: 0\nphysical_read_ios: 0\n' >> "$root/sys/block/zram0/zms_stat"
  printf 'physical_read_failed_pages: 0\nphysical_write_pages: 0\n' >> "$root/sys/block/zram0/zms_stat"
  printf 'physical_write_bytes: 0\nphysical_write_ios: 0\n' >> "$root/sys/block/zram0/zms_stat"
  printf 'physical_write_failed_pages: 0\n' >> "$root/sys/block/zram0/zms_stat"
  for node in \
    hybridswap_enable hybridswap_core_enable hybridswap_swapd_pause \
    hybridswap_loglevel hybridswap_dev_life hybridswap_quota_day \
    hybridswap_zram_increase; do
    printf '0\n' > "$root/sys/block/zram0/$node"
  done
  printf '75\n' > "$root/dev/memcg/memory.zram_wm_ratio"
  printf '0 512 1024 512\n' > "$root/dev/memcg/memory.avail_buffers"
  printf '1\n' > "$root/dev/memcg/memory.erm_avail_buffer_enable"
  printf '512 1024\n' > "$root/dev/memcg/memory.erm_avail_buffer"
}

run_api() {
  CHSWAP_SYS_ROOT="$1/sys" \
  CHSWAP_PROC_ROOT="$1/proc" \
  CHSWAP_DEV_ROOT="$1/dev" \
  CHSWAP_MODDIR="$1/module" \
  CHSWAP_DATA_DIR="$1/module/data" \
  CHSWAP_SKIP_KSUD=1 \
  "$HOST_BIN" api "$2" "$3" "${@:4}"
}

UNSUPPORTED="$TMP_DIR/unsupported"
make_zram_tree "$UNSUPPORTED"
rm -f "$UNSUPPORTED/sys/block/zram0/hybridswap_report"
rm -f "$UNSUPPORTED/sys/block/zram0/hybridswap_loop_device"

run_api "$UNSUPPORTED" GET /v1/capabilities | jq -e \
  '.ok == true and .data.frontend_gate.allowed == false and
   (.data.kernel.required_nodes_missing | length) > 0' >/dev/null
run_api "$UNSUPPORTED" GET /v1/status | jq -e \
  '.ok == true and .data.frontend_gate.allowed == false and
   (.warnings[] | select(.code == "W_KERNEL_NODE_MISSING"))' >/dev/null
OUT_JSON="$TMP_DIR/chswap.out"

if run_api "$UNSUPPORTED" POST /v1/apply --json '{"mode":"runtime"}' >"$OUT_JSON"; then
  echo "unsupported apply unexpectedly succeeded" >&2
  exit 1
fi
jq -e '.ok == false and .error.code == "E_KERNEL_NODE_MISSING"' \
  "$OUT_JSON" >/dev/null
if run_api "$UNSUPPORTED" POST /v1/boot/apply \
  --json '{"phase":"service","timeout_ms":1000}' >"$OUT_JSON"; then
  echo "unsupported boot apply unexpectedly succeeded" >&2
  exit 1
fi
jq -e '.ok == false and .error.code == "E_KERNEL_NODE_MISSING"' \
  "$OUT_JSON" >/dev/null

SUPPORTED="$TMP_DIR/supported"
make_zram_tree "$SUPPORTED"

run_api "$SUPPORTED" GET /v1/capabilities | jq -e \
  '.ok == true and .data.frontend_gate.allowed == true and
   (.data.kernel.required_nodes_missing // [] | length) == 0' >/dev/null
run_api "$SUPPORTED" POST /v1/profile/validate \
  --json "$(cat "$ROOT_DIR/contracts/examples/profile.default.json")" | jq -e \
  '.ok == true and .data.valid == true' >/dev/null
if run_api "$SUPPORTED" POST /v1/apply \
  --json '{"mode":"dry-run"}{"extra":true}' >"$OUT_JSON"; then
  echo "trailing JSON apply unexpectedly succeeded" >&2
  exit 1
fi
jq -e '.ok == false and .error.code == "E_INVALID_JSON"' \
  "$OUT_JSON" >/dev/null
diagnostic_profile=$(jq '.mode = "diagnostic"' \
  "$ROOT_DIR/contracts/examples/profile.default.json")
diagnostic_apply=$(jq -nc \
  --argjson profile "$diagnostic_profile" \
  '{mode:"reconcile", profile:$profile}')
run_api "$SUPPORTED" POST /v1/apply --json "$diagnostic_apply" | jq -e \
  '.ok == true and .data.changed == false and
   .data.steps[0].name == "profile.mode" and
   .data.steps[0].status == "skipped"' >/dev/null
run_api "$SUPPORTED" POST /v1/apply --json '{"mode":"runtime"}' | jq -e \
  '.ok == true and .data.degraded == false' >/dev/null
run_api "$SUPPORTED" POST /v1/diagnostics/export | jq -e \
  '.ok == true and (.data.path | length > 0)' >/dev/null

echo "backend tests passed"

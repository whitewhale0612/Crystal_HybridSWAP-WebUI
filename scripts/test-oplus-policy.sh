#!/usr/bin/env bash
set -euo pipefail

command -v xmllint >/dev/null 2>&1 || {
  echo "xmllint is required" >&2
  exit 1
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

STOCK="$TMP_DIR/stock/etc/extension"
MODULE="$TMP_DIR/module"
mkdir -p "$STOCK" "$MODULE"

cat >"$STOCK/sys_osense_appmng_decisionmaker_config.xml" <<'EOF'
<resources>
  <filter-conf>
    <version>2025011501</version>
    <filter-name>sys_osense_appmng_decisionmaker_config</filter-name>
  </filter-conf>
  <config name="memory_config">
    <scene name="app_switch"><mem total="16" value="1000|1300|1600|2000|2200"/></scene>
    <scene name="app_switch_C2"><mem total="16" value="1000|1300|1600|3300|3700"/></scene>
    <scene name="app_switch_qb"><mem total="16" value="1000|1100|1200|1300|1600"/></scene>
    <scene name="launcher"><mem total="16" value="1000|1300|1600|2000|2200"/></scene>
    <scene name="launcher_C2"><mem total="16" value="1000|1300|1600|3300|3700"/></scene>
    <scene name="launcher_qb"><mem total="16" value="1000|1100|1200|1300|1600"/></scene>
  </config>
  <config name="clear_level">
    <scene name="app_switch"><mem total="16" value="1000-1200-79|1300-1500-59|1600-1800-39|2000-2300-19|2200-2600-0"/></scene>
    <scene name="app_switch_C2"><mem total="16" value="1000-1200-79|1300-1500-59|1600-1800-39|3300-3600-19|3700-4100-0"/></scene>
    <scene name="app_switch_qb"><mem total="16" value="1000-1200-79|1100-1300-59|1200-1400-39|1300-1500-19|1600-1800-0"/></scene>
    <scene name="launcher"><mem total="16" value="1000-1200-79|1300-1500-59|1600-1800-39|2000-2300-19|2200-2600-0"/></scene>
    <scene name="launcher_C2"><mem total="16" value="1000-1200-79|1300-1500-59|1600-1800-39|3300-3600-19|3700-4100-0"/></scene>
    <scene name="launcher_qb"><mem total="16" value="1000-1200-79|1100-1300-59|1200-1400-39|1300-1500-19|1600-1800-0"/></scene>
  </config>
  <config name="rules_engine">
    <scene id="1000" name="SCENE_APP_SWITCH">
      <rule><policy name="clear" type="smart-force" level_scene="app_switch" level="3"/></rule>
      <rule><policy name="clear" type="smart-force" level_scene="app_switch" level="2"/></rule>
    </scene>
    <scene id="1001" name="SCENE_LAUNCHER">
      <rule><policy name="clear" type="smart-force" level_scene="launcher" level="3"/></rule>
      <rule><policy name="clear" type="smart-force" level_scene="launcher" level="2"/></rule>
    </scene>
  </config>
</resources>
EOF

cat >"$STOCK/sys_osense_memory_config.xml" <<'EOF'
<resources>
  <policy name="resRelease"><config name="res_release" type="bundle">
    <item name="thermal" type="int" value="39"/>
  </config></policy>
  <policy name="swap"><config name="swap" type="bundle">
    <item name="nandSwitch" type="bool" value="true"/>
    <item name="inactiveZramRatioQb" type="string" value="10|15"/>
    <item name="inactiveZramRatio" type="string" value="20|25|30"/>
    <item name="appAnonRatio" type="string" value="20|40|70|90"/>
    <item name="appZramRatio" type="string" value="0|0|20|35"/>
  </config></policy>
  <policy name="dynaZram"><config name="dynaZramConfig" type="bundle">
    <item name="switch" type="bool" value="true"/>
  </config></policy>
</resources>
EOF

cat >"$STOCK/sys_memory_nirvana_config.xml" <<'EOF'
<filter-conf>
  <config name="baseConfig" type="bundle">
    <item name="EnableLightNoSwap" type="bool" value="false"/>
  </config>
  <config name="memoryReleasePolicyConfig" type="bundle">
    <item name="FileMinPercent" type="int" value="40"/>
    <item name="ReleaseMinTargetKb" type="long" value="100000"/>
    <item name="HighTempThreshold" type="int" value="39"/>
  </config>
  <config name="strategyConfig" type="subconfig">
    <item name="ExpectedBgCountMap" type="string" value="6-8|8-12"/>
    <item name="CamExpectedBgCount" type="string" value="6-8|8-12"/>
    <item name="NoSwapLow" type="string" value="6-200000|8-300000"/>
    <item name="NoSwapHigh" type="string" value="6-200000|8-400000"/>
    <item name="NoSwapMax" type="string" value="6-200000|8-400000"/>
  </config>
  <config name="featureControlConfig" type="bundle">
    <item name="ThermalEnterThres" type="int" value="41000"/>
    <item name="ThermalExitThres" type="int" value="39000"/>
  </config>
</filter-conf>
EOF

cat >"$STOCK/sys_osense_feature_common_config.xml" <<'EOF'
<filter-conf>
  <item name="POLICY_PRELOAD" type="bool" value="true"/>
  <config name="idleRecover" type="bundle">
    <item name="interval" type="long" value="20000"/>
  </config>
  <item name="2" type="string" value="0 70000 0 150000 1 70000 1 150000"/>
  <config name="dynamicSwappinessConfig" type="bundle">
    <item name="EnableIdleDynamicSwappiness" type="bool" value="false"/>
    <item name="EnableBalancedSwappiness" type="bool" value="false"/>
    <item name="BalancedSwappiness" type="int" value="100"/>
    <item name="EnableHighloadSwappiness" type="bool" value="false"/>
    <item name="HighloadSwappiness" type="int" value="0"/>
  </config>
</filter-conf>
EOF

cat >"$STOCK/sys_mm_swap_config.xml" <<'EOF'
<filter-conf>
  <scene name="default">
    <cond zramLoad="95"/>
    <limit maxSwapOutKb="20480" totalSwapOutLimitKb="1024000"/>
    <inactiveRatio zramRatio="40|40|55"/>
    <appBase zramRatioThd="80" bgDurThd="150000" minZramRatioInc="5"/>
    <appRatio zramRatio="20|40|70|90"/>
  </scene>
</filter-conf>
EOF

cat >"$STOCK/sys_osense_memory_decisionmaker_config.xml" <<'EOF'
<filter-conf>
  <scene name="lowmem"><mem total="8" value="1000|1200|1400|1700"/></scene>
  <scene name="lowmem_qb"><mem total="8" value="1000|1100|1300"/></scene>
  <scene name="lowmem_zram"><mem total="8" value="1000|1200|2000"/></scene>
  <config name="freemem_config"><mem total="12" value="120|300|400"/></config>
  <config name="zramload_config"><mem total="8" value="90"/></config>
  <config name="bglimit_config"><mem total="12" value="3|5|7"/></config>
  <config name="compress_level"><mem total="8" value="300"/></config>
</filter-conf>
EOF

# shellcheck source=../module/oplus-policy.sh
. "$ROOT_DIR/module/oplus-policy.sh"
chswap_prepare_oplus_policy "$TMP_DIR/stock" "$MODULE" >/dev/null

DEST="$MODULE/my_stock/etc/extension"
MEMORY="$DEST/sys_osense_memory_config.xml"

expect() {
  grep -Fq "$2" "$1" || {
    echo "missing expected text in $1: $2" >&2
    exit 1
  }
}

reject() {
  if grep -Fq "$2" "$1"; then
    echo "unexpected text in $1: $2" >&2
    exit 1
  fi
}

[ "$(find "$DEST" -maxdepth 1 -type f | wc -l)" -eq 6 ]
[ ! -e "$MODULE/oplus-policy" ]
reject "$MEMORY" 'name="dynaZram"'
expect "$MEMORY" 'name="thermal" type="int" value="42"'
expect "$MEMORY" 'name="appZramRatio" type="string" value="45|65|82|93"'
expect "$MEMORY" 'name="fileRatioThreshold" type="string" value="2|40-3|40'
expect "$DEST/sys_memory_nirvana_config.xml" 'name="ExpectedBgCountMap" type="string" value="6-99|8-99|12-99|16-99|24-99"'
expect "$DEST/sys_osense_feature_common_config.xml" 'name="BalancedSwappiness" type="int" value="200"'
expect "$DEST/sys_osense_feature_common_config.xml" 'name="EnableHighloadSwappiness" type="bool" value="true"'
expect "$DEST/sys_mm_swap_config.xml" 'maxSwapOutKb="40960" totalSwapOutLimitKb="2048000"'
expect "$DEST/sys_osense_memory_decisionmaker_config.xml" '<mem total="8" value="700"/>'

APPMNG="$DEST/sys_osense_appmng_decisionmaker_config.xml"
expect "$APPMNG" '<version>2099123101</version>'
[ "$(grep -c 'value="900|1200|1450|1700|1900"' "$APPMNG")" -eq 4 ]
[ "$(grep -c 'value="900-1150-79|1200-1450-59|1450-1700-39|1700-2000-19|1900-2200-0"' "$APPMNG")" -eq 4 ]
[ "$(grep 'policy name="clear"' "$APPMNG" | grep -c 'type="smart".*level="2"')" -eq 2 ]
[ "$(grep 'policy name="clear"' "$APPMNG" | grep -c 'type="smart-force".*level="3"')" -eq 2 ]
expect "$APPMNG" '<scene name="app_switch_qb"><mem total="16" value="1000|1100|1200|1300|1600"/></scene>'
expect "$APPMNG" '<scene name="launcher_qb"><mem total="16" value="1000|1100|1200|1300|1600"/></scene>'
reject "$APPMNG" 'type="smart-force" level_scene="app_switch" level="2"'
reject "$APPMNG" 'type="smart-force" level_scene="launcher" level="2"'

cp "$APPMNG" "$TMP_DIR/appmng.before"
chswap_patch_appmng_decisionmaker "$APPMNG"
cmp -s "$TMP_DIR/appmng.before" "$APPMNG"

cp "$MEMORY" "$TMP_DIR/memory.before"
chswap_patch_osense_memory "$MEMORY"
cmp -s "$TMP_DIR/memory.before" "$MEMORY"
[ "$(sed -n '/<policy name="swap">/,/<\/policy>/p' "$MEMORY" |
  grep -c 'name="fileRatioThreshold"')" -eq 1 ]

chswap_prepare_oplus_policy "$TMP_DIR/not-oplus" "$TMP_DIR/non-oplus" >/dev/null
[ ! -e "$TMP_DIR/non-oplus/my_stock" ]
[ ! -e "$TMP_DIR/non-oplus/oplus-policy" ]

BAD_STOCK="$TMP_DIR/bad-stock/etc/extension"
BAD_MODULE="$TMP_DIR/bad-module"
mkdir -p "$BAD_STOCK" "$BAD_MODULE"
cat >"$BAD_STOCK/sys_osense_appmng_decisionmaker_config.xml" <<'EOF'
<resources>
  <version>1</version>
  <config name="memory_config"/>
</resources>
EOF
chswap_prepare_oplus_policy "$TMP_DIR/bad-stock" "$BAD_MODULE" >/dev/null
[ ! -e "$BAD_MODULE/my_stock/etc/extension/sys_osense_appmng_decisionmaker_config.xml" ]

mkdir -p "$TMP_DIR/bin"
cat >"$TMP_DIR/bin/mount" <<'EOF'
#!/usr/bin/env sh
printf '%s\n' "$*" >>"$CHSWAP_MOUNT_LOG"
EOF
chmod 0755 "$TMP_DIR/bin/mount"
CHSWAP_MOUNT_LOG="$TMP_DIR/mount.calls" \
  PATH="$TMP_DIR/bin:$PATH" \
  chswap_mount_oplus_policy "$MODULE" "$TMP_DIR/mount.log" "$TMP_DIR/stock"
[ "$(wc -l <"$TMP_DIR/mount.calls")" -eq 6 ]
[ "$(grep -c '^oplus-policy: mounted ' "$TMP_DIR/mount.log")" -eq 6 ]
grep -Fq "$TMP_DIR/stock/etc/extension/sys_osense_memory_config.xml" \
  "$TMP_DIR/mount.calls"
grep -Fq "$TMP_DIR/stock/etc/extension/sys_osense_appmng_decisionmaker_config.xml" \
  "$TMP_DIR/mount.calls"
if grep -Fq '/oplus-policy/my_stock/' "$TMP_DIR/mount.calls"; then
  echo "legacy Oplus mount path was used" >&2
  exit 1
fi

xmllint --noout "$DEST"/*.xml

echo "Oplus policy tests passed"

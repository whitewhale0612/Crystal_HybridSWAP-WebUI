#!/system/bin/sh

CHSWAP_OPLUS_POLICY_FILES="
sys_osense_appmng_decisionmaker_config.xml
sys_osense_memory_config.xml
sys_memory_nirvana_config.xml
sys_osense_feature_common_config.xml
sys_mm_swap_config.xml
sys_osense_memory_decisionmaker_config.xml
"

CHSWAP_APPMNG_SCENES="
app_switch
app_switch_C2
app_switch_C3
app_switch_C4
app_switch_C102
app_switch_C103
app_switch_C104
launcher
launcher_C2
launcher_C3
launcher_C4
launcher_C102
launcher_C103
launcher_C104
"

CHSWAP_APPMNG_TRIGGER_16='900|1200|1450|1700|1900'
CHSWAP_APPMNG_CLEAR_16='900-1150-79|1200-1450-59|1450-1700-39|1700-2000-19|1900-2200-0'
CHSWAP_APPMNG_VERSION=2099123101

chswap_oplus_print() {
  local message="$1"
  if command -v ui_print >/dev/null 2>&1; then
    ui_print "$message"
  else
    printf '%s\n' "$message"
  fi
}

chswap_copy_policy_file() {
  local src="$1"
  local dst="$2"

  cp -af "$src" "$dst" 2>/dev/null || cp -pf "$src" "$dst" 2>/dev/null ||
    return 1
  if command -v chcon >/dev/null 2>&1; then
    chcon --reference="$src" "$dst" 2>/dev/null || true
  fi
  chmod 0644 "$dst" 2>/dev/null || true
}

chswap_set_item() {
  local file="$1"
  local name="$2"
  local type="$3"
  local value="$4"

  grep -q "<item name=\"$name\" type=\"$type\"" "$file" || return 1
  sed -i "/<item name=\"$name\" type=\"$type\"/s/value=\"[^\"]*\"/value=\"$value\"/g" "$file"
}

chswap_patch_appmng_scene() {
  local file="$1"
  local config="$2"
  local scene="$3"
  local value="$4"
  local block
  local row

  block="$(sed -n "/<config name=\"$config\">/,/<\\/config>/p" "$file")"
  if printf '%s\n' "$block" |
    grep "<scene name=\"$scene\">.*<mem total=\"16\"" >/dev/null; then
    sed -i "/<config name=\"$config\">/,/<\\/config>/ {
      /<scene name=\"$scene\">.*<mem total=\"16\"/s/value=\"[^\"]*\"/value=\"$value\"/
    }" "$file"
    sed -n "/<config name=\"$config\">/,/<\\/config>/p" "$file" |
      grep "<scene name=\"$scene\">.*<mem total=\"16\"" |
      grep -Fq "value=\"$value\""
    return
  fi

  row="$(printf '%s\n' "$block" | sed -n "/<scene name=\"$scene\">/,/<\\/scene>/ {
    /<mem total=\"16\"/p
  }")"
  [ -n "$row" ] || return 1

  sed -i "/<config name=\"$config\">/,/<\\/config>/ {
    /<scene name=\"$scene\">/,/<\\/scene>/ {
      /<mem total=\"16\"/s/value=\"[^\"]*\"/value=\"$value\"/
    }
  }" "$file"

  sed -n "/<config name=\"$config\">/,/<\\/config>/ {
    /<scene name=\"$scene\">/,/<\\/scene>/ {
      /<mem total=\"16\"/p
    }
  }" "$file" | grep -Fq "value=\"$value\""
}

chswap_patch_appmng_rule_type() {
  local file="$1"
  local scene="$2"
  local rules

  rules="$(sed -n "/name=\"$scene\"/,/<\\/scene>/p" "$file")"
  if printf '%s\n' "$rules" | grep 'policy name="clear"' |
    grep 'type="smart"' | grep -q 'level="2"'; then
    return 0
  fi
  printf '%s\n' "$rules" | grep 'policy name="clear"' |
    grep 'type="smart-force"' | grep -q 'level="2"' || return 1

  sed -i "/name=\"$scene\"/,/<\\/scene>/ {
    /policy name=\"clear\"/ {
      /type=\"smart-force\"/ {
        /level=\"2\"/s/type=\"smart-force\"/type=\"smart\"/
      }
    }
  }" "$file"

  sed -n "/name=\"$scene\"/,/<\\/scene>/p" "$file" |
    grep 'policy name="clear"' |
    grep 'type="smart"' |
    grep -q 'level="2"'
}

chswap_patch_appmng_decisionmaker() {
  local file="$1"
  local scene
  local patched_memory=0
  local patched_clear=0

  grep -q '<config name="memory_config">' "$file" || return 1
  grep -q '<config name="clear_level">' "$file" || return 1
  grep -q 'name="SCENE_APP_SWITCH"' "$file" || return 1
  grep -q 'name="SCENE_LAUNCHER"' "$file" || return 1

  for scene in $CHSWAP_APPMNG_SCENES; do
    if chswap_patch_appmng_scene "$file" memory_config "$scene" \
      "$CHSWAP_APPMNG_TRIGGER_16"; then
      patched_memory=$((patched_memory + 1))
    fi
    if chswap_patch_appmng_scene "$file" clear_level "$scene" \
      "$CHSWAP_APPMNG_CLEAR_16"; then
      patched_clear=$((patched_clear + 1))
    fi
  done

  [ "$patched_memory" -ge 2 ] || return 1
  [ "$patched_clear" -ge 2 ] || return 1
  chswap_patch_appmng_rule_type "$file" SCENE_APP_SWITCH || return 1
  chswap_patch_appmng_rule_type "$file" SCENE_LAUNCHER || return 1

  sed -i \
    "s#<version>[0-9][0-9]*</version>#<version>$CHSWAP_APPMNG_VERSION</version>#g" \
    "$file"
  grep -Fq "<version>$CHSWAP_APPMNG_VERSION</version>" "$file"
}

chswap_swap_policy_has_item() {
  local file="$1"
  local name="$2"
  sed -n '/<policy name="swap">/,/<\/policy>/p' "$file" |
    grep -q "<item name=\"$name\""
}

chswap_ensure_swap_policy_item() {
  local file="$1"
  local name="$2"
  local type="$3"
  local value="$4"

  if chswap_swap_policy_has_item "$file" "$name"; then
    sed -i "/<policy name=\"swap\">/,/<\/policy>/ {
      /<item name=\"$name\" type=\"$type\"/s/value=\"[^\"]*\"/value=\"$value\"/g
    }" "$file"
    return
  fi

  grep -q '<item name="nandSwitch"' "$file" || return 1
  sed -i "/<policy name=\"swap\">/,/<\/policy>/ {
    /<item name=\"nandSwitch\"/a\\
            <item name=\"$name\" type=\"$type\" value=\"$value\"/>
  }" "$file"
}

chswap_patch_osense_memory() {
  local file="$1"

  sed -i '/<policy name="dynaZram">/,/<\/policy>/d' "$file"
  sed -i '/<config name="res_release"/,/<\/config>/ {
    /<item name="thermal" type="int"/s/value="[^"]*"/value="42"/
  }' "$file"
  chswap_set_item "$file" inactiveZramRatioQb string '60|80' || true
  chswap_set_item "$file" inactiveZramRatio string '55|70|85' || true
  chswap_set_item "$file" appAnonRatio string '30|55|80|92' || true
  chswap_set_item "$file" appZramRatio string '45|65|82|93' || true

  # Insert in reverse order so repeated sed append operations produce the
  # intended order immediately after nandSwitch.
  chswap_ensure_swap_policy_item "$file" releaseAvailMemThreshold string \
    '2|1000-3|1000-4|1500-6|2000-8|3000-12|4000-16|5000-24|5000' || true
  chswap_ensure_swap_policy_item "$file" fileMemThreshold string \
    '2|500-3|500-4|500-6|1000-8|1500-12|2000-16|3000-24|4500' || true
  chswap_ensure_swap_policy_item "$file" fileRatioThreshold string \
    '2|40-3|40-4|40-6|40-8|40-12|40-16|40-24|40' || true
}

chswap_patch_memory_nirvana() {
  local file="$1"

  chswap_set_item "$file" EnableLightNoSwap bool true || true
  chswap_set_item "$file" HighTempThreshold int 48 || true
  chswap_set_item "$file" ThermalEnterThres int 48000 || true
  chswap_set_item "$file" ThermalExitThres int 46000 || true
  chswap_set_item "$file" FileMinPercent int 30 || true
  chswap_set_item "$file" ReleaseMinTargetKb long 60000 || true
  chswap_set_item "$file" ExpectedBgCountMap string \
    '6-99|8-99|12-99|16-99|24-99' || true
  chswap_set_item "$file" CamExpectedBgCount string \
    '6-99|8-99|12-99|16-99|24-99' || true
  chswap_set_item "$file" NoSwapLow string \
    '6-80000|8-100000|12-50000|16-200000|24-300000' || true
  chswap_set_item "$file" NoSwapHigh string \
    '6-80000|8-80000|12-60000|16-250000|24-350000' || true
  chswap_set_item "$file" NoSwapMax string \
    '6-80000|8-1500000|12-70000|16-300000|24-400000' || true
}

chswap_patch_osense_feature_common() {
  local file="$1"

  chswap_set_item "$file" POLICY_PRELOAD bool false || true
  chswap_set_item "$file" EnableIdleDynamicSwappiness bool true || true
  chswap_set_item "$file" EnableBalancedSwappiness bool true || true
  chswap_set_item "$file" BalancedSwappiness int 200 || true
  chswap_set_item "$file" EnableHighloadSwappiness bool true || true
  chswap_set_item "$file" HighloadSwappiness int 200 || true
  sed -i '/<config name="idleRecover"/,/<\/config>/ {
    /<item name="interval" type="long"/s/value="[^"]*"/value="30000"/
  }' "$file"
  sed -i 's/value="0 70000 0 150000 1 70000 1 150000"/value="0 90000 0 180000 1 90000 1 180000"/g' "$file"
}

chswap_patch_mm_swap() {
  local file="$1"

  sed -i \
    -e 's/zramRatio="20|40|70|90"/zramRatio="45|65|82|93"/g' \
    -e 's/zramRatio="40|40|55"/zramRatio="55|65|80"/g' \
    -e 's/zramRatioThd="80"/zramRatioThd="90"/g' \
    -e 's/bgDurThd="150000"/bgDurThd="90000"/g' \
    -e 's/minZramRatioInc="5"/minZramRatioInc="8"/g' \
    -e 's/zramLoad="95"/zramLoad="98"/g' \
    -e 's/maxSwapOutKb="20480"/maxSwapOutKb="40960"/g' \
    -e 's/totalSwapOutLimitKb="1024000"/totalSwapOutLimitKb="2048000"/g' \
    "$file"
}

chswap_patch_memory_decisionmaker() {
  local file="$1"

  sed -i '/<config name="zramload_config">/,/<\/config>/s/value="90"/value="95"/g' "$file"
  sed -i \
    -e '/<scene name="lowmem">/,/<\/scene>/s/value="1000|1200|1400|1700"/value="800|1000|1100|1400"/' \
    -e '/<scene name="lowmem">/,/<\/scene>/s/value="1200|1400|1600|1900"/value="1000|1100|1300|1500"/' \
    -e '/<scene name="lowmem">/,/<\/scene>/s/value="1300|1500|1700|2000"/value="1050|1200|1400|1600"/' \
    -e '/<scene name="lowmem">/,/<\/scene>/s/value="1400|1700|2700|3000"/value="1100|1400|2200|2400"/' \
    -e '/<scene name="lowmem_zram">/,/<\/scene>/s/value="1000|1200|2000"/value="800|1000|1600"/' \
    -e '/<scene name="lowmem_zram">/,/<\/scene>/s/value="1200|1400|2000"/value="1000|1100|1600"/' \
    -e '/<scene name="lowmem_zram">/,/<\/scene>/s/value="1300|1500|2000"/value="1050|1200|1600"/' \
    -e '/<scene name="lowmem_zram">/,/<\/scene>/s/value="1400|1700|2700"/value="1100|1400|2200"/' \
    -e '/<scene name="lowmem_qb">/,/<\/scene>/s/value="1000|1100|1300"/value="800|900|1050"/' \
    -e '/<scene name="lowmem_qb">/,/<\/scene>/s/value="1000|1200|1600"/value="800|1000|1300"/g' \
    -e '/<config name="compress_level">/,/<\/config>/s/<mem total="8" value="300"\/>/<mem total="8" value="700"\/>/' \
    -e '/<config name="compress_level">/,/<\/config>/s/<mem total="12" value="400"\/>/<mem total="12" value="1000"\/>/' \
    -e '/<config name="compress_level">/,/<\/config>/s/<mem total="16" value="550"\/>/<mem total="16" value="1200"\/>/' \
    -e '/<config name="compress_level">/,/<\/config>/s/<mem total="24" value="600"\/>/<mem total="24" value="1500"\/>/' \
    -e '/<config name="bglimit_config">/,/<\/config>/s/value="3|5|7"/value="3|12|15"/' \
    -e '/<config name="bglimit_config">/,/<\/config>/s/value="3|10|10"/value="3|15|20"/g' \
    -e '/<config name="freemem_config">/,/<\/config>/s/value="120|300|400"/value="100|200|300"/' \
    -e '/<config name="freemem_config">/,/<\/config>/s/value="120|300|600"/value="100|200|400"/' \
    -e '/<config name="freemem_config">/,/<\/config>/s/value="220|400|700"/value="150|300|500"/' \
    "$file"
}

chswap_prepare_oplus_policy() {
  local source_root="$1"
  local module_root="$2"
  local source_dir="$source_root/etc/extension"
  local legacy_root="$module_root/oplus-policy"
  local policy_root="$module_root/my_stock"
  local dest_dir="$policy_root/etc/extension"
  local copied=0
  local name src dst

  # Older development builds nested my_stock below oplus-policy, which caused
  # module managers to expose /oplus-policy/my_stock. Remove that stale layout.
  rm -rf "$legacy_root" "$policy_root" 2>/dev/null
  [ -d "$source_dir" ] || return 0
  mkdir -p "$dest_dir" || return 1

  for name in $CHSWAP_OPLUS_POLICY_FILES; do
    src="$source_dir/$name"
    dst="$dest_dir/$name"
    [ -f "$src" ] || continue
    if ! chswap_copy_policy_file "$src" "$dst"; then
      chswap_oplus_print "! Failed to copy Oplus policy: $name"
      continue
    fi

    case "$name" in
      sys_osense_appmng_decisionmaker_config.xml)
        if ! chswap_patch_appmng_decisionmaker "$dst"; then
          chswap_oplus_print "! Unsupported Oplus app-management XML; skipping"
          rm -f "$dst" 2>/dev/null
          continue
        fi
        ;;
      sys_osense_memory_config.xml)
        chswap_patch_osense_memory "$dst"
        ;;
      sys_memory_nirvana_config.xml)
        chswap_patch_memory_nirvana "$dst"
        ;;
      sys_osense_feature_common_config.xml)
        chswap_patch_osense_feature_common "$dst"
        ;;
      sys_mm_swap_config.xml)
        chswap_patch_mm_swap "$dst"
        ;;
      sys_osense_memory_decisionmaker_config.xml)
        chswap_patch_memory_decisionmaker "$dst"
        ;;
    esac

    copied=$((copied + 1))
  done

  if [ "$copied" -eq 0 ]; then
    rm -rf "$policy_root" 2>/dev/null
    return 0
  fi
  chswap_oplus_print "- Prepared Oplus background policy ($copied XML files)"
}

chswap_mount_oplus_policy() {
  local module_root="$1"
  local log_file="$2"
  local target_root="${3:-/my_stock}"
  local source_dir="$module_root/my_stock/etc/extension"
  local target_dir="$target_root/etc/extension"
  local name src dst

  [ -d "$source_dir" ] || return 0
  [ -d "$target_dir" ] || return 0

  for name in $CHSWAP_OPLUS_POLICY_FILES; do
    src="$source_dir/$name"
    dst="$target_dir/$name"
    [ -f "$src" ] || continue
    [ -f "$dst" ] || continue
    if mount -o bind "$src" "$dst" 2>/dev/null; then
      echo "oplus-policy: mounted $name" >>"$log_file"
    else
      echo "oplus-policy: failed to mount $name" >>"$log_file"
    fi
  done
}

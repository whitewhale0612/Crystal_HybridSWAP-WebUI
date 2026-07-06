package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func (a *App) capabilities() (Capabilities, []Warning) {
	report := a.crystalReport()
	missing := a.managementSurfaceMissing("zram0")
	caps := Capabilities{
		BackendVersion: a.moduleVersion(),
		APIVersion:     apiVersion,
		FrontendGate:   gateForMissing(missing),
		Kernel: KernelCaps{
			CrystalPresent:       a.crystalPresent(report),
			ZramControlPresent:   a.exists(a.sys("class/zram-control")),
			RequiredNodesMissing: missing,
			CrystalReport:        report,
		},
		Zram: a.zramCaps("zram0"),
		ProfileStorage: ProfileStorage{
			Backend: a.profileBackend(),
			Path:    a.profilePathForBackend(),
		},
	}
	return caps, unsupportedWarnings(missing)
}

func (a *App) status() (Status, []Warning) {
	profile, profileWarnings := a.loadProfile()
	missing := a.managementSurfaceMissing(profile.Zram.Device)
	zram := a.zramStatus(profile.Zram.Device)
	swap := a.swapStatus(profile.Zram.Device, profile.Swap.Enabled)
	zram.ActiveSwap = swap.Active
	crystal := a.crystalStatus(profile.Zram.Device)
	zms := a.zmsStatus(profile.Zram.Device)
	policy := a.policyStatus()
	boot := a.loadBootStatus()
	storage := StorageStatus{
		ProfileBackend: a.profileBackend(),
		ProfilePath:    a.profilePathForBackend(),
		DataDir:        a.DataDir,
	}

	reasons := []string{}
	if len(missing) > 0 {
		reasons = append(reasons, unsupportedMessage)
	}
	if !zram.Found {
		reasons = append(reasons, "selected zram device was not found")
	}
	if profile.Mode == "full-control" && profile.Swap.Enabled {
		if !swap.Active {
			reasons = append(reasons, "zram swap is not active")
		} else if swap.Priority != profile.Swap.Priority {
			reasons = append(reasons, "zram swap priority does not match the profile")
		}
	}
	if profile.Mode == "full-control" && profile.Backing.Mode == "block" && zram.Found {
		if zram.BackingBoundVia != "hybridswap_loop_device" ||
			zram.BackingDev != backingBlockDevice {
			reasons = append(reasons, "block backing is not bound to "+backingBlockDevice)
		} else if !zms.Present || zms.Enabled != 1 {
			reasons = append(reasons, "ZMS is not enabled for the configured block backing")
		}
	} else if profile.Mode == "full-control" && profile.Backing.Mode == "loop" && zram.Found {
		if zram.BackingBoundVia != "hybridswap_loop_device" || zram.BackingDev == "" {
			reasons = append(reasons, "loop backing is not bound")
		} else if !zms.Present || zms.Enabled != 1 {
			reasons = append(reasons, "ZMS is not enabled for the configured loop backing")
		}
	}
	status := Status{
		Mode:            profile.Mode,
		FrontendGate:    gateForMissing(missing),
		Degraded:        len(reasons) > 0,
		DegradedReasons: reasons,
		Zram:            zram,
		Swap:            swap,
		Crystal:         crystal,
		ZMS:             zms,
		Policy:          policy,
		Boot:            boot,
		Storage:         storage,
	}
	warnings := append([]Warning{}, unsupportedWarnings(missing)...)
	warnings = append(warnings, profileWarnings...)
	return status, warnings
}

// appsResult reads the root memcg's memory.total_info_per_app table (one row
// per tracked app/cgroup). Read-only: when the node is absent it returns
// Supported=false with an empty list rather than an error, so the UI can show a
// friendly "unsupported" state instead of failing.
func (a *App) appsResult() (AppsResult, []Warning) {
	empty := AppsResult{Supported: false, Count: 0, Apps: []AppMemInfo{}}
	memcg := a.memcgRoot()
	if memcg == "" {
		return empty, nil
	}
	path := filepath.Join(memcg, "memory.total_info_per_app")
	if !a.exists(path) {
		return empty, nil
	}
	apps := parseTotalInfoPerApp(readTrim(path))
	pending := a.fillAppLabels(apps)
	return AppsResult{Supported: true, Count: len(apps), Apps: apps, LabelsPending: pending}, nil
}

// parseTotalInfoPerApp parses the whitespace-delimited total_info_per_app table
// (a header row followed by one row per app, 16 columns). Tokens are mapped by
// position after splitting on runs of whitespace — values can exceed the fixed
// column width, so byte offsets are unreliable. The header row (first token is
// the literal "anon") and malformed short rows are skipped. The KB size columns
// are normalized to bytes; the leading anon placeholder column is discarded.
func parseTotalInfoPerApp(raw string) []AppMemInfo {
	out := []AppMemInfo{}
	const kb = 1024
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 16 || f[0] == "anon" {
			continue
		}
		out = append(out, AppMemInfo{
			ZramComprBytes:   parseIntDefault(f[1], 0) * kb,
			ZramOrigBytes:    parseIntDefault(f[2], 0) * kb,
			EswapComprBytes:  parseIntDefault(f[3], 0) * kb,
			EswapOrigBytes:   parseIntDefault(f[4], 0) * kb,
			Name:             f[5],
			Score:            parseIntDefault(f[6], 0),
			UID:              parseIntDefault(f[7], 0),
			Policy:           parseIntDefault(f[8], 0),
			SwapoutTrigMB:    parseIntDefault(f[9], 0),
			SwapinMB:         parseIntDefault(f[10], 0),
			SwapinRequest:    parseIntDefault(f[11], 0),
			SwapinReqPages:   parseIntDefault(f[12], 0),
			SwapinEffPages:   parseIntDefault(f[13], 0),
			SwapinMovedPages: parseIntDefault(f[14], 0),
			SwapinRet:        parseIntDefault(f[15], 0),
		})
	}
	return out
}

func (a *App) zramStatus(device string) ZramStatus {
	if device == "" {
		device = a.firstZram()
	}
	if device == "" {
		device = "zram0"
	}
	zdir := a.sys("block/" + device)
	st := ZramStatus{
		Device:          device,
		Found:           a.exists(zdir),
		BackingBoundVia: "none",
	}
	if !st.Found {
		return st
	}
	st.Initstate = parseIntDefault(readTrim(filepath.Join(zdir, "initstate")), 0)
	st.DisksizeBytes = parseIntDefault(readTrim(filepath.Join(zdir, "disksize")), 0)
	st.MemLimitBytes = parseIntDefault(readTrim(filepath.Join(zdir, "mem_limit")), 0)
	st.CompAlgorithm = selectedAlgorithm(readTrim(filepath.Join(zdir, "comp_algorithm")))
	st.RecompAlgorithm = selectedAlgorithm(readTrim(filepath.Join(zdir, "recomp_algorithm")))
	if backing := readTrim(filepath.Join(zdir, "hybridswap_loop_device")); backing != "" &&
		backing != "none" {
		st.BackingDev = backing
		st.BackingBoundVia = "hybridswap_loop_device"
	} else if backing := readTrim(filepath.Join(zdir, "backing_dev")); backing != "" &&
		backing != "none" {
		st.BackingDev = backing
		st.BackingBoundVia = "backing_dev"
	}
	st.MMStat = parseMMStat(readTrim(filepath.Join(zdir, "mm_stat")))
	return st
}

func (a *App) swapStatus(device string, enabled bool) SwapStatus {
	st := SwapStatus{Enabled: enabled, Active: false, Priority: -1}
	devPath := a.devBlockZram(device)
	data := readTrim(a.proc("swaps"))
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Filename") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if fields[0] == devPath || strings.HasSuffix(fields[0], "/"+device) {
			st.Active = true
			st.Device = fields[0]
			st.SizeBytes = parseIntDefault(fields[2], 0) * 1024
			st.UsedBytes = parseIntDefault(fields[3], 0) * 1024
			st.Priority = parseIntDefault(fields[4], -1)
			break
		}
	}
	return st
}

func (a *App) crystalStatus(device string) CrystalStatus {
	zdir := a.sys("block/" + device)
	enableRaw := readTrim(filepath.Join(zdir, "hybridswap_enable"))
	coreRaw := readTrim(filepath.Join(zdir, "hybridswap_core_enable"))
	// Live writeback-quota counters live in hybridswap_crystal_stat as page
	// counts (space-separated "key value"), NOT in hybridswap_report.
	stat := parseKeyValue(readTrim(filepath.Join(zdir, "hybridswap_crystal_stat")), " ")
	devLifeRaw := readTrim(filepath.Join(zdir, "hybridswap_dev_life"))
	const pageBytes = 4096
	out := CrystalStatus{
		Enable:         parseCrystalEnabled(enableRaw, "hybridswap"),
		CoreEnable:     parseCrystalEnabled(coreRaw, "reclaim_in"),
		SwapdPause:     readIntAny(0, filepath.Join(zdir, "hybridswap_swapd_pause")),
		Loglevel:       parseLastIntDefault(readTrim(filepath.Join(zdir, "hybridswap_loglevel")), 0),
		DevLife:        parseDevLifeLevel(stat, devLifeRaw),
		QuotaDay:       readIntAny(0, filepath.Join(zdir, "hybridswap_quota_day")),
		QuotaUsed:      parseIntDefault(stat["writeback_quota_used_pages"], 0) * pageBytes,
		QuotaRemaining: parseIntDefault(stat["writeback_quota_remaining_pages"], 0) * pageBytes,
		ZramIncrease:   readIntAny(0, filepath.Join(zdir, "hybridswap_zram_increase")),
	}
	// Prefer the stat node's authoritative limit when present.
	if lim := parseIntDefault(stat["writeback_quota_limit_bytes"], 0); lim > 0 {
		out.QuotaDay = lim
	}
	return out
}

func parseDevLifeLevel(stat map[string]string, raw string) int64 {
	if stat != nil {
		if levelRaw := strings.TrimSpace(stat["dev_life_level"]); levelRaw != "" {
			if level := parseIntDefault(levelRaw, -1); level >= 0 {
				return level
			}
		}
	}
	return parseCrystalEnabled(raw, "")
}

func parseCrystalEnabled(raw string, marker string) int64 {
	raw = strings.ToLower(raw)
	if marker != "" {
		if strings.Contains(raw, marker+" enable") {
			return 1
		}
		if strings.Contains(raw, marker+" disable") {
			return 0
		}
	}
	if strings.Contains(raw, "enable") && !strings.Contains(raw, "disable") {
		return 1
	}
	return parseIntDefault(raw, 0)
}

// zramCaps reports the kernel-supported compression algorithms (with the
// active one) and total RAM, so the UI can offer real choices instead of a
// hardcoded list and bound the disksize slider by physical memory.
func (a *App) zramCaps(device string) ZramCaps {
	algos, active := parseCompAlgorithms(readTrim(a.sys(filepath.Join("block", device, "comp_algorithm"))))
	return ZramCaps{
		CompAlgorithms:      algos,
		CompAlgorithmActive: active,
		MemTotalBytes:       a.memTotalBytes(),
	}
}

// parseCompAlgorithms turns "lzo lz4 [zstd]" into (["lzo","lz4","zstd"], "zstd").
func parseCompAlgorithms(raw string) ([]string, string) {
	fields := strings.Fields(raw)
	algos := make([]string, 0, len(fields))
	active := ""
	for _, f := range fields {
		if strings.HasPrefix(f, "[") && strings.HasSuffix(f, "]") {
			f = strings.TrimSuffix(strings.TrimPrefix(f, "["), "]")
			active = f
		}
		if f != "" {
			algos = append(algos, f)
		}
	}
	return algos, active
}

func (a *App) memTotalBytes() int64 {
	raw := readTrim(a.proc("meminfo"))
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return parseIntDefault(fields[1], 0) * 1024
			}
		}
	}
	return 0
}

func (a *App) zmsStatus(device string) ZmsStatus {
	stats := parseKeyValue(readTrim(a.sys(filepath.Join("block", device, "zms_stat"))), ":")
	enabled := parseIntDefault(stats["enabled"], 0)
	stored := parseIntDefault(stats["stored_bytes"], 0)
	packed := parseIntDefault(stats["packed_bytes"], 0)
	usedBlocks := parseIntDefault(stats["used_blocks"], 0)
	objects := parseIntDefault(stats["objects"], 0)
	out := ZmsStatus{
		Enabled:     enabled,
		Present:     len(stats) > 0,
		Ret:         parseIntDefault(stats["ret"], 0),
		NrBlocks:    parseIntDefault(stats["nr_blocks"], 0),
		UsedBlocks:  usedBlocks,
		Objects:     objects,
		StoredBytes: stored,
		PackedBytes: packed,
	}
	if stored > 0 {
		out.SpaceDeltaBytes = stored - packed
		out.SpaceSavedPct = round2(float64(out.SpaceDeltaBytes) * 100 / float64(stored))
		out.SlotOverheadBytes = packed - stored
		out.SlotOverheadPct = round2(float64(out.SlotOverheadBytes) * 100 / float64(stored))
	}
	if usedBlocks > 0 {
		out.BlockBytes = usedBlocks * 4096
		out.BlockWasteBytes = out.BlockBytes - packed
		out.BlockUtilizationPct = round2(float64(packed) * 100 / float64(out.BlockBytes))
		out.PackingEfficiencyPct = out.BlockUtilizationPct
	}
	if objects > 0 {
		out.OriginalBytes = objects * 4096
		out.BackingSavingsBytes = out.OriginalBytes - out.BlockBytes
		out.BackingSavingsPct = round2(float64(out.BackingSavingsBytes) * 100 /
			float64(out.OriginalBytes))
	}
	return out
}

// policyStatus reports the live memcg policy values (not profile defaults) so
// the UI always shows the real system state.
func (a *App) policyStatus() PolicyStatus {
	memcg := a.memcgRoot()
	out := PolicyStatus{}
	if memcg == "" {
		return out
	}
	out.ZramWmRatio = parseIntDefault(readTrim(filepath.Join(memcg,
		"memory.zram_wm_ratio")), 0)
	out.AvailBuffers = parseAvailBuffers(readTrim(filepath.Join(memcg,
		"memory.avail_buffers")), AvailBuffers{})
	out.ErmAvailBufferEnable = parseIntDefault(readTrim(filepath.Join(memcg,
		"memory.erm_avail_buffer_enable")), 0)
	out.ErmAvailBuffer = parseErmAvailBuffer(readTrim(filepath.Join(memcg,
		"memory.erm_avail_buffer")), ErmAvailBuffer{})
	return out
}

func parseAvailBuffers(raw string, fallback AvailBuffers) AvailBuffers {
	kv := parseKeyValue(raw, ":")
	if len(kv) > 0 {
		return AvailBuffers{
			AvailMB:             parseIntDefault(kv["avail_buffers"], fallback.AvailMB),
			MinMB:               parseIntDefault(kv["min_avail_buffers"], fallback.MinMB),
			HighMB:              parseIntDefault(kv["high_avail_buffers"], fallback.HighMB),
			FreeSwapThresholdMB: parseIntDefault(kv["free_swap_threshold"], fallback.FreeSwapThresholdMB),
		}
	}
	fields := parseIntFields(raw)
	if len(fields) >= 4 {
		return AvailBuffers{
			AvailMB:             fields[0],
			MinMB:               fields[1],
			HighMB:              fields[2],
			FreeSwapThresholdMB: fields[3],
		}
	}
	return fallback
}

func parseErmAvailBuffer(raw string, fallback ErmAvailBuffer) ErmAvailBuffer {
	matches := regexp.MustCompile(`[0-9]+`).FindAllString(raw, -1)
	if len(matches) >= 2 {
		return ErmAvailBuffer{
			MinMB:  parseIntDefault(matches[0], fallback.MinMB),
			HighMB: parseIntDefault(matches[1], fallback.HighMB),
		}
	}
	fields := parseIntFields(raw)
	if len(fields) >= 2 {
		return ErmAvailBuffer{MinMB: fields[0], HighMB: fields[1]}
	}
	return fallback
}

func (a *App) crystalPresent(report map[string]string) bool {
	if len(report) > 0 {
		return true
	}
	for _, dev := range a.zramDevices() {
		if a.exists(a.sys(filepath.Join("block", dev, "hybridswap_enable"))) {
			return true
		}
	}
	return false
}

func (a *App) crystalReport() map[string]string {
	for _, dev := range a.zramDevices() {
		path := a.sys(filepath.Join("block", dev, "hybridswap_report"))
		if data := readTrim(path); data != "" {
			return parseKeyValue(data, "=")
		}
	}
	return map[string]string{}
}

func (a *App) zramDevices() []string {
	entries, err := os.ReadDir(a.sys("block"))
	if err != nil {
		return nil
	}
	devs := []string{}
	re := regexp.MustCompile(`^zram[0-9]+$`)
	for _, entry := range entries {
		if re.MatchString(entry.Name()) && a.exists(a.sys("block/"+entry.Name())) {
			devs = append(devs, entry.Name())
		}
	}
	sort.Strings(devs)
	return devs
}

func (a *App) firstZram() string {
	devs := a.zramDevices()
	if len(devs) == 0 {
		return ""
	}
	return devs[0]
}

func (a *App) memcgRoot() string {
	if a.MemcgRoot != "" && a.exists(a.MemcgRoot) {
		return a.MemcgRoot
	}
	for _, candidate := range []string{
		filepath.Join(a.DevRoot, "memcg"),
		a.sys("fs/cgroup/memory"),
		a.sys("fs/cgroup"),
	} {
		if a.exists(filepath.Join(candidate, "memory.zram_wm_ratio")) {
			return candidate
		}
	}
	return ""
}

func (a *App) devBlockZram(device string) string {
	if a.DevRoot == "/dev" {
		return filepath.Join("/dev/block", device)
	}
	return filepath.Join(a.DevRoot, "block", device)
}

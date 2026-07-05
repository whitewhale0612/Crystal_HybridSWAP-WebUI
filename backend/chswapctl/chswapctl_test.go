package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseSize(t *testing.T) {
	tests := map[string]int64{
		"0":    0,
		"512M": 512 * 1024 * 1024,
		"12G":  12 * 1024 * 1024 * 1024,
	}
	for input, want := range tests {
		if got := parseSize(input); got != want {
			t.Fatalf("parseSize(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestParseSizeRejectsOverflow(t *testing.T) {
	if got := parseSize("9223372036854775807G"); got != -1 {
		t.Fatalf("overflow parseSize = %d, want -1", got)
	}
}

func TestValidateDefaultProfile(t *testing.T) {
	if err := validateProfile(defaultProfile()); err != nil {
		t.Fatalf("default profile did not validate: %v", err)
	}
}

func TestStrictJSONRejectsTrailingData(t *testing.T) {
	var req ApplyRequest
	if err := decodeJSON([]byte(`{"mode":"dry-run"}{"extra":true}`), &req); err == nil {
		t.Fatal("decodeJSON accepted trailing JSON")
	}

	body := []byte(`{
	  "schema":"chswap.profile.v1",
	  "mode":"full-control",
	  "boot":{"post_fs_timeout_ms":8000},
	  "zram":{"device":"zram0","disksize":null,"comp_algorithm":null},
	  "backing":{"mode":"block"},
	  "swap":{"enabled":true,"priority":32767},
	  "crystal":{"enable":null,"core_enable":null,"swapd_pause":null,"loglevel":null,"dev_life":null,"quota_day":null,"zram_increase":null},
	  "policy":{"zram_wm_ratio":null,"avail_buffers":null,"erm_avail_buffer_enable":null}
	}{"extra":true}`)
	if _, err := decodeProfile(body); err == nil {
		t.Fatal("decodeProfile accepted trailing JSON")
	}
}

func TestDecodeProfileRequiresNullableFieldsToBePresent(t *testing.T) {
	valid := []byte(`{"schema":"chswap.profile.v1","mode":"full-control","boot":{"post_fs_timeout_ms":8000},"zram":{"device":"zram0","disksize":null,"comp_algorithm":null},"backing":{"mode":"block"},"swap":{"enabled":true,"priority":32767},"crystal":{"enable":null,"core_enable":null,"swapd_pause":null,"loglevel":null,"dev_life":null,"quota_day":null,"zram_increase":null},"policy":{"zram_wm_ratio":null,"avail_buffers":null,"erm_avail_buffer_enable":null}}`)
	if _, err := decodeProfile(valid); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}

	cases := map[string]string{
		"missing crystal.enable": strings.Replace(string(valid), `"enable":null,`, "", 1),
		"missing policy":         strings.Replace(string(valid), `,"policy":{"zram_wm_ratio":null,"avail_buffers":null,"erm_avail_buffer_enable":null}`, "", 1),
		"missing boot field":     strings.Replace(string(valid), `"post_fs_timeout_ms":8000`, "", 1),
		"missing zram disksize":  strings.Replace(string(valid), `"disksize":null,`, "", 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeProfile([]byte(body)); err == nil {
				t.Fatal("decodeProfile accepted missing required profile field")
			}
		})
	}
}

func TestDecodeProfileRejectsNonNullableNullFields(t *testing.T) {
	valid := `{"schema":"chswap.profile.v1","mode":"full-control","boot":{"post_fs_timeout_ms":8000},"zram":{"device":"zram0","disksize":null,"comp_algorithm":null},"backing":{"mode":"block"},"swap":{"enabled":true,"priority":32767},"crystal":{"enable":null,"core_enable":null,"swapd_pause":null,"loglevel":null,"dev_life":null,"quota_day":null,"zram_increase":null},"policy":{"zram_wm_ratio":null,"avail_buffers":null,"erm_avail_buffer_enable":null}}`
	cases := map[string]string{
		"profile.schema":               strings.Replace(valid, `"schema":"chswap.profile.v1"`, `"schema":null`, 1),
		"profile.mode":                 strings.Replace(valid, `"mode":"full-control"`, `"mode":null`, 1),
		"profile.boot.timeout":         strings.Replace(valid, `"post_fs_timeout_ms":8000`, `"post_fs_timeout_ms":null`, 1),
		"profile.zram.device":          strings.Replace(valid, `"device":"zram0"`, `"device":null`, 1),
		"profile.backing.mode":         strings.Replace(valid, `"backing":{"mode":"block"}`, `"backing":{"mode":null}`, 1),
		"profile.swap.enabled":         strings.Replace(valid, `"enabled":true`, `"enabled":null`, 1),
		"profile.swap.priority":        strings.Replace(valid, `"priority":32767`, `"priority":null`, 1),
		"profile.avail_buffers.member": strings.Replace(valid, `"avail_buffers":null`, `"avail_buffers":{"avail_mb":1,"min_mb":2,"high_mb":null,"free_swap_threshold_mb":4}`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeProfile([]byte(body)); err == nil {
				t.Fatal("decodeProfile accepted a null non-nullable field")
			}
		})
	}
}

func TestApplyRequestRejectsIncompleteEmbeddedProfile(t *testing.T) {
	body := []byte(`{
	  "mode":"dry-run",
	  "profile":{
	    "schema":"chswap.profile.v1",
	    "mode":"full-control",
	    "boot":{"post_fs_timeout_ms":8000},
	    "zram":{"device":"zram0","disksize":null,"comp_algorithm":null},
	    "backing":{"mode":"block"},
	    "swap":{"enabled":true,"priority":32767},
	    "crystal":{"core_enable":null,"swapd_pause":null,"loglevel":null,"dev_life":null,"quota_day":null,"zram_increase":null},
	    "policy":{"zram_wm_ratio":null,"avail_buffers":null,"erm_avail_buffer_enable":null}
	  }
	}`)
	if err := validateApplyRequestShape(body); err == nil {
		t.Fatal("embedded profile missing crystal.enable was accepted")
	}
}

func TestApplyRequestRejectsNullEmbeddedProfile(t *testing.T) {
	app := testApp(t)
	var env Envelope
	ok := captureJSON(t, func() bool {
		return app.applyAPI([]byte(`{"mode":"dry-run","profile":null}`))
	}, &env)
	if ok || env.Error == nil || env.Error.Code != "E_INVALID_PROFILE" {
		t.Fatalf("profile:null was not rejected: ok=%v env=%+v", ok, env)
	}
}

func TestAppValidateRejectsUnsafeProfileValues(t *testing.T) {
	app := testApp(t)
	if err := mkdirAll(app.ProcRoot); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(app.ProcRoot, "meminfo"), "MemTotal: 25165824 kB\n")

	p := defaultProfile()
	p.Swap.Enabled = false
	if err := app.validateProfile(p); err == nil {
		t.Fatal("swap.enabled=false was accepted")
	}

	p = defaultProfile()
	p.Zram.Disksize = strp("999G")
	if err := app.validateProfile(p); err == nil {
		t.Fatal("oversized zram.disksize was accepted")
	}

	p = defaultProfile()
	p.Backing.Mode = "loop"
	tooLarge := maxLoopBackingBytes + 1
	p.Backing.LoopSizeBytes = &tooLarge
	if err := app.validateProfile(p); err == nil {
		t.Fatal("oversized loop backing was accepted")
	}

	p = defaultProfile()
	p.Crystal.DevLife = i64p(maxDevLifeLevel + 1)
	if err := app.validateProfile(p); err == nil {
		t.Fatal("oversized dev_life was accepted")
	}

	p = defaultProfile()
	p.Crystal.ZramIncrease = i64p(maxZramIncreaseMiB + 1)
	if err := app.validateProfile(p); err == nil {
		t.Fatal("oversized zram_increase was accepted")
	}

	p = defaultProfile()
	p.Policy.AvailBuffers = &AvailBuffers{
		AvailMB: maxAvailBufferMB + 1, MinMB: 0, HighMB: 0, FreeSwapThresholdMB: 0,
	}
	if err := app.validateProfile(p); err == nil {
		t.Fatal("oversized avail_buffers value was accepted")
	}
}

func TestInvalidStoredProfileFallsBackToDiagnostic(t *testing.T) {
	app := testApp(t)
	if err := mkdirAll(app.DataDir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(app.DataDir, "profile.json"),
		`{"schema":"chswap.profile.v1"}`); err != nil {
		t.Fatal(err)
	}

	profile, warnings := app.loadProfile()
	if profile.Mode != "diagnostic" {
		t.Fatalf("invalid stored profile fell back to %q, want diagnostic", profile.Mode)
	}
	if len(warnings) != 1 || warnings[0].Code != "W_PROFILE_STORE_INVALID" {
		t.Fatalf("missing invalid profile warning: %+v", warnings)
	}
}

func TestStoredProfileMigratesRemovedBootFields(t *testing.T) {
	app := testApp(t)
	body := []byte(`{
	  "schema":"chswap.profile.v1",
	  "mode":"full-control",
	  "boot":{"early_claim":false,"post_fs_timeout_ms":7000,"watchdog_seconds":180},
	  "zram":{"device":"zram0","disksize":null,"comp_algorithm":null},
	  "backing":{"mode":"block"},
	  "swap":{"enabled":true,"priority":32767},
	  "crystal":{"enable":null,"core_enable":null,"swapd_pause":null,"loglevel":null,"dev_life":null,"quota_day":null,"zram_increase":null},
	  "policy":{"zram_wm_ratio":null,"avail_buffers":null,"erm_avail_buffer_enable":null}
	}`)
	if _, err := decodeProfile(body); err == nil {
		t.Fatal("public profile decoder accepted removed boot fields")
	}
	profile, err := app.decodeStoredProfile(body)
	if err != nil {
		t.Fatalf("stored profile migration failed: %v", err)
	}
	if profile.Boot.PostFSTimeoutMS != 7000 {
		t.Fatalf("post_fs_timeout_ms = %d, want 7000", profile.Boot.PostFSTimeoutMS)
	}
}

func TestRuntimeApplyDegradesMissingExplicitNode(t *testing.T) {
	app := testApp(t)
	if err := mkdirAll(filepath.Join(app.SysRoot, "block", "zram0")); err != nil {
		t.Fatal(err)
	}
	p := defaultProfile()
	p.Crystal.Enable = i64p(1)

	result := app.applyRuntime(ApplyResult{Steps: []ApplyStep{}}, p, false)
	if !result.Degraded {
		t.Fatalf("missing explicit node did not degrade: %+v", result)
	}
	if !hasFailedStep(result, "crystal.enable") {
		t.Fatalf("missing failed crystal.enable step: %+v", result.Steps)
	}
}

func TestDefaultBlockBackingUsesRawHybridswapPartition(t *testing.T) {
	if backingBlockDevice != "/dev/block/by-name/hybridswap" {
		t.Fatalf("backingBlockDevice = %q", backingBlockDevice)
	}
}

func TestKSUDModuleConfigArgsIncludeInternalModule(t *testing.T) {
	got := ksudModuleConfigArgs("set", "profile.json", "--stdin")
	want := []string{
		"module", "config", "--internal", moduleID,
		"set", "profile.json", "--stdin",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ksud args = %#v, want %#v", got, want)
	}
}

func TestManagementSupportUsesCurrentKernelNodes(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{
		"initstate", "disksize", "reset", "comp_algorithm",
		"hybridswap_enable", "hybridswap_core_enable",
		"hybridswap_swapd_pause", "hybridswap_loglevel",
		"hybridswap_report", "hybridswap_crystal_stat",
		"hybridswap_loop_device",
		"hybridswap_dev_life", "hybridswap_quota_day",
		"hybridswap_zram_increase",
	} {
		writeFile(filepath.Join(zdir, node), "0\n")
	}
	if err := mkdirAll(app.sys("class/zram-control")); err != nil {
		t.Fatal(err)
	}
	memcg := filepath.Join(app.DevRoot, "memcg")
	if err := mkdirAll(memcg); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{
		"memory.zram_wm_ratio",
		"memory.avail_buffers",
		"memory.erm_avail_buffer_enable",
		"memory.erm_avail_buffer",
	} {
		writeFile(filepath.Join(memcg, node), "0\n")
	}

	if missing := app.managementSurfaceMissing("zram0"); len(missing) != 0 {
		t.Fatalf("management surface missing nodes: %+v", missing)
	}
	caps, warnings := app.capabilities()
	if !caps.FrontendGate.Allowed || len(caps.Kernel.RequiredNodesMissing) != 0 {
		t.Fatalf("gate was not opened: %+v", caps.FrontendGate)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected unsupported warnings: %+v", warnings)
	}
}

func TestModuleVersionReadsModuleProp(t *testing.T) {
	app := testApp(t)
	if got := app.moduleVersion(); got != "Unknown" {
		t.Fatalf("missing module.prop version = %q, want Unknown", got)
	}
	if err := mkdirAll(app.ModDir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(app.ModDir, "module.prop"),
		"id="+moduleID+"\nversion=9.9.9\n"); err != nil {
		t.Fatal(err)
	}
	if got := app.moduleVersion(); got != "9.9.9" {
		t.Fatalf("module version = %q, want 9.9.9", got)
	}
	if err := writeFile(filepath.Join(app.ModDir, "module.prop"), "id="+moduleID+"\n"); err != nil {
		t.Fatal(err)
	}
	if got := app.moduleVersion(); got != "Unknown" {
		t.Fatalf("module.prop without version = %q, want Unknown", got)
	}
}

func TestZmsMath(t *testing.T) {
	app := &App{}
	path := t.TempDir()
	app.SysRoot = path
	if err := mkdirAll(path + "/block/zram0"); err != nil {
		t.Fatal(err)
	}
	data := "enabled: 1\nused_blocks: 42359\nobjects: 126976\n" +
		"stored_bytes: 145540224\n" +
		"packed_bytes: 146486320\n"
	if err := writeFile(path+"/block/zram0/zms_stat", data); err != nil {
		t.Fatal(err)
	}
	got := app.zmsStatus("zram0")
	if got.SpaceDeltaBytes != -946096 {
		t.Fatalf("delta = %d", got.SpaceDeltaBytes)
	}
	if got.SpaceSavedPct != -0.65 {
		t.Fatalf("space_saved_pct = %v", got.SpaceSavedPct)
	}
	if got.PackingEfficiencyPct != 84.43 {
		t.Fatalf("packing_efficiency_pct = %v", got.PackingEfficiencyPct)
	}
	if got.SlotOverheadBytes != 946096 || got.SlotOverheadPct != 0.65 {
		t.Fatalf("slot overhead = %d / %v", got.SlotOverheadBytes, got.SlotOverheadPct)
	}
	if got.BlockBytes != 173502464 || got.BlockWasteBytes != 27016144 {
		t.Fatalf("block accounting = %d / %d", got.BlockBytes, got.BlockWasteBytes)
	}
	if got.BlockUtilizationPct != 84.43 {
		t.Fatalf("block_utilization_pct = %v", got.BlockUtilizationPct)
	}
	if got.OriginalBytes != 520093696 {
		t.Fatalf("original_bytes = %d", got.OriginalBytes)
	}
	if got.BackingSavingsBytes != 346591232 || got.BackingSavingsPct != 66.64 {
		t.Fatalf("backing savings = %d / %v", got.BackingSavingsBytes, got.BackingSavingsPct)
	}
}

func TestCrystalTextParsers(t *testing.T) {
	if got := parseCrystalEnabled("hybridswap enable reclaim_in enable", "hybridswap"); got != 1 {
		t.Fatalf("hybridswap enable parsed as %d", got)
	}
	if got := parseCrystalEnabled("hybridswap enable reclaim_in enable", "reclaim_in"); got != 1 {
		t.Fatalf("reclaim_in enable parsed as %d", got)
	}
	if got := parseCrystalEnabled("disable", ""); got != 0 {
		t.Fatalf("disable parsed as %d", got)
	}
	if got := parseLastIntDefault("Hybridswap log level: 1", 0); got != 1 {
		t.Fatalf("loglevel parsed as %d", got)
	}
}

func TestSwapStatusDetectsActiveZram(t *testing.T) {
	app := testApp(t)
	if err := mkdirAll(app.ProcRoot); err != nil {
		t.Fatal(err)
	}
	swaps := "Filename\tType\tSize\tUsed\tPriority\n" +
		"/dev/block/zram0 partition 1024 8 32767\n"
	if err := writeFile(filepath.Join(app.ProcRoot, "swaps"), swaps); err != nil {
		t.Fatal(err)
	}

	got := app.swapStatus("zram0", true)
	if !got.Active {
		t.Fatalf("swap active = false, want true")
	}
	if got.Priority != 32767 || got.SizeBytes != 1024*1024 || got.UsedBytes != 8*1024 {
		t.Fatalf("bad swap status: %+v", got)
	}
}

func TestSwapStatusJSONKeepsZeroUsage(t *testing.T) {
	got := SwapStatus{
		Enabled:   true,
		Active:    true,
		Priority:  32767,
		Device:    "/dev/block/zram0",
		SizeBytes: 25769799680,
		UsedBytes: 0,
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"used_bytes":0`) ||
		!strings.Contains(text, `"size_bytes":25769799680`) {
		t.Fatalf("zero swap usage was omitted from JSON: %s", text)
	}
}

func TestPolicyTextParsers(t *testing.T) {
	avail := parseAvailBuffers("avail_buffers: 5000\n"+
		"min_avail_buffers: 4500\n"+
		"high_avail_buffers: 5000\n"+
		"free_swap_threshold: 1536\n", AvailBuffers{})
	if avail.AvailMB != 5000 || avail.MinMB != 4500 ||
		avail.HighMB != 5000 || avail.FreeSwapThresholdMB != 1536 {
		t.Fatalf("bad avail buffer parse: %+v", avail)
	}
	erm := parseErmAvailBuffer("[0-0]", ErmAvailBuffer{MinMB: 512, HighMB: 1024})
	if erm.MinMB != 0 || erm.HighMB != 0 {
		t.Fatalf("bad erm parse: %+v", erm)
	}
}

func TestCrystalStatusUsesDevLifeLevelStat(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(zdir, "hybridswap_dev_life"), "enable\n")
	writeFile(filepath.Join(zdir, "hybridswap_crystal_stat"), "dev_life_level 2\n")

	got := app.crystalStatus("zram0")
	if got.DevLife != 2 {
		t.Fatalf("dev_life = %d, want stat level 2", got.DevLife)
	}
}

func TestWriteStepChmodsZramTakeoverNode(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(zdir, "disksize")
	if err := writeFile(node, "0\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(node, 0444); err != nil {
		t.Fatal(err)
	}

	result := app.writeStep(ApplyResult{Steps: []ApplyStep{}}, defaultProfile(),
		"zram.disksize", node, "4096", false)
	if result.Degraded {
		t.Fatalf("writeStep degraded: %+v", result)
	}
	if got := readTrim(node); got != "4096" {
		t.Fatalf("disksize = %q, want 4096", got)
	}
	info, err := os.Stat(node)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0200 == 0 {
		t.Fatalf("disksize mode = %o, want user-write bit", info.Mode().Perm())
	}
}

func TestWriteKernelNodeRejectsUnlistedPath(t *testing.T) {
	app := testApp(t)
	if err := mkdirAll(app.SysRoot); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(app.SysRoot, "not-a-kernel-node")
	if err := writeFile(node, "0\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeKernelNode(node, []byte("1\n")); err == nil {
		t.Fatal("writeKernelNode accepted non-allowlisted path")
	}
}

func TestWriteKernelNodeDoesNotCreateMissingNode(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(zdir, "disksize")
	if err := writeKernelNode(node, []byte("4096\n")); err == nil {
		t.Fatal("writeKernelNode created a missing sysfs node")
	}
	if _, err := os.Stat(node); !os.IsNotExist(err) {
		t.Fatalf("missing node was created or stat failed unexpectedly: %v", err)
	}
}

func TestWriteKernelNodeRejectsSymlinkedNode(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside-disksize")
	if err := writeFile(target, "0\n"); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(zdir, "disksize")
	if err := os.Symlink(target, node); err != nil {
		t.Fatal(err)
	}
	if err := writeKernelNode(node, []byte("4096\n")); err == nil {
		t.Fatal("writeKernelNode accepted a symlinked sysfs node")
	}
	if got := readTrim(target); got != "0" {
		t.Fatalf("symlink target was modified: %q", got)
	}
}

func TestWriteStepNormalizedSkipsSemanticCrystalMatch(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(zdir, "hybridswap_enable")
	if err := writeFile(node, "hybridswap enable reclaim_in enable swapd enable\n"); err != nil {
		t.Fatal(err)
	}

	result := app.writeStepNormalized(ApplyResult{Steps: []ApplyStep{}}, defaultProfile(),
		"crystal.enable", node, "1", false, normalizeCrystalEnabled("hybridswap"))
	if len(result.Steps) != 1 || result.Steps[0].Status != "unchanged" {
		t.Fatalf("normalized write result = %+v", result.Steps)
	}
	if got := readTrim(node); got != "hybridswap enable reclaim_in enable swapd enable" {
		t.Fatalf("node changed unexpectedly: %q", got)
	}
}

func TestRuntimeApplySkipsSemanticCrystalMatches(t *testing.T) {
	app := testApp(t)
	app.MemcgRoot = filepath.Join(app.DevRoot, "memcg")
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAll(app.MemcgRoot); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(zdir, "hybridswap_enable"), "hybridswap enable reclaim_in enable swapd enable\n")
	writeFile(filepath.Join(zdir, "hybridswap_core_enable"), "hybridswap enable reclaim_in enable\n")
	writeFile(filepath.Join(zdir, "hybridswap_loglevel"), "Hybridswap log level: 1\n")
	writeFile(filepath.Join(zdir, "hybridswap_dev_life"), "enable\n")
	writeFile(filepath.Join(zdir, "hybridswap_crystal_stat"), "dev_life_level 1\n")
	writeFile(filepath.Join(app.MemcgRoot, "memory.avail_buffers"),
		"avail_buffers: 0\nmin_avail_buffers: 512\nhigh_avail_buffers: 1024\nfree_swap_threshold: 512\n")

	p := defaultProfile()
	p.Crystal.Enable = i64p(1)
	p.Crystal.CoreEnable = i64p(1)
	p.Crystal.Loglevel = i64p(1)
	p.Crystal.DevLife = i64p(1)
	p.Policy.AvailBuffers = &AvailBuffers{AvailMB: 0, MinMB: 512, HighMB: 1024, FreeSwapThresholdMB: 512}

	result := app.applyRuntime(ApplyResult{Steps: []ApplyStep{}}, p, false)
	for _, step := range result.Steps {
		if step.Status == "changed" || step.Status == "failed" || step.Status == "denied" {
			t.Fatalf("semantic match should not write: %+v", result.Steps)
		}
	}
}

func TestRuntimeApplySkipsMatchingDevLifeLevel(t *testing.T) {
	app := testApp(t)
	app.MemcgRoot = filepath.Join(app.DevRoot, "memcg")
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAll(app.MemcgRoot); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(zdir, "hybridswap_dev_life"), "enable\n")
	writeFile(filepath.Join(zdir, "hybridswap_crystal_stat"), "dev_life_level 2\n")

	p := defaultProfile()
	p.Crystal.DevLife = i64p(2)
	result := app.applyRuntime(ApplyResult{Steps: []ApplyStep{}}, p, false)
	for _, step := range result.Steps {
		if step.Name == "crystal.dev_life" && step.Status != "unchanged" {
			t.Fatalf("matching dev_life level should not write: %+v", result.Steps)
		}
	}
	if got := readTrim(filepath.Join(zdir, "hybridswap_dev_life")); got != "enable" {
		t.Fatalf("dev_life node changed unexpectedly: %q", got)
	}
}

func TestWriteStepForceRewritesSameBackingTrigger(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(zdir, "hybridswap_loop_device")
	if err := writeFile(node, backingBlockDevice+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(node, 0444); err != nil {
		t.Fatal(err)
	}

	result := app.writeStepForce(ApplyResult{Steps: []ApplyStep{}}, defaultProfile(),
		"zram.hybridswap_loop_device", node, backingBlockDevice, false)
	if result.Degraded {
		t.Fatalf("writeStepForce degraded: %+v", result)
	}
	if len(result.Steps) != 1 || result.Steps[0].Status != "changed" {
		t.Fatalf("force write step = %+v, want one changed step", result.Steps)
	}
	info, err := os.Stat(node)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0200 == 0 {
		t.Fatalf("backing mode = %o, want user-write bit", info.Mode().Perm())
	}
}

func TestFullControlApplyStopsBeforeBackingWhenDisksizeFails(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAll(app.ProcRoot); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(zdir, "initstate"), "0\n")
	if err := os.Mkdir(filepath.Join(zdir, "disksize"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(zdir, "hybridswap_loop_device"), "none\n")
	writeFile(filepath.Join(app.ProcRoot, "meminfo"), "MemTotal: 24117244 kB\n")
	writeFile(filepath.Join(app.ProcRoot, "swaps"), "Filename\tType\tSize\tUsed\tPriority\n")

	p := defaultProfile()
	p.Zram.Disksize = strp("24G")
	result, err := app.apply("full-control", p)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Degraded || !hasFailedStep(result, "zram.disksize") {
		t.Fatalf("missing disksize failure: %+v", result)
	}
	for _, step := range result.Steps {
		if step.Name == "zram.hybridswap_loop_device" || strings.HasPrefix(step.Name, "swap.") {
			t.Fatalf("step ran after disksize failure: %+v", step)
		}
	}
}

func TestFullControlApplyDoesNotDenyAlreadyMatchingActiveZram(t *testing.T) {
	app := testApp(t)
	if err := mkdirAll(filepath.Join(app.SysRoot, "block", "zram0")); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAll(app.ProcRoot); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(app.SysRoot, "block", "zram0", "initstate"), "1\n")
	writeFile(filepath.Join(app.SysRoot, "block", "zram0", "disksize"), "25769803776\n")
	writeFile(filepath.Join(app.SysRoot, "block", "zram0", "hybridswap_loop_device"), backingBlockDevice+"\n")
	writeFile(filepath.Join(app.SysRoot, "block", "zram0", "zms_stat"), "enabled: 1\n")
	writeFile(filepath.Join(app.ProcRoot, "meminfo"), "MemTotal: 24117244 kB\n")
	writeFile(filepath.Join(app.ProcRoot, "swaps"),
		"Filename\tType\tSize\tUsed\tPriority\n/dev/block/zram0 partition 25165820 0 32767\n")
	p := defaultProfile()
	p.Zram.Disksize = strp("24G")

	result, err := app.apply("full-control", p)
	if err != nil {
		t.Fatal(err)
	}
	if result.Degraded {
		t.Fatalf("matching active zram degraded: %+v", result)
	}
	for _, step := range result.Steps {
		if step.Name == "zram.init" {
			t.Fatalf("unexpected init denial step: %+v", step)
		}
	}
}

func TestFullControlApplyStopsBeforeSwaponWhenBlockBackingFails(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAll(app.ProcRoot); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(zdir, "initstate"), "0\n")
	writeFile(filepath.Join(zdir, "disksize"), "0\n")
	if err := os.Mkdir(filepath.Join(zdir, "hybridswap_loop_device"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(app.ProcRoot, "meminfo"), "MemTotal: 24117244 kB\n")
	writeFile(filepath.Join(app.ProcRoot, "swaps"), "Filename\tType\tSize\tUsed\tPriority\n")

	p := defaultProfile()
	p.Zram.Disksize = strp("24G")
	result, err := app.apply("full-control", p)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Degraded {
		t.Fatalf("backing failure was not degraded: %+v", result)
	}
	if !hasFailedStep(result, "zram.hybridswap_loop_device") {
		t.Fatalf("missing failed backing step: %+v", result.Steps)
	}
	for _, step := range result.Steps {
		if strings.HasPrefix(step.Name, "swap.") {
			t.Fatalf("swap step ran after backing failure: %+v", step)
		}
	}
}

func TestStatusDegradedWhenConfiguredBlockBackingIsMissing(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAll(filepath.Join(app.SysRoot, "kernel", "crystal_hybridswap")); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(zdir, "initstate"), "1\n")
	writeFile(filepath.Join(zdir, "disksize"), "25769803776\n")
	writeFile(filepath.Join(zdir, "hybridswap_report"), "Crystal Hybridswap stage-3\n")
	writeFile(filepath.Join(zdir, "hybridswap_loop_device"), "\n")
	writeFile(filepath.Join(zdir, "backing_dev"), "none\n")
	writeFile(filepath.Join(zdir, "zms_stat"), "enabled: 0\nret: 0\n")
	if err := mkdirAll(app.ProcRoot); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(app.ProcRoot, "swaps"),
		"Filename\tType\tSize\tUsed\tPriority\n/dev/block/zram0 partition 25165820 0 32767\n")

	status, _ := app.status()
	if !status.Degraded {
		t.Fatalf("status not degraded: %+v", status)
	}
	got := strings.Join(status.DegradedReasons, "\n")
	if !strings.Contains(got, "block backing is not bound") {
		t.Fatalf("missing backing degraded reason: %q", got)
	}
}

func TestStatusDegradedWhenFullControlSwapIsInactive(t *testing.T) {
	app := testApp(t)
	zdir := filepath.Join(app.SysRoot, "block", "zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAll(filepath.Join(app.SysRoot, "kernel", "crystal_hybridswap")); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAll(app.ProcRoot); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(zdir, "initstate"), "1\n")
	writeFile(filepath.Join(zdir, "disksize"), "25769803776\n")
	writeFile(filepath.Join(zdir, "hybridswap_report"), "Crystal Hybridswap stage-3\n")
	writeFile(filepath.Join(zdir, "hybridswap_loop_device"), backingBlockDevice+"\n")
	writeFile(filepath.Join(zdir, "zms_stat"), "enabled: 1\n")
	writeFile(filepath.Join(app.ProcRoot, "swaps"), "Filename\tType\tSize\tUsed\tPriority\n")

	status, _ := app.status()
	if !status.Degraded {
		t.Fatalf("inactive swap was not degraded: %+v", status)
	}
	got := strings.Join(status.DegradedReasons, "\n")
	if !strings.Contains(got, "zram swap is not active") {
		t.Fatalf("missing swap degraded reason: %q", got)
	}
}

func TestProfileRequiredNodesMissingReportsConfiguredMemcgFields(t *testing.T) {
	app := testApp(t)
	profile := defaultProfile()
	profile.Policy.AvailBuffers = &AvailBuffers{
		AvailMB: 1, MinMB: 2, HighMB: 3, FreeSwapThresholdMB: 4,
	}

	missing := strings.Join(app.profileRequiredNodesMissing(profile), "\n")
	if !strings.Contains(missing, "memory.avail_buffers") {
		t.Fatalf("missing policy field not reported: %q", missing)
	}
	if strings.Contains(missing, "memory.zram_wm_ratio") {
		t.Fatalf("unconfigured policy field reported: %q", missing)
	}
}

func TestBootApplyRejectsInvalidTimeout(t *testing.T) {
	app := testApp(t)
	if ok := app.bootApplyAPI([]byte(`{"phase":"service","timeout_ms":1}`)); ok {
		t.Fatal("invalid timeout_ms was accepted")
	}
}

func TestBootApplyRejectsNullNonNullableFields(t *testing.T) {
	for name, body := range map[string]string{
		"phase":      `{"phase":null}`,
		"timeout_ms": `{"phase":"service","timeout_ms":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateBootApplyRequestShape([]byte(body)); err == nil {
				t.Fatal("boot apply request accepted a null non-nullable field")
			}
		})
	}
}

func TestBootApplyWarnsWhenBootStatusCannotBeSaved(t *testing.T) {
	app := testApp(t)
	if err := mkdirAll(app.DataDir); err != nil {
		t.Fatal(err)
	}
	p := defaultProfile()
	p.Mode = "diagnostic"
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.DataDir, "profile.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(app.DataDir, "state-file")
	if err := writeFile(stateFile, "not a directory\n"); err != nil {
		t.Fatal(err)
	}
	app.StateDir = stateFile

	var env struct {
		OK       bool        `json:"ok"`
		Data     ApplyResult `json:"data"`
		Warnings []Warning   `json:"warnings"`
	}
	ok := captureJSON(t, func() bool {
		return app.bootApplyAPI([]byte(`{"phase":"service"}`))
	}, &env)
	if !ok || !env.OK {
		t.Fatalf("diagnostic boot apply failed: ok=%v envelope=%+v", ok, env)
	}
	if !env.Data.Degraded {
		t.Fatalf("boot status save failure did not degrade result: %+v", env.Data)
	}
	if len(env.Warnings) != 1 || env.Warnings[0].Code != "W_BOOT_STATUS_SAVE_FAILED" {
		t.Fatalf("missing boot status save warning: %+v", env.Warnings)
	}
	if env.Data.Boot == nil || env.Data.Boot.Result != "skipped" {
		t.Fatalf("unexpected boot status in result: %+v", env.Data.Boot)
	}
}

func TestDaemonLockExcludesSecondOwner(t *testing.T) {
	app := testApp(t)
	unlock, locked, err := app.lockDaemon()
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("first daemon lock was not acquired")
	}
	defer unlock()
	if pid := strings.TrimSpace(readTrim(filepath.Join(app.StateDir, "daemon.pid"))); pid == "" {
		t.Fatal("daemon pidfile was not written")
	}

	unlock2, locked2, err := app.lockDaemon()
	if err != nil {
		t.Fatal(err)
	}
	if locked2 {
		unlock2()
		t.Fatal("second daemon lock unexpectedly succeeded")
	}
}

func testApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	return &App{
		SysRoot:        filepath.Join(root, "sys"),
		ProcRoot:       filepath.Join(root, "proc"),
		DevRoot:        filepath.Join(root, "dev"),
		ModDir:         filepath.Join(root, "module"),
		DataDir:        data,
		StateDir:       filepath.Join(data, "state"),
		LogDir:         filepath.Join(data, "logs"),
		DiagnosticsDir: filepath.Join(data, "diagnostics"),
		SkipKSUD:       true,
	}
}

func mkdirAll(path string) error {
	return osMkdirAll(path, 0700)
}

func writeFile(path, data string) error {
	return osWriteFile(path, []byte(data), 0600)
}

func captureJSON(t *testing.T, fn func() bool, out any) bool {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	ok := fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), out); err != nil {
		t.Fatalf("failed to decode envelope %q: %v", data, err)
	}
	return ok
}

var osMkdirAll = os.MkdirAll
var osWriteFile = os.WriteFile

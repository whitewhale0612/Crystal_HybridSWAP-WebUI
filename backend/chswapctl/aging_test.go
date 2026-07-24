package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func agingFixture(t *testing.T, fg string) (*App, string) {
	t.Helper()
	app := testApp(t)
	app.ForceWaitMS = 50
	zdir := filepath.Join(app.SysRoot, "block/zram0")
	if err := mkdirAll(zdir); err != nil {
		t.Fatal(err)
	}
	for node, data := range map[string]string{
		"initstate": "1\n", "disksize": "17179869184\n",
		"hybridswap_enable":      "hybridswap enable reclaim_in enable swapd enable\n",
		"hybridswap_core_enable": "reclaim_in enable\n",
		"hybridswap_swapd_pause": "0\n", "hybridswap_quota_day": "86400\n",
		"hybridswap_crystal_stat": "writeback_quota_limit_bytes 1073741824\nwriteback_quota_used_bytes 0\nwriteback_quota_remaining_bytes 1073741824\n",
	} {
		if err := writeFile(filepath.Join(zdir, node), data); err != nil {
			t.Fatal(err)
		}
	}
	memcg := filepath.Join(app.DevRoot, "memcg")
	dir := filepath.Join(memcg, "apps", "uid_10234")
	if err := mkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		filepath.Join(memcg, "memory.zram_wm_ratio"):      "75\n",
		filepath.Join(memcg, "memory.total_info_per_app"): "anon zram_c zram_p eswap_c eswap_p memcg_n score uid policy out_trig in_mb in_request in_req_pages in_eff_pages in_moved_pages in_ret\n0 131072 524288 0 0 com.example.app 300 10234 2 0 0 0 0 0 0 0\n",
		filepath.Join(memcg, "memory.force_swapout"):      "0\n",
		filepath.Join(memcg, "memory.force_swapin"):       "0\n",
		filepath.Join(dir, "memory.name"):                 "com.example.app\n",
		filepath.Join(dir, "memory.app_uid"):              "10234\n",
		filepath.Join(dir, "memory.force_swapout"):        "0\n",
		filepath.Join(dir, "memory.swap_stat"):            "forceSwapoutLastRet: 42\n",
		filepath.Join(app.ProcRoot, "fg_info/fg_uids"):    "fg_uids: " + fg + "\n",
		filepath.Join(app.ProcRoot, "meminfo"):            "MemAvailable:       1024000 kB\n",
	} {
		if err := mkdirAll(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(path, data); err != nil {
			t.Fatal(err)
		}
	}
	return app, dir
}

func enabledAgingProfile() *Profile {
	p := defaultProfile()
	p.Aging = AgingProfile{
		Enabled: true, IdleAfterSec: 60, MinZramMB: 64,
		PressureAvailableBelowMB: 4096, CooldownSec: 1800, MaxAppsPerCycle: 1,
	}
	return p
}

func TestAgingSkipsForegroundApp(t *testing.T) {
	app, dir := agingFixture(t, "10234")
	state := agingState{Version: agingStateVersion, Apps: map[string]agingAppState{
		"com.example.app|10234": {LastForegroundUnix: time.Now().Add(-time.Hour).Unix()},
	}}
	raw, _ := json.Marshal(state)
	if err := mkdirAll(filepath.Dir(app.agingStatePath())); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(app.agingStatePath(), string(raw)); err != nil {
		t.Fatal(err)
	}
	app.agingCycle(enabledAgingProfile(), func(string, ...any) {})
	got, _ := os.ReadFile(filepath.Join(dir, "memory.force_swapout"))
	if strings.TrimSpace(string(got)) != "0" {
		t.Fatalf("foreground app was triggered: %q", got)
	}
}

func TestAgingTriggersStaleBackgroundAppAndSetsCooldown(t *testing.T) {
	app, dir := agingFixture(t, "99999")
	state := agingState{Version: agingStateVersion, Apps: map[string]agingAppState{
		"com.example.app|10234": {LastForegroundUnix: time.Now().Add(-time.Hour).Unix()},
	}}
	raw, _ := json.Marshal(state)
	if err := mkdirAll(filepath.Dir(app.agingStatePath())); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(app.agingStatePath(), string(raw)); err != nil {
		t.Fatal(err)
	}
	app.agingCycle(enabledAgingProfile(), func(string, ...any) {})
	got, _ := os.ReadFile(filepath.Join(dir, "memory.force_swapout"))
	if strings.TrimSpace(string(got)) != "1" {
		t.Fatalf("stale background app was not triggered: %q", got)
	}
	state = app.loadAgingState()
	entry := state.Apps["com.example.app|10234"]
	if entry.LastResult != "ok" || entry.LastBytes != 42*pageBytes || entry.CooldownUntilUnix <= time.Now().Unix() {
		t.Fatalf("aging result/cooldown not recorded: %+v", entry)
	}
	if entry.Failures != 0 || entry.LastRet != 42 || entry.LastAttemptZramOrigBytes == 0 {
		t.Fatalf("successful aging attempt state not recorded: %+v", entry)
	}
}

func TestAgingKernelFailurePersistsExponentialBackoff(t *testing.T) {
	app, dir := agingFixture(t, "99999")
	if err := writeFile(filepath.Join(dir, "memory.swap_stat"), "forceSwapoutLastRet: -11\n"); err != nil {
		t.Fatal(err)
	}
	state := agingState{Version: agingStateVersion, Apps: map[string]agingAppState{
		"com.example.app|10234": {LastForegroundUnix: time.Now().Add(-time.Hour).Unix()},
	}}
	raw, _ := json.Marshal(state)
	if err := mkdirAll(filepath.Dir(app.agingStatePath())); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(app.agingStatePath(), string(raw)); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Unix()
	app.agingCycle(enabledAgingProfile(), func(string, ...any) {})
	entry := app.loadAgingState().Apps["com.example.app|10234"]
	if entry.LastResult != "error" || entry.LastRet != -int64(syscall.EAGAIN) || entry.Failures != 1 {
		t.Fatalf("kernel error did not persist as a failed aging attempt: %+v", entry)
	}
	if entry.CooldownUntilUnix < before+int64(agingRetryBackoff/time.Second) {
		t.Fatalf("failure cooldown too short: %+v", entry)
	}
	if entry.LastAttemptZramOrigBytes == 0 || entry.LastAttemptEswapOrigBytes != 0 {
		t.Fatalf("pre-attempt footprint not recorded: %+v", entry)
	}
}

func TestAgingCandidateRankingFavorsFreshWriteback(t *testing.T) {
	candidates := []agingCandidate{
		{App: AppMemInfo{Name: "already-written"}, LastFG: 1, ZramBytes: 512, HasWrittenBack: true, WritebackRatioPPM: 900000},
		{App: AppMemInfo{Name: "fresh"}, LastFG: 100, ZramBytes: 64, HasWrittenBack: false},
		{App: AppMemInfo{Name: "partly-written"}, LastFG: 2, ZramBytes: 256, HasWrittenBack: true, WritebackRatioPPM: 300000},
	}
	sortAgingCandidates(candidates)
	if candidates[0].App.Name != "fresh" || candidates[1].App.Name != "partly-written" || candidates[2].App.Name != "already-written" {
		t.Fatalf("unexpected aging candidate order: %+v", candidates)
	}
}

func TestAgingCandidateRankingRotatesByLastAttempt(t *testing.T) {
	candidates := []agingCandidate{
		{App: AppMemInfo{Name: "recent"}, LastAttempt: 200, LastFG: 1},
		{App: AppMemInfo{Name: "old"}, LastAttempt: 100, LastFG: 999},
		{App: AppMemInfo{Name: "never"}, LastAttempt: 0, LastFG: 999},
	}
	sortAgingCandidates(candidates)
	if candidates[0].App.Name != "never" || candidates[1].App.Name != "old" || candidates[2].App.Name != "recent" {
		t.Fatalf("unexpected aging rotation order: %+v", candidates)
	}
}

func TestAgingResolvesMixedUIDToMotherAndMigratesState(t *testing.T) {
	app, dir := agingFixture(t, "99999")
	memcg := filepath.Dir(filepath.Dir(dir))
	if err := writeFile(filepath.Join(memcg, "memory.total_info_per_app"),
		"anon zram_c zram_p eswap_c eswap_p memcg_n score uid policy out_trig in_mb in_request in_req_pages in_eff_pages in_moved_pages in_ret\n"+
			"0 131072 524288 0 0 com.example.app 300 -2 2 0 0 0 0 0 0 0\n"); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		filepath.Join(dir, "memory.app_uid"):       "-2\n",
		filepath.Join(dir, "cgroup.procs"):         "100\n101\n",
		filepath.Join(app.ProcRoot, "100/status"):  "Name:\tcom.example.app\nUid:\t10234\t10234\t10234\t10234\n",
		filepath.Join(app.ProcRoot, "101/status"):  "Name:\twebview\nUid:\t99001\t99001\t99001\t99001\n",
		filepath.Join(app.ProcRoot, "100/cmdline"): "com.example.app\n",
		filepath.Join(app.ProcRoot, "101/cmdline"): "com.google.android.webview:sandboxed_process0\n",
	} {
		if err := mkdirAll(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(path, data); err != nil {
			t.Fatal(err)
		}
	}
	state := agingState{Version: agingStateVersion, Apps: map[string]agingAppState{
		"com.example.app|-2": {LastForegroundUnix: time.Now().Add(-time.Hour).Unix()},
	}}
	raw, _ := json.Marshal(state)
	if err := mkdirAll(filepath.Dir(app.agingStatePath())); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(app.agingStatePath(), string(raw)); err != nil {
		t.Fatal(err)
	}

	app.agingCycle(enabledAgingProfile(), func(string, ...any) {})
	if got, _ := os.ReadFile(filepath.Join(dir, "memory.force_swapout")); strings.TrimSpace(string(got)) != "1" {
		t.Fatalf("mixed memcg was not triggered through the mother UID: %q", got)
	}
	state = app.loadAgingState()
	if state.Apps["com.example.app|10234"].LastResult != "ok" {
		t.Fatalf("normalized aging state missing: %+v", state.Apps)
	}
	if _, exists := state.Apps["com.example.app|-2"]; exists {
		t.Fatalf("stale mixed UID state was not migrated: %+v", state.Apps)
	}
}

func TestAgingMixedUIDProtectsMotherUIDInForeground(t *testing.T) {
	app, dir := agingFixture(t, "10234")
	memcg := filepath.Dir(filepath.Dir(dir))
	if err := writeFile(filepath.Join(memcg, "memory.total_info_per_app"),
		"anon zram_c zram_p eswap_c eswap_p memcg_n score uid policy out_trig in_mb in_request in_req_pages in_eff_pages in_moved_pages in_ret\n"+
			"0 131072 524288 0 0 com.example.app 300 -2 2 0 0 0 0 0 0 0\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "memory.app_uid"), "-2\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "cgroup.procs"), "100\n101\n"); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		filepath.Join(app.ProcRoot, "100/status"):  "Name:\tcom.example.app\nUid:\t10234\t10234\t10234\t10234\n",
		filepath.Join(app.ProcRoot, "101/status"):  "Name:\twebview\nUid:\t99001\t99001\t99001\t99001\n",
		filepath.Join(app.ProcRoot, "100/cmdline"): "com.example.app\n",
		filepath.Join(app.ProcRoot, "101/cmdline"): "com.google.android.webview:sandboxed_process0\n",
	} {
		if err := mkdirAll(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(path, data); err != nil {
			t.Fatal(err)
		}
	}
	app.agingCycle(enabledAgingProfile(), func(string, ...any) {})
	if got, _ := os.ReadFile(filepath.Join(dir, "memory.force_swapout")); strings.TrimSpace(string(got)) != "0" {
		t.Fatalf("mixed foreground mother UID was triggered: %q", got)
	}
}

func TestForceSwapResultKindStorageFull(t *testing.T) {
	if got := forceSwapResultKind(-int64(syscall.ENOSPC), true); got != "storage_full" {
		t.Fatalf("ENOSPC classified as %q", got)
	}
}

func TestAgingRepeatedFailuresNeedNewZramToRearm(t *testing.T) {
	entry := agingAppState{
		Failures:                 agingRearmAfterFailures,
		LastResult:               "nodata",
		LastAttemptZramOrigBytes: 512 * 1024 * 1024,
	}
	if !agingNeedsRearm(entry, AppMemInfo{ZramOrigBytes: 600 * 1024 * 1024}, 64) {
		t.Fatal("unchanged or marginally changed footprint should remain suppressed")
	}
	if agingNeedsRearm(entry, AppMemInfo{ZramOrigBytes: 640 * 1024 * 1024}, 64) {
		t.Fatal("materially grown footprint should rearm the app")
	}
	entry.LastResult = "quota"
	if agingNeedsRearm(entry, AppMemInfo{ZramOrigBytes: 512 * 1024 * 1024}, 64) {
		t.Fatal("global quota failures must recover through backoff, not footprint growth")
	}
}

func TestAgingFailureBackoffSchedule(t *testing.T) {
	for failures, want := range map[int64]time.Duration{
		1: 5 * time.Minute,
		2: 15 * time.Minute,
		3: 30 * time.Minute,
		4: time.Hour,
		7: 6 * time.Hour,
	} {
		if got := agingFailureBackoff(failures); got != want {
			t.Fatalf("failures=%d backoff=%s, want %s", failures, got, want)
		}
	}
}

func TestAgingFailsSafeWhenForegroundUIDUnavailable(t *testing.T) {
	app, dir := agingFixture(t, "99999")
	if err := os.Remove(filepath.Join(app.ProcRoot, "fg_info/fg_uids")); err != nil {
		t.Fatal(err)
	}
	app.agingCycle(enabledAgingProfile(), func(string, ...any) {})
	got, _ := os.ReadFile(filepath.Join(dir, "memory.force_swapout"))
	if strings.TrimSpace(string(got)) != "0" {
		t.Fatalf("aging triggered without foreground UID: %q", got)
	}
}

func TestForegroundUIDsFallsBackToTopAppCpuset(t *testing.T) {
	app := testApp(t)
	for path, data := range map[string]string{
		filepath.Join(app.DevRoot, "cpuset/top-app/cgroup.procs"): "123\n456\n",
		filepath.Join(app.ProcRoot, "123/status"):                 "Name:\tsystem_server\nUid:\t1000\t1000\t1000\t1000\n",
		filepath.Join(app.ProcRoot, "456/status"):                 "Name:\tcom.example\nUid:\t10234\t10234\t10234\t10234\n",
	} {
		if err := mkdirAll(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(path, data); err != nil {
			t.Fatal(err)
		}
	}
	uids, ok := app.foregroundUIDs()
	if !ok || !uids[10234] || uids[1000] {
		t.Fatalf("unexpected top-app UID set: ok=%v uids=%v", ok, uids)
	}
}

func TestAgingForegroundRefreshResetsOnlyOnTopAppEntry(t *testing.T) {
	app := testApp(t)
	state := agingState{Version: agingStateVersion, Apps: map[string]agingAppState{
		"com.example.app|10234": {LastForegroundUnix: 100},
	}}
	raw, _ := json.Marshal(state)
	if err := mkdirAll(filepath.Dir(app.agingStatePath())); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(app.agingStatePath(), string(raw)); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		filepath.Join(app.DevRoot, "cpuset/top-app/cgroup.procs"): "123\n",
		filepath.Join(app.ProcRoot, "123/status"):                 "Name:\tcom.example\nUid:\t10234\t10234\t10234\t10234\n",
	} {
		if err := mkdirAll(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(path, data); err != nil {
			t.Fatal(err)
		}
	}
	app.agingForegroundRefresh(func(string, ...any) {})
	first := app.loadAgingState().Apps["com.example.app|10234"].LastForegroundUnix
	if first <= 100 {
		t.Fatalf("top-app entry did not reset foreground time: %d", first)
	}
	app.agingForegroundRefresh(func(string, ...any) {})
	second := app.loadAgingState().Apps["com.example.app|10234"].LastForegroundUnix
	if second != first {
		t.Fatalf("steady top-app sample rewrote foreground time: first=%d second=%d", first, second)
	}

	if err := writeFile(filepath.Join(app.DevRoot, "cpuset/top-app/cgroup.procs"), "\n"); err != nil {
		t.Fatal(err)
	}
	app.agingForegroundRefresh(func(string, ...any) {})
	if err := writeFile(filepath.Join(app.DevRoot, "cpuset/top-app/cgroup.procs"), "123\n"); err != nil {
		t.Fatal(err)
	}
	app.agingForegroundRefresh(func(string, ...any) {})
	third := app.loadAgingState().Apps["com.example.app|10234"].LastForegroundUnix
	if third < second {
		t.Fatalf("top-app re-entry moved foreground time backwards: second=%d third=%d", second, third)
	}
}

func TestImportantDirectZygoteProcessIsProtected(t *testing.T) {
	app := testApp(t)
	dir := forceSwapTree(t, app, "com.netease.cloudmusic", "10358", "forceSwapoutLastRet: 0\n")
	for path, data := range map[string]string{
		filepath.Join(dir, "cgroup.procs"):                 "20963\n",
		filepath.Join(app.ProcRoot, "20963/status"):        "Name:\tcloudmusic\nPPid:\t1861\nUid:\t10358\t10358\t10358\t10358\n",
		filepath.Join(app.ProcRoot, "20963/oom_score_adj"): "100\n",
		filepath.Join(app.ProcRoot, "1861/status"):         "Name:\tmain\nPPid:\t1\nUid:\t0\t0\t0\t0\n",
		filepath.Join(app.ProcRoot, "1861/cmdline"):        "zygote64\x00",
	} {
		if err := mkdirAll(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(path, data); err != nil {
			t.Fatal(err)
		}
	}
	if !app.appHasProtectedProcess(AppMemInfo{Name: "com.netease.cloudmusic", UID: 10358}) {
		t.Fatal("active zygote-spawned music process was not protected")
	}
}

func addDirectZygoteProcess(t *testing.T, app *App, dir, pid, ppid, uid, adj string) {
	t.Helper()
	for path, data := range map[string]string{
		filepath.Join(dir, "cgroup.procs"):                pid + "\n",
		filepath.Join(app.ProcRoot, pid, "status"):        "Name:\tmain\nPPid:\t" + ppid + "\nUid:\t" + uid + "\t" + uid + "\t" + uid + "\t" + uid + "\n",
		filepath.Join(app.ProcRoot, pid, "oom_score_adj"): adj + "\n",
		filepath.Join(app.ProcRoot, ppid, "status"):       "Name:\tmain\nPPid:\t1\nUid:\t0\t0\t0\t0\n",
		filepath.Join(app.ProcRoot, ppid, "cmdline"):      "zygote64\x00",
	} {
		if err := mkdirAll(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(path, data); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdj200WithoutUserVisibleStateIsEligible(t *testing.T) {
	app := testApp(t)
	dir := forceSwapTree(t, app, "com.example.app", "10234", "forceSwapoutLastRet: 0\n")
	addDirectZygoteProcess(t, app, dir, "20001", "1901", "10234", "200")
	app.commandOutFn = func(time.Duration, string, ...string) (string, error) {
		return "no active state\n", nil
	}
	if app.appHasProtectedProcess(AppMemInfo{Name: "com.example.app", UID: 10234}) {
		t.Fatal("adj=200 process without foreground service/media should be eligible")
	}
}

func TestAdj200ForegroundServiceIsProtected(t *testing.T) {
	app := testApp(t)
	dir := forceSwapTree(t, app, "com.example.app", "10234", "forceSwapoutLastRet: 0\n")
	addDirectZygoteProcess(t, app, dir, "20002", "1902", "10234", "200")
	app.commandOutFn = func(_ time.Duration, name string, args ...string) (string, error) {
		if name != "dumpsys" {
			return "", nil
		}
		if len(args) >= 2 && args[0] == "activity" {
			return "* ServiceRecord{1 u0 com.example.app/.PlaybackService c:com.example.app}\n    isForeground=true foregroundId=7\n", nil
		}
		return "MEDIA SESSION SERVICE\n", nil
	}
	if !app.appHasProtectedProcess(AppMemInfo{Name: "com.example.app", UID: 10234}) {
		t.Fatal("foreground service was not protected")
	}
}

func TestAdj200ActiveMediaIsProtected(t *testing.T) {
	app := testApp(t)
	dir := forceSwapTree(t, app, "com.example.app", "10234", "forceSwapoutLastRet: 0\n")
	addDirectZygoteProcess(t, app, dir, "20003", "1903", "10234", "200")
	app.commandOutFn = func(_ time.Duration, name string, args ...string) (string, error) {
		if name == "dumpsys" && len(args) >= 1 && args[0] == "media_session" {
			return "Audio playback (lastly played comes first)\n  uid=10234 packages=com.example.app\n", nil
		}
		return "MEDIA SESSION SERVICE\n", nil
	}
	if !app.appHasProtectedProcess(AppMemInfo{Name: "com.example.app", UID: 10234}) {
		t.Fatal("active media UID was not protected")
	}
}

func TestAdj200AndroidStateReadFailureFailsSafe(t *testing.T) {
	app := testApp(t)
	dir := forceSwapTree(t, app, "com.example.app", "10234", "forceSwapoutLastRet: 0\n")
	addDirectZygoteProcess(t, app, dir, "20004", "1904", "10234", "200")
	app.commandOutFn = func(time.Duration, string, ...string) (string, error) {
		return "", syscall.EIO
	}
	if !app.appHasProtectedProcess(AppMemInfo{Name: "com.example.app", UID: 10234}) {
		t.Fatal("Android state read failure did not fail safe")
	}
}

func TestLowAdjSameUIDChildDoesNotProtectWholeApp(t *testing.T) {
	app := testApp(t)
	dir := forceSwapTree(t, app, "com.xingin.xhs", "10351", "forceSwapoutLastRet: 0\n")
	for path, data := range map[string]string{
		filepath.Join(dir, "cgroup.procs"):                 "19829\n20934\n21000\n",
		filepath.Join(app.ProcRoot, "19829/status"):        "Name:\tcom.xingin.xhs\nPPid:\t1922\nUid:\t10351\t10351\t10351\t10351\n",
		filepath.Join(app.ProcRoot, "19829/oom_score_adj"): "900\n",
		filepath.Join(app.ProcRoot, "20934/status"):        "Name:\t:widgetProvider\nPPid:\t19829\nUid:\t10351\t10351\t10351\t10351\n",
		filepath.Join(app.ProcRoot, "20934/oom_score_adj"): "0\n",
		filepath.Join(app.ProcRoot, "21000/status"):        "Name:\tzygote\nPPid:\t20934\nUid:\t10351\t10351\t10351\t10351\n",
		filepath.Join(app.ProcRoot, "21000/oom_score_adj"): "0\n",
		filepath.Join(app.ProcRoot, "1922/status"):         "Name:\tmain\nPPid:\t1\nUid:\t0\t0\t0\t0\n",
		filepath.Join(app.ProcRoot, "1922/cmdline"):        "zygote64\x00",
	} {
		if err := mkdirAll(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(path, data); err != nil {
			t.Fatal(err)
		}
	}
	if app.appHasProtectedProcess(AppMemInfo{Name: "com.xingin.xhs", UID: 10351}) {
		t.Fatal("same-uid widget child incorrectly protected the whole app")
	}
}

package main

// aging.go implements the optional userspace app-level aging policy. The
// kernel's force_swapout ABI operates on a complete memcg, so this worker is
// intentionally conservative and fail-safe around the foreground UID.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	agingStateVersion          = 4
	agingRetryBackoff          = 5 * time.Minute
	agingFailureBackoffMax     = 6 * time.Hour
	agingRearmAfterFailures    = 3
	agingStateMaxApps          = 512
	agingForegroundPoll        = 5 * time.Second
	agingFullCycle             = 60 * time.Second
	agingStorageCooldown       = 30 * time.Minute
	agingStorageTriggerPct     = int64(2)
	agingStorageRecoverPct     = int64(5)
	agingStorageRecoverSamples = 2
)

type agingState struct {
	Version int                       `json:"version"`
	Apps    map[string]agingAppState  `json:"apps"`
	Games   map[string]agingGameState `json:"games,omitempty"`
	Storage agingStorageState         `json:"storage,omitempty"`
}

type agingGameState struct {
	Package      string `json:"package"`
	UID          int64  `json:"uid"`
	LastSeenUnix int64  `json:"last_seen_unix"`
	LastPID      int64  `json:"last_pid,omitempty"`
}

type agingStorageState struct {
	BlockedUntilUnix int64  `json:"blocked_until_unix,omitempty"`
	RecoverySamples  int    `json:"recovery_samples,omitempty"`
	LastFreeBlocks   int64  `json:"last_free_blocks,omitempty"`
	LastNrBlocks     int64  `json:"last_nr_blocks,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

type agingStorageCapacity struct {
	Known      bool
	NrBlocks   int64
	UsedBlocks int64
	FreeBlocks int64
}

type agingAppState struct {
	LastForegroundUnix        int64  `json:"last_foreground_unix"`
	CooldownUntilUnix         int64  `json:"cooldown_until_unix,omitempty"`
	LastAttemptUnix           int64  `json:"last_attempt_unix,omitempty"`
	LastAttemptZramOrigBytes  int64  `json:"last_attempt_zram_orig_bytes,omitempty"`
	LastAttemptZramComprBytes int64  `json:"last_attempt_zram_compr_bytes,omitempty"`
	LastAttemptEswapOrigBytes int64  `json:"last_attempt_eswap_orig_bytes,omitempty"`
	LastResult                string `json:"last_result,omitempty"`
	LastRet                   int64  `json:"last_ret,omitempty"`
	LastBytes                 int64  `json:"last_bytes,omitempty"`
	Failures                  int64  `json:"failures,omitempty"`
}

type agingCandidate struct {
	App               AppMemInfo
	LastAttempt       int64
	LastFG            int64
	ZramBytes         int64
	HasWrittenBack    bool
	WritebackRatioPPM int64
}

func agingGamePackage(cmdline string) string {
	fields := strings.Fields(strings.ReplaceAll(cmdline, "\x00", " "))
	if len(fields) == 0 {
		return ""
	}
	name := fields[0]
	if strings.HasPrefix(name, "/") {
		return ""
	}
	if i := strings.IndexByte(name, ':'); i > 0 {
		name = name[:i]
	}
	if strings.Contains(name, "/") {
		return ""
	}
	if !strings.Contains(name, ".") {
		return ""
	}
	return name
}

// agingGameProcessIdentity resolves a game probe PID to a regular app UID and
// package. The short parent walk handles a vendor probe that points at an
// isolated child instead of the game's mother process.
func (a *App) agingGameProcessIdentity(pid int64) (string, int64, bool) {
	for depth := 0; depth < 4 && pid > 0; depth++ {
		status := readTrim(a.proc(filepath.Join(strconv.FormatInt(pid, 10), "status")))
		if status == "" {
			return "", 0, false
		}
		uid := int64(-1)
		ppid := int64(-1)
		for _, line := range strings.Split(status, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			switch fields[0] {
			case "Uid:":
				uid = parseIntDefault(fields[1], -1)
			case "PPid:":
				ppid = parseIntDefault(fields[1], -1)
			}
		}
		cmdline := readTrim(a.proc(filepath.Join(strconv.FormatInt(pid, 10), "cmdline")))
		if uid >= 10000 && !agingIsolatedTask(uid, cmdline) {
			if pkg := agingGamePackage(cmdline); pkg != "" {
				return pkg, uid, true
			}
		}
		if ppid <= 0 || ppid == pid {
			break
		}
		pid = ppid
	}
	return "", 0, false
}

func agingWritebackRatioPPM(app AppMemInfo) int64 {
	total := app.ZramOrigBytes + app.EswapOrigBytes
	if app.EswapOrigBytes <= 0 || total <= 0 {
		return 0
	}
	return app.EswapOrigBytes * 1_000_000 / total
}

// agingFailureBackoff gives an application which repeatedly produces no
// useful work progressively more room before it is considered again:
// 5m, 15m, 30m, 1h, 2h, 4h, then 6h. It bounds noisy kernel-error retries
// without permanently blacklisting an app whose ZRAM footprint later grows.
func agingFailureBackoff(failures int64) time.Duration {
	if failures <= 0 {
		return agingRetryBackoff
	}
	backoff := agingRetryBackoff
	for n := int64(1); n < failures && backoff < agingFailureBackoffMax; n++ {
		if n == 1 {
			backoff *= 3
		} else {
			backoff *= 2
		}
		if backoff > agingFailureBackoffMax {
			return agingFailureBackoffMax
		}
	}
	return backoff
}

func agingHardFailure(result string) bool {
	switch result {
	case "error", "nodata", "quota", "nodev":
		return true
	default:
		return false
	}
}

func agingResultNeedsRearm(result string) bool {
	return result == "error" || result == "nodata"
}

// agingNeedsRearm suppresses repeatedly unproductive attempts until this
// memcg has accumulated materially more resident ZRAM. The required growth is
// the greater of the configured candidate floor and 25% of the last attempted
// original footprint. This is a re-arm condition, not an ESWAP blacklist:
// apps that grow again remain eligible.
func agingNeedsRearm(entry agingAppState, app AppMemInfo, minZramMB int64) bool {
	if entry.Failures < agingRearmAfterFailures || !agingResultNeedsRearm(entry.LastResult) || entry.LastAttemptZramOrigBytes <= 0 {
		return false
	}
	growth := minZramMB * 1024 * 1024
	if relative := entry.LastAttemptZramOrigBytes / 4; relative > growth {
		growth = relative
	}
	return app.ZramOrigBytes < entry.LastAttemptZramOrigBytes+growth
}

func sortAgingCandidates(candidates []agingCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		// Rotate candidates after cooldown instead of repeatedly choosing the
		// same deterministic top-ranked app. A zero timestamp means never tried.
		if candidates[i].LastAttempt != candidates[j].LastAttempt {
			if candidates[i].LastAttempt == 0 {
				return true
			}
			if candidates[j].LastAttempt == 0 {
				return false
			}
			return candidates[i].LastAttempt < candidates[j].LastAttempt
		}
		// An app with no backing-store footprint gets a turn before an app
		// that has already been substantially offloaded.
		if candidates[i].HasWrittenBack != candidates[j].HasWrittenBack {
			return !candidates[i].HasWrittenBack
		}
		if candidates[i].WritebackRatioPPM != candidates[j].WritebackRatioPPM {
			return candidates[i].WritebackRatioPPM < candidates[j].WritebackRatioPPM
		}
		if candidates[i].LastFG != candidates[j].LastFG {
			return candidates[i].LastFG < candidates[j].LastFG
		}
		return candidates[i].ZramBytes > candidates[j].ZramBytes
	})
}

func agingRecordAttempt(entry *agingAppState, app AppMemInfo, now int64) {
	entry.LastAttemptUnix = now
	entry.LastAttemptZramOrigBytes = app.ZramOrigBytes
	entry.LastAttemptZramComprBytes = app.ZramComprBytes
	entry.LastAttemptEswapOrigBytes = app.EswapOrigBytes
}

func (a *App) agingStatePath() string {
	return filepath.Join(a.StateDir, "aging.json")
}

func (a *App) loadAgingState() agingState {
	state := agingState{
		Version: agingStateVersion,
		Apps:    map[string]agingAppState{},
		Games:   map[string]agingGameState{},
	}
	raw, err := os.ReadFile(a.agingStatePath())
	if err != nil || len(raw) == 0 {
		return state
	}
	if json.Unmarshal(raw, &state) != nil || state.Apps == nil {
		return agingState{
			Version: agingStateVersion,
			Apps:    map[string]agingAppState{},
			Games:   map[string]agingGameState{},
		}
	}
	if state.Games == nil {
		state.Games = map[string]agingGameState{}
	}
	state.Version = agingStateVersion
	return state
}

func (a *App) saveAgingState(state agingState) {
	if state.Apps == nil {
		state.Apps = map[string]agingAppState{}
	}
	if state.Games == nil {
		state.Games = map[string]agingGameState{}
	}
	_ = a.ensureDirs()
	data, err := json.MarshalIndent(state, "", "  ")
	if err == nil {
		_ = atomicWrite(a.agingStatePath(), append(data, '\n'), 0600)
	}
}

func (a *App) agingStorageCapacity(device string) agingStorageCapacity {
	if device == "" {
		device = a.firstZram()
		if device == "" {
			device = "zram0"
		}
	}
	stats := parseKeyValue(readTrim(a.sys(filepath.Join("block", device, "zms_stat"))), ":")
	nr := parseIntDefault(stats["nr_blocks"], 0)
	used := parseIntDefault(stats["used_blocks"], -1)
	if nr <= 0 || used < 0 {
		return agingStorageCapacity{}
	}
	free := nr - used
	if free < 0 {
		free = 0
	}
	return agingStorageCapacity{
		Known: true, NrBlocks: nr, UsedBlocks: used, FreeBlocks: free,
	}
}

func agingStoragePercent(blocks, total int64) int64 {
	if total <= 0 || blocks <= 0 {
		return 0
	}
	return blocks * 100 / total
}

func agingStorageLow(capacity agingStorageCapacity) bool {
	return capacity.Known && agingStoragePercent(capacity.FreeBlocks, capacity.NrBlocks) <= agingStorageTriggerPct
}

func agingStorageRecovered(capacity agingStorageCapacity) bool {
	return capacity.Known && agingStoragePercent(capacity.FreeBlocks, capacity.NrBlocks) >= agingStorageRecoverPct
}

func (a *App) agingStorageBlock(state *agingState, now int64, capacity agingStorageCapacity, reason string) {
	state.Storage.BlockedUntilUnix = now + int64(agingStorageCooldown/time.Second)
	state.Storage.RecoverySamples = 0
	state.Storage.Reason = reason
	if capacity.Known {
		state.Storage.LastFreeBlocks = capacity.FreeBlocks
		state.Storage.LastNrBlocks = capacity.NrBlocks
	}
}

// agingStorageGate applies a fixed global backing-store fuse. A low-capacity
// trigger uses a 2% threshold; recovery requires 5% free space for two full
// aging cycles, providing hysteresis without an app-by-app retry storm.
func (a *App) agingStorageGate(state *agingState, device string, now int64, logln func(string, ...any)) bool {
	capacity := a.agingStorageCapacity(device)
	if state.Storage.BlockedUntilUnix > now {
		return false
	}

	if state.Storage.BlockedUntilUnix > 0 {
		if !capacity.Known {
			a.agingStorageBlock(state, now, capacity, "capacity_unavailable")
			a.saveAgingState(*state)
			logln("aging: storage still blocked reason=capacity_unavailable retry_sec=%d", int64(agingStorageCooldown/time.Second))
			return false
		}
		state.Storage.LastFreeBlocks = capacity.FreeBlocks
		state.Storage.LastNrBlocks = capacity.NrBlocks
		if !agingStorageRecovered(capacity) {
			a.agingStorageBlock(state, now, capacity, "capacity_low")
			a.saveAgingState(*state)
			logln("aging: storage still full free_blocks=%d nr_blocks=%d retry_sec=%d",
				capacity.FreeBlocks, capacity.NrBlocks, int64(agingStorageCooldown/time.Second))
			return false
		}
		state.Storage.RecoverySamples++
		if state.Storage.RecoverySamples < agingStorageRecoverSamples {
			state.Storage.BlockedUntilUnix = now + int64(agingFullCycle/time.Second)
			state.Storage.Reason = "recovering"
			a.saveAgingState(*state)
			logln("aging: storage recovery sample=%d/%d free_blocks=%d nr_blocks=%d",
				state.Storage.RecoverySamples, agingStorageRecoverSamples,
				capacity.FreeBlocks, capacity.NrBlocks)
			return false
		}
		state.Storage = agingStorageState{}
		a.saveAgingState(*state)
		logln("aging: storage recovered; aging resumed free_blocks=%d nr_blocks=%d",
			capacity.FreeBlocks, capacity.NrBlocks)
		return true
	}

	if agingStorageLow(capacity) {
		a.agingStorageBlock(state, now, capacity, "capacity_low")
		a.saveAgingState(*state)
		logln("aging: storage blocked reason=capacity_low free_blocks=%d nr_blocks=%d retry_sec=%d",
			capacity.FreeBlocks, capacity.NrBlocks, int64(agingStorageCooldown/time.Second))
		return false
	}
	return true
}

func agingKey(app AppMemInfo) string {
	return fmt.Sprintf("%s|%d", app.Name, app.UID)
}

// agingIsolatedTask identifies Android isolated/WebView workers without
// treating their UID as the app identity. The UID range covers the device's
// 90xxx/99xxx isolated allocations; the command-line check also handles
// vendor-specific allocations such as Chromium's sandbox processes.
func agingIsolatedTask(uid int64, cmdline string) bool {
	lower := strings.ToLower(cmdline)
	if strings.Contains(lower, "sandboxed_process") ||
		strings.Contains(lower, "isolated") ||
		strings.Contains(lower, "org.chromium.content.app") {
		return true
	}
	return uid >= 90000 && uid < 100000
}

// agingEffectiveUIDFromDirs resolves a mixed memcg to its one regular
// application UID. The result is deliberately conservative: an absent,
// ambiguous, or clone-colliding task set is not eligible for automatic aging.
func (a *App) agingEffectiveUIDFromDirs(app AppMemInfo, dirs []string) (int64, bool) {
	if len(dirs) != 1 {
		return 0, false
	}
	raw := readTrim(filepath.Join(dirs[0], "cgroup.procs"))
	if raw == "" {
		raw = readTrim(filepath.Join(dirs[0], "tasks"))
	}
	regular := map[int64]bool{}
	for _, pid := range strings.Fields(raw) {
		if _, err := strconv.ParseInt(pid, 10, 64); err != nil {
			continue
		}
		status := readTrim(a.proc(filepath.Join(pid, "status")))
		uid := int64(-1)
		for _, line := range strings.Split(status, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "Uid:" {
				uid = parseIntDefault(fields[1], -1)
				break
			}
		}
		if uid < 10000 || agingIsolatedTask(uid,
			readTrim(a.proc(filepath.Join(pid, "cmdline")))) {
			continue
		}
		regular[uid] = true
	}
	if len(regular) != 1 {
		return 0, false
	}
	for uid := range regular {
		return uid, true
	}
	return 0, false
}

// agingMemcgIndex walks the driver tree once per full cycle. The previous
// implementation recursively walked it once per app and again for each
// process-protection check, multiplying I/O with the number of candidates.
func (a *App) agingMemcgIndex(root string) map[string][]string {
	index := map[string][]string{}
	if root == "" {
		return index
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == root {
			return nil
		}
		if name := readTrim(filepath.Join(path, "memory.name")); name != "" {
			index[name] = append(index[name], path)
		}
		return nil
	})
	return index
}

// foregroundUIDs prefers the legacy Oplus fg_info node and falls back to the
// Android top-app cpuset used by current crystalfrostwork kernels. Protecting
// every app UID in top-app is intentional: the group can contain the resumed
// app, input method, and other latency-sensitive helpers at the same time.
func (a *App) foregroundUIDs() (map[int64]bool, bool) {
	raw := readTrim(a.proc("fg_info/fg_uids"))
	if raw != "" {
		fields := strings.Fields(raw)
		if len(fields) > 0 {
			value := fields[len(fields)-1]
			if i := strings.LastIndexByte(value, ':'); i >= 0 {
				value = strings.TrimSpace(value[i+1:])
			}
			if uid, err := strconv.ParseInt(value, 10, 64); err == nil && uid >= 0 {
				return map[int64]bool{uid: true}, true
			}
		}
	}
	return a.topAppUIDs()
}

func (a *App) topAppUIDs() (map[int64]bool, bool) {
	paths := []string{
		filepath.Join(a.DevRoot, "cpuset/top-app/cgroup.procs"),
		a.sys("fs/cgroup/cpuset/top-app/cgroup.procs"),
		a.sys("fs/cgroup/top-app/cgroup.procs"),
	}
	for _, path := range paths {
		raw := readTrim(path)
		if raw == "" {
			continue
		}
		uids := map[int64]bool{}
		for _, pid := range strings.Fields(raw) {
			status := readTrim(a.proc(filepath.Join(pid, "status")))
			for _, line := range strings.Split(status, "\n") {
				fields := strings.Fields(line)
				if len(fields) < 2 || fields[0] != "Uid:" {
					continue
				}
				uid, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil && uid >= 10000 {
					uids[uid] = true
				}
				break
			}
		}
		if len(uids) > 0 {
			return uids, true
		}
	}
	return nil, false
}

func agingKeyUID(key string) (int64, bool) {
	i := strings.LastIndexByte(key, '|')
	if i < 0 || i+1 >= len(key) {
		return 0, false
	}
	uid, err := strconv.ParseInt(key[i+1:], 10, 64)
	return uid, err == nil
}

func cloneUIDSet(in map[int64]bool) map[int64]bool {
	out := make(map[int64]bool, len(in))
	for uid := range in {
		out[uid] = true
	}
	return out
}

// agingForegroundRefresh is intentionally narrower than agingCycle. It reads
// only the kernel top-app PID set and the corresponding /proc status files, so
// a short foreground visit can refresh the idle timer without repeating the
// app table, ZRAM, memcg, or force-swapout work. The state file is written only
// when a UID enters top-app or a game PID changes, not on every poll.
func (a *App) agingForegroundRefresh(logln func(string, ...any)) {
	a.agingGameObserve()
	foreground, ok := a.topAppUIDs()
	if !ok {
		if a.agingForegroundProbeOK {
			logln("aging: top-app foreground probe unavailable; hold aging state")
		}
		// Treat an unavailable/empty sample as a transition boundary. When
		// top-app becomes readable again, the next UID observation resets its
		// timer even if it is the same UID as before the gap.
		a.agingForegroundSeen = nil
		a.agingForegroundProbeOK = false
		return
	}

	entered := map[int64]bool{}
	for uid := range foreground {
		if a.agingForegroundSeen == nil || !a.agingForegroundSeen[uid] {
			entered[uid] = true
		}
	}
	a.agingForegroundSeen = cloneUIDSet(foreground)
	if !a.agingForegroundProbeOK {
		logln("aging: top-app foreground probe recovered")
	}
	a.agingForegroundProbeOK = true
	if len(entered) == 0 {
		return
	}

	state := a.loadAgingState()
	if state.Apps == nil {
		state.Apps = map[string]agingAppState{}
	}
	now := time.Now().Unix()
	changed := false
	for key, entry := range state.Apps {
		uid, valid := agingKeyUID(key)
		if !valid || !entered[uid] || entry.LastForegroundUnix == now {
			continue
		}
		entry.LastForegroundUnix = now
		state.Apps[key] = entry
		changed = true
	}
	if !changed {
		return
	}

	a.saveAgingState(state)
	uids := make([]int64, 0, len(entered))
	for uid := range entered {
		uids = append(uids, uid)
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	for _, uid := range uids {
		logln("aging: foreground enter uid=%d; idle timer reset", uid)
	}
}

func (a *App) memAvailableBytes() (int64, bool) {
	raw, err := os.ReadFile(a.proc("meminfo"))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil && kb >= 0 {
				return kb * 1024, true
			}
		}
	}
	return 0, false
}

// agingGamePID reads the vendor game foreground probe. Devices without this
// optional node keep the previous aging behavior; an unreadable existing node
// is treated conservatively and holds automatic writeback for that cycle.
func (a *App) agingGamePID() (pid int64, supported bool, valid bool) {
	path := a.proc("game_opt/game_pid")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return -1, false, true
		}
		return -1, true, false
	}
	for _, field := range strings.Fields(string(raw)) {
		key, value, found := strings.Cut(field, "=")
		if !found || key != "game_pid" {
			continue
		}
		pid, err = strconv.ParseInt(value, 10, 64)
		return pid, true, err == nil
	}
	return -1, true, false
}

// agingGameGate is a volatile cycle fuse. It deliberately does not persist
// state because game foreground status can change at any time; the next 60s
// cycle should resume aging immediately after game_pid returns to -1.
func (a *App) agingGameGate(logln func(string, ...any)) bool {
	pid, supported, valid := a.agingGamePID()
	if !supported {
		if a.agingLastGameState != "" {
			logln("aging: game probe unavailable; aging resumed")
			a.agingLastGameState = ""
		}
		return true
	}
	if !valid {
		if a.agingLastGameState != "unavailable" {
			logln("aging: game probe unreadable; hold aging state")
			a.agingLastGameState = "unavailable"
		}
		return false
	}
	if pid != -1 {
		if a.agingLastGameState != "active" {
			logln("aging: game foreground pid=%d; automatic writeback paused", pid)
			a.agingLastGameState = "active"
		}
		return false
	}
	if a.agingLastGameState != "" {
		logln("aging: game foreground ended; aging resumed")
		a.agingLastGameState = ""
	}
	return true
}

// agingGameObserve records a game identity only when the vendor PID changes.
// It runs alongside the existing five-second foreground sampler, so a game
// opened and backgrounded between 60-second aging cycles is still protected
// without adding a process scan or a shell command.
func (a *App) agingGameObserve() {
	pid, supported, valid := a.agingGamePID()
	if !supported || !valid || pid == -1 {
		a.agingLastObservedGamePID = -1
		return
	}
	if a.agingLastObservedGamePID == pid {
		return
	}
	pkg, uid, ok := a.agingGameProcessIdentity(pid)
	if !ok {
		return
	}
	a.agingLastObservedGamePID = pid
	state := a.loadAgingState()
	if state.Games == nil {
		state.Games = map[string]agingGameState{}
	}
	key := fmt.Sprintf("%s|%d", pkg, uid)
	state.Games[key] = agingGameState{
		Package:      pkg,
		UID:          uid,
		LastSeenUnix: time.Now().Unix(),
		LastPID:      pid,
	}
	a.saveAgingState(state)
}

// agingGameProtected matches by UID and tolerates the kernel's truncated
// memcg name. The package check prevents a reused UID from inheriting a stale
// game exemption when the current memcg belongs to a different package.
func agingGameProtected(state agingState, app AppMemInfo) bool {
	for _, game := range state.Games {
		if game.UID != app.UID || game.Package == "" || app.Name == "" {
			continue
		}
		if game.Package == app.Name ||
			(len(app.Name) == forceSwapMaxNameLen && strings.HasPrefix(game.Package, app.Name)) {
			return true
		}
	}
	return false
}

func agingProtectedName(name string) bool {
	lower := strings.ToLower(name)
	if lower == "android" {
		return true
	}
	for _, part := range []string{
		"systemui", "launcher", "inputmethod", "keyboard", "telecom",
		"dialer", "audioserver", "surfaceflinger",
	} {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return agingNameHasToken(lower, "phone")
}

func agingNameHasToken(name, token string) bool {
	for offset := 0; offset < len(name); {
		relative := strings.Index(name[offset:], token)
		if relative < 0 {
			return false
		}
		start := offset + relative
		end := start + len(token)
		before := start == 0 || !agingASCIIAlphaNumeric(name[start-1])
		after := end == len(name) || !agingASCIIAlphaNumeric(name[end])
		if before && after {
			return true
		}
		offset = start + 1
	}
	return false
}

func agingASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= '0' && value <= '9'
}

const (
	agingProtectedProcessNone      = 0
	agingProtectedProcessCritical  = 1
	agingProtectedProcessSensitive = 2
)

// agingAndroidProtection is a best-effort snapshot of Android's user-visible
// execution state. It is collected once per aging cycle, only if an app has a
// directly zygote-spawned process with oom_score_adj == 200.
type agingAndroidProtection struct {
	foregroundServicePackages map[string]bool
	mediaUIDs                 map[int64]bool
	servicesKnown             bool
	mediaKnown                bool
}

func newAgingAndroidProtection() *agingAndroidProtection {
	return &agingAndroidProtection{
		foregroundServicePackages: map[string]bool{},
		mediaUIDs:                 map[int64]bool{},
	}
}

func (p *agingAndroidProtection) reason(app AppMemInfo) string {
	if p == nil {
		return "android_state_unavailable"
	}
	if p.foregroundServicePackages[app.Name] {
		return "foreground_service"
	}
	if p.mediaUIDs[app.UID] {
		return "active_media"
	}
	// Unknown state is protected. A failed or restricted dumpsys must never
	// turn a perceptible Android process into a force-swap candidate.
	if !p.servicesKnown || !p.mediaKnown {
		return "android_state_unavailable"
	}
	return ""
}

var agingServiceRecordPackageRE = regexp.MustCompile(`\bu[0-9]+\s+([A-Za-z0-9_.]+)/[^\s}]+`)

func parseAgingForegroundServicePackages(raw string) map[string]bool {
	packages := map[string]bool{}
	current := ""
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(line, "ServiceRecord{") {
			current = ""
			if match := agingServiceRecordPackageRE.FindStringSubmatch(line); len(match) == 2 {
				current = match[1]
			}
			continue
		}
		if current != "" && strings.Contains(line, "isForeground=true") {
			packages[current] = true
		}
	}
	return packages
}

func parseAgingMediaUIDs(raw string) map[int64]bool {
	uids := map[int64]bool{}
	inAudioPlayback := false
	currentUID := int64(-1)
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "Audio playback (lastly played comes first)" {
			inAudioPlayback = true
			continue
		}
		if inAudioPlayback && strings.HasPrefix(trimmed, "Media session config:") {
			inAudioPlayback = false
		}
		if inAudioPlayback && strings.HasPrefix(trimmed, "uid=") {
			fields := strings.Fields(trimmed)
			if len(fields) > 0 {
				value := strings.TrimPrefix(fields[0], "uid=")
				if uid, err := strconv.ParseInt(value, 10, 64); err == nil && uid >= 0 {
					uids[uid] = true
				}
			}
		}
		if strings.HasPrefix(trimmed, "ownerUid=") {
			value := strings.TrimPrefix(trimmed, "ownerUid=")
			fields := strings.Fields(value)
			if len(fields) == 0 {
				currentUID = -1
			} else {
				currentUID = parseIntDefault(strings.TrimSuffix(fields[0], ","), -1)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "active=") {
			if currentUID >= 0 && trimmed == "active=true" {
				uids[currentUID] = true
			}
			currentUID = -1
		}
	}
	return uids
}

func (a *App) agingAndroidProtectionSnapshot() *agingAndroidProtection {
	snapshot := newAgingAndroidProtection()
	services, err := a.agingCommandOut(1200*time.Millisecond, "dumpsys", "activity", "services")
	if err == nil && strings.TrimSpace(services) != "" {
		snapshot.servicesKnown = true
		snapshot.foregroundServicePackages = parseAgingForegroundServicePackages(services)
	}
	media, err := a.agingCommandOut(1200*time.Millisecond, "dumpsys", "media_session")
	if err == nil && strings.TrimSpace(media) != "" {
		snapshot.mediaKnown = true
		snapshot.mediaUIDs = parseAgingMediaUIDs(media)
	}
	return snapshot
}

func (a *App) agingCommandOut(timeout time.Duration, name string, args ...string) (string, error) {
	if a.commandOutFn != nil {
		return a.commandOutFn(timeout, name, args...)
	}
	return a.runCommandOut(timeout, name, args...)
}

// appProtectedProcessLevel identifies low-oom-score processes created directly
// by a system zygote. Children created by an app can retain a stale inherited
// oom_score_adj; those children are deliberately ignored. A score below 200 is
// unconditionally critical. Exactly 200 is Android's perceptible boundary and
// is sensitive only when the package still has a foreground service or active
// media playback (the caller supplies that Android snapshot).
func (a *App) appProtectedProcessLevel(app AppMemInfo) int {
	root := a.memcgRoot()
	if root == "" {
		return agingProtectedProcessNone
	}
	dir, found := a.findAppMemcg(root, app.Name, app.UID)
	if !found {
		return agingProtectedProcessNone
	}
	return a.appProtectedProcessLevelInDir(app, dir)
}

func (a *App) appProtectedProcessLevelInDirs(app AppMemInfo, dirs []string) int {
	if len(dirs) == 0 {
		return agingProtectedProcessNone
	}
	var dir string
	if len(dirs) == 1 {
		dir = dirs[0]
	} else {
		drifted := ""
		for _, candidate := range dirs {
			cur, err := strconv.ParseInt(readTrim(filepath.Join(candidate, "memory.app_uid")), 10, 64)
			if err == nil && cur == app.UID {
				dir = candidate
				break
			}
			if app.UID > 0 && cur == -2 && a.memcgTaskUIDs(candidate)[app.UID] {
				dir = candidate
				break
			}
			if drifted == "" && (err != nil || cur < 0) {
				drifted = candidate
			}
		}
		if dir == "" {
			dir = drifted
		}
	}
	if dir == "" {
		return agingProtectedProcessNone
	}
	return a.appProtectedProcessLevelInDir(app, dir)
}

func (a *App) appProtectedProcessLevelInDir(app AppMemInfo, dir string) int {
	if dir == "" {
		return agingProtectedProcessNone
	}
	raw := readTrim(filepath.Join(dir, "cgroup.procs"))
	if raw == "" {
		raw = readTrim(filepath.Join(dir, "tasks"))
	}
	level := agingProtectedProcessNone
	for _, pid := range strings.Fields(raw) {
		if _, err := strconv.ParseInt(pid, 10, 64); err != nil {
			continue
		}
		adj := parseIntDefault(readTrim(a.proc(filepath.Join(pid, "oom_score_adj"))), 1000)
		if !a.processParentIsSystemZygote(pid, app.UID) {
			continue
		}
		if adj < 200 {
			return agingProtectedProcessCritical
		}
		if adj == 200 {
			level = agingProtectedProcessSensitive
		}
	}
	return level
}

// appHasProtectedProcess is retained for callers and tests that only need the
// conservative boolean. The aging cycle uses the cached Android snapshot to
// avoid invoking dumpsys once per app.
func (a *App) appHasProtectedProcess(app AppMemInfo) bool {
	level := a.appProtectedProcessLevel(app)
	if level == agingProtectedProcessCritical {
		return true
	}
	if level != agingProtectedProcessSensitive {
		return false
	}
	return a.agingAndroidProtectionSnapshot().reason(app) != ""
}

func (a *App) processParentIsSystemZygote(pid string, appUID int64) bool {
	status := readTrim(a.proc(filepath.Join(pid, "status")))
	ppid := int64(-1)
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "PPid:" {
			ppid = parseIntDefault(fields[1], -1)
			break
		}
	}
	if ppid <= 0 {
		return false
	}
	parentStatus := readTrim(a.proc(filepath.Join(strconv.FormatInt(ppid, 10), "status")))
	parentUID := int64(-1)
	for _, line := range strings.Split(parentStatus, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "Uid:" {
			parentUID = parseIntDefault(fields[1], -1)
			break
		}
	}
	if parentUID < 0 || parentUID == appUID || parentUID >= 10000 {
		return false
	}
	cmdline := strings.ToLower(readTrim(a.proc(filepath.Join(strconv.FormatInt(ppid, 10), "cmdline"))))
	return strings.Contains(cmdline, "zygote")
}

func (a *App) agingRuntimeReady(p *Profile) bool {
	if p == nil {
		return false
	}
	z := a.zramStatus(p.Zram.Device)
	if !z.Found {
		return false
	}
	crystal := a.crystalStatus(p.Zram.Device)
	if crystal.Enable == 0 || crystal.CoreEnable == 0 || crystal.SwapdPause != 0 {
		return false
	}
	// A zero quota remaining value is meaningful when the live stat node is
	// present, but old kernels may expose no quota fields at all. The profile
	// and kernel capability gate handle old kernels; here only reject an
	// explicitly exhausted quota.
	if crystal.QuotaDay > 0 && crystal.QuotaRemaining <= 0 {
		return false
	}
	return true
}

type agingCycleStats struct {
	apps, unresolved, mixedResolved, foreground, protectedName, gameProtected int
	critical, sensitive, belowMin, cooldown, rearm                            int
	candidates, attempted                                                     int
}

func (a *App) agingCycle(p *Profile, logln func(string, ...any)) {
	if p == nil || !p.Aging.Enabled {
		return
	}
	now := time.Now().Unix()
	state := a.loadAgingState()
	if state.Apps == nil {
		state.Apps = map[string]agingAppState{}
	}
	if !a.agingGameGate(logln) {
		return
	}
	if !a.agingStorageGate(&state, p.Zram.Device, now, logln) {
		return
	}
	fgUIDs, ok := a.foregroundUIDs()
	if !ok {
		if a.agingLastCycleState != "foreground_unavailable" {
			logln("aging: cycle paused reason=foreground_unavailable")
			a.agingLastCycleState = "foreground_unavailable"
		}
		return
	}
	result := a.readAppsResult()
	if !result.Supported || len(result.Apps) == 0 {
		return
	}
	if !a.agingRuntimeReady(p) {
		if a.agingLastCycleState != "runtime_unavailable" {
			logln("aging: cycle paused reason=runtime_unavailable")
			a.agingLastCycleState = "runtime_unavailable"
		}
		return
	}
	a.agingLastCycleState = ""
	seen := map[string]bool{}
	stats := agingCycleStats{}
	candidates := make([]agingCandidate, 0, len(result.Apps))
	var androidProtection *agingAndroidProtection
	memcgIndex := a.agingMemcgIndex(a.memcgRoot())
	for _, rawApp := range result.Apps {
		if rawApp.Name == "" {
			continue
		}
		stats.apps++
		app := rawApp
		dirs := memcgIndex[rawApp.Name]
		var effectiveUID int64
		var resolved bool
		if app.UID == -2 {
			effectiveUID, resolved = a.agingEffectiveUIDFromDirs(app, dirs)
			if resolved {
				stats.mixedResolved++
			}
		} else if app.UID > 0 {
			effectiveUID, resolved = app.UID, true
		}
		if !resolved {
			stats.unresolved++
			seen[agingKey(rawApp)] = true
			continue
		}
		app.UID = effectiveUID
		key := agingKey(app)
		seen[key] = true
		rawKey := agingKey(rawApp)
		entry := state.Apps[key]
		if rawKey != key {
			// Migrate state written by older versions while the kernel exposed
			// this memcg only as uid=-2. The normalized key remains stable when
			// an isolated process exits.
			if _, exists := state.Apps[key]; !exists {
				if old, exists := state.Apps[rawKey]; exists {
					entry = old
				}
			}
			delete(state.Apps, rawKey)
		}
		if entry.LastForegroundUnix == 0 {
			entry.LastForegroundUnix = now
		}
		if fgUIDs[effectiveUID] {
			entry.LastForegroundUnix = now
		}
		state.Apps[key] = entry
		if fgUIDs[app.UID] {
			stats.foreground++
			continue
		}
		if agingProtectedName(app.Name) {
			stats.protectedName++
			continue
		}
		if agingGameProtected(state, app) {
			stats.gameProtected++
			continue
		}
		processLevel := a.appProtectedProcessLevelInDirs(app, dirs)
		if processLevel == agingProtectedProcessCritical {
			stats.critical++
			continue
		}
		if processLevel == agingProtectedProcessSensitive {
			stats.sensitive++
			if androidProtection == nil {
				androidProtection = a.agingAndroidProtectionSnapshot()
			}
			if androidProtection.reason(app) != "" {
				continue
			}
		}
		if app.ZramComprBytes < p.Aging.MinZramMB*1024*1024 {
			stats.belowMin++
			continue
		}
		if now < entry.CooldownUntilUnix || now-entry.LastForegroundUnix < p.Aging.IdleAfterSec {
			stats.cooldown++
			continue
		}
		if agingNeedsRearm(entry, app, p.Aging.MinZramMB) {
			stats.rearm++
			continue
		}
		candidates = append(candidates, agingCandidate{
			App: app, LastAttempt: entry.LastAttemptUnix,
			LastFG: entry.LastForegroundUnix, ZramBytes: app.ZramComprBytes,
			HasWrittenBack:    app.EswapOrigBytes > 0,
			WritebackRatioPPM: agingWritebackRatioPPM(app),
		})
	}
	for key := range state.Apps {
		if !seen[key] && len(state.Apps) > agingStateMaxApps {
			delete(state.Apps, key)
		}
	}
	sortAgingCandidates(candidates)
	stats.candidates = len(candidates)
	emitSummary := func() {
		logln("aging: cycle apps=%d unresolved=%d mixed=%d foreground=%d protected=%d game_protected=%d critical=%d sensitive=%d below_min=%d cooldown=%d rearm=%d candidates=%d attempted=%d",
			stats.apps, stats.unresolved, stats.mixedResolved, stats.foreground,
			stats.protectedName, stats.gameProtected, stats.critical, stats.sensitive, stats.belowMin,
			stats.cooldown, stats.rearm, stats.candidates, stats.attempted)
	}
	avail, pressureOK := a.memAvailableBytes()
	if !pressureOK || avail >= p.Aging.PressureAvailableBelowMB*1024*1024 {
		a.saveAgingState(state)
		emitSummary()
		return
	}
	limit := int(p.Aging.MaxAppsPerCycle)
	if limit > len(candidates) {
		limit = len(candidates)
	}
	for i := 0; i < limit; i++ {
		candidate := candidates[i]
		if !a.agingGameGate(logln) {
			break
		}
		// Recheck immediately before locking and again after taking the lock.
		current, valid := a.foregroundUIDs()
		if !valid || current[candidate.App.UID] {
			logln("aging: foreground changed; stop cycle")
			break
		}
		unlock, err := a.lockApply()
		if err != nil {
			logln("aging: apply lock: %v", err)
			break
		}
		current, valid = a.foregroundUIDs()
		if !valid || current[candidate.App.UID] {
			unlock()
			logln("aging: foreground changed while locking; stop cycle")
			break
		}
		if !a.agingGameGate(logln) {
			unlock()
			break
		}
		stats.attempted++
		entry := state.Apps[agingKey(candidate.App)]
		agingRecordAttempt(&entry, candidate.App, now)
		force, forceErr := a.forceSwapLocked(candidate.App.Name, candidate.App.UID, "swapout")
		unlock()
		if forceErr != nil {
			entry.Failures++
			entry.LastResult = forceErr.Error()
			entry.LastRet = 0
			entry.LastBytes = 0
			backoff := agingFailureBackoff(entry.Failures)
			entry.CooldownUntilUnix = now + int64(backoff/time.Second)
			state.Apps[agingKey(candidate.App)] = entry
			logln("aging: %s uid=%d failed=%v bytes=0 failures=%d cooldown_sec=%d",
				candidate.App.Name, candidate.App.UID, forceErr, entry.Failures, int64(backoff/time.Second))
			continue
		}
		entry.LastResult = force.Result
		entry.LastRet = force.Ret
		entry.LastBytes = force.Bytes
		if force.Result == "storage_full" {
			// Backing-store exhaustion is global. Do not grow this app's
			// failure streak; establish the persistent global fuse instead.
			capacity := a.agingStorageCapacity(p.Zram.Device)
			a.agingStorageBlock(&state, now, capacity, "enospc")
			state.Apps[agingKey(candidate.App)] = entry
			a.saveAgingState(state)
			logln("aging: storage blocked reason=enospc free_blocks=%d nr_blocks=%d retry_sec=%d",
				capacity.FreeBlocks, capacity.NrBlocks, int64(agingStorageCooldown/time.Second))
			break
		} else if force.Result == "ok" && force.Bytes > 0 {
			entry.Failures = 0
			entry.CooldownUntilUnix = now + p.Aging.CooldownSec
		} else if agingHardFailure(force.Result) {
			entry.Failures++
			backoff := agingFailureBackoff(entry.Failures)
			entry.CooldownUntilUnix = now + int64(backoff/time.Second)
		} else {
			entry.CooldownUntilUnix = now + int64(agingRetryBackoff/time.Second)
		}
		state.Apps[agingKey(candidate.App)] = entry
		logln("aging: %s uid=%d result=%s ret=%d completed=%t bytes=%d failures=%d cooldown_sec=%d",
			candidate.App.Name, candidate.App.UID, force.Result, force.Ret, force.Completed,
			force.Bytes, entry.Failures, entry.CooldownUntilUnix-now)
		if force.Result == "storage_full" {
			break
		}
	}
	a.saveAgingState(state)
	emitSummary()
}

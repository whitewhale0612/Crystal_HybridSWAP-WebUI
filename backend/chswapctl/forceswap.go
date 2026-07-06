// forceswap.go — trigger the kernel's per-app force swapout/swapin.
//
// POST /v1/apps/force-swap maps an app row from GET /v1/apps back to its memcg
// directory and writes the per-memcg trigger node (memory.force_swapout /
// memory.force_swapin). The kernel queues the actual work asynchronously and
// the write always succeeds, so the handler then polls the memcg's
// memory.swap_stat forceSwap*LastRet field — reset to 0 by the trigger write,
// set to processed pages (>0) or a negative errno on completion — until the
// wait budget runs out.

package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	forceSwapDefaultWaitMS = 3000
	forceSwapPollMS        = 100
	// The kernel truncates memcg entry names to CHS_MEMCG_NAME_MAX-1 bytes;
	// longer names can never match a row returned by GET /v1/apps.
	forceSwapMaxNameLen = 31
)

func (a *App) forceSwapAPI(body []byte) bool {
	var req ForceSwapRequest
	if err := decodeJSON(body, &req); err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_JSON", err.Error(), nil), nil)
		return false
	}
	if req.Op != "swapout" && req.Op != "swapin" {
		writeEnvelope(false, nil, apiError("E_INVALID_REQUEST",
			`op must be "swapout" or "swapin"`, nil), nil)
		return false
	}
	if req.Name == "" || len(req.Name) > forceSwapMaxNameLen ||
		strings.ContainsAny(req.Name, "/\x00") {
		writeEnvelope(false, nil, apiError("E_INVALID_REQUEST",
			"name must be a memcg entry name as returned by GET /v1/apps", nil), nil)
		return false
	}

	node := "memory.force_" + req.Op
	memcg := a.memcgRoot()
	if memcg == "" || !a.exists(filepath.Join(memcg, node)) {
		return respondUnsupported([]string{filepath.Join(memcg, node)})
	}

	// Serialize against apply/daemon reconcile: a force swap racing a ZRAM
	// takeover (swapoff → reset → rebuild) would target a dying device.
	unlock, err := a.lockApply()
	if err != nil {
		writeEnvelope(false, nil, apiError(errorCodeFromErr(err), err.Error(), nil), nil)
		return false
	}
	defer unlock()

	dir, found := a.findAppMemcg(memcg, req.Name, req.UID)
	if !found {
		writeEnvelope(false, nil, apiError("E_APP_NOT_FOUND",
			fmt.Sprintf("no memcg named %q; the app may have exited", req.Name),
			map[string]any{"name": req.Name, "uid": req.UID}), nil)
		return false
	}

	target := filepath.Join(dir, node)
	started := time.Now()
	if err := writeKernelNode(target, []byte("1")); err != nil {
		writeEnvelope(false, nil, apiError(kernelWriteErrorCode(err), err.Error(),
			map[string]any{"target": target}), nil)
		return false
	}

	retKey := "forceSwapoutLastRet"
	if req.Op == "swapin" {
		retKey = "forceSwapinLastRet"
	}
	waitMS := a.ForceWaitMS
	if waitMS <= 0 {
		waitMS = forceSwapDefaultWaitMS
	}
	ret, completed := a.pollForceSwapResult(filepath.Join(dir, "memory.swap_stat"),
		retKey, waitMS)

	result := ForceSwapResult{
		Op:         req.Op,
		Name:       req.Name,
		UID:        req.UID,
		Target:     target,
		Completed:  completed,
		Result:     forceSwapResultKind(ret, completed),
		Ret:        ret,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if ret > 0 {
		result.Pages = ret
		result.Bytes = ret * pageBytes
	}
	return a.respondOK(result)
}

// findAppMemcg locates the memcg directory whose driver entry name — read back
// via memory.name, the same value total_info_per_app rows carry — equals name.
// Directories without a driver entry read as empty and never match. The uid
// disambiguates between several same-named directories (dual-app clones share
// the package name): memory.app_uid rescans live tasks on every read and
// drifts to -1 once the app's processes exit, so a sole name match is accepted
// regardless of uid, and among several matches a drifted candidate is
// acceptable — but a directory that affirmatively reports a different real uid
// is another app's clone and must never be triggered in its place.
func (a *App) findAppMemcg(root string, name string, uid int64) (string, bool) {
	var byName []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == root {
			return nil
		}
		if readTrim(filepath.Join(path, "memory.name")) == name {
			byName = append(byName, path)
		}
		return nil
	})
	if len(byName) == 0 {
		return "", false
	}
	if len(byName) == 1 {
		return byName[0], true
	}
	drifted := ""
	for _, dir := range byName {
		cur, err := strconv.ParseInt(readTrim(filepath.Join(dir, "memory.app_uid")), 10, 64)
		if err == nil && cur == uid {
			return dir, true
		}
		if drifted == "" && (err != nil || cur < 0) {
			drifted = dir
		}
	}
	if drifted != "" {
		return drifted, true
	}
	return "", false
}

// pollForceSwapResult polls one forceSwap*LastRet row of memory.swap_stat until
// it turns non-zero (the trigger write reset it to 0; the worker always stores
// pages processed or a negative errno) or the budget elapses. Attribution is
// best-effort for swapout: the kernel's auto policy (active once avail_buffers
// is configured) queues per-memcg swapouts through the same worker and stores
// into the same last_ret field, so a concurrent auto result can be read here
// in place of the trigger's own — the user's swapout still runs, only the
// reported numbers may belong to the neighbouring auto pass.
func (a *App) pollForceSwapResult(statPath string, key string, budgetMS int64) (int64, bool) {
	deadline := time.Now().Add(time.Duration(budgetMS) * time.Millisecond)
	for {
		if ret, ok := readSwapStatInt(statPath, key); ok && ret != 0 {
			return ret, true
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		time.Sleep(forceSwapPollMS * time.Millisecond)
	}
}

// readSwapStatInt reads one "key:   value[ KB]" row of a memory.swap_stat file.
func readSwapStatInt(path string, key string) (int64, bool) {
	raw := readTrim(path)
	if raw == "" {
		return 0, false
	}
	val, ok := parseKeyValue(raw, ":")[key]
	if !ok {
		return 0, false
	}
	fields := strings.Fields(val)
	if len(fields) == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// forceSwapResultKind classifies the raw kernel result for the UI. The errno
// values mirror what the driver's workers store into *_last_ret.
func forceSwapResultKind(ret int64, completed bool) string {
	switch {
	case !completed:
		return "pending"
	case ret > 0:
		return "ok"
	case ret == -int64(syscall.ENODATA):
		return "nodata"
	case ret == -int64(syscall.EBUSY):
		return "busy"
	case ret == -int64(syscall.EDQUOT):
		return "quota"
	case ret == -int64(syscall.ENODEV):
		return "nodev"
	default:
		return "error"
	}
}

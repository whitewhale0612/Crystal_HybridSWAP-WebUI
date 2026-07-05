package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (a *App) applyAPI(body []byte) bool {
	var req ApplyRequest
	if err := decodeJSON(body, &req); err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_JSON", err.Error(), nil), nil)
		return false
	}
	if err := validateApplyRequestShape(body); err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_PROFILE", err.Error(), nil), nil)
		return false
	}
	if !oneOf(req.Mode, "dry-run", "runtime", "full-control", "reconcile") {
		writeEnvelope(false, nil, apiError("E_INVALID_PROFILE", "apply mode is invalid", nil), nil)
		return false
	}
	warnings := []Warning{}
	profile := req.Profile
	if profile == nil {
		var profileWarnings []Warning
		profile, profileWarnings = a.loadProfile()
		warnings = append(warnings, profileWarnings...)
	}
	if err := a.validateProfile(profile); err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_PROFILE", err.Error(), nil), warnings)
		return false
	}
	if req.Mode != "dry-run" {
		if missing := a.profileRequiredNodesMissing(profile); len(missing) > 0 {
			return respondUnsupportedWithWarnings(missing, warnings)
		}
	}
	result, err := a.apply(req.Mode, profile)
	if err != nil {
		writeEnvelope(false, nil, apiError(errorCodeFromErr(err), err.Error(), nil), warnings)
		return false
	}
	return a.respondOK(result, warnings...)
}

func (a *App) apply(mode string, profile *Profile) (ApplyResult, error) {
	unlock, err := a.lockApply()
	if err != nil {
		return ApplyResult{}, err
	}
	defer unlock()

	result := ApplyResult{Steps: []ApplyStep{}}
	if profile.Mode == "diagnostic" {
		result.Steps = append(result.Steps, ApplyStep{
			Name:    "profile.mode",
			Status:  "skipped",
			Message: "diagnostic profile is read-only",
		})
		return result, nil
	}
	zram := a.zramStatus(profile.Zram.Device)
	zram.ActiveSwap = a.swapStatus(profile.Zram.Device, profile.Swap.Enabled).Active
	if mode == "full-control" && !zram.Found {
		result.Degraded = true
		result.DegradedReasons = append(result.DegradedReasons,
			"selected zram device was not found")
		result.Steps = append(result.Steps, ApplyStep{
			Name:      "zram.device",
			Status:    "failed",
			Target:    a.sys("block/" + profile.Zram.Device),
			Message:   "selected zram device was not found",
			ErrorCode: "E_DEVICE_NOT_FOUND",
		})
	} else if mode == "full-control" {
		result = a.applyFullControlMode(result, profile, zram)
	} else {
		result = a.applyRuntime(result, profile, mode == "dry-run")
	}

	for _, step := range result.Steps {
		if step.Status == "changed" {
			result.Changed = true
			break
		}
	}
	return result, nil
}

func (a *App) applyFullControlMode(result ApplyResult, p *Profile, z ZramStatus) ApplyResult {
	want := a.effectiveDisksizeBytes(p)
	if z.Initstate != 0 && z.DisksizeBytes == want {
		result = a.applyRuntime(result, p, false)
		result = a.applyBackingTriggerIfNeeded(result, p, false)
		if !hasFailedStep(result, "zram.hybridswap_loop_device") && p.Swap.Enabled && !z.ActiveSwap {
			result = a.applySwapActivation(result, p, false)
		}
		return result
	}
	return a.rebuildZram(result, p, z)
}

func (a *App) rebuildZram(result ApplyResult, p *Profile, z ZramStatus) ApplyResult {
	dev := p.Zram.Device
	devPath := a.devBlockZram(dev)
	zdir := a.sys("block/" + dev)
	z.ActiveSwap = a.swapStatus(dev, p.Swap.Enabled).Active
	if z.ActiveSwap {
		if err := a.runCommand(8000*time.Millisecond, "swapoff", devPath); err != nil {
			result.Steps = append(result.Steps, ApplyStep{
				Name:      "swap.swapoff",
				Status:    "failed",
				Target:    devPath,
				Message:   err.Error(),
				ErrorCode: swapCommandErrorCode("swapoff", err),
			})
			result.Degraded = true
			result.DegradedReasons = append(result.DegradedReasons, "swapoff failed")
			return result
		}
		result.Steps = append(result.Steps, ApplyStep{
			Name:    "swap.swapoff",
			Status:  "changed",
			Target:  devPath,
			Message: "zram swap deactivated for rebuild",
		})
	}
	if z.Initstate != 0 {
		reset := filepath.Join(zdir, "reset")
		if err := writeKernelNode(reset, []byte("1\n")); err != nil {
			result.Steps = append(result.Steps, ApplyStep{
				Name:      "zram.reset",
				Status:    "failed",
				Target:    reset,
				Message:   err.Error(),
				ErrorCode: kernelWriteErrorCode(err),
			})
			result.Degraded = true
			result.DegradedReasons = append(result.DegradedReasons, "zram reset failed")
			return result
		}
		result.Steps = append(result.Steps, ApplyStep{
			Name:    "zram.reset",
			Status:  "changed",
			Target:  reset,
			Message: "zram reset for full-control rebuild",
		})
	}
	return a.applyFullControl(result, p, false)
}

func (a *App) applyBackingTriggerIfNeeded(result ApplyResult, p *Profile, dryRun bool) ApplyResult {
	if p.Backing.Mode == "loop" {
		if !dryRun {
			a.ensureLoopBacking(p, func(string, ...any) {})
		}
		return a.applyLoopBackingTriggerIfNeeded(result, p, dryRun)
	}
	if p.Backing.Mode != "block" {
		return result
	}
	return a.applyBlockBackingTriggerIfNeeded(result, p, dryRun)
}

func (a *App) applyBlockBackingTriggerIfNeeded(result ApplyResult, p *Profile, dryRun bool) ApplyResult {
	node := filepath.Join(a.sys("block/"+p.Zram.Device), "hybridswap_loop_device")
	if strings.TrimSpace(readTrim(node)) == backingBlockDevice &&
		a.zmsStatus(p.Zram.Device).Enabled == 1 {
		return result
	}
	return a.writeStepForce(result, p, "zram.hybridswap_loop_device",
		node, backingBlockDevice, dryRun)
}

func (a *App) applyLoopBackingTriggerIfNeeded(result ApplyResult, p *Profile, dryRun bool) ApplyResult {
	loopDev := a.resolveLoopPath(filepath.Join(a.DataDir, "writeback.img"))
	if loopDev == "" && !dryRun {
		result.Degraded = true
		result.DegradedReasons = append(result.DegradedReasons, "loop backing is not attached")
		result.Steps = append(result.Steps, ApplyStep{
			Name:      "zram.hybridswap_loop_device",
			Status:    "failed",
			Target:    filepath.Join(a.sys("block/"+p.Zram.Device), "hybridswap_loop_device"),
			Message:   "loop backing is not attached",
			ErrorCode: "E_BACKING_DEVICE_MISSING",
		})
		return result
	}
	if loopDev == "" {
		loopDev = "<loop-device>"
	}
	if strings.TrimSpace(readTrim(filepath.Join(a.sys("block/"+p.Zram.Device), "hybridswap_loop_device"))) == loopDev &&
		a.zmsStatus(p.Zram.Device).Enabled == 1 {
		return result
	}
	return a.writeStepForce(result, p, "zram.hybridswap_loop_device",
		filepath.Join(a.sys("block/"+p.Zram.Device), "hybridswap_loop_device"), loopDev, dryRun)
}

// effectiveDisksizeBytes is the disksize to program: the user's value if set,
// otherwise the strong-takeover default of total RAM rounded up to whole GiB.
func (a *App) effectiveDisksizeBytes(p *Profile) int64 {
	if p.Zram.Disksize != nil {
		if b := parseSize(*p.Zram.Disksize); b > 0 {
			return b
		}
	}
	mt := a.memTotalBytes()
	if mt <= 0 {
		return 12 * bytesGiB
	}
	return ((mt + bytesGiB - 1) / bytesGiB) * bytesGiB
}

func (a *App) applyFullControl(result ApplyResult, p *Profile, dryRun bool) ApplyResult {
	dev := p.Zram.Device
	zdir := a.sys("block/" + dev)
	// comp_algorithm is init-only; only program it when the user picked one.
	if p.Zram.CompAlgorithm != nil {
		result = a.writeStep(result, p, "zram.comp_algorithm",
			filepath.Join(zdir, "comp_algorithm"), *p.Zram.CompAlgorithm, dryRun)
		if hasFailedStep(result, "zram.comp_algorithm") {
			return result
		}
	}
	// disksize: strong takeover always sizes the device (default = RAM rounded up).
	result = a.writeStep(result, p, "zram.disksize",
		filepath.Join(zdir, "disksize"),
		strconv.FormatInt(a.effectiveDisksizeBytes(p), 10), dryRun)
	if hasFailedStep(result, "zram.disksize") {
		return result
	}
	// Crystal's backing bind is a trigger as well as a value: rewriting the same
	// hybridswap_loop_device after disksize brings ZMS online after reset.
	if p.Backing.Mode == "block" {
		result = a.writeStepForce(result, p, "zram.hybridswap_loop_device",
			filepath.Join(zdir, "hybridswap_loop_device"), backingBlockDevice, dryRun)
		if hasFailedStep(result, "zram.hybridswap_loop_device") {
			return result
		}
	} else if p.Backing.Mode == "loop" {
		if !dryRun {
			a.ensureLoopBacking(p, func(string, ...any) {})
		}
		result = a.applyLoopBackingTriggerIfNeeded(result, p, dryRun)
		if hasFailedStep(result, "zram.hybridswap_loop_device") {
			return result
		}
	}
	result = a.applyRuntime(result, p, dryRun)
	result = a.applySwapActivation(result, p, dryRun)
	return result
}

func (a *App) applySwapActivation(result ApplyResult, p *Profile, dryRun bool) ApplyResult {
	if !p.Swap.Enabled || dryRun {
		return result
	}
	devPath := a.devBlockZram(p.Zram.Device)
	if err := a.runCommand(8000*time.Millisecond, "mkswap", devPath); err != nil {
		result.Steps = append(result.Steps, ApplyStep{
			Name:      "swap.mkswap",
			Status:    "failed",
			Target:    devPath,
			Message:   err.Error(),
			ErrorCode: swapCommandErrorCode("mkswap", err),
		})
		result.Degraded = true
		result.DegradedReasons = append(result.DegradedReasons, "mkswap failed")
		return result
	}
	if err := a.runCommand(8000*time.Millisecond, "swapon",
		"-p", strconv.FormatInt(p.Swap.Priority, 10), devPath); err != nil {
		result.Steps = append(result.Steps, ApplyStep{
			Name:      "swap.swapon",
			Status:    "failed",
			Target:    devPath,
			Message:   err.Error(),
			ErrorCode: swapCommandErrorCode("swapon", err),
		})
		result.Degraded = true
		result.DegradedReasons = append(result.DegradedReasons, "swapon failed")
		return result
	}
	result.Steps = append(result.Steps, ApplyStep{
		Name:    "swap.swapon",
		Status:  "changed",
		Target:  devPath,
		Message: "zram swap activated",
	})
	return result
}

func hasFailedStep(result ApplyResult, name string) bool {
	for i := len(result.Steps) - 1; i >= 0; i-- {
		step := result.Steps[i]
		if step.Name != name {
			continue
		}
		return step.Status == "failed" || step.Status == "denied"
	}
	return false
}

func (a *App) applyRuntime(result ApplyResult, p *Profile, dryRun bool) ApplyResult {
	dev := p.Zram.Device
	zdir := a.sys("block/" + dev)
	// Crystal runtime knobs — only reconcile fields the user explicitly set.
	writes := []struct {
		name      string
		path      string
		val       *int64
		normalize func(string) string
	}{
		{"crystal.enable", filepath.Join(zdir, "hybridswap_enable"), p.Crystal.Enable, normalizeCrystalEnabled("hybridswap")},
		{"crystal.core_enable", filepath.Join(zdir, "hybridswap_core_enable"), p.Crystal.CoreEnable, normalizeCrystalEnabled("reclaim_in")},
		{"crystal.swapd_pause", filepath.Join(zdir, "hybridswap_swapd_pause"), p.Crystal.SwapdPause, normalizeIntText},
		{"crystal.loglevel", filepath.Join(zdir, "hybridswap_loglevel"), p.Crystal.Loglevel, normalizeLastIntText},
		{"crystal.dev_life", filepath.Join(zdir, "hybridswap_dev_life"), p.Crystal.DevLife,
			a.normalizeDevLifeLevel(filepath.Join(zdir, "hybridswap_crystal_stat"))},
		{"crystal.quota_day", filepath.Join(zdir, "hybridswap_quota_day"), p.Crystal.QuotaDay, normalizeIntText},
		{"crystal.zram_increase", filepath.Join(zdir, "hybridswap_zram_increase"), p.Crystal.ZramIncrease, normalizeIntText},
	}
	for _, item := range writes {
		if item.val == nil {
			continue
		}
		result = a.writeStepNormalized(result, p, item.name, item.path,
			strconv.FormatInt(*item.val, 10), dryRun, item.normalize)
	}

	memcg := a.memcgRoot()
	if memcg == "" {
		status := "skipped"
		msg := "root memcg policy directory was not found"
		if p.Policy.ZramWmRatio != nil || p.Policy.AvailBuffers != nil ||
			p.Policy.ErmAvailBufferEnable != nil {
			status = "failed"
			result.Degraded = true
			result.DegradedReasons = append(result.DegradedReasons,
				"root memcg policy directory was not found")
		}
		result.Steps = append(result.Steps, ApplyStep{
			Name:      "policy.memcg",
			Status:    status,
			Message:   msg,
			ErrorCode: "E_KERNEL_NODE_MISSING",
		})
		return result
	}
	if p.Policy.ZramWmRatio != nil {
		result = a.writeStep(result, p, "policy.zram_wm_ratio",
			filepath.Join(memcg, "memory.zram_wm_ratio"),
			strconv.FormatInt(*p.Policy.ZramWmRatio, 10), dryRun)
	}
	if ab := p.Policy.AvailBuffers; ab != nil {
		result = a.writeStepNormalized(result, p, "policy.avail_buffers",
			filepath.Join(memcg, "memory.avail_buffers"),
			fmt.Sprintf("%d %d %d %d", ab.AvailMB, ab.MinMB, ab.HighMB, ab.FreeSwapThresholdMB),
			dryRun, normalizeAvailBuffersText)
	}
	if p.Policy.ErmAvailBufferEnable != nil {
		result = a.writeStep(result, p, "policy.erm_avail_buffer_enable",
			filepath.Join(memcg, "memory.erm_avail_buffer_enable"),
			strconv.FormatInt(*p.Policy.ErmAvailBufferEnable, 10), dryRun)
	}
	return result
}

func (a *App) writeStep(result ApplyResult, p *Profile, name, path, value string,
	dryRun bool) ApplyResult {
	return a.writeStepWithForce(result, p, name, path, value, dryRun, false, nil)
}

func (a *App) writeStepForce(result ApplyResult, p *Profile, name, path, value string,
	dryRun bool) ApplyResult {
	return a.writeStepWithForce(result, p, name, path, value, dryRun, true, nil)
}

func (a *App) writeStepNormalized(result ApplyResult, p *Profile, name, path, value string,
	dryRun bool, normalize func(string) string) ApplyResult {
	return a.writeStepWithForce(result, p, name, path, value, dryRun, false, normalize)
}

func (a *App) writeStepWithForce(result ApplyResult, p *Profile, name, path, value string,
	dryRun bool, force bool, normalize func(string) string) ApplyResult {
	oldValue := readTrim(path)
	if oldValue == "" && !a.exists(path) {
		result.Degraded = true
		result.DegradedReasons = append(result.DegradedReasons, name+" node missing")
		result.Steps = append(result.Steps, ApplyStep{
			Name:      name,
			Status:    "failed",
			Target:    path,
			Message:   "kernel node is missing",
			ErrorCode: "E_KERNEL_NODE_MISSING",
			NewValue:  value,
		})
		return result
	}
	oldComparable := oldValue
	if normalize != nil {
		oldComparable = normalize(oldValue)
	}
	if oldComparable == value && !force {
		result.Steps = append(result.Steps, ApplyStep{
			Name:     name,
			Status:   "unchanged",
			Target:   path,
			OldValue: oldValue,
			NewValue: value,
		})
		return result
	}
	if dryRun {
		result.Steps = append(result.Steps, ApplyStep{
			Name:     name,
			Status:   "skipped",
			Target:   path,
			Message:  "dry-run; no write performed",
			OldValue: oldValue,
			NewValue: value,
		})
		return result
	}
	if a.deadlineExceeded() {
		result.Degraded = true
		result.DegradedReasons = append(result.DegradedReasons, "boot apply timeout")
		result.Steps = append(result.Steps, ApplyStep{
			Name:      name,
			Status:    "failed",
			Target:    path,
			Message:   "boot apply timeout",
			ErrorCode: "E_BOOT_TIMEOUT",
			OldValue:  oldValue,
			NewValue:  value,
		})
		return result
	}
	if err := writeKernelNode(path, []byte(value+"\n")); err != nil {
		result.Steps = append(result.Steps, ApplyStep{
			Name:      name,
			Status:    "failed",
			Target:    path,
			Message:   err.Error(),
			ErrorCode: kernelWriteErrorCode(err),
			OldValue:  oldValue,
			NewValue:  value,
		})
		result.Degraded = true
		result.DegradedReasons = append(result.DegradedReasons, name+" failed")
		return result
	}
	newValue := readTrim(path)
	if newValue == "" {
		newValue = value
	} else if normalize != nil && normalize(newValue) == value {
		newValue = value
	}
	result.Steps = append(result.Steps, ApplyStep{
		Name:     name,
		Status:   "changed",
		Target:   path,
		OldValue: oldValue,
		NewValue: newValue,
	})
	return result
}

func (a *App) normalizeDevLifeLevel(statPath string) func(string) string {
	return func(raw string) string {
		stat := parseKeyValue(readTrim(statPath), " ")
		return strconv.FormatInt(parseDevLifeLevel(stat, raw), 10)
	}
}

func normalizeCrystalEnabled(marker string) func(string) string {
	return func(raw string) string {
		return strconv.FormatInt(parseCrystalEnabled(raw, marker), 10)
	}
}

func normalizeIntText(raw string) string {
	return strconv.FormatInt(parseIntDefault(raw, 0), 10)
}

func normalizeLastIntText(raw string) string {
	return strconv.FormatInt(parseLastIntDefault(raw, 0), 10)
}

func normalizeAvailBuffersText(raw string) string {
	ab := parseAvailBuffers(raw, AvailBuffers{})
	return fmt.Sprintf("%d %d %d %d", ab.AvailMB, ab.MinMB, ab.HighMB, ab.FreeSwapThresholdMB)
}

func (a *App) bootApplyAPI(body []byte) bool {
	warnings := []Warning{}
	var req BootApplyRequest
	if err := validateBootApplyRequestShape(body); err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_JSON", err.Error(), nil), nil)
		return false
	}
	if err := decodeJSON(body, &req); err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_JSON", err.Error(), nil), nil)
		return false
	}
	if !oneOf(req.Phase, "post-fs-data", "service", "boot-completed") {
		writeEnvelope(false, nil, apiError("E_INVALID_PROFILE", "boot phase is invalid", nil), nil)
		return false
	}
	if req.TimeoutMS != 0 && (req.TimeoutMS < 1000 || req.TimeoutMS > 30000) {
		writeEnvelope(false, nil, apiError("E_INVALID_PROFILE",
			"timeout_ms is out of range", nil), nil)
		return false
	}
	start := nowMS()

	profile, profileWarnings := a.loadProfile()
	warnings = append(warnings, profileWarnings...)
	if missing := a.profileRequiredNodesMissing(profile); len(missing) > 0 {
		finish := nowMS()
		boot := BootStatus{
			Phase:           req.Phase,
			Result:          "failed",
			StartedAtMS:     start,
			FinishedAtMS:    finish,
			DurationMS:      finish - start,
			DegradedReasons: []string{unsupportedMessage},
			LastErrorCode:   unsupportedCode,
			LogTail:         []string{"required management nodes are missing"},
		}
		if err := a.saveBootStatus(boot); err != nil {
			warnings = append(warnings, bootStatusSaveWarning(err))
		}
		return respondUnsupportedWithWarnings(missing, warnings)
	}
	timeoutMS := req.TimeoutMS
	if timeoutMS == 0 {
		timeoutMS = 30000
		if req.Phase == "post-fs-data" {
			timeoutMS = profile.Boot.PostFSTimeoutMS
		}
	}
	oldDeadline := a.DeadlineMS
	a.DeadlineMS = start + timeoutMS
	defer func() {
		a.DeadlineMS = oldDeadline
	}()
	mode := profile.Mode
	if mode == "diagnostic" {
		boot := BootStatus{
			Phase:           req.Phase,
			Result:          "skipped",
			StartedAtMS:     start,
			FinishedAtMS:    nowMS(),
			DurationMS:      nowMS() - start,
			DegradedReasons: []string{},
			LogTail:         []string{"diagnostic profile; boot apply skipped"},
		}
		result := ApplyResult{Changed: false, Degraded: false, Steps: []ApplyStep{}, Boot: &boot}
		if err := a.saveBootStatus(boot); err != nil {
			warnings = append(warnings, bootStatusSaveWarning(err))
			result.Degraded = true
			result.DegradedReasons = append(result.DegradedReasons, "boot status save failed")
		}
		return a.respondOK(result, warnings...)
	}
	applyMode := "runtime"
	if req.Phase == "post-fs-data" && mode == "full-control" {
		applyMode = "full-control"
	} else if req.Phase == "service" || req.Phase == "boot-completed" {
		applyMode = "reconcile"
	}
	result, err := a.apply(applyMode, profile)
	if err == nil && a.deadlineExceeded() {
		err = errors.New("E_BOOT_TIMEOUT: boot apply timeout")
	}
	finish := nowMS()
	boot := BootStatus{
		Phase:           req.Phase,
		Result:          "active",
		StartedAtMS:     start,
		FinishedAtMS:    finish,
		DurationMS:      finish - start,
		DegradedReasons: result.DegradedReasons,
		LogTail:         []string{req.Phase + ": " + applyMode + " apply finished"},
	}
	if result.Degraded {
		boot.Result = "degraded"
	}
	if err != nil {
		boot.Result = "failed"
		boot.LastErrorCode = errorCodeFromErr(err)
		boot.DegradedReasons = append(boot.DegradedReasons, err.Error())
		result.Degraded = true
		result.DegradedReasons = append(result.DegradedReasons, err.Error())
	}
	result.Boot = &boot
	if saveErr := a.saveBootStatus(boot); saveErr != nil {
		warnings = append(warnings, bootStatusSaveWarning(saveErr))
		if err == nil {
			boot.Result = "degraded"
			boot.DegradedReasons = append(boot.DegradedReasons, "boot status save failed")
			result.Degraded = true
			result.DegradedReasons = append(result.DegradedReasons, "boot status save failed")
			result.Boot = &boot
		}
	}
	if err != nil {
		writeEnvelope(false, result, apiError(errorCodeFromErr(err), err.Error(), nil), warnings)
		return false
	}
	return a.respondOK(result, warnings...)
}

func validateBootApplyRequestShape(body []byte) error {
	var root map[string]json.RawMessage
	if err := decodeStrictJSON(body, &root, "empty JSON body"); err != nil {
		return err
	}
	if root == nil {
		return errors.New("boot apply request must be an object")
	}
	if err := requireProfileKeys(root, "boot_apply", "phase"); err != nil {
		return err
	}
	if err := requireNonNullProfileFields(root, "boot_apply", "phase"); err != nil {
		return err
	}
	if _, ok := root["timeout_ms"]; ok {
		if err := requireNonNullProfileFields(root, "boot_apply", "timeout_ms"); err != nil {
			return err
		}
	}
	return nil
}

func bootStatusSaveWarning(err error) Warning {
	return Warning{
		Code:    "W_BOOT_STATUS_SAVE_FAILED",
		Message: "boot status could not be saved",
		Details: map[string]any{
			"error": err.Error(),
		},
	}
}

func (a *App) lockApply() (func(), error) {
	if err := a.ensureDirs(); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(a.StateDir, "lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("E_APPLY_LOCKED: another apply operation is active")
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func (a *App) loadBootStatus() BootStatus {
	path := filepath.Join(a.StateDir, "last_boot.json")
	if data, err := os.ReadFile(path); err == nil {
		var status BootStatus
		if err := json.Unmarshal(data, &status); err == nil && status.Phase != "" {
			if status.LogTail == nil {
				status.LogTail = []string{}
			}
			return status
		}
	}
	return BootStatus{
		Phase:        "none",
		Result:       "unknown",
		StartedAtMS:  0,
		FinishedAtMS: 0,
		LogTail:      []string{},
	}
}

func (a *App) saveBootStatus(status BootStatus) error {
	if err := a.ensureDirs(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(a.StateDir, "last_boot.json"), append(data, '\n'), 0600)
}

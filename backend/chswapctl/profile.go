package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

const (
	maxLoopBackingBytes      = 32 * bytesGiB
	fallbackMaxDisksizeBytes = 64 * bytesGiB
	maxDevLifeLevel          = 3
	maxZramIncreaseMiB       = 1 << 20
	maxAvailBufferMB         = 4294967295
	minAgingIdleSec          = 60
	maxAgingIdleSec          = 7 * 24 * 60 * 60
	maxAgingMinZramMB        = 4096
	maxAgingPressureMB       = 64 * 1024
	maxAgingCooldownSec      = 7 * 24 * 60 * 60
	maxAgingAppsPerCycle     = 8
)

func (a *App) putProfile(body []byte) bool {
	profile, err := decodeProfile(body)
	if err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_PROFILE", err.Error(), nil), nil)
		return false
	}
	if err := a.validateProfile(profile); err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_PROFILE", err.Error(), nil), nil)
		return false
	}
	if missing := a.profileRequiredNodesMissing(profile); len(missing) > 0 {
		return respondUnsupported(missing)
	}
	if warnings, err := a.saveProfile(profile); err != nil {
		writeEnvelope(false, nil, apiError("E_INTERNAL", err.Error(), nil), warnings)
		return false
	} else {
		return a.respondOK(profile, warnings...)
	}
}

func (a *App) validateProfileAPI(body []byte) bool {
	profile, err := decodeProfile(body)
	if err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_PROFILE", err.Error(), nil), nil)
		return false
	}
	if err := a.validateProfile(profile); err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_PROFILE", err.Error(), nil), nil)
		return false
	}
	return a.respondOK(ValidationResult{Valid: true, NormalizedProfile: profile})
}

func decodeProfile(body []byte) (*Profile, error) {
	if err := validateProfileShape(body); err != nil {
		return nil, err
	}
	var profile Profile
	if err := decodeStrictJSON(body, &profile, "empty profile body"); err != nil {
		return nil, err
	}
	// Profiles saved before app aging had no aging object. Normalize the zero
	// value to the disabled defaults so the UI receives a valid profile.
	if profile.Aging == (AgingProfile{}) {
		profile.Aging = defaultAgingProfile()
	}
	return &profile, nil
}

func defaultAgingProfile() AgingProfile {
	return AgingProfile{
		Enabled: false, IdleAfterSec: 900, MinZramMB: 64,
		PressureAvailableBelowMB: 4096, CooldownSec: 1800, MaxAppsPerCycle: 1,
	}
}

func validateProfileShape(body []byte) error {
	var root map[string]json.RawMessage
	if err := decodeStrictJSON(body, &root, "empty profile body"); err != nil {
		return err
	}
	if root == nil {
		return errors.New("profile must be an object")
	}
	if err := requireProfileKeys(root, "profile",
		"schema", "mode", "boot", "zram", "backing", "swap", "crystal", "policy"); err != nil {
		return err
	}
	if err := requireNonNullProfileFields(root, "profile", "schema", "mode"); err != nil {
		return err
	}
	boot, err := requiredProfileObject(root, "profile.boot", "boot")
	if err != nil {
		return err
	}
	if err := requireProfileKeys(boot, "profile.boot",
		"post_fs_timeout_ms"); err != nil {
		return err
	}
	if err := requireNonNullProfileFields(boot, "profile.boot",
		"post_fs_timeout_ms"); err != nil {
		return err
	}
	zram, err := requiredProfileObject(root, "profile.zram", "zram")
	if err != nil {
		return err
	}
	if err := requireProfileKeys(zram, "profile.zram",
		"device", "disksize", "comp_algorithm"); err != nil {
		return err
	}
	if err := requireNonNullProfileFields(zram, "profile.zram", "device"); err != nil {
		return err
	}
	backing, err := requiredProfileObject(root, "profile.backing", "backing")
	if err != nil {
		return err
	}
	if err := requireProfileKeys(backing, "profile.backing", "mode"); err != nil {
		return err
	}
	if err := requireNonNullProfileFields(backing, "profile.backing", "mode"); err != nil {
		return err
	}
	swap, err := requiredProfileObject(root, "profile.swap", "swap")
	if err != nil {
		return err
	}
	if err := requireProfileKeys(swap, "profile.swap", "enabled", "priority"); err != nil {
		return err
	}
	if err := requireNonNullProfileFields(swap, "profile.swap", "enabled", "priority"); err != nil {
		return err
	}
	crystal, err := requiredProfileObject(root, "profile.crystal", "crystal")
	if err != nil {
		return err
	}
	if err := requireProfileKeys(crystal, "profile.crystal",
		"enable", "core_enable", "swapd_pause", "loglevel",
		"dev_life", "quota_day", "zram_increase"); err != nil {
		return err
	}
	policy, err := requiredProfileObject(root, "profile.policy", "policy")
	if err != nil {
		return err
	}
	if err := requireProfileKeys(policy, "profile.policy",
		"zram_wm_ratio", "avail_buffers", "erm_avail_buffer_enable"); err != nil {
		return err
	}
	if raw, ok := policy["avail_buffers"]; ok && !rawIsJSONNull(raw) {
		var ab map[string]json.RawMessage
		if err := json.Unmarshal(raw, &ab); err != nil || ab == nil {
			return errors.New("profile.policy.avail_buffers must be an object or null")
		}
		if err := requireProfileKeys(ab, "profile.policy.avail_buffers",
			"avail_mb", "min_mb", "high_mb", "free_swap_threshold_mb"); err != nil {
			return err
		}
		if err := requireNonNullProfileFields(ab, "profile.policy.avail_buffers",
			"avail_mb", "min_mb", "high_mb", "free_swap_threshold_mb"); err != nil {
			return err
		}
	}
	if raw, ok := root["aging"]; ok && !rawIsJSONNull(raw) {
		aging, err := requiredProfileObject(root, "profile.aging", "aging")
		if err != nil {
			return err
		}
		if err := requireProfileKeys(aging, "profile.aging",
			"enabled", "idle_after_sec", "min_zram_mb",
			"pressure_available_below_mb", "cooldown_sec",
			"max_apps_per_cycle"); err != nil {
			return err
		}
		if err := requireNonNullProfileFields(aging, "profile.aging",
			"enabled", "idle_after_sec", "min_zram_mb",
			"pressure_available_below_mb", "cooldown_sec",
			"max_apps_per_cycle"); err != nil {
			return err
		}
	}
	return nil
}

func validateApplyRequestShape(body []byte) error {
	var root map[string]json.RawMessage
	if err := decodeStrictJSON(body, &root, "empty JSON body"); err != nil {
		return err
	}
	raw, ok := root["profile"]
	if !ok {
		return nil
	}
	if rawIsJSONNull(raw) {
		return errors.New("apply.profile must be an object when provided")
	}
	return validateProfileShape(raw)
}

func requiredProfileObject(parent map[string]json.RawMessage, path string,
	key string) (map[string]json.RawMessage, error) {
	raw, ok := parent[key]
	if !ok {
		return nil, errors.New(path + " is required")
	}
	if rawIsJSONNull(raw) {
		return nil, errors.New(path + " must be an object")
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return nil, errors.New(path + " must be an object")
	}
	return out, nil
}

func requireProfileKeys(obj map[string]json.RawMessage, path string,
	keys ...string) error {
	for _, key := range keys {
		if _, ok := obj[key]; !ok {
			return errors.New(path + "." + key + " is required")
		}
	}
	return nil
}

func requireNonNullProfileFields(obj map[string]json.RawMessage, path string,
	keys ...string) error {
	for _, key := range keys {
		if rawIsJSONNull(obj[key]) {
			return errors.New(path + "." + key + " must not be null")
		}
	}
	return nil
}

func rawIsJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func validateProfile(p *Profile) error {
	if p.Schema != "chswap.profile.v1" {
		return errors.New("profile.schema must be chswap.profile.v1")
	}
	if !oneOf(p.Mode, "diagnostic", "full-control") {
		return errors.New("profile.mode is invalid")
	}
	if p.Boot.PostFSTimeoutMS < 1000 || p.Boot.PostFSTimeoutMS > 30000 {
		return errors.New("profile.boot.post_fs_timeout_ms is out of range")
	}
	if !regexp.MustCompile(`^zram[0-9]+$`).MatchString(p.Zram.Device) {
		return errors.New("profile.zram.device is invalid")
	}
	if p.Zram.Disksize != nil && parseSize(*p.Zram.Disksize) <= 0 {
		return errors.New("profile.zram.disksize is invalid")
	}
	if p.Zram.CompAlgorithm != nil && *p.Zram.CompAlgorithm == "" {
		return errors.New("profile.zram.comp_algorithm is invalid")
	}
	if !oneOf(p.Backing.Mode, "none", "block", "loop") {
		return errors.New("profile.backing.mode is invalid")
	}
	if p.Backing.LoopSizeBytes != nil && *p.Backing.LoopSizeBytes <= 0 {
		return errors.New("profile.backing.loop_size_bytes is invalid")
	}
	if p.Swap.Priority < -1 || p.Swap.Priority > 32767 {
		return errors.New("profile.swap.priority is out of range")
	}
	if err := validBool01(p.Crystal.Enable, "crystal.enable"); err != nil {
		return err
	}
	if err := validBool01(p.Crystal.CoreEnable, "crystal.core_enable"); err != nil {
		return err
	}
	if err := validBool01(p.Crystal.SwapdPause, "crystal.swapd_pause"); err != nil {
		return err
	}
	if p.Crystal.Loglevel != nil && (*p.Crystal.Loglevel < 0 || *p.Crystal.Loglevel > 3) {
		return errors.New("profile.crystal.loglevel is out of range")
	}
	if p.Crystal.DevLife != nil && (*p.Crystal.DevLife < 0 || *p.Crystal.DevLife > maxDevLifeLevel) {
		return errors.New("profile.crystal.dev_life is out of range")
	}
	if err := validNonNeg(p.Crystal.QuotaDay, "crystal.quota_day"); err != nil {
		return err
	}
	if p.Crystal.ZramIncrease != nil && (*p.Crystal.ZramIncrease < 0 || *p.Crystal.ZramIncrease > maxZramIncreaseMiB) {
		return errors.New("profile.crystal.zram_increase is out of range")
	}
	if p.Policy.ZramWmRatio != nil && (*p.Policy.ZramWmRatio < 0 || *p.Policy.ZramWmRatio > 100) {
		return errors.New("profile.policy.zram_wm_ratio is out of range")
	}
	if ab := p.Policy.AvailBuffers; ab != nil {
		if ab.AvailMB < 0 || ab.MinMB < 0 || ab.HighMB < 0 || ab.FreeSwapThresholdMB < 0 {
			return errors.New("profile.policy.avail_buffers contains a negative value")
		}
		if ab.AvailMB > maxAvailBufferMB || ab.MinMB > maxAvailBufferMB ||
			ab.HighMB > maxAvailBufferMB || ab.FreeSwapThresholdMB > maxAvailBufferMB {
			return errors.New("profile.policy.avail_buffers is out of range")
		}
	}
	if err := validBool01(p.Policy.ErmAvailBufferEnable, "policy.erm_avail_buffer_enable"); err != nil {
		return err
	}
	if p.Aging.IdleAfterSec < minAgingIdleSec || p.Aging.IdleAfterSec > maxAgingIdleSec {
		return errors.New("profile.aging.idle_after_sec is out of range")
	}
	if p.Aging.MinZramMB <= 0 || p.Aging.MinZramMB > maxAgingMinZramMB {
		return errors.New("profile.aging.min_zram_mb is out of range")
	}
	if p.Aging.PressureAvailableBelowMB <= 0 || p.Aging.PressureAvailableBelowMB > maxAgingPressureMB {
		return errors.New("profile.aging.pressure_available_below_mb is out of range")
	}
	if p.Aging.CooldownSec < minAgingIdleSec || p.Aging.CooldownSec > maxAgingCooldownSec {
		return errors.New("profile.aging.cooldown_sec is out of range")
	}
	if p.Aging.MaxAppsPerCycle <= 0 || p.Aging.MaxAppsPerCycle > maxAgingAppsPerCycle {
		return errors.New("profile.aging.max_apps_per_cycle is out of range")
	}
	return nil
}

func (a *App) validateProfile(p *Profile) error {
	if err := validateProfile(p); err != nil {
		return err
	}
	if !p.Swap.Enabled {
		return errors.New("profile.swap.enabled must be true")
	}
	if p.Zram.Disksize != nil {
		size := parseSize(*p.Zram.Disksize)
		if size > a.maxDisksizeBytes() {
			return errors.New("profile.zram.disksize exceeds supported maximum")
		}
	}
	if p.Zram.CompAlgorithm != nil {
		algos := a.zramCaps(p.Zram.Device).CompAlgorithms
		if len(algos) > 0 && !slices.Contains(algos, *p.Zram.CompAlgorithm) {
			return errors.New("profile.zram.comp_algorithm is not supported by the kernel")
		}
	}
	if p.Backing.LoopSizeBytes != nil && *p.Backing.LoopSizeBytes > maxLoopBackingBytes {
		return errors.New("profile.backing.loop_size_bytes exceeds supported maximum")
	}
	return nil
}

func (a *App) maxDisksizeBytes() int64 {
	mem := a.memTotalBytes()
	if mem <= 0 {
		return fallbackMaxDisksizeBytes
	}
	ceilGiB := (mem + bytesGiB - 1) / bytesGiB
	return ceilGiB * 2 * bytesGiB
}

func validBool01(p *int64, name string) error {
	if p != nil && !oneOf(*p, int64(0), int64(1)) {
		return errors.New("profile." + name + " must be 0 or 1")
	}
	return nil
}

func validNonNeg(p *int64, name string) error {
	if p != nil && *p < 0 {
		return errors.New("profile." + name + " must be non-negative")
	}
	return nil
}

// defaultProfile is the "nothing configured yet" baseline: every tunable is
// unset (nil = follow the system). Only structural invariants are fixed —
// managed device is zram0, swap is force-enabled, backing defaults to the
// fixed block device. The daemon supplies runtime defaults (e.g. disksize=RAM)
// for the strong-takeover fields without persisting them into the profile.
func defaultProfile() *Profile {
	return &Profile{
		Schema:  "chswap.profile.v1",
		Mode:    "full-control",
		Zram:    ZramProfile{Device: "zram0"},
		Backing: BackingProfile{Mode: "block"},
		Crystal: CrystalProfile{},
		Policy:  PolicyProfile{},
		Aging:   defaultAgingProfile(),
		Boot:    BootProfile{PostFSTimeoutMS: 8000},
		Swap:    SwapProfile{Enabled: true, Priority: 32767},
	}
}

func diagnosticFallbackProfile() *Profile {
	profile := defaultProfile()
	profile.Mode = "diagnostic"
	return profile
}

func invalidProfileWarning(source string, err error, fallback string) Warning {
	w := Warning{
		Code:    "W_PROFILE_STORE_INVALID",
		Message: source + " profile is invalid; " + fallback,
	}
	if err != nil {
		w.Details = map[string]any{
			"source": source,
			"error":  err.Error(),
		}
	}
	return w
}

func (a *App) decodeStoredProfile(data []byte) (*Profile, error) {
	trimmed := bytes.TrimSpace(data)
	profile, err := a.decodeAndValidateProfile(trimmed)
	if err != nil {
		migrated, changed, migrateErr := migrateStoredProfileJSON(trimmed)
		if !changed || migrateErr != nil {
			return nil, err
		}
		return a.decodeAndValidateProfile(migrated)
	}
	return profile, nil
}

func (a *App) decodeAndValidateProfile(data []byte) (*Profile, error) {
	profile, err := decodeProfile(data)
	if err != nil {
		return nil, err
	}
	if err := a.validateProfile(profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func migrateStoredProfileJSON(data []byte) ([]byte, bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, false, err
	}
	rawBoot, ok := root["boot"]
	if !ok || rawIsJSONNull(rawBoot) {
		return data, false, nil
	}
	var boot map[string]json.RawMessage
	if err := json.Unmarshal(rawBoot, &boot); err != nil || boot == nil {
		return data, false, err
	}
	changed := false
	for _, key := range []string{"early_claim", "watchdog_seconds"} {
		if _, ok := boot[key]; ok {
			delete(boot, key)
			changed = true
		}
	}
	if !changed {
		return data, false, nil
	}
	encodedBoot, err := json.Marshal(boot)
	if err != nil {
		return nil, false, err
	}
	root["boot"] = encodedBoot
	out, err := json.Marshal(root)
	return out, true, err
}

func (a *App) loadProfile() (*Profile, []Warning) {
	warnings := []Warning{}
	invalidStoredProfile := false
	if !a.SkipKSUD && a.KSUD != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		out, err := exec.CommandContext(ctx, a.KSUD,
			ksudModuleConfigArgs("get", "profile.json")...).Output()
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			profile, decErr := a.decodeStoredProfile(out)
			if decErr == nil {
				return profile, warnings
			}
			invalidStoredProfile = true
			warnings = append(warnings, invalidProfileWarning("KernelSU",
				decErr, "trying file fallback"))
		}
	}
	path := filepath.Join(a.DataDir, "profile.json")
	if data, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(data)) > 0 {
		profile, decErr := a.decodeStoredProfile(data)
		if decErr == nil {
			return profile, warnings
		}
		invalidStoredProfile = true
		warnings = append(warnings, invalidProfileWarning("profile.json",
			decErr, "using diagnostic fallback"))
	}
	if invalidStoredProfile {
		return diagnosticFallbackProfile(), warnings
	}
	return defaultProfile(), warnings
}

func (a *App) saveProfile(profile *Profile) ([]Warning, error) {
	warnings := []Warning{}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return warnings, err
	}
	data = append(data, '\n')

	ksudOK := false
	if !a.SkipKSUD && a.KSUD != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
		defer cancel()
		cmd := exec.CommandContext(ctx, a.KSUD,
			ksudModuleConfigArgs("set", "profile.json", "--stdin")...)
		cmd.Stdin = bytes.NewReader(data)
		if err := cmd.Run(); err == nil {
			ksudOK = true
		} else {
			warnings = append(warnings, Warning{
				Code:    "W_KSUD_CONFIG_FAILED",
				Message: "KernelSU module config save failed; using file fallback",
			})
		}
	}

	if err := a.ensureDirs(); err != nil {
		return warnings, err
	}
	path := filepath.Join(a.DataDir, "profile.json")
	if err := atomicWrite(path, data, 0600); err != nil {
		return warnings, err
	}
	if !ksudOK {
		warnings = append(warnings, Warning{
			Code:    "W_PROFILE_FILE_FALLBACK",
			Message: "profile saved in module data file",
		})
	}
	return warnings, nil
}

func (a *App) profileBackend() string {
	if !a.SkipKSUD && a.KSUD != "" {
		return "ksud-module-config"
	}
	return "module-data-file"
}

func (a *App) profilePathForBackend() string {
	if a.profileBackend() == "ksud-module-config" {
		return "profile.json"
	}
	return filepath.Join(a.DataDir, "profile.json")
}

func ksudModuleConfigArgs(command string, args ...string) []string {
	out := []string{"module", "config", "--internal", moduleID, command}
	return append(out, args...)
}

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	bytesGiB            int64 = 1 << 30
	maxRequestBodyBytes       = 1 << 20
)

// readBody resolves the request body.
//
// Priority is an explicit --json argument (an inline value, or "-"/"@-" to read
// stdin). Without --json, GET requests never carry a body, and other methods
// read stdin only when data is actually available — bounded by a short timeout
// so an open-but-idle stdin can never block the process.
//
// The bound matters because KernelSU/SukiSU WebUI runs commands in a persistent
// root shell whose stdin stays open with no data and never reaches EOF; an
// unconditional io.ReadAll(os.Stdin) would block forever there and wedge the
// WebUI's exec bridge. Callers that need to pipe a body should use --json (the
// WebUI and boot scripts always do) or the explicit "-" form.
func readBody(method string, args []string) ([]byte, error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--json" {
			if i+1 >= len(args) {
				return nil, errors.New("--json requires a body")
			}
			if args[i+1] == "-" || args[i+1] == "@-" {
				return readAllLimited(os.Stdin)
			}
			if len(args[i+1]) > maxRequestBodyBytes {
				return nil, errors.New("JSON body is too large")
			}
			return []byte(args[i+1]), nil
		}
	}

	// GET has no request body.
	if strings.ToUpper(method) == "GET" {
		return nil, nil
	}

	info, err := os.Stdin.Stat()
	if err != nil || (info.Mode()&os.ModeCharDevice) != 0 {
		// stdin is a terminal (or unknown) -> no piped body.
		return nil, nil
	}

	// stdin is a pipe/file/socket: read what's there, but never hang on an idle
	// open pipe.
	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		b, e := readAllLimited(os.Stdin)
		ch <- readResult{b, e}
	}()
	select {
	case r := <-ch:
		return r.data, r.err
	case <-time.After(500 * time.Millisecond):
		return nil, nil
	}
}

func readAllLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxRequestBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRequestBodyBytes {
		return nil, errors.New("JSON body is too large")
	}
	return data, nil
}

func oneOf[T comparable](value T, allowed ...T) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func (a *App) ensureDirs() error {
	for _, dir := range []string{a.DataDir, a.StateDir, a.LogDir, a.DiagnosticsDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) moduleVersion() string {
	data, err := os.ReadFile(filepath.Join(a.ModDir, "module.prop"))
	if err != nil {
		return "Unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && key == "version" && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "Unknown"
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func decodeJSON(body []byte, out any) error {
	return decodeStrictJSON(body, out, "empty JSON body")
}

func decodeStrictJSON(body []byte, out any, emptyMessage string) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return errors.New(emptyMessage)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func errorCodeFromErr(err error) string {
	msg := err.Error()
	for _, code := range []string{
		"E_APPLY_LOCKED",
		"E_BACKING_DEVICE_MISSING",
		"E_BOOT_TIMEOUT",
		"E_DEVICE_NOT_FOUND",
		"E_KERNEL_WRITE_FAILED",
		"E_KERNEL_NODE_MISSING",
		"E_MKSWAP_FAILED",
		"E_SWAPOFF_FAILED",
		"E_SWAPON_FAILED",
	} {
		if strings.Contains(msg, code) {
			return code
		}
	}
	return "E_INTERNAL"
}

func kernelWriteErrorCode(err error) string {
	if os.IsNotExist(err) {
		return "E_KERNEL_NODE_MISSING"
	}
	return "E_KERNEL_WRITE_FAILED"
}

func swapCommandErrorCode(name string, err error) string {
	if err != nil && strings.Contains(err.Error(), "E_BOOT_TIMEOUT") {
		return "E_BOOT_TIMEOUT"
	}
	switch name {
	case "mkswap":
		return "E_MKSWAP_FAILED"
	case "swapoff":
		return "E_SWAPOFF_FAILED"
	case "swapon":
		return "E_SWAPON_FAILED"
	default:
		return "E_INTERNAL"
	}
}

func (a *App) diagnosticsAPI() bool {
	if err := a.ensureDirs(); err != nil {
		writeEnvelope(false, nil, apiError("E_INTERNAL", err.Error(), nil), nil)
		return false
	}
	path := filepath.Join(a.DiagnosticsDir,
		fmt.Sprintf("diagnostics-%d.txt", time.Now().UnixMilli()))
	var b strings.Builder
	b.WriteString("Crystal Hybridswap WebUI diagnostics\n")
	b.WriteString("generated_at_ms: " + strconv.FormatInt(nowMS(), 10) + "\n\n")
	writeSection(&b, "capabilities", mustJSON(a.capabilities()))
	writeSection(&b, "status", mustJSON(a.status()))
	profile, _ := a.loadProfile()
	writeSection(&b, "profile", mustJSON(profile, nil))
	writeSection(&b, "proc_swaps", readTrim(a.proc("swaps")))
	for _, file := range []string{
		"initstate", "mm_stat", "io_stat", "bd_stat", "zms_stat",
		"writeback_cold_stat", "hybridswap_report",
	} {
		dev := profile.Zram.Device
		writeSection(&b, "sys_block_"+dev+"_"+file,
			readTrim(a.sys(filepath.Join("block", dev, file))))
	}
	if err := atomicWrite(path, []byte(b.String()), 0600); err != nil {
		writeEnvelope(false, nil, apiError("E_INTERNAL", err.Error(), nil), nil)
		return false
	}
	return a.respondOK(DiagnosticsResult{Path: path},
		unsupportedWarnings(a.managementSurfaceMissing(profile.Zram.Device))...)
}

func writeSection(b *strings.Builder, title string, value string) {
	b.WriteString("## " + title + "\n")
	if value == "" {
		b.WriteString("(empty)\n\n")
		return
	}
	b.WriteString(value)
	if !strings.HasSuffix(value, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func mustJSON(v any, warnings []Warning) string {
	out, err := json.MarshalIndent(Envelope{
		OK:       true,
		Schema:   apiSchema,
		Data:     v,
		Warnings: warnings,
	}, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(out)
}

func selectedAlgorithm(raw string) string {
	for _, field := range strings.Fields(raw) {
		if strings.HasPrefix(field, "[") && strings.HasSuffix(field, "]") {
			return strings.Trim(field, "[]")
		}
	}
	fields := strings.Fields(raw)
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func parseMMStat(raw string) map[string]any {
	fields := strings.Fields(raw)
	names := []string{
		"orig_data_size",
		"compr_data_size",
		"mem_used_total",
		"mem_limit",
		"mem_used_max",
		"same_pages",
		"pages_compacted",
		"huge_pages",
		"huge_pages_since",
	}
	out := map[string]any{}
	for i, item := range fields {
		if i >= len(names) {
			break
		}
		out[names[i]] = parseIntDefault(item, 0)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func readIntAny(def int64, paths ...string) int64 {
	for _, path := range paths {
		if val := readTrim(path); val != "" {
			return parseIntDefault(val, def)
		}
	}
	return def
}

func (a *App) managementSurfaceMissing(device string) []string {
	if device == "" {
		device = "zram0"
	}
	zdir := a.sys("block/" + device)
	paths := []string{
		zdir,
		filepath.Join(zdir, "initstate"),
		filepath.Join(zdir, "disksize"),
		filepath.Join(zdir, "reset"),
		filepath.Join(zdir, "comp_algorithm"),
		filepath.Join(zdir, "hybridswap_enable"),
		filepath.Join(zdir, "hybridswap_core_enable"),
		filepath.Join(zdir, "hybridswap_swapd_pause"),
		filepath.Join(zdir, "hybridswap_loglevel"),
		filepath.Join(zdir, "hybridswap_report"),
		filepath.Join(zdir, "hybridswap_crystal_stat"),
		filepath.Join(zdir, "hybridswap_loop_device"),
		filepath.Join(zdir, "hybridswap_dev_life"),
		filepath.Join(zdir, "hybridswap_quota_day"),
		filepath.Join(zdir, "hybridswap_zram_increase"),
		a.sys("class/zram-control"),
	}
	missing := missingPaths(a, paths)
	memcg := a.memcgRoot()
	if memcg == "" {
		missing = append(missing,
			"memory.zram_wm_ratio",
			"memory.avail_buffers",
			"memory.erm_avail_buffer_enable",
			"memory.erm_avail_buffer")
	} else {
		missing = append(missing, missingPaths(a, []string{
			filepath.Join(memcg, "memory.zram_wm_ratio"),
			filepath.Join(memcg, "memory.avail_buffers"),
			filepath.Join(memcg, "memory.erm_avail_buffer_enable"),
			filepath.Join(memcg, "memory.erm_avail_buffer"),
		})...)
	}
	return missing
}

func (a *App) profileRequiredNodesMissing(p *Profile) []string {
	if p == nil || p.Mode == "diagnostic" {
		return nil
	}
	zdir := a.sys("block/" + p.Zram.Device)
	paths := []string{
		zdir,
		filepath.Join(zdir, "initstate"),
		filepath.Join(zdir, "disksize"),
		filepath.Join(zdir, "reset"),
		filepath.Join(zdir, "hybridswap_report"),
		a.sys("class/zram-control"),
	}
	if p.Zram.CompAlgorithm != nil {
		paths = append(paths, filepath.Join(zdir, "comp_algorithm"))
	}
	if p.Backing.Mode == "block" || p.Backing.Mode == "loop" {
		paths = append(paths, filepath.Join(zdir, "hybridswap_loop_device"))
	}
	if p.Crystal.Enable != nil {
		paths = append(paths, filepath.Join(zdir, "hybridswap_enable"))
	}
	if p.Crystal.CoreEnable != nil {
		paths = append(paths, filepath.Join(zdir, "hybridswap_core_enable"))
	}
	if p.Crystal.SwapdPause != nil {
		paths = append(paths, filepath.Join(zdir, "hybridswap_swapd_pause"))
	}
	if p.Crystal.Loglevel != nil {
		paths = append(paths, filepath.Join(zdir, "hybridswap_loglevel"))
	}
	if p.Crystal.DevLife != nil {
		paths = append(paths,
			filepath.Join(zdir, "hybridswap_dev_life"),
			filepath.Join(zdir, "hybridswap_crystal_stat"))
	}
	if p.Crystal.QuotaDay != nil {
		paths = append(paths, filepath.Join(zdir, "hybridswap_quota_day"))
	}
	if p.Crystal.ZramIncrease != nil {
		paths = append(paths, filepath.Join(zdir, "hybridswap_zram_increase"))
	}
	missing := missingPaths(a, paths)
	if p.Policy.ZramWmRatio == nil && p.Policy.AvailBuffers == nil &&
		p.Policy.ErmAvailBufferEnable == nil {
		return missing
	}
	memcg := a.memcgRoot()
	if memcg == "" {
		if p.Policy.ZramWmRatio != nil {
			missing = append(missing, "memory.zram_wm_ratio")
		}
		if p.Policy.AvailBuffers != nil {
			missing = append(missing, "memory.avail_buffers")
		}
		if p.Policy.ErmAvailBufferEnable != nil {
			missing = append(missing, "memory.erm_avail_buffer_enable")
		}
		return missing
	}
	if p.Policy.ZramWmRatio != nil {
		missing = append(missing, missingPaths(a, []string{
			filepath.Join(memcg, "memory.zram_wm_ratio"),
		})...)
	}
	if p.Policy.AvailBuffers != nil {
		missing = append(missing, missingPaths(a, []string{
			filepath.Join(memcg, "memory.avail_buffers"),
		})...)
	}
	if p.Policy.ErmAvailBufferEnable != nil {
		missing = append(missing, missingPaths(a, []string{
			filepath.Join(memcg, "memory.erm_avail_buffer_enable"),
		})...)
	}
	return missing
}

func missingPaths(a *App, paths []string) []string {
	missing := []string{}
	seen := map[string]bool{}
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if !a.exists(path) {
			missing = append(missing, path)
		}
	}
	return missing
}

func (a *App) sys(path string) string {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimPrefix(path, "sys/")
	return filepath.Join(a.SysRoot, path)
}

func (a *App) proc(path string) string {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimPrefix(path, "proc/")
	return filepath.Join(a.ProcRoot, path)
}

func (a *App) exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readTrim(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writeKernelNode(path string, data []byte) error {
	if !isAllowedKernelWritePath(path) {
		return fmt.Errorf("refusing to write non-allowlisted kernel node: %s", path)
	}
	if err := rejectSymlink(path); err != nil {
		return err
	}
	var chmodErr error
	if isZramTakeoverNode(path) {
		chmodErr = ensureUserWritable(path)
		if os.IsNotExist(chmodErr) {
			return chmodErr
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if chmodErr != nil {
			return fmt.Errorf("chmod u+w: %v; open: %w", chmodErr, err)
		}
		return err
	}
	if n, err := f.Write(data); err != nil {
		_ = f.Close()
		if chmodErr != nil {
			return fmt.Errorf("chmod u+w: %v; write: %w", chmodErr, err)
		}
		return err
	} else if n != len(data) {
		_ = f.Close()
		return fmt.Errorf("short write to kernel node: wrote %d of %d bytes", n, len(data))
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nil
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write symlinked kernel node: %s", path)
	}
	return nil
}

func ensureUserWritable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to chmod symlinked kernel node: %s", path)
	}
	mode := info.Mode().Perm()
	if mode&0200 != 0 {
		return nil
	}
	return os.Chmod(path, mode|0200)
}

func isAllowedKernelWritePath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	base := filepath.Base(clean)
	if isZramWriteNode(base) {
		pattern := `(^|/)sys/(block/zram[0-9]+|class/zram-control)/` +
			regexp.QuoteMeta(base) + `$`
		return regexp.MustCompile(pattern).MatchString(clean)
	}
	if isMemcgWriteNode(base) {
		pattern := `(^|/)(dev/memcg|sys/fs/cgroup|sys/fs/cgroup/memory)(/.*)?/` +
			regexp.QuoteMeta(base) + `$`
		return regexp.MustCompile(pattern).MatchString(clean)
	}
	return false
}

func isZramWriteNode(base string) bool {
	switch base {
	case "disksize", "reset", "comp_algorithm", "hybridswap_loop_device",
		"hybridswap_enable", "hybridswap_core_enable",
		"hybridswap_swapd_pause", "hybridswap_loglevel",
		"hybridswap_dev_life", "hybridswap_quota_day",
		"hybridswap_zram_increase":
		return true
	default:
		return false
	}
}

func isMemcgWriteNode(base string) bool {
	switch base {
	case "memory.zram_wm_ratio", "memory.avail_buffers",
		"memory.erm_avail_buffer_enable",
		"memory.force_swapout", "memory.force_swapin":
		return true
	default:
		return false
	}
}

func isZramTakeoverNode(path string) bool {
	switch filepath.Base(path) {
	case "disksize", "reset", "comp_algorithm", "hybridswap_loop_device":
		return true
	default:
		return false
	}
}

func parseKeyValue(raw string, sep string) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var key, val string
		if sep == " " {
			// whitespace-separated "key value" (e.g. hybridswap_crystal_stat)
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			key, val = parts[0], parts[1]
		} else {
			s := sep
			if s == "" {
				s = "="
			}
			parts := strings.SplitN(line, s, 2)
			if len(parts) != 2 {
				continue
			}
			key, val = parts[0], parts[1]
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return out
}

func parseIntFields(raw string) []int64 {
	out := []int64{}
	for _, field := range strings.Fields(raw) {
		out = append(out, parseIntDefault(field, 0))
	}
	return out
}

func parseIntDefault(raw string, def int64) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	fields := strings.Fields(raw)
	if len(fields) > 0 {
		raw = fields[0]
	}
	val, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return val
}

func parseLastIntDefault(raw string, def int64) int64 {
	matches := regexp.MustCompile(`-?[0-9]+`).FindAllString(raw, -1)
	if len(matches) == 0 {
		return def
	}
	return parseIntDefault(matches[len(matches)-1], def)
}

func parseSize(raw string) int64 {
	raw = strings.TrimSpace(strings.ToUpper(raw))
	if raw == "" {
		return -1
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(raw, "K"):
		mult = 1024
		raw = strings.TrimSuffix(raw, "K")
	case strings.HasSuffix(raw, "M"):
		mult = 1024 * 1024
		raw = strings.TrimSuffix(raw, "M")
	case strings.HasSuffix(raw, "G"):
		mult = 1024 * 1024 * 1024
		raw = strings.TrimSuffix(raw, "G")
	case strings.HasSuffix(raw, "T"):
		mult = 1024 * 1024 * 1024 * 1024
		raw = strings.TrimSuffix(raw, "T")
	}
	val, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || val < 0 {
		return -1
	}
	const maxInt64 = int64(9223372036854775807)
	if mult != 0 && val > maxInt64/mult {
		return -1
	}
	return val * mult
}

// three-state pointer helpers
func i64p(v int64) *int64 { return &v }

func strp(v string) *string { return &v }

func derefI64(p *int64, def int64) int64 {
	if p != nil {
		return *p
	}
	return def
}

func derefStr(p *string, def string) string {
	if p != nil {
		return *p
	}
	return def
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}

func (a *App) deadlineExceeded() bool {
	return a.DeadlineMS > 0 && nowMS() >= a.DeadlineMS
}

func (a *App) boundedTimeout(timeout time.Duration) (time.Duration, error) {
	if a.DeadlineMS == 0 {
		return timeout, nil
	}
	remaining := time.Duration(a.DeadlineMS-nowMS()) * time.Millisecond
	if remaining <= 0 {
		return 0, errors.New("E_BOOT_TIMEOUT: boot apply timeout")
	}
	if remaining < timeout {
		return remaining, nil
	}
	return timeout, nil
}

func (a *App) runCommand(timeout time.Duration, name string, args ...string) error {
	bounded, err := a.boundedTimeout(timeout)
	if err != nil {
		return err
	}
	err = runCommand(bounded, name, args...)
	if err != nil && a.deadlineExceeded() {
		return fmt.Errorf("E_BOOT_TIMEOUT: %w", err)
	}
	return err
}

func (a *App) runCommandOut(timeout time.Duration, name string, args ...string) (string, error) {
	bounded, err := a.boundedTimeout(timeout)
	if err != nil {
		return "", err
	}
	out, err := runCommandOut(bounded, name, args...)
	if err != nil && a.deadlineExceeded() {
		return out, fmt.Errorf("E_BOOT_TIMEOUT: %w", err)
	}
	return out, err
}

func runCommand(timeout time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// runDaemon is the standing WebUI owner. It polls managed nodes and reconciles
// the saved profile onto the kernel only when an external actor has drifted a
// managed field.
// Exits 0 on SIGTERM/SIGINT or when the module is disabled.
func (a *App) runDaemon(args []string) int {
	var override time.Duration // --interval N forces a fixed cadence (testing)
	for i := 0; i < len(args); i++ {
		if args[i] == "--interval" && i+1 < len(args) {
			if n, err := strconv.Atoi(args[i+1]); err == nil && n >= 1 {
				override = time.Duration(n) * time.Second
			}
			i++
		}
	}

	_ = a.ensureDirs()
	var out io.Writer = os.Stderr
	if f, err := os.OpenFile(filepath.Join(a.LogDir, "daemon.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		out = f
		defer f.Close()
	}
	logln := func(format string, v ...any) {
		fmt.Fprintf(out, "[%d] "+format+"\n", append([]any{time.Now().Unix()}, v...)...)
	}

	unlockDaemon, locked, err := a.lockDaemon()
	if err != nil {
		logln("daemon lock: %v", err)
		return 1
	}
	if !locked {
		if pid := readTrim(filepath.Join(a.StateDir, "daemon.pid")); pid != "" {
			logln("daemon already running (pid %s); exits", pid)
		} else {
			logln("daemon already running; exits")
		}
		return 0
	}
	defer unlockDaemon()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	logln("daemon start (cadence: 1s pre-unlock, 60s post-unlock)")
	for {
		if a.moduleDisabled() {
			logln("module disabled; daemon exits")
			return 0
		}
		profile, _ := a.loadProfile()
		if profile.Mode == "diagnostic" {
			logln("diagnostic profile; idle hold (no takeover)")
		} else {
			a.enforceState(profile, logln)
		}
		interval := override
		if interval == 0 {
			interval = 1 * time.Second
			if a.userUnlocked() {
				interval = 60 * time.Second
			}
		}
		select {
		case s := <-sigCh:
			logln("signal %v; daemon exits", s)
			return 0
		case <-time.After(interval):
		}
	}
}

func (a *App) moduleDisabled() bool {
	for _, name := range []string{"disable", "remove"} {
		if _, err := os.Stat(filepath.Join(a.ModDir, name)); err == nil {
			return true
		}
	}
	return false
}

// userUnlocked reports whether first-unlock (FBE) has completed: /storage/emulated
// (or /data/media) becomes mounted, which we use to drop the poll cadence to 60s.
func (a *App) userUnlocked() bool {
	data, err := os.ReadFile(a.proc("mounts"))
	if err != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, "/storage/emulated") || strings.Contains(s, "/data/media")
}

// enforceState is the strong-takeover step: keep zram initialized at the right
// disksize with swap active, maintain loop backing when selected, then reconcile
// the user's explicitly-set runtime policy. Unset fields are left to the system.
func (a *App) enforceState(p *Profile, logln func(string, ...any)) {
	dev := p.Zram.Device
	z := a.zramStatus(dev)
	if !z.Found {
		logln("zram %s not found; skipping takeover", dev)
		return
	}
	z.ActiveSwap = a.swapStatus(dev, p.Swap.Enabled).Active
	want := a.effectiveDisksizeBytes(p)
	if z.Initstate == 0 || z.DisksizeBytes != want {
		logln("takeover rebuild: initstate=%d disksize=%d want=%d", z.Initstate, z.DisksizeBytes, want)
	}
	res, err := a.apply("full-control", p)
	if err != nil {
		logln("full-control apply: %v", err)
		return
	}
	if res.Degraded {
		logln("full-control degraded: %s", describeApplyIssues(res))
	}
	for _, s := range res.Steps {
		if s.Status == "changed" {
			logln("corrected %s: %q -> %q", s.Name, s.OldValue, s.NewValue)
		}
	}
	if !res.Degraded && (z.Initstate == 0 || z.DisksizeBytes != want) {
		logln("zram rebuilt to disksize=%d", want)
	}
}

// ensureLoopBacking maintains a module-owned writeback file mounted as a loop
// device. Binding it via hybridswap_loop_device is reported by the apply path.
func (a *App) ensureLoopBacking(p *Profile, logln func(string, ...any)) {
	size := int64(1) << 30
	if p.Backing.LoopSizeBytes != nil && *p.Backing.LoopSizeBytes > 0 {
		size = *p.Backing.LoopSizeBytes
	}
	_ = a.ensureDirs()
	path := filepath.Join(a.DataDir, "writeback.img")
	if fi, err := os.Stat(path); err != nil || fi.Size() != size {
		f, cerr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
		if cerr != nil {
			logln("loop file create: %v", cerr)
			return
		}
		f.Close()
		if err := os.Truncate(path, size); err != nil {
			logln("loop file resize: %v", err)
			return
		}
	}
	loopDev := a.resolveLoop(path, logln)
	if loopDev == "" {
		return
	}
}

// resolveLoop returns the loop device backing the given file, attaching a free
// one if the file is not already mounted.
func (a *App) resolveLoop(path string, logln func(string, ...any)) string {
	if out, err := a.runCommandOut(4*time.Second, "losetup", "-j", path); err == nil {
		if line := strings.TrimSpace(out); line != "" {
			if i := strings.IndexByte(line, ':'); i > 0 {
				return line[:i]
			}
		}
	}
	if out, err := a.runCommandOut(4*time.Second, "losetup", "-f", "--show", path); err == nil {
		return strings.TrimSpace(out)
	} else {
		logln("losetup attach: %v", err)
	}
	return ""
}

func (a *App) resolveLoopPath(path string) string {
	if out, err := a.runCommandOut(4*time.Second, "losetup", "-j", path); err == nil {
		if line := strings.TrimSpace(out); line != "" {
			if i := strings.IndexByte(line, ':'); i > 0 {
				return line[:i]
			}
		}
	}
	return ""
}

func (a *App) lockDaemon() (func(), bool, error) {
	if err := a.ensureDirs(); err != nil {
		return nil, false, err
	}
	lockPath := filepath.Join(a.StateDir, "daemon.pid")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, false, nil
		}
		return nil, false, err
	}
	if err := f.Truncate(0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, false, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, false, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, false, err
	}
	_ = f.Sync()
	return func() {
		_ = f.Truncate(0)
		_ = f.Sync()
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}

func describeApplyIssues(res ApplyResult) string {
	parts := append([]string{}, res.DegradedReasons...)
	for _, step := range res.Steps {
		if step.Status != "failed" && step.Status != "denied" {
			continue
		}
		msg := step.Name
		if step.Message != "" {
			msg += ": " + step.Message
		}
		parts = append(parts, msg)
	}
	if len(parts) == 0 {
		return "apply result was degraded without detailed errors"
	}
	return strings.Join(parts, "; ")
}

func runCommandOut(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

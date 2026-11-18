package main

// memcgv2.go — Android cgroup v2 memcg layout support.
//
// Android 17 no longer mounts the cgroup v1 memcg hierarchy at /dev/memcg.
// Memory is controlled on the unified hierarchy at /sys/fs/cgroup, with the
// memory controller enabled down to one memcg per UID:
//
//	/sys/fs/cgroup/apps/uid_<uid>/pid_<pid>    application processes
//	/sys/fs/cgroup/system/uid_<uid>/pid_<pid>  system processes
//
// Two things differ from the v1 per-package memcgs the rest of the backend
// was written against:
//
//   - processes live in the pid_<pid> children, so the memcg's own
//     cgroup.procs is empty; memcgProcs reads the whole subtree;
//   - no framework writes memory.name, so the driver names each memcg after
//     its cgroup directory ("uid_10229"). syncMemcgV2Names writes the owning
//     package name, which is what total_info_per_app rows, the aging state
//     keys and the label resolver expect.

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// memcgNameMax mirrors the driver's CHS_MEMCG_NAME_MAX (32, NUL included).
// The driver truncates longer names; comparing against the truncated form
// keeps the sync from rewriting the same long name every cycle.
const memcgNameMax = 31

const defaultPackagesList = "/data/system/packages.list"

var memcgV2UIDDir = regexp.MustCompile(`^uid_([0-9]+)$`)

// memcgProcs returns the PIDs of every process in dir and its descendant
// cgroups. On v1 the per-app memcg has no children and this is its own
// cgroup.procs; on v2 it collects the pid_<pid> children.
func memcgProcs(dir string) []string {
	seen := map[string]bool{}
	var pids []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		raw := readTrim(filepath.Join(path, "cgroup.procs"))
		if raw == "" {
			raw = readTrim(filepath.Join(path, "tasks"))
		}
		for _, pid := range strings.Fields(raw) {
			if _, err := strconv.ParseInt(pid, 10, 64); err != nil || seen[pid] {
				continue
			}
			seen[pid] = true
			pids = append(pids, pid)
		}
		return nil
	})
	return pids
}

// agingNonAppUID reports UIDs that must never be aged automatically: system
// UIDs (app id below 10000) and isolated/WebView UIDs (app id 90000-99999),
// for every Android user. On v1 these processes were not in per-app memcgs;
// on v2 every UID has its own memcg, including system_server's uid_1000.
func agingNonAppUID(uid int64) bool {
	if uid < 0 {
		return true
	}
	appID := uid % 100000
	return appID < 10000 || appID >= 90000
}

// readPackagesByAppID parses packages.list ("<package> <uid> ...") into app id
// -> sorted package names. Several packages can share one UID.
func readPackagesByAppID(path string) map[int64][]string {
	out := map[int64][]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		uid, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || uid < 0 {
			continue
		}
		appID := uid % 100000
		out[appID] = append(out[appID], fields[0])
	}
	for id := range out {
		sort.Strings(out[id])
	}
	return out
}

func truncateMemcgName(name string) string {
	if len(name) > memcgNameMax {
		return name[:memcgNameMax]
	}
	return name
}

// memcgV2PackageFor picks the package that owns a v2 per-UID memcg. A UID
// shared by several packages is resolved by the main process name of a live
// task; otherwise the lexically first package keeps the choice stable.
func (a *App) memcgV2PackageFor(dir string, pkgs []string) string {
	if len(pkgs) == 1 {
		return pkgs[0]
	}
	known := map[string]bool{}
	for _, pkg := range pkgs {
		known[pkg] = true
	}
	for _, pid := range memcgProcs(dir) {
		cmdline := readTrim(a.proc(filepath.Join(pid, "cmdline")))
		if i := strings.IndexByte(cmdline, 0); i >= 0 {
			cmdline = cmdline[:i]
		}
		if i := strings.IndexByte(cmdline, ':'); i >= 0 {
			cmdline = cmdline[:i]
		}
		if known[cmdline] {
			return cmdline
		}
	}
	return pkgs[0]
}

// syncMemcgV2Names names every v2 apps/uid_<uid> memcg after its package.
// Only names still at the driver default (the directory name, or "unknown")
// are replaced, so a name set by anyone else is left alone. It returns how
// many memcgs were renamed. It is a no-op on v1 kernels.
func (a *App) syncMemcgV2Names(logln func(string, ...any)) int {
	listPath := a.PackagesList
	if listPath == "" {
		return 0
	}
	root := a.memcgRoot()
	if root == "" || !a.exists(filepath.Join(root, "cgroup.controllers")) {
		return 0
	}
	appsDir := filepath.Join(root, "apps")
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return 0
	}
	var pkgsByAppID map[int64][]string
	renamed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		m := memcgV2UIDDir.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		dir := filepath.Join(appsDir, entry.Name())
		nameNode := filepath.Join(dir, "memory.name")
		current := readTrim(nameNode)
		if current != entry.Name() && current != "unknown" {
			continue
		}
		uid, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || agingNonAppUID(uid) {
			continue
		}
		if pkgsByAppID == nil {
			pkgsByAppID = readPackagesByAppID(listPath)
			if len(pkgsByAppID) == 0 {
				return 0
			}
		}
		pkgs := pkgsByAppID[uid%100000]
		if len(pkgs) == 0 {
			continue
		}
		name := truncateMemcgName(a.memcgV2PackageFor(dir, pkgs))
		if name == current {
			continue
		}
		if err := writeKernelNode(nameNode, []byte(name)); err != nil {
			if logln != nil {
				logln("memcg v2: name %s: %v", dir, err)
			}
			continue
		}
		renamed++
	}
	return renamed
}

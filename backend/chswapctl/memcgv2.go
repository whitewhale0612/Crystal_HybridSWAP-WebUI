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
// Processes live in the pid_<pid> children, so the memcg's own cgroup.procs
// is empty; memcgProcs reads the whole subtree.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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

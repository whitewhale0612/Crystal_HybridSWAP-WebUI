package main

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// v2Fixture builds an Android 17 style unified hierarchy under SysRoot:
// root nodes at fs/cgroup, per-UID memcgs with pid_<pid> children.
func v2Fixture(t *testing.T) (*App, string) {
	t.Helper()
	app := testApp(t)
	root := filepath.Join(app.SysRoot, "fs/cgroup")
	for _, dir := range []string{root, filepath.Join(app.ProcRoot)} {
		if err := mkdirAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	for node, val := range map[string]string{
		"cgroup.controllers":   "memory\n",
		"memory.zram_wm_ratio": "75\n",
	} {
		if err := writeFile(filepath.Join(root, node), val); err != nil {
			t.Fatal(err)
		}
	}
	return app, root
}

func v2AddMemcg(t *testing.T, app *App, root, rel string, uid int64, pids map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, rel)
	if err := mkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "memory.name"), filepath.Base(rel)+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "cgroup.procs"), ""); err != nil {
		t.Fatal(err)
	}
	for pid, cmdline := range pids {
		child := filepath.Join(dir, "pid_"+pid)
		if err := mkdirAll(child); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(filepath.Join(child, "cgroup.procs"), pid+"\n"); err != nil {
			t.Fatal(err)
		}
		procDir := filepath.Join(app.ProcRoot, pid)
		if err := mkdirAll(procDir); err != nil {
			t.Fatal(err)
		}
		status := "Name:\tx\nUid:\t" + itoa(uid) + "\t" + itoa(uid) + "\t" + itoa(uid) + "\t" + itoa(uid) + "\n"
		if err := writeFile(filepath.Join(procDir, "status"), status); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(filepath.Join(procDir, "cmdline"), cmdline+"\x00"); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestMemcgRootFindsCgroupV2(t *testing.T) {
	app, root := v2Fixture(t)
	if got := app.memcgRoot(); got != root {
		t.Fatalf("memcgRoot=%q want %q", got, root)
	}
}

func TestMemcgProcsReadsV2Children(t *testing.T) {
	app, root := v2Fixture(t)
	dir := v2AddMemcg(t, app, root, "apps/uid_10229", 10229,
		map[string]string{"100": "com.example", "101": "com.example:push"})
	got := memcgProcs(dir)
	sort.Strings(got)
	if strings.Join(got, ",") != "100,101" {
		t.Fatalf("memcgProcs=%v", got)
	}
	uids := app.memcgTaskUIDs(dir)
	if len(uids) != 1 || !uids[10229] {
		t.Fatalf("memcgTaskUIDs=%v", uids)
	}
}

func TestAgingNonAppUID(t *testing.T) {
	for uid, want := range map[int64]bool{
		-1: true, 0: true, 1000: true, 9999: true,
		10000: false, 10229: false, 89999: false,
		90000: true, 99001: true,
		1010229: false, 99910229: false, 99901000: true, 1099001: true,
	} {
		if got := agingNonAppUID(uid); got != want {
			t.Errorf("agingNonAppUID(%d)=%v want %v", uid, got, want)
		}
	}
}

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

func TestSyncMemcgV2Names(t *testing.T) {
	app, root := v2Fixture(t)
	single := v2AddMemcg(t, app, root, "apps/uid_10229", 10229,
		map[string]string{"200": "com.example.single"})
	shared := v2AddMemcg(t, app, root, "apps/uid_10300", 10300,
		map[string]string{"300": "com.example.b:remote"})
	clone := v2AddMemcg(t, app, root, "apps/uid_99910229", 99910229,
		map[string]string{"400": "com.example.single"})
	long := v2AddMemcg(t, app, root, "apps/uid_10400", 10400, nil)
	isolated := v2AddMemcg(t, app, root, "apps/uid_99001", 99001, nil)
	system := v2AddMemcg(t, app, root, "system/uid_1000", 1000, nil)
	vendorNamed := v2AddMemcg(t, app, root, "apps/uid_10500", 10500, nil)
	if err := writeFile(filepath.Join(vendorNamed, "memory.name"), "vendor.name\n"); err != nil {
		t.Fatal(err)
	}
	unknownPkg := v2AddMemcg(t, app, root, "apps/uid_10600", 10600, nil)

	list := filepath.Join(app.DataDir, "packages.list")
	if err := mkdirAll(app.DataDir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(list, strings.Join([]string{
		"com.example.single 10229 0 /data/user/0/com.example.single default:targetSdkVersion=34 3003",
		"com.example.a 10300 0 /data/user/0/com.example.a default 3003",
		"com.example.b 10300 0 /data/user/0/com.example.b default 3003",
		"com.example.this.package.name.is.long 10400 0 /data/x default none",
		"com.example.vendor 10500 0 /data/x default none",
		"android 1000 0 /data/system platform none",
		"",
	}, "\n")); err != nil {
		t.Fatal(err)
	}

	app.PackagesList = ""
	if n := app.syncMemcgV2Names(nil); n != 0 {
		t.Fatalf("naming must be disabled without packages.list, renamed=%d", n)
	}
	app.PackagesList = list
	if n := app.syncMemcgV2Names(nil); n != 4 {
		t.Fatalf("renamed=%d want 4", n)
	}
	for dir, want := range map[string]string{
		single:      "com.example.single",
		shared:      "com.example.b",
		clone:       "com.example.single",
		long:        "com.example.this.package.name.i",
		isolated:    "uid_99001",
		system:      "uid_1000",
		vendorNamed: "vendor.name",
		unknownPkg:  "uid_10600",
	} {
		if got := readTrim(filepath.Join(dir, "memory.name")); got != want {
			t.Errorf("%s memory.name=%q want %q", dir, got, want)
		}
	}
	if len("com.example.this.package.name.i") != memcgNameMax {
		t.Fatalf("truncation fixture out of sync with memcgNameMax")
	}
	// Names already synced (including truncated ones) are left alone.
	if n := app.syncMemcgV2Names(nil); n != 0 {
		t.Fatalf("second sync renamed=%d want 0", n)
	}
}

func TestSyncMemcgV2NamesNoopOnV1(t *testing.T) {
	app := testApp(t)
	memcg := filepath.Join(app.DevRoot, "memcg")
	dir := filepath.Join(memcg, "apps", "uid_10229")
	if err := mkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(memcg, "memory.zram_wm_ratio"), "75\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "memory.name"), "uid_10229\n"); err != nil {
		t.Fatal(err)
	}
	list := filepath.Join(app.DataDir, "packages.list")
	if err := mkdirAll(app.DataDir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(list, "com.example 10229 0 /data/x default none\n"); err != nil {
		t.Fatal(err)
	}
	app.PackagesList = list
	if n := app.syncMemcgV2Names(nil); n != 0 {
		t.Fatalf("v1 hierarchy must not be renamed, renamed=%d", n)
	}
}

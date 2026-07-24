package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDaemonLoggerSuppressesRepeatedMessages(t *testing.T) {
	dir := t.TempDir()
	logger := newDaemonLogger(dir)
	logger.Printf("repeated state")
	logger.Printf("repeated state")
	logger.Printf("different state")
	logger.Close()

	raw, err := os.ReadFile(filepath.Join(dir, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Count(text, "repeated state") != 1 {
		t.Fatalf("repeated message was not suppressed: %q", text)
	}
	if !strings.Contains(text, "different state") {
		t.Fatalf("independent message was lost: %q", text)
	}
}

func TestAgingKernelReadDoesNotResolveLabels(t *testing.T) {
	app := testApp(t)
	memcg := filepath.Join(app.DevRoot, "memcg")
	if err := mkdirAll(memcg); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(memcg, "memory.total_info_per_app"),
		"anon zram_c zram_p eswap_c eswap_p memcg_n score uid policy out_trig in_mb in_request in_req_pages in_eff_pages in_moved_pages in_ret\n"+
			"0 1 2 0 0 com.example 1 10234 0 0 0 0 0 0 0 0\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(memcg, "memory.force_swapout"), "0\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(memcg, "memory.force_swapin"), "0\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(memcg, "memory.zram_wm_ratio"), "75\n"); err != nil {
		t.Fatal(err)
	}
	app.commandOutFn = func(timeout time.Duration, name string, args ...string) (string, error) {
		t.Fatalf("kernel app read unexpectedly invoked %s", name)
		return "", nil
	}
	result := app.readAppsResult()
	if !result.Supported || len(result.Apps) != 1 || result.Apps[0].Label != "" {
		t.Fatalf("unexpected kernel-only result: %+v", result)
	}
}

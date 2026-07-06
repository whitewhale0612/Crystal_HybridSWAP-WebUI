package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	app, err := newApp()
	if err != nil {
		writeEnvelope(false, nil, apiError("E_INTERNAL", err.Error(), nil), nil)
		os.Exit(1)
	}
	if len(os.Args) >= 2 && os.Args[1] == "daemon" {
		os.Exit(app.runDaemon(os.Args[2:]))
	}
	if len(os.Args) < 2 || os.Args[1] != "api" {
		fmt.Fprintf(os.Stderr, "usage: %s api METHOD PATH [--json BODY]\n       %s daemon [--interval SEC]\n", os.Args[0], os.Args[0])
		os.Exit(2)
	}

	ok := app.handleAPI(os.Args[2:])
	if !ok {
		os.Exit(1)
	}
}

func newApp() (*App, error) {
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)
	modDir := os.Getenv("CHSWAP_MODDIR")
	if modDir == "" {
		if filepath.Base(exeDir) == "bin" {
			modDir = filepath.Dir(exeDir)
		} else if filepath.Base(exeDir) == "backend" {
			modDir = filepath.Join(filepath.Dir(exeDir), "module")
		} else {
			modDir = filepath.Dir(exeDir)
		}
	}
	modDir, _ = filepath.Abs(modDir)

	dataDir := getenvDefault("CHSWAP_DATA_DIR", filepath.Join(modDir, "data"))
	app := &App{
		SysRoot:        getenvDefault("CHSWAP_SYS_ROOT", "/sys"),
		ProcRoot:       getenvDefault("CHSWAP_PROC_ROOT", "/proc"),
		DevRoot:        getenvDefault("CHSWAP_DEV_ROOT", "/dev"),
		MemcgRoot:      os.Getenv("CHSWAP_MEMCG_ROOT"),
		ModDir:         modDir,
		DataDir:        dataDir,
		StateDir:       filepath.Join(dataDir, "state"),
		LogDir:         filepath.Join(dataDir, "logs"),
		DiagnosticsDir: filepath.Join(dataDir, "diagnostics"),
		KSUD:           os.Getenv("CHSWAP_KSUD"),
		SkipKSUD:       os.Getenv("CHSWAP_SKIP_KSUD") == "1",
		ForceWaitMS:    parseIntDefault(os.Getenv("CHSWAP_FORCE_WAIT_MS"), 0),
	}
	if app.KSUD == "" {
		if _, err := os.Stat("/data/adb/ksud"); err == nil {
			app.KSUD = "/data/adb/ksud"
		} else if path, err := exec.LookPath("ksud"); err == nil {
			app.KSUD = path
		}
	}
	return app, nil
}

func getenvDefault(name string, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

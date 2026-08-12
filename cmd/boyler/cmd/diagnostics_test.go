package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	buildversion "boyler/internal/version"
)

func TestDiagnosticCommandsAreRegistered(t *testing.T) {
	for _, path := range [][]string{{"doctor"}, {"daemon", "status"}, {"system", "info"}, {"version"}} {
		command, remaining, err := rootCmd.Find(path)
		if err != nil || len(remaining) != 0 || command == rootCmd {
			t.Fatalf("command %v was not registered: command=%v remaining=%v error=%v", path, command, remaining, err)
		}
	}
}

func TestProbeDaemon(t *testing.T) {
	missing := probeDaemon(filepath.Join(t.TempDir(), "missing.sock"), time.Second)
	if missing.Reachable || missing.Error == "" {
		t.Fatalf("missing status = %#v", missing)
	}
	unconfigured := probeDaemon("", time.Second)
	if unconfigured.Reachable || unconfigured.Error != "UNIX_SOCKET is not configured" {
		t.Fatalf("unconfigured status = %#v", unconfigured)
	}
}

func TestVersionJSON(t *testing.T) {
	oldVersion, oldCommit, oldDate := buildversion.Version, buildversion.Commit, buildversion.BuildDate
	buildversion.Version, buildversion.Commit, buildversion.BuildDate = "v1.2.3", "abc123", "2026-08-13T00:00:00Z"
	t.Cleanup(func() {
		buildversion.Version, buildversion.Commit, buildversion.BuildDate = oldVersion, oldCommit, oldDate
	})
	oldJSON := versionJSON
	versionJSON = true
	t.Cleanup(func() { versionJSON = oldJSON })
	var output bytes.Buffer
	versionCmd.SetOut(&output)
	t.Cleanup(func() { versionCmd.SetOut(nil) })

	if err := versionCmd.RunE(versionCmd, nil); err != nil {
		t.Fatal(err)
	}
	var actual buildversion.Info
	if err := json.Unmarshal(output.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Version != "v1.2.3" || actual.Commit != "abc123" || actual.OS != runtime.GOOS {
		t.Fatalf("version info = %#v", actual)
	}
}

func TestPathCheckValidatesTypeAndExecutableBit(t *testing.T) {
	directory := t.TempDir()
	if check := pathCheck("directory", directory, false); check.Status != checkPass {
		t.Fatalf("directory check = %#v", check)
	}
	if check := pathCheck("runtime", directory, true); check.Status != checkFail {
		t.Fatalf("runtime directory check = %#v", check)
	}
	if check := pathCheck("missing", "", false); check.Status != checkFail {
		t.Fatalf("missing path check = %#v", check)
	}
}

func TestSystemInformationHasStableIdentityFields(t *testing.T) {
	info := collectSystemInformation()
	if info.OS != runtime.GOOS || info.Architecture != runtime.GOARCH || info.GoVersion == "" || info.Kernel == "" {
		t.Fatalf("system information = %#v", info)
	}
}

package system

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	systemservice "boyler/internal/daemon/application/system_service"
)

func TestInspectorReportsInvalidRuntimeAndStorage(t *testing.T) {
	root := t.TempDir()
	inspector := NewInspector(Config{
		RuntimePath:    filepath.Join(root, "missing-runtime"),
		ImagesPath:     filepath.Join(root, "missing-images"),
		ContainersPath: filepath.Join(root, "missing-containers"),
	})
	report, err := inspector.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"RUNTIME_NOT_EXECUTABLE", "IMAGE_STORAGE_UNAVAILABLE", "CONTAINER_STORAGE_UNAVAILABLE"} {
		if !hasFailedCode(report.Checks, code) {
			t.Fatalf("missing failed check %s: %#v", code, report.Checks)
		}
	}
}

func TestInspectorAcceptsExecutableAndDirectories(t *testing.T) {
	root := t.TempDir()
	runtimePath := filepath.Join(root, "myrunc")
	if err := os.WriteFile(runtimePath, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	inspector := NewInspector(Config{RuntimePath: runtimePath, ImagesPath: root, ContainersPath: root})
	report, err := inspector.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"RUNTIME_NOT_EXECUTABLE", "IMAGE_STORAGE_UNAVAILABLE", "CONTAINER_STORAGE_UNAVAILABLE"} {
		for _, check := range report.Checks {
			if check.Code == code && check.Status != systemservice.StatusPass {
				t.Fatalf("check %s = %#v", code, check)
			}
		}
	}
}

func hasFailedCode(checks []systemservice.Check, code string) bool {
	for _, check := range checks {
		if check.Code == code && check.Status == systemservice.StatusFail {
			return true
		}
	}
	return false
}

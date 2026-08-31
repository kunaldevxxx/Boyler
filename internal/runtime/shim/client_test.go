package shim

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestWaitForStatusSuccess(t *testing.T) {
	dir := t.TempDir()
	c := &Client{stateDir: dir}
	id := "test-container"
	_ = os.MkdirAll(c.shimDir(id), 0755)

	go func() {
		time.Sleep(50 * time.Millisecond)
		data, _ := json.Marshal(ShimState{ID: id, Status: "created"})
		_ = os.WriteFile(c.stateFilePath(id), data, 0644)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.waitForStatus(ctx, id, "created"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForStatusShimError(t *testing.T) {
	dir := t.TempDir()
	c := &Client{stateDir: dir}
	id := "test-container"
	_ = os.MkdirAll(c.shimDir(id), 0755)

	go func() {
		time.Sleep(50 * time.Millisecond)
		data, _ := json.Marshal(ShimState{ID: id, Status: "error", ErrorMsg: "myrunc create failed"})
		_ = os.WriteFile(c.stateFilePath(id), data, 0644)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.waitForStatus(ctx, id, "created")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWaitForStatusTimeout(t *testing.T) {
	dir := t.TempDir()
	c := &Client{stateDir: dir}
	id := "test-container"
	_ = os.MkdirAll(c.shimDir(id), 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := c.waitForStatus(ctx, id, "created")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestReadShimState(t *testing.T) {
	dir := t.TempDir()
	c := &Client{stateDir: dir}
	id := "test-container"
	_ = os.MkdirAll(c.shimDir(id), 0755)

	want := ShimState{ID: id, PID: 123, Status: "running", BundlePath: "/tmp/b"}
	data, _ := json.Marshal(want)
	_ = os.WriteFile(c.stateFilePath(id), data, 0644)

	got, err := c.readShimState(id)
	if err != nil {
		t.Fatalf("readShimState: %v", err)
	}
	if got.ID != want.ID || got.PID != want.PID || got.Status != want.Status {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

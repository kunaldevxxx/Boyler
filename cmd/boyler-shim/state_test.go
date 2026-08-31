package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestWriteStateRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/shim.json"
	want := shimState{
		ID:         "abc123",
		PID:        42,
		Status:     "created",
		BundlePath: "/tmp/bundle",
		SocketPath: "/tmp/shim.sock",
		StartedAt:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := writeState(path, want); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got shimState
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ID != want.ID || got.PID != want.PID || got.Status != want.Status || got.BundlePath != want.BundlePath {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestWriteStateAtomic(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/shim.json"

	if err := writeState(path, shimState{ID: "first", Status: "created"}); err != nil {
		t.Fatal(err)
	}
	if err := writeState(path, shimState{ID: "second", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var got shimState
	_ = json.Unmarshal(data, &got)
	if got.ID != "second" {
		t.Errorf("expected second write to win, got %q", got.ID)
	}
}

func TestWriteStateErrorState(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/shim.json"
	st := shimState{ID: "c1", Status: "error", ErrorMsg: "myrunc failed"}
	if err := writeState(path, st); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var got shimState
	_ = json.Unmarshal(data, &got)
	if got.Status != "error" || got.ErrorMsg != "myrunc failed" {
		t.Errorf("unexpected state: %+v", got)
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// shimState mirrors internal/runtime/shim.ShimState and is written to
// shim.json so the daemon can read container state without a socket call.
type shimState struct {
	ID         string    `json:"id"`
	PID        int       `json:"pid"`
	Status     string    `json:"status"` // created | running | stopped | error
	BundlePath string    `json:"bundle"`
	SocketPath string    `json:"socket"`
	StartedAt  time.Time `json:"startedAt"`
	ErrorMsg   string    `json:"error_msg,omitempty"`
}

// writeState atomically replaces the state file using a temp-file rename.
func writeState(path string, st shimState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".shim-state-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

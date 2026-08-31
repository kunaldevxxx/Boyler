package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type shimState struct {
	ID         string    `json:"id"`
	PID        int       `json:"pid"`
	Status     string    `json:"status"`
	BundlePath string    `json:"bundle"`
	SocketPath string    `json:"socket"`
	StartedAt  time.Time `json:"startedAt"`
	ErrorMsg   string    `json:"error_msg,omitempty"`
}

// writeState atomically replaces the state file using a temp-file rename.
// retErr is a named return so the deferred cleanup can see whether to remove
// the temp file.
func writeState(path string, st shimState) (retErr error) {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".shim-state-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename state file: %w", err)
	}
	return nil
}

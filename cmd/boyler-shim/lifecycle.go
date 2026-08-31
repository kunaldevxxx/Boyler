package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

const watchInterval = 500 * time.Millisecond

type myruncStateFile struct {
	ID         string `json:"id"`
	PID        int    `json:"pid"`
	OciVersion string `json:"ociVersion"`
	Status     string `json:"status"`
	BundlePath string `json:"bundle"`
}

type manager struct {
	id         string
	bundlePath string
	myruncPath string
	shimDir    string
	statePath  string
	sockPath   string

	pid     int
	stopped atomic.Bool
}

func newManager(id, bundlePath, myruncPath, stateDir string) *manager {
	dir := filepath.Join(stateDir, id)
	return &manager{
		id:         id,
		bundlePath: bundlePath,
		myruncPath: myruncPath,
		shimDir:    dir,
		statePath:  filepath.Join(dir, "shim.json"),
		sockPath:   filepath.Join(dir, "shim.sock"),
	}
}

func (m *manager) create() error {
	if err := os.MkdirAll(m.shimDir, 0755); err != nil {
		return fmt.Errorf("create shim dir: %w", err)
	}
	cmd := exec.Command(m.myruncPath, "create", m.id, "--bundle", m.bundlePath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("myrunc create: %w", err)
	}

	pid, err := m.readMyruncPID()
	if err != nil {
		return err
	}
	m.pid = pid

	return writeState(m.statePath, shimState{
		ID:         m.id,
		PID:        pid,
		Status:     "created",
		BundlePath: m.bundlePath,
		SocketPath: m.sockPath,
		StartedAt:  time.Now(),
	})
}

func (m *manager) readMyruncPID() (int, error) {
	myruncStateDir := os.Getenv("STATE_PATH_MYRUNC")
	if myruncStateDir == "" {
		myruncStateDir = "/var/run/myrunc"
	}
	path := filepath.Join(myruncStateDir, m.id, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read myrunc state.json: %w", err)
	}
	var st myruncStateFile
	if err := json.Unmarshal(data, &st); err != nil {
		return 0, fmt.Errorf("parse myrunc state.json: %w", err)
	}
	return st.PID, nil
}

func (m *manager) run() error {
	cmd := exec.Command(m.myruncPath, "run", m.id)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("myrunc run: %w", err)
	}
	return m.updateStatus("running")
}

func (m *manager) kill(signal string) error {
	cmd := exec.Command(m.myruncPath, "kill", m.id, signal)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("myrunc kill: %w", err)
	}
	return nil
}

func (m *manager) delete() error {
	cmd := exec.Command(m.myruncPath, "delete", m.id)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("myrunc delete: %w", err)
	}
	if err := os.RemoveAll(m.shimDir); err != nil {
		return fmt.Errorf("remove shim dir: %w", err)
	}
	return nil
}

func (m *manager) writeErrorState(cause error) error {
	return writeState(m.statePath, shimState{
		ID:         m.id,
		Status:     "error",
		BundlePath: m.bundlePath,
		SocketPath: m.sockPath,
		StartedAt:  time.Now(),
		ErrorMsg:   cause.Error(),
	})
}

func (m *manager) updateStatus(status string) error {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return err
	}
	var st shimState
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	st.Status = status
	return writeState(m.statePath, st)
}

func (m *manager) watchContainer() {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for range ticker.C {
		if m.stopped.Load() {
			return
		}
		if !processAlive(m.pid) {
			if err := m.updateStatus("stopped"); err != nil {
				return
			}
			m.stopped.Store(true)
			return
		}
	}
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrProcessDone) {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if errno == syscall.ESRCH {
			return false
		}
		if errno == syscall.EPERM {
			return true
		}
	}
	return false
}

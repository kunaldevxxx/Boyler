package shim

import (
	"boyler/internal/runtime"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Client implements runtime.Runtime by delegating to a per-container shim
// process. The shim runs in its own session (Setsid) so it survives daemon
// crashes, keeping the container alive and reachable.
type Client struct {
	shimBinPath string // path to the boyler-shim binary
	stateDir    string // root directory for per-container shim state
	myruncPath  string // path to the myrunc binary
}

func NewClient(shimBinPath, stateDir, myruncPath string) runtime.Runtime {
	return &Client{
		shimBinPath: shimBinPath,
		stateDir:    stateDir,
		myruncPath:  myruncPath,
	}
}

func (c *Client) shimDir(id string) string        { return filepath.Join(c.stateDir, id) }
func (c *Client) stateFilePath(id string) string   { return filepath.Join(c.shimDir(id), "shim.json") }
func (c *Client) socketPath(id string) string      { return filepath.Join(c.shimDir(id), "shim.sock") }

// Create spawns a detached shim process for the container and blocks until
// the shim reports that the container has reached the "created" state.
func (c *Client) Create(ctx context.Context, id string, bundlePath string) error {
	absBundlePath, err := filepath.Abs(bundlePath)
	if err != nil {
		return fmt.Errorf("invalid bundle path: %w", err)
	}

	cmd := exec.CommandContext(ctx, c.shimBinPath, "start",
		"--id", id,
		"--bundle", absBundlePath,
		"--myrunc", c.myruncPath,
		"--state-dir", c.stateDir,
	)
	// Setsid puts the shim in its own session: it won't receive signals
	// delivered to the daemon's process group and is adopted by init on
	// daemon crash.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn shim: %w", err)
	}
	// Reap the shim when it eventually exits to avoid zombies.
	go func() { _ = cmd.Wait() }()

	if err := c.waitForStatus(ctx, id, "created"); err != nil {
		return fmt.Errorf("shim did not reach created state: %w", err)
	}
	return nil
}

// waitForStatus polls shim.json until it sees wantStatus or ctx is done.
func (c *Client) waitForStatus(ctx context.Context, id, wantStatus string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			st, err := c.readShimState(id)
			if err != nil {
				continue // file may not exist yet
			}
			if st.Status == wantStatus {
				return nil
			}
			if st.Status == "error" {
				return fmt.Errorf("shim error: %s", st.ErrorMsg)
			}
		}
	}
}

func (c *Client) readShimState(id string) (*ShimState, error) {
	data, err := os.ReadFile(c.stateFilePath(id))
	if err != nil {
		return nil, err
	}
	var st ShimState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Run sends the run command to the shim which signals the container to start.
func (c *Client) Run(ctx context.Context, id string) error {
	return c.send(ctx, id, Request{Cmd: "run"})
}

// Kill sends a kill command with the specified signal.
func (c *Client) Kill(ctx context.Context, id string, signal os.Signal) error {
	sigNum := fmt.Sprintf("%d", signal.(syscall.Signal))
	return c.send(ctx, id, Request{Cmd: "kill", Signal: sigNum})
}

// Delete instructs the shim to delete the container and exit.
func (c *Client) Delete(ctx context.Context, id string) error {
	return c.send(ctx, id, Request{Cmd: "delete"})
}

// State reads shim.json directly without a socket round-trip. This is fast
// and works even if the shim socket is temporarily unavailable.
func (c *Client) State(ctx context.Context, id string) (*runtime.State, error) {
	st, err := c.readShimState(id)
	if err != nil {
		return nil, fmt.Errorf("read shim state for %s: %w", id, err)
	}
	return &runtime.State{
		ID:         st.ID,
		PID:        st.PID,
		OciVerion:  runtime.OCI_VERSION,
		Status:     runtime.Status(st.Status),
		BundlePath: st.BundlePath,
	}, nil
}

// ExecPTY opens a PTY attached to the container's namespaces via nsenter.
func (c *Client) ExecPTY(ctx context.Context, pid int64) (io.ReadWriteCloser, error) {
	cmd := exec.CommandContext(
		ctx,
		"sudo", "nsenter",
		"-t", strconv.FormatInt(pid, 10),
		"-m", "-u", "-i", "-n", "-p", "-r", "-w",
		"/bin/sh",
	)
	file, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &ptyProcess{ReadWriteCloser: file, cmd: cmd}, nil
}

type ptyProcess struct {
	io.ReadWriteCloser
	cmd *exec.Cmd
}

func (p *ptyProcess) Close() error {
	err := p.ReadWriteCloser.Close()
	_ = p.cmd.Wait()
	return err
}

// send dials the shim socket, writes a request, and reads the response.
func (c *Client) send(ctx context.Context, id string, req Request) error {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", c.socketPath(id))
	if err != nil {
		return fmt.Errorf("connect to shim %s: %w", id, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("send to shim: %w", err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("decode shim response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("shim rejected %s: %s", req.Cmd, resp.Error)
	}
	return nil
}

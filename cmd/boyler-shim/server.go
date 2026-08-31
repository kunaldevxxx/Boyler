package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
)

// ipcRequest and ipcResponse are the wire types for daemon <-> shim IPC.
// They deliberately mirror internal/runtime/shim.{Request,Response} so both
// sides decode the same JSON without a shared import.
type ipcRequest struct {
	Cmd    string `json:"cmd"`
	Signal string `json:"signal,omitempty"`
}

type ipcResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// server listens on the shim Unix socket and dispatches daemon commands to the
// container manager. It is the long-running loop of the shim process.
type server struct {
	mgr *manager
}

func newServer(mgr *manager) *server {
	return &server{mgr: mgr}
}

// run creates the container, starts the socket listener, then serves requests
// until the socket is explicitly closed (on delete).
func (s *server) run() error {
	if err := s.mgr.create(); err != nil {
		s.mgr.writeErrorState(err)
		return fmt.Errorf("create container: %w", err)
	}

	_ = os.Remove(s.mgr.sockPath)
	ln, err := net.Listen("unix", s.mgr.sockPath)
	if err != nil {
		return fmt.Errorf("listen on shim socket %s: %w", s.mgr.sockPath, err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Accept fails when ln is closed (delete path); treat as clean exit.
			return nil
		}
		s.handleConn(conn, ln)
	}
}

// handleConn processes a single daemon request synchronously. Delete closes
// the listener so the serve loop exits after this call returns.
func (s *server) handleConn(conn net.Conn, ln net.Listener) {
	defer conn.Close()

	var req ipcRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(ipcResponse{Error: "decode request: " + err.Error()})
		return
	}

	var resp ipcResponse
	switch req.Cmd {
	case "run":
		if err := s.mgr.run(); err != nil {
			resp.Error = err.Error()
		} else {
			resp.OK = true
			go s.mgr.watchContainer()
		}

	case "kill":
		if err := s.mgr.kill(req.Signal); err != nil {
			resp.Error = err.Error()
		} else {
			resp.OK = true
		}

	case "delete":
		resp.OK = true
		_ = json.NewEncoder(conn).Encode(resp)
		s.mgr.delete()
		ln.Close() // unblock Accept → clean exit
		return

	default:
		resp.Error = fmt.Sprintf("unknown command %q", req.Cmd)
	}

	_ = json.NewEncoder(conn).Encode(resp)
}

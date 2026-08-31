package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
)

type ipcRequest struct {
	Cmd    string `json:"cmd"`
	Signal string `json:"signal,omitempty"`
}

type ipcResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type server struct {
	mgr *manager
}

func newServer(mgr *manager) *server {
	return &server{mgr: mgr}
}

func (s *server) run() error {
	if err := s.mgr.create(); err != nil {
		stateErr := s.mgr.writeErrorState(err)
		return errors.Join(fmt.Errorf("create container: %w", err), stateErr)
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
			return nil
		}
		s.handleConn(conn, ln)
	}
}

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
		if err := s.mgr.delete(); err != nil {
			resp.Error = err.Error()
			_ = json.NewEncoder(conn).Encode(resp)
			return
		}
		resp.OK = true
		_ = json.NewEncoder(conn).Encode(resp)
		ln.Close()
		return

	default:
		resp.Error = fmt.Sprintf("unknown command %q", req.Cmd)
	}

	_ = json.NewEncoder(conn).Encode(resp)
}

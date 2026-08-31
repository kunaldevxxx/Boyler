package shim

import "time"

// ShimState is persisted to disk and served over the IPC socket so the
// daemon can recover container state after a crash without talking to the
// container runtime again.
type ShimState struct {
	ID         string    `json:"id"`
	PID        int       `json:"pid"`
	Status     string    `json:"status"` // created | running | stopped | error
	BundlePath string    `json:"bundle"`
	SocketPath string    `json:"socket"`
	StartedAt  time.Time `json:"startedAt"`
	ErrorMsg   string    `json:"error_msg,omitempty"`
}

// Request is the JSON message the daemon sends to the shim over the socket.
type Request struct {
	Cmd    string `json:"cmd"`            // state | run | kill | delete
	Signal string `json:"signal,omitempty"` // signal number for kill
}

// Response is the JSON reply the shim sends back.
type Response struct {
	OK    bool       `json:"ok"`
	Error string     `json:"error,omitempty"`
	State *ShimState `json:"state,omitempty"`
}

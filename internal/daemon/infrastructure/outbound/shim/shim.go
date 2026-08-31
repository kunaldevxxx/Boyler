// Package shim documents the role of the shim layer in Boyler's container
// architecture.
//
// The shim is a thin, long-lived process that sits between the daemon and
// myrunc. One shim process is created for each container. Because the shim
// runs in its own session (POSIX setsid), it survives daemon crashes: the
// container keeps running, the shim keeps tracking it, and after a daemon
// restart the daemon can re-read the on-disk state written by the shim.
//
//	[daemon]  --Setsid spawn-->  [boyler-shim]  --exec-->  [myrunc]  --fork-->  [container init]
//	             |                     |
//	             |  Unix socket IPC    |  shim.json (state file)
//	             +<--------------------+
//
// The daemon-side client that implements runtime.Runtime and talks to the shim
// lives in internal/runtime/shim. The shim binary itself lives in cmd/boyler-shim.
package shim

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"boyler/cmd/boyler/cmd/ui"
	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
	buildversion "boyler/internal/version"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type daemonStatus struct {
	Status        string `json:"status"`
	Health        string `json:"health"`
	Socket        string `json:"socket"`
	Version       string `json:"version,omitempty"`
	APIVersion    string `json:"apiVersion,omitempty"`
	StartedAt     string `json:"startedAt,omitempty"`
	Reachable     bool   `json:"reachable"`
	Compatible    bool   `json:"compatible"`
	PID           int32  `json:"pid,omitempty"`
	UptimeSeconds int64  `json:"uptimeSeconds,omitempty"`
	Error         string `json:"error,omitempty"`
}

var daemonStatusJSON bool

var daemonCmd = &cobra.Command{Use: "daemon", Short: "Manage and inspect the Boyler daemon", GroupID: groupSystem}

var daemonStatusCmd = &cobra.Command{
	Use: "status", Short: "Check whether the Boyler daemon is healthy", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		loadEnv()
		ctx, cancel := context.WithTimeout(cmd.Context(), daemonRequestTimeout)
		defer cancel()
		status := inspectDaemon(ctx)
		if daemonStatusJSON {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(status); err != nil {
				return err
			}
		} else {
			printDaemonStatus(cmd, status)
		}
		if !status.Reachable {
			return fmt.Errorf("daemon is unavailable: %s", status.Error)
		}
		if !status.Compatible {
			return fmt.Errorf("CLI API %s is incompatible with daemon API %s", buildversion.APIVersion, status.APIVersion)
		}
		return nil
	},
}

func init() {
	daemonStatusCmd.Flags().BoolVar(&daemonStatusJSON, "json", false, "Print daemon status as JSON")
	daemonCmd.AddCommand(daemonStatusCmd)
	rootCmd.AddCommand(daemonCmd)
}

func inspectDaemon(ctx context.Context) daemonStatus {
	socket := os.Getenv("UNIX_SOCKET")
	result := daemonStatus{Status: "stopped", Health: "not-serving", Socket: socket}
	if err := validateSocket(socket); err != nil {
		result.Error = err.Error()
		return result
	}
	client, connection, err := NewGrpcDaemonInspectionClient()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer connection.Close()
	health, err := grpc_health_v1.NewHealthClient(connection).Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: pb.DaemonInspectionService_ServiceDesc.ServiceName})
	if err != nil {
		result.Error = commandError(err).Error()
		return result
	}
	if health.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		result.Error = "gRPC health service is not serving"
		return result
	}
	version, err := client.Version(ctx, &pb.VersionRequest{})
	if err != nil {
		result.Error = commandError(err).Error()
		return result
	}
	info, err := client.SystemInfo(ctx, &pb.SystemInfoRequest{})
	if err != nil {
		result.Error = commandError(err).Error()
		return result
	}
	result.Status, result.Health, result.Reachable = "running", "serving", true
	result.Version, result.APIVersion = version.Version, version.ApiVersion
	result.Compatible = version.ApiVersion == buildversion.APIVersion
	result.PID, result.StartedAt, result.UptimeSeconds = info.DaemonPid, info.DaemonStartedAt, info.DaemonUptimeSeconds
	return result
}

func validateSocket(socket string) error {
	if socket == "" {
		return fmt.Errorf("UNIX_SOCKET is not configured")
	}
	if !filepath.IsAbs(socket) {
		return fmt.Errorf("UNIX_SOCKET must be an absolute path")
	}
	info, err := os.Lstat(socket)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("configured socket must not be a symlink")
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("configured path is not a Unix socket")
	}
	return nil
}

// probeDaemon remains a lightweight socket probe for local diagnostics and tests.
func probeDaemon(socket string, timeout time.Duration) daemonStatus {
	result := daemonStatus{Status: "stopped", Socket: socket}
	if err := validateSocket(socket); err != nil {
		result.Error = err.Error()
		return result
	}
	connection, err := net.DialTimeout("unix", socket, timeout)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	_ = connection.Close()
	result.Status, result.Reachable = "running", true
	return result
}

func printDaemonStatus(cmd *cobra.Command, status daemonStatus) {
	theme := ui.NewTheme(cmd.OutOrStdout(), colorMode.value)
	if !status.Reachable {
		fmt.Fprintf(cmd.OutOrStdout(), "%s Boyler daemon is unavailable\n", theme.Error(theme.Symbol("✗", "-")))
		if status.Socket != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  socket: %s\n", status.Socket)
		}
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s Boyler daemon is running\n", theme.Success(theme.Symbol("✓", "+")))
	fmt.Fprintf(cmd.OutOrStdout(), "  socket:  %s\n  health:  %s\n  version: %s\n  API:     %s\n  PID:     %d\n  uptime:  %s\n", status.Socket, status.Health, status.Version, status.APIVersion, status.PID, (time.Duration(status.UptimeSeconds) * time.Second).String())
}

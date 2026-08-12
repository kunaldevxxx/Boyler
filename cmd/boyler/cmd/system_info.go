package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
	"github.com/spf13/cobra"
)

type systemInformation struct {
	OS                  string   `json:"os"`
	Architecture        string   `json:"architecture"`
	Kernel              string   `json:"kernel"`
	Hostname            string   `json:"hostname"`
	GoVersion           string   `json:"goVersion"`
	CgroupVersion       string   `json:"cgroupVersion"`
	CgroupControllers   []string `json:"cgroupControllers,omitempty"`
	OverlayFS           bool     `json:"overlayFS"`
	Root                bool     `json:"root"`
	IPv4Forwarding      bool     `json:"ipv4Forwarding"`
	RuntimePath         string   `json:"runtimePath,omitempty"`
	ImagesPath          string   `json:"imagesPath,omitempty"`
	ContainersPath      string   `json:"containersPath,omitempty"`
	DaemonStartedAt     string   `json:"daemonStartedAt,omitempty"`
	DaemonUptimeSeconds int64    `json:"daemonUptimeSeconds,omitempty"`
	DaemonPID           int32    `json:"daemonPid,omitempty"`
}

var systemInfoJSON, systemInfoLocal bool

var systemCmd = &cobra.Command{Use: "system", Short: "Inspect the host system", GroupID: groupSystem}

var systemInfoCmd = &cobra.Command{
	Use: "info", Short: "Show host capabilities used by Boyler", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		var info systemInformation
		if systemInfoLocal {
			info = collectSystemInformation()
		} else {
			loadEnv()
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
			defer cancel()
			remote, err := daemonSystemInformation(ctx)
			if err != nil {
				return err
			}
			info = remote
		}
		if systemInfoJSON {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(info)
		}
		printSystemInformation(cmd, info)
		return nil
	},
}

func init() {
	systemInfoCmd.Flags().BoolVar(&systemInfoJSON, "json", false, "Print system information as JSON")
	systemInfoCmd.Flags().BoolVar(&systemInfoLocal, "local", false, "Inspect the CLI process instead of the daemon")
	systemCmd.AddCommand(systemInfoCmd)
	rootCmd.AddCommand(systemCmd)
}

func daemonSystemInformation(ctx context.Context) (systemInformation, error) {
	if err := validateSocket(os.Getenv("UNIX_SOCKET")); err != nil {
		return systemInformation{}, err
	}
	client, connection, err := NewGrpcDaemonInspectionClient()
	if err != nil {
		return systemInformation{}, err
	}
	defer connection.Close()
	value, err := client.SystemInfo(ctx, &pb.SystemInfoRequest{})
	if err != nil {
		return systemInformation{}, commandError(err)
	}
	return systemInformation{
		OS: value.Os, Architecture: value.Architecture, Kernel: value.Kernel, Hostname: value.Hostname,
		GoVersion: value.GoVersion, CgroupVersion: value.CgroupVersion, CgroupControllers: value.CgroupControllers,
		OverlayFS: value.OverlayFs, Root: value.Root, IPv4Forwarding: value.Ipv4Forwarding,
		RuntimePath: value.RuntimePath, ImagesPath: value.ImagesPath, ContainersPath: value.ContainersPath,
		DaemonStartedAt: value.DaemonStartedAt, DaemonUptimeSeconds: value.DaemonUptimeSeconds, DaemonPID: value.DaemonPid,
	}, nil
}

func collectSystemInformation() systemInformation {
	hostname, _ := os.Hostname()
	kernel := readTrimmedFile("/proc/sys/kernel/osrelease")
	if kernel == "" {
		kernel = "unknown"
	}
	return systemInformation{OS: runtime.GOOS, Architecture: runtime.GOARCH, Kernel: kernel, Hostname: hostname,
		GoVersion: runtime.Version(), CgroupVersion: detectCgroupVersion(), CgroupControllers: strings.Fields(readTrimmedFile("/sys/fs/cgroup/cgroup.controllers")),
		OverlayFS: overlayFSSupported(), Root: os.Geteuid() == 0, IPv4Forwarding: readTrimmedFile("/proc/sys/net/ipv4/ip_forward") == "1"}
}

func detectCgroupVersion() string {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return "v2"
	}
	if _, err := os.Stat("/sys/fs/cgroup"); err == nil {
		return "v1"
	}
	return "unavailable"
}

func overlayFSSupported() bool {
	filesystems, err := os.ReadFile("/proc/filesystems")
	return err == nil && strings.Contains(string(filesystems), "overlay")
}

func readTrimmedFile(path string) string {
	value, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}

func printSystemInformation(cmd *cobra.Command, info systemInformation) {
	fmt.Fprintf(cmd.OutOrStdout(), "Operating system:  %s\nArchitecture:      %s\nKernel:            %s\nHostname:          %s\nGo:                %s\nCgroups:           %s\nOverlayFS:         %t\nIPv4 forwarding:  %t\nRunning as root:   %t\n", info.OS, info.Architecture, info.Kernel, info.Hostname, info.GoVersion, info.CgroupVersion, info.OverlayFS, info.IPv4Forwarding, info.Root)
	if info.DaemonPID != 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Daemon PID:        %d\nDaemon uptime:     %s\n", info.DaemonPID, (time.Duration(info.DaemonUptimeSeconds) * time.Second).String())
	}
}

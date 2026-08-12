package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

type systemInformation struct {
	OS            string `json:"os"`
	Architecture  string `json:"architecture"`
	Kernel        string `json:"kernel"`
	Hostname      string `json:"hostname"`
	GoVersion     string `json:"goVersion"`
	CgroupVersion string `json:"cgroupVersion"`
	OverlayFS     bool   `json:"overlayFS"`
	Root          bool   `json:"root"`
}

var systemInfoJSON bool

var systemCmd = &cobra.Command{
	Use:     "system",
	Short:   "Inspect the host system",
	GroupID: groupSystem,
}

var systemInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show host capabilities used by Boyler",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		info := collectSystemInformation()
		if systemInfoJSON {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(info)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Operating system:  %s\n", info.OS)
		fmt.Fprintf(cmd.OutOrStdout(), "Architecture:      %s\n", info.Architecture)
		fmt.Fprintf(cmd.OutOrStdout(), "Kernel:            %s\n", info.Kernel)
		fmt.Fprintf(cmd.OutOrStdout(), "Hostname:          %s\n", info.Hostname)
		fmt.Fprintf(cmd.OutOrStdout(), "Go:                %s\n", info.GoVersion)
		fmt.Fprintf(cmd.OutOrStdout(), "Cgroups:           %s\n", info.CgroupVersion)
		fmt.Fprintf(cmd.OutOrStdout(), "OverlayFS:         %t\n", info.OverlayFS)
		fmt.Fprintf(cmd.OutOrStdout(), "Running as root:   %t\n", info.Root)
		return nil
	},
}

func init() {
	systemInfoCmd.Flags().BoolVar(&systemInfoJSON, "json", false, "Print system information as JSON")
	systemCmd.AddCommand(systemInfoCmd)
	rootCmd.AddCommand(systemCmd)
}

func collectSystemInformation() systemInformation {
	hostname, _ := os.Hostname()
	kernel := readTrimmedFile("/proc/sys/kernel/osrelease")
	if kernel == "" {
		kernel = "unknown"
	}
	return systemInformation{
		OS: runtime.GOOS, Architecture: runtime.GOARCH, Kernel: kernel,
		Hostname: hostname, GoVersion: runtime.Version(),
		CgroupVersion: detectCgroupVersion(), OverlayFS: overlayFSSupported(), Root: os.Geteuid() == 0,
	}
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

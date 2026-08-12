package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"boyler/cmd/boyler/cmd/ui"

	"github.com/spf13/cobra"
)

type checkLevel string

const (
	checkPass checkLevel = "pass"
	checkWarn checkLevel = "warning"
	checkFail checkLevel = "fail"
)

type doctorCheck struct {
	Name   string     `json:"name"`
	Status checkLevel `json:"status"`
	Detail string     `json:"detail"`
	Hint   string     `json:"hint,omitempty"`
}

type doctorReport struct {
	Healthy bool          `json:"healthy"`
	Checks  []doctorCheck `json:"checks"`
}

var (
	doctorJSON         bool
	errDoctorUnhealthy = errors.New("Boyler prerequisites are not satisfied")
)

var doctorCmd = &cobra.Command{
	Use:     "doctor",
	Short:   "Check whether this host can run Boyler safely",
	GroupID: groupSystem,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		loadEnv()
		report := runDoctorChecks()
		if doctorJSON {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(report); err != nil {
				return err
			}
		} else {
			printDoctorReport(cmd, report)
		}
		if !report.Healthy {
			return errDoctorUnhealthy
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Print diagnostics as JSON")
	rootCmd.AddCommand(doctorCmd)
}

func runDoctorChecks() doctorReport {
	checks := []doctorCheck{
		booleanCheck("Linux host", runtime.GOOS == "linux", runtime.GOOS, "Boyler currently supports Linux only"),
		warningCheck("CLI privileges", os.Geteuid() == 0, fmt.Sprintf("effective UID %d", os.Geteuid()), "the CLI may stay unprivileged; the daemon currently requires root"),
		valueCheck("Cgroups v2", detectCgroupVersion(), "v2", "enable the unified cgroup v2 hierarchy"),
		booleanCheck("OverlayFS", overlayFSSupported(), "kernel filesystem support", "load or enable the overlay kernel module"),
		valueCheck("IPv4 forwarding", readTrimmedFile("/proc/sys/net/ipv4/ip_forward"), "1", "enable net.ipv4.ip_forward for container networking"),
		pathCheck("Runtime binary", os.Getenv("BIN_MYRUNC"), true),
		pathCheck("Images directory", os.Getenv("IMAGE_PATH"), false),
		pathCheck("Containers directory", os.Getenv("CONTAINER_DIR"), false),
	}
	status := probeDaemon(os.Getenv("UNIX_SOCKET"), 300*time.Millisecond)
	if status.Reachable {
		checks = append(checks, doctorCheck{Name: "Daemon", Status: checkPass, Detail: status.Socket})
	} else {
		detail := status.Error
		if status.Socket != "" {
			detail = status.Socket + ": " + detail
		}
		checks = append(checks, doctorCheck{Name: "Daemon", Status: checkWarn, Detail: detail, Hint: "start it with 'boyler init'"})
	}
	healthy := true
	for _, check := range checks {
		if check.Status == checkFail {
			healthy = false
		}
	}
	return doctorReport{Healthy: healthy, Checks: checks}
}

func warningCheck(name string, ok bool, detail, hint string) doctorCheck {
	status := checkPass
	if !ok {
		status = checkWarn
	}
	return doctorCheck{Name: name, Status: status, Detail: detail, Hint: hintIfFailed(ok, hint)}
}

func booleanCheck(name string, ok bool, detail, hint string) doctorCheck {
	status := checkPass
	if !ok {
		status = checkFail
	}
	return doctorCheck{Name: name, Status: status, Detail: detail, Hint: hintIfFailed(ok, hint)}
}

func valueCheck(name, actual, expected, hint string) doctorCheck {
	ok := actual == expected
	detail := actual
	if detail == "" {
		detail = "unavailable"
	}
	return booleanCheck(name, ok, detail, hint)
}

func pathCheck(name, path string, executable bool) doctorCheck {
	if strings.TrimSpace(path) == "" {
		return doctorCheck{Name: name, Status: checkFail, Detail: "not configured", Hint: "set the corresponding Boyler configuration value"}
	}
	info, err := os.Stat(path)
	if err != nil {
		return doctorCheck{Name: name, Status: checkFail, Detail: err.Error(), Hint: "verify the configured path and installation"}
	}
	if executable {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return doctorCheck{Name: name, Status: checkFail, Detail: path, Hint: "the runtime must be a regular executable file"}
		}
	} else if !info.IsDir() {
		return doctorCheck{Name: name, Status: checkFail, Detail: path, Hint: "the configured path must be a directory"}
	}
	return doctorCheck{Name: name, Status: checkPass, Detail: path}
}

func hintIfFailed(ok bool, hint string) string {
	if ok {
		return ""
	}
	return hint
}

func printDoctorReport(cmd *cobra.Command, report doctorReport) {
	theme := ui.NewTheme(cmd.OutOrStdout(), colorMode.value)
	for _, check := range report.Checks {
		symbol := theme.Success(theme.Symbol("✓", "PASS"))
		if check.Status == checkWarn {
			symbol = theme.Warning(theme.Symbol("!", "WARN"))
		} else if check.Status == checkFail {
			symbol = theme.Error(theme.Symbol("✗", "FAIL"))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %-22s %s\n", symbol, check.Name, check.Detail)
		if check.Hint != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "     hint: %s\n", check.Hint)
		}
	}
	if report.Healthy {
		fmt.Fprintln(cmd.OutOrStdout(), "\nBoyler is ready on this host.")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "\nBoyler is not ready; fix the failed checks above.")
	}
}

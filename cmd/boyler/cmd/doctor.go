package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"boyler/cmd/boyler/cmd/ui"
	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
	buildversion "boyler/internal/version"

	"github.com/spf13/cobra"
)

type checkLevel string

const (
	checkPass checkLevel = "pass"
	checkWarn checkLevel = "warning"
	checkFail checkLevel = "fail"
)

type doctorCheck struct {
	Code      string     `json:"code"`
	Source    string     `json:"source"`
	Component string     `json:"component"`
	Name      string     `json:"name"`
	Status    checkLevel `json:"status"`
	Detail    string     `json:"detail"`
	Hint      string     `json:"hint,omitempty"`
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
		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
		defer cancel()
		report := runDoctorChecks(ctx)
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

func runDoctorChecks(ctx context.Context) doctorReport {
	checks := []doctorCheck{
		withIdentity(booleanCheck("Linux host", runtime.GOOS == "linux", runtime.GOOS, "Boyler currently supports Linux only"), "HOST_NOT_LINUX", "local", "kernel"),
		withIdentity(warningCheck("CLI privileges", os.Geteuid() == 0, fmt.Sprintf("effective UID %d", os.Geteuid()), "the CLI may stay unprivileged; the daemon currently requires root"), "CLI_UNPRIVILEGED", "local", "cli"),
		withIdentity(valueCheck("Cgroups v2", detectCgroupVersion(), "v2", "enable the unified cgroup v2 hierarchy"), "CGROUP_V2_UNAVAILABLE", "local", "cgroups"),
		withIdentity(booleanCheck("OverlayFS", overlayFSSupported(), "kernel filesystem support", "load or enable the overlay kernel module"), "OVERLAYFS_UNAVAILABLE", "local", "filesystem"),
		withIdentity(valueCheck("IPv4 forwarding", readTrimmedFile("/proc/sys/net/ipv4/ip_forward"), "1", "enable net.ipv4.ip_forward for container networking"), "IP_FORWARDING_DISABLED", "local", "network"),
	}
	status := inspectDaemon(ctx)
	if status.Reachable {
		checks = append(checks, doctorCheck{Code: "DAEMON_HEALTHY", Source: "daemon", Component: "daemon", Name: "Daemon", Status: checkPass, Detail: status.Socket})
		if status.Compatible {
			checks = append(checks, doctorCheck{Code: "API_COMPATIBLE", Source: "daemon", Component: "api", Name: "CLI/daemon API", Status: checkPass, Detail: status.APIVersion})
		} else {
			checks = append(checks, doctorCheck{Code: "CLI_DAEMON_VERSION_MISMATCH", Source: "daemon", Component: "api", Name: "CLI/daemon API", Status: checkFail, Detail: fmt.Sprintf("CLI %s, daemon %s", buildversion.APIVersion, status.APIVersion), Hint: "install matching CLI and daemon releases"})
		}
		remote, err := daemonDoctor(ctx)
		if err != nil {
			checks = append(checks, doctorCheck{Code: "DAEMON_DOCTOR_FAILED", Source: "daemon", Component: "daemon", Name: "Daemon diagnostics", Status: checkFail, Detail: err.Error()})
		} else {
			checks = append(checks, remote...)
		}
	} else {
		detail := status.Error
		if status.Socket != "" {
			detail = status.Socket + ": " + detail
		}
		checks = append(checks, doctorCheck{Code: "DAEMON_UNREACHABLE", Source: "local", Component: "daemon", Name: "Daemon", Status: checkWarn, Detail: detail, Hint: "start it with 'boyler init'"})
		checks = append(checks,
			withIdentity(pathCheck("Runtime binary", os.Getenv("BIN_MYRUNC"), true), "RUNTIME_NOT_EXECUTABLE", "local", "runtime"),
			withIdentity(pathCheck("Images directory", os.Getenv("IMAGE_PATH"), false), "IMAGE_STORAGE_UNAVAILABLE", "local", "storage"),
			withIdentity(pathCheck("Containers directory", os.Getenv("CONTAINER_DIR"), false), "CONTAINER_STORAGE_UNAVAILABLE", "local", "storage"),
		)
	}
	healthy := true
	for _, check := range checks {
		if check.Status == checkFail {
			healthy = false
		}
	}
	return doctorReport{Healthy: healthy, Checks: checks}
}

func daemonDoctor(ctx context.Context) ([]doctorCheck, error) {
	client, connection, err := NewGrpcDaemonInspectionClient()
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	report, err := client.Doctor(ctx, &pb.DoctorRequest{})
	if err != nil {
		return nil, commandError(err)
	}
	checks := make([]doctorCheck, 0, len(report.Checks))
	for _, item := range report.Checks {
		status := checkLevel(strings.ToLower(strings.TrimPrefix(item.Status.String(), "DIAGNOSTIC_STATUS_")))
		checks = append(checks, doctorCheck{Code: item.Code, Source: "daemon", Component: item.Component, Name: item.Name, Status: status, Detail: item.Detail, Hint: item.Hint})
	}
	return checks, nil
}

func withIdentity(check doctorCheck, code, source, component string) doctorCheck {
	check.Code, check.Source, check.Component = code, source, component
	return check
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

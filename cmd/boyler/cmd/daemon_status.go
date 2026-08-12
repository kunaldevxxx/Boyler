package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"boyler/cmd/boyler/cmd/ui"

	"github.com/spf13/cobra"
)

type daemonStatus struct {
	Status    string `json:"status"`
	Socket    string `json:"socket"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

var daemonStatusJSON bool

var daemonCmd = &cobra.Command{
	Use:     "daemon",
	Short:   "Manage and inspect the Boyler daemon",
	GroupID: groupSystem,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check whether the Boyler daemon is reachable",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		loadEnv()
		status := probeDaemon(os.Getenv("UNIX_SOCKET"), time.Second)
		if daemonStatusJSON {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(status); err != nil {
				return err
			}
		} else {
			theme := ui.NewTheme(cmd.OutOrStdout(), colorMode.value)
			if status.Reachable {
				fmt.Fprintf(cmd.OutOrStdout(), "%s Boyler daemon is running\n", theme.Success(theme.Symbol("✓", "+")))
				fmt.Fprintf(cmd.OutOrStdout(), "  socket: %s\n", status.Socket)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s Boyler daemon is unavailable\n", theme.Error(theme.Symbol("✗", "-")))
				if status.Socket != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  socket: %s\n", status.Socket)
				}
			}
		}
		if !status.Reachable {
			return fmt.Errorf("daemon is unavailable: %s", status.Error)
		}
		return nil
	},
}

func init() {
	daemonStatusCmd.Flags().BoolVar(&daemonStatusJSON, "json", false, "Print daemon status as JSON")
	daemonCmd.AddCommand(daemonStatusCmd)
	rootCmd.AddCommand(daemonCmd)
}

func probeDaemon(socket string, timeout time.Duration) daemonStatus {
	result := daemonStatus{Status: "stopped", Socket: socket}
	if socket == "" {
		result.Error = "UNIX_SOCKET is not configured"
		return result
	}
	info, err := os.Stat(socket)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if info.Mode()&os.ModeSocket == 0 {
		result.Error = "configured path is not a Unix socket"
		return result
	}
	connection, err := net.DialTimeout("unix", socket, timeout)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	_ = connection.Close()
	result.Status = "running"
	result.Reachable = true
	return result
}

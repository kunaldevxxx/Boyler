package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(initDaemonCmd)
}

var initDaemonCmd = &cobra.Command{
	Use:     "init",
	Short:   "Start the Boyler daemon",
	GroupID: groupSystem,
	RunE: func(cmd *cobra.Command, args []string) error {
		loadEnv()
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate Boyler executable: %w", err)
		}
		resPath, _ := filepath.EvalSymlinks(exePath)
		binDir := filepath.Dir(resPath)
		projectRoot := filepath.Dir(binDir)
		daemonPath := filepath.Join(binDir, "daemon_boyler_linux")
		cmdCommand := exec.Command(daemonPath)
		cmdCommand.Dir = projectRoot
		if err := cmdCommand.Start(); err != nil {
			return fmt.Errorf("start Boyler daemon: %w", err)
		}
		printSuccess(cmd.OutOrStdout(), "Boyler daemon started")
		return nil
	},
}

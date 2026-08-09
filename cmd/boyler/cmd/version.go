package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)

}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		loadEnv()
		fmt.Fprintf(cmd.OutOrStdout(), "Boyler version %s\n", os.Getenv("VERSION"))
	},
}

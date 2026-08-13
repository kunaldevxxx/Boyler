package cmd

import (
	"encoding/json"
	"fmt"
	"runtime"

	"boyler/cmd/boyler/cmd/ui"
	buildversion "boyler/internal/version"

	"github.com/spf13/cobra"
)

var versionJSON bool

func init() {
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "Print version information as JSON")
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:     "version",
	Short:   "Show version information",
	GroupID: groupSystem,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		info := buildversion.Current(runtime.Version(), runtime.GOOS, runtime.GOARCH)
		if versionJSON {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(info)
		}
		theme := ui.NewTheme(cmd.OutOrStdout(), colorMode.value)
		fmt.Fprintf(cmd.OutOrStdout(), "%s version %s\n", theme.Brand("Boyler"), theme.Brand(info.Version))
		fmt.Fprintf(cmd.OutOrStdout(), "  commit: %s\n  built:  %s\n  target: %s/%s\n", info.Commit, info.BuildDate, info.OS, info.Arch)
		fmt.Fprintf(cmd.OutOrStdout(), "  API:    %s\n  state:  %s\n  runtime:%s\n", info.APIVersion, info.StateVersion, info.RuntimeVersion)
		return nil
	},
}

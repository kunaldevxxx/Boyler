package cmd

import (
	"context"
	"fmt"

	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(inspectCmd)
	inspectCmd.Flags().StringVarP(&inspectFormat, "format", "f", "", "Format output using a Go template or 'json'")
}

var inspectFormat string

var inspectCmd = &cobra.Command{
	Use:     "inspect [CONTAINER_ID]",
	Short:   "Display detailed information on a container",
	GroupID: groupObserve,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		loadEnv()
		id := args[0]
		client, conn, err := NewGrpcDaemonClient()
		if err != nil {
			return commandError(err)
		}
		defer conn.Close()
		req := &pb.InspectRequest{ContainerId: id}
		ctx, cancel := context.WithTimeout(cmd.Context(), daemonRequestTimeout)
		defer cancel()

		res, err := client.InspectContainer(ctx, req)
		if err != nil {
			return commandError(err)
		}
		if inspectFormat != "" {
			if err := printInspectTemplate(cmd.OutOrStdout(), res, inspectFormat); err != nil {
				return fmt.Errorf("format inspect response: %w", err)
			}
			return nil
		}
		if err := printInspect(cmd.OutOrStdout(), res); err != nil {
			return fmt.Errorf("format inspect response: %w", err)
		}
		return nil
	},
}

package cmd

import (
	"context"
	"time"

	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
	"github.com/spf13/cobra"
)

var (
	containerId string
)

func init() {
	rootCmd.AddCommand(stopCmd)
}

var stopCmd = &cobra.Command{
	Use:     "stop [CONTAINER_ID]",
	Short:   "Stop a container",
	GroupID: groupLifecycle,
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		loadEnv()
		id := args[0]
		client, conn, err := NewGrpcDaemonClient()
		if err != nil {
			return commandError(err)
		}
		defer conn.Close()
		req := &pb.StopRequest{ContainerId: id}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err = client.StopContainer(ctx, req)
		if err != nil {
			return commandError(err)
		}
		printActionResult(cmd.OutOrStdout(), "Stopped", id)
		return nil
	},
}

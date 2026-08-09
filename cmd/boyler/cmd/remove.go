package cmd

import (
	"context"
	"fmt"
	"time"

	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(removeCmd)
}

var removeCmd = &cobra.Command{
	Use:   "remove [CONTAINER_ID]",
	Short: "Remove a container",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		loadEnv()
		id := args[0]
		client, conn, err := NewGrpcDaemonClient()
		if err != nil {
			return commandError(err)
		}
		defer conn.Close()

		req := &pb.RemoveRequest{ContainerId: id}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err = client.RemoveContainer(ctx, req)
		if err != nil {
			return commandError(err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), id)
		return nil
	},
}

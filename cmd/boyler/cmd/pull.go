package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"boyler/cmd/boyler/cmd/ui"
	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
)

func init() {
	rootCmd.AddCommand(pull)
}

var pull = &cobra.Command{
	Use:     "pull [IMAGE]",
	Short:   "Download an image",
	GroupID: groupImages,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		loadEnv()
		image := args[0]
		client, conn, err := NewGrpcDaemonPullingClient()
		if err != nil {
			return commandError(err)
		}
		defer conn.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stream, err := client.PullImage(ctx, &pb.PullImageRequest{
			ImageIdentity: image,
		})
		if err != nil {
			return commandError(err)
		}

		repository, tag, canonical := pullReference(image)
		events := make(chan tea.Msg, 100)
		go grpcReader(stream, canonical, events)
		theme := ui.NewTheme(cmd.OutOrStdout(), colorMode.value)
		if theme.Terminal() {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n\n", theme.Heading("Pulling"), theme.Brand(repository+":"+tag))
		}

		programOptions := []tea.ProgramOption{tea.WithOutput(cmd.OutOrStdout())}
		if !theme.Terminal() {
			programOptions = append(programOptions, tea.WithoutRenderer())
		}
		p := tea.NewProgram(ui.New(events, theme), programOptions...)
		finalModel, err := p.Run()
		if err != nil {
			return err
		}
		model, ok := finalModel.(ui.Model)
		if ok && model.Err() != nil {
			return commandError(model.Err())
		}
		if !theme.Terminal() {
			fmt.Fprintf(cmd.OutOrStdout(), "docker.io/%s\n", canonical)
		}
		return nil
	},
}

func grpcReader(stream grpc.ServerStreamingClient[pb.PullImageEvent], image string, events chan<- tea.Msg) {
	defer close(events)
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			events <- ui.DoneMsg{Image: image}
			return
		}
		if err != nil {
			events <- ui.DoneMsg{Image: image, Err: err}
			return
		}

		events <- ui.ProgressMsg{
			ID:       resp.Layid,
			Status:   resp.Status,
			Progress: ratio(resp.Progress, resp.Total),
			Current:  resp.Progress,
			Total:    resp.Total,
		}
	}
}

func pullReference(value string) (repository, tag, canonical string) {
	value = strings.TrimPrefix(value, "docker.io/")
	tag = "latest"
	repository = value
	lastSlash := strings.LastIndex(repository, "/")
	lastColon := strings.LastIndex(repository, ":")
	if lastColon > lastSlash {
		tag = repository[lastColon+1:]
		repository = repository[:lastColon]
	}
	if !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}
	canonical = repository + ":" + tag
	return repository, tag, canonical
}

func ratio(current, total int64) float64 {
	if total <= 0 {
		return 0
	}
	if current >= total {
		return 1
	}
	return float64(current) / float64(total)
}

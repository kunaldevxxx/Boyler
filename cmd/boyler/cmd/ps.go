package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(psCmd)
	psCmd.Flags().BoolVarP(&psQuiet, "quiet", "q", false, "Only display container IDs")
	psCmd.Flags().BoolVar(&psNoTrunc, "no-trunc", false, "Don't truncate output")
	psCmd.Flags().StringSliceVarP(&psFilters, "filter", "f", nil, "Filter output (key=value)")
	psCmd.Flags().StringVar(&psFormat, "format", "", "Format output using 'table', 'json', or a Go template")
}

var (
	psQuiet   bool
	psNoTrunc bool
	psFilters []string
	psFormat  string
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List containers",
	RunE: func(cmd *cobra.Command, args []string) error {
		loadEnv()
		client, conn, err := NewGrpcDaemonClient()
		if err != nil {
			return commandError(err)
		}
		defer conn.Close()

		resp, err := client.ContainersList(
			context.Background(),
			&pb.PsRequest{},
		)
		if err != nil {
			return commandError(err)
		}
		if err := printContainersWithOptions(cmd.OutOrStdout(), resp, time.Now(), psOptions{
			quiet:   psQuiet,
			noTrunc: psNoTrunc,
			filters: psFilters,
			format:  psFormat,
		}); err != nil {
			return fmt.Errorf("Error: %w", err)
		}
		return nil
	},
}

type psOptions struct {
	quiet   bool
	noTrunc bool
	filters []string
	format  string
}

type containerRow struct {
	ID      string `json:"ID"`
	Image   string `json:"Image"`
	Command string `json:"Command"`
	Created string `json:"CreatedAt"`
	Status  string `json:"Status"`
	Ports   string `json:"Ports"`
	Names   string `json:"Names"`
}

func printContainers(output io.Writer, resp *pb.PsResponse, now time.Time) {
	_ = printContainersWithOptions(output, resp, now, psOptions{})
}

func printContainersWithOptions(output io.Writer, resp *pb.PsResponse, now time.Time, opts psOptions) error {
	if opts.quiet && opts.format != "" {
		return fmt.Errorf("conflicting options: --quiet and --format")
	}

	containers, err := filterContainers(resp.GetContainers(), opts.filters)
	if err != nil {
		return err
	}
	rows := make([]containerRow, 0, len(containers))
	for _, container := range containers {
		id := container.GetContainerId()
		if !opts.noTrunc {
			id = shortContainerID(id)
		}
		command := strconv.Quote(container.GetCommand())
		if !opts.noTrunc {
			command = truncateText(command, 20)
		}
		rows = append(rows, containerRow{
			ID:      id,
			Image:   container.GetImage(),
			Command: command,
			Created: relativeTime(container.GetCreated(), now),
			Status:  displayStatus(container.GetStatus()),
			Ports:   "",
			Names:   container.GetName(),
		})
	}

	if opts.quiet {
		for _, row := range rows {
			fmt.Fprintln(output, row.ID)
		}
		return nil
	}

	switch opts.format {
	case "", "table":
		printContainerTable(output, rows)
		return nil
	case "json":
		encoder := json.NewEncoder(output)
		for _, row := range rows {
			if err := encoder.Encode(row); err != nil {
				return err
			}
		}
		return nil
	default:
		tmpl, err := newOutputTemplate("ps", opts.format)
		if err != nil {
			return fmt.Errorf("invalid ps format: %w", err)
		}
		for _, row := range rows {
			if err := tmpl.Execute(output, row); err != nil {
				return fmt.Errorf("execute ps format: %w", err)
			}
			fmt.Fprintln(output)
		}
		return nil
	}
}

func printContainerTable(output io.Writer, rows []containerRow) {
	w := tabwriter.NewWriter(output, 0, 0, 3, ' ', 0)
	fmt.Fprintln(
		w,
		"CONTAINER ID\tIMAGE\tCOMMAND\tCREATED\tSTATUS\tPORTS\tNAMES",
	)
	for _, row := range rows {
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.ID,
			row.Image,
			row.Command,
			row.Created,
			row.Status,
			row.Ports,
			row.Names,
		)
	}
	_ = w.Flush()
}

func filterContainers(containers []*pb.ContainerListItem, filters []string) ([]*pb.ContainerListItem, error) {
	grouped := make(map[string][]string)
	for _, filter := range filters {
		key, value, ok := strings.Cut(filter, "=")
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("invalid filter %q: expected key=value", filter)
		}
		switch key {
		case "id", "name", "image", "status":
			grouped[key] = append(grouped[key], value)
		default:
			return nil, fmt.Errorf("unsupported filter %q", key)
		}
	}

	result := make([]*pb.ContainerListItem, 0, len(containers))
	for _, container := range containers {
		matchesAll := true
		for key, values := range grouped {
			matchesOne := false
			for _, value := range values {
				if containerMatches(container, key, value) {
					matchesOne = true
					break
				}
			}
			if !matchesOne {
				matchesAll = false
				break
			}
		}
		if matchesAll {
			result = append(result, container)
		}
	}
	return result, nil
}

func containerMatches(container *pb.ContainerListItem, key, value string) bool {
	switch key {
	case "id":
		return strings.HasPrefix(container.GetContainerId(), value)
	case "name":
		return strings.Contains(container.GetName(), value)
	case "image":
		return strings.Contains(container.GetImage(), value)
	case "status":
		actual := strings.ToLower(container.GetStatus())
		expected := strings.ToLower(value)
		if expected == "paused" {
			expected = "pause"
		}
		if expected == "exited" {
			expected = "stopped"
		}
		return actual == expected
	default:
		return false
	}
}

func truncateText(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

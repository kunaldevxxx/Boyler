package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"boyler/cmd/boyler/cmd/ui"
	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"

	"github.com/charmbracelet/lipgloss"
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
	Use:     "ps",
	Short:   "List containers",
	GroupID: groupObserve,
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
			return err
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
	theme := ui.NewTheme(output, colorMode.value)
	type column struct {
		header string
		value  func(containerRow) string
		style  func(string) string
	}
	columns := []column{
		{header: "CONTAINER ID", value: func(row containerRow) string { return row.ID }, style: theme.Muted},
		{header: "IMAGE", value: func(row containerRow) string { return row.Image }},
	}
	if !theme.Terminal() || theme.Width() >= 100 {
		columns = append(columns,
			column{header: "COMMAND", value: func(row containerRow) string { return row.Command }},
			column{header: "CREATED", value: func(row containerRow) string { return row.Created }, style: theme.Muted},
		)
	}
	columns = append(columns,
		column{header: "STATUS", value: func(row containerRow) string { return terminalStatus(theme, row.Status) }},
		column{header: "NAMES", value: func(row containerRow) string { return row.Names }, style: theme.Brand},
	)

	widths := make([]int, len(columns))
	for index, col := range columns {
		widths[index] = lipgloss.Width(col.header)
		for _, row := range rows {
			if width := lipgloss.Width(col.value(row)); width > widths[index] {
				widths[index] = width
			}
		}
	}

	for index, col := range columns {
		writeTableCell(output, theme.Label(col.header), widths[index], index == len(columns)-1)
	}
	fmt.Fprintln(output)
	for _, row := range rows {
		for index, col := range columns {
			value := col.value(row)
			if col.style != nil {
				value = col.style(value)
			}
			writeTableCell(output, value, widths[index], index == len(columns)-1)
		}
		fmt.Fprintln(output)
	}
}

func writeTableCell(output io.Writer, value string, width int, last bool) {
	fmt.Fprint(output, value)
	if !last {
		padding := width - lipgloss.Width(value) + 3
		fmt.Fprint(output, strings.Repeat(" ", padding))
	}
}

func terminalStatus(theme ui.Theme, status string) string {
	if !theme.Terminal() && !theme.Enabled() {
		return status
	}
	switch {
	case strings.Contains(status, "Paused"):
		return theme.Warning(theme.Symbol("●", "*") + " " + status)
	case strings.HasPrefix(status, "Up"):
		return theme.Success(theme.Symbol("●", "*") + " " + status)
	case strings.HasPrefix(status, "Exited"):
		return theme.Error(theme.Symbol("○", "-") + " " + status)
	default:
		return theme.Muted(status)
	}
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

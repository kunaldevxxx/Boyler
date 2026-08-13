package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"

	"github.com/spf13/cobra"
)

var (
	imagesQuiet   bool
	imagesNoTrunc bool
	imagesFormat  string
	imagesFilters []string
	rmiForce      bool
	rmiJSON       bool
	pruneAll      bool
	pruneDryRun   bool
	pruneForce    bool
	pruneJSON     bool
)

var imageCmd = &cobra.Command{
	Use:     "image",
	Short:   "Manage images",
	GroupID: groupImages,
}

var imagesCmd = &cobra.Command{
	Use:     "images",
	Short:   "List images",
	GroupID: groupImages,
	Args:    cobra.NoArgs,
	RunE:    runImages,
}

var imageListCmd = &cobra.Command{Use: "ls", Short: "List images", Args: cobra.NoArgs, RunE: runImages}

var imageInspectCmd = &cobra.Command{
	Use: "inspect IMAGE", Short: "Display detailed information on an image", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, conn, err := newImageClient()
		if err != nil {
			return commandError(err)
		}
		defer conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err := client.InspectImage(ctx, &pb.InspectImageRequest{ImageReference: args[0]})
		if err != nil {
			return commandError(err)
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(response)
	},
}

var imageRemoveCmd = &cobra.Command{
	Use: "rm IMAGE [IMAGE...]", Aliases: []string{"remove"}, Short: "Remove one or more image references", Args: cobra.MinimumNArgs(1),
	RunE: runImageRemove,
}

var imagePruneCmd = &cobra.Command{
	Use: "prune", Short: "Remove unused image data", Args: cobra.NoArgs,
	RunE: runImagePrune,
}

var rmiCmd = &cobra.Command{
	Use: "rmi IMAGE [IMAGE...]", Short: "Remove one or more image references", GroupID: groupImages, Args: cobra.MinimumNArgs(1),
	RunE: runImageRemove,
}

func init() {
	rootCmd.AddCommand(imagesCmd, imageCmd, rmiCmd)
	imageCmd.AddCommand(imageListCmd, imageInspectCmd, imageRemoveCmd, imagePruneCmd)
	for _, command := range []*cobra.Command{imagesCmd, imageListCmd} {
		command.Flags().BoolVarP(&imagesQuiet, "quiet", "q", false, "Only display image IDs")
		command.Flags().BoolVar(&imagesNoTrunc, "no-trunc", false, "Don't truncate output")
		command.Flags().StringVar(&imagesFormat, "format", "", "Format output using 'table', 'json', or a Go template")
		command.Flags().StringSliceVarP(&imagesFilters, "filter", "f", nil, "Filter output (reference, repository, tag, or digest)")
	}
	for _, command := range []*cobra.Command{imageRemoveCmd, rmiCmd} {
		command.Flags().BoolVarP(&rmiForce, "force", "f", false, "Remove the reference even if containers use the image")
		command.Flags().BoolVar(&rmiJSON, "json", false, "Print the operation result as JSON")
	}
	imagePruneCmd.Flags().BoolVarP(&pruneAll, "all", "a", false, "Remove image references not used by containers")
	imagePruneCmd.Flags().BoolVar(&pruneDryRun, "dry-run", false, "Show what would be removed")
	imagePruneCmd.Flags().BoolVarP(&pruneForce, "force", "f", false, "Do not prompt for confirmation")
	imagePruneCmd.Flags().BoolVar(&pruneJSON, "json", false, "Print the prune result as JSON")
}

func newImageClient() (pb.ImageServiceClient, io.Closer, error) {
	loadEnv()
	client, connection, err := NewGrpcDaemonPullingClient()
	return client, connection, err
}

func runImages(cmd *cobra.Command, _ []string) error {
	client, conn, err := newImageClient()
	if err != nil {
		return commandError(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := client.ListImages(ctx, &pb.ListImagesRequest{})
	if err != nil {
		return commandError(err)
	}
	filtered, err := filterImages(response.GetImages(), imagesFilters)
	if err != nil {
		return err
	}
	return printImages(cmd.OutOrStdout(), filtered, imagesQuiet, imagesNoTrunc, imagesFormat)
}

func filterImages(images []*pb.ImageSummary, filters []string) ([]*pb.ImageSummary, error) {
	result := images
	for _, filter := range filters {
		key, value, ok := strings.Cut(filter, "=")
		if !ok || value == "" {
			return nil, fmt.Errorf("invalid image filter %q: expected key=value", filter)
		}
		next := make([]*pb.ImageSummary, 0, len(result))
		for _, image := range result {
			repository, tag, _ := strings.Cut(image.GetReference(), ":")
			var candidate string
			switch key {
			case "reference":
				candidate = image.GetReference()
			case "repository":
				candidate = repository
			case "tag":
				candidate = tag
			case "digest":
				candidate = image.GetDigest()
			default:
				return nil, fmt.Errorf("unsupported image filter %q", key)
			}
			if strings.Contains(candidate, value) {
				next = append(next, image)
			}
		}
		result = next
	}
	return result, nil
}

type imageRow struct{ Repository, Tag, ID, CreatedAt, Size string }

func printImages(output io.Writer, images []*pb.ImageSummary, quiet, noTrunc bool, format string) error {
	rows := make([]imageRow, 0, len(images))
	for _, image := range images {
		repository, tag, _ := strings.Cut(image.GetReference(), ":")
		id := strings.TrimPrefix(image.GetDigest(), "sha256:")
		if !noTrunc && len(id) > 12 {
			id = id[:12]
		}
		rows = append(rows, imageRow{Repository: repository, Tag: tag, ID: id, CreatedAt: image.GetCreatedAt(), Size: humanBytes(image.GetSize())})
	}
	if quiet {
		for _, row := range rows {
			fmt.Fprintln(output, row.ID)
		}
		return nil
	}
	if format == "json" {
		return json.NewEncoder(output).Encode(images)
	}
	if format != "" && format != "table" {
		template, err := newOutputTemplate("images", format)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := template.Execute(output, row); err != nil {
				return err
			}
			fmt.Fprintln(output)
		}
		return nil
	}
	w := tabwriter.NewWriter(output, 0, 4, 3, ' ', 0)
	fmt.Fprintln(w, "REPOSITORY\tTAG\tIMAGE ID\tCREATED\tSIZE")
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", row.Repository, row.Tag, row.ID, row.CreatedAt, row.Size)
	}
	return w.Flush()
}

func runImageRemove(cmd *cobra.Command, args []string) error {
	client, conn, err := newImageClient()
	if err != nil {
		return commandError(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	response, err := client.RemoveImage(ctx, &pb.RemoveImageRequest{ImageReferences: args, Force: rmiForce})
	if err != nil {
		return commandError(err)
	}
	if rmiJSON {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(response); err != nil {
			return err
		}
	} else {
		for _, image := range response.GetImages() {
			fmt.Fprintf(cmd.OutOrStdout(), "Untagged: %s\n", image.GetReference())
		}
	}
	if !rmiJSON {
		for _, failure := range response.GetFailures() {
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s: %s\n", failure.GetReference(), failure.GetError())
		}
	}
	if len(response.GetFailures()) > 0 {
		return fmt.Errorf("failed to remove %d image reference(s)", len(response.GetFailures()))
	}
	return nil
}

func runImagePrune(cmd *cobra.Command, _ []string) error {
	if !pruneDryRun && !pruneForce {
		confirmed, err := confirmPrune(cmd.InOrStdin(), cmd.OutOrStdout(), pruneAll)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(cmd.OutOrStdout(), "Prune cancelled.")
			return nil
		}
	}
	client, conn, err := newImageClient()
	if err != nil {
		return commandError(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response, err := client.PruneImages(ctx, &pb.PruneImagesRequest{All: pruneAll, DryRun: pruneDryRun})
	if err != nil {
		return commandError(err)
	}
	if pruneJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(response)
	}
	prefix := "Deleted"
	if pruneDryRun {
		prefix = "Would delete"
	}
	for _, value := range response.GetDeletedReferences() {
		fmt.Fprintf(cmd.OutOrStdout(), "%s reference: %s\n", prefix, value)
	}
	for _, value := range response.GetDeletedManifests() {
		fmt.Fprintf(cmd.OutOrStdout(), "%s manifest: %s\n", prefix, value)
	}
	for _, value := range response.GetDeletedRootfs() {
		fmt.Fprintf(cmd.OutOrStdout(), "%s rootfs: %s\n", prefix, value)
	}
	for _, value := range response.GetDeletedLayers() {
		fmt.Fprintf(cmd.OutOrStdout(), "%s layer: %s\n", prefix, value)
	}
	for _, value := range response.GetQuarantinedReferences() {
		action := "Quarantined"
		if pruneDryRun {
			action = "Would quarantine"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s corrupt reference: %s\n", action, value)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Total reclaimed space: %s\n", humanBytes(response.GetReclaimedBytes()))
	return nil
}

func confirmPrune(input io.Reader, output io.Writer, all bool) (bool, error) {
	detail := "unused image data"
	if all {
		detail = "all image references not used by containers and their unused data"
	}
	fmt.Fprintf(output, "This will remove %s. Continue? [y/N] ", detail)
	scanner := bufio.NewScanner(input)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}

func humanBytes(size int64) string {
	const unit = int64(1024)
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	div, exp := unit, 0
	for n := size / unit; n >= unit && exp < 3; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(size)/float64(div), "KMGT"[exp])
}

/*
Copyright © 2026 Arrdin <arrdin32@gmail.com>
*/
package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"boyler/cmd/boyler/cmd/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

const (
	groupLifecycle = "lifecycle"
	groupImages    = "images"
	groupObserve   = "observe"
	groupSystem    = "system"
)

type colorModeValue struct{ value string }

func (value *colorModeValue) String() string { return value.value }
func (value *colorModeValue) Type() string   { return "mode" }
func (value *colorModeValue) Set(next string) error {
	if !ui.ValidColorMode(next) {
		return fmt.Errorf("must be one of auto, always, never")
	}
	value.value = next
	return nil
}

var colorMode = colorModeValue{value: ui.ColorAuto}

var rootCmd = &cobra.Command{
	Use:   "boyler",
	Short: "A lightweight container engine",
	Long:  "A lightweight container engine",
	Example: "  boyler pull alpine\n" +
		"  boyler create --name web nginx\n" +
		"  boyler ps --filter status=running",
	SilenceErrors: true,
	SilenceUsage:  true,
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		theme := ui.NewTheme(os.Stderr, colorMode.value)
		fmt.Fprintf(os.Stderr, "%s %s\n", theme.Error(theme.Symbol("✗", "Error:")), err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddGroup(
		&cobra.Group{ID: groupLifecycle, Title: "Container lifecycle"},
		&cobra.Group{ID: groupImages, Title: "Images"},
		&cobra.Group{ID: groupObserve, Title: "Observe"},
		&cobra.Group{ID: groupSystem, Title: "System"},
	)
	rootCmd.SetHelpCommandGroupID(groupSystem)
	rootCmd.SetCompletionCommandGroupID(groupSystem)
	rootCmd.PersistentFlags().Var(&colorMode, "color", "Color output: auto, always, or never")
	rootCmd.SetHelpFunc(renderHelp)
}

func renderHelp(cmd *cobra.Command, _ []string) {
	output := cmd.OutOrStdout()
	theme := ui.NewTheme(output, colorMode.value)

	if cmd == cmd.Root() {
		renderBanner(output, theme)
	} else {
		fmt.Fprintf(output, "%s\n  %s\n\n", theme.Brand(strings.ToUpper(cmd.Name())), cmd.Short)
	}

	fmt.Fprintln(output, theme.Heading("Usage"))
	if cmd.HasAvailableSubCommands() {
		fmt.Fprintf(output, "  %s [options] <command>\n", cmd.CommandPath())
	} else {
		fmt.Fprintf(output, "  %s\n", cmd.UseLine())
	}

	if cmd.HasAvailableSubCommands() {
		commands := cmd.Commands()
		sort.SliceStable(commands, func(i, j int) bool { return commands[i].Name() < commands[j].Name() })
		printed := make(map[*cobra.Command]bool)
		for _, group := range cmd.Groups() {
			if !groupHasCommands(commands, group.ID) {
				continue
			}
			fmt.Fprintf(output, "\n%s\n", theme.Heading(group.Title))
			groupCommands := commandsInGroup(commands, group.ID)
			for _, subcommand := range groupCommands {
				printed[subcommand] = true
			}
			printHelpCommands(output, theme, cmd, expandHelpCommands(groupCommands))
		}

		var ungrouped []*cobra.Command
		for _, subcommand := range commands {
			if subcommand.IsAvailableCommand() && !printed[subcommand] {
				ungrouped = append(ungrouped, subcommand)
			}
		}
		if len(ungrouped) > 0 {
			fmt.Fprintf(output, "\n%s\n", theme.Heading("Commands"))
			printHelpCommands(output, theme, cmd, ungrouped)
		}
	}
	if cmd.Example != "" {
		fmt.Fprintf(output, "\n%s\n%s\n", theme.Heading("Examples"), theme.Brand(cmd.Example))
	}

	if cmd.HasAvailableLocalFlags() {
		fmt.Fprintf(output, "\n%s\n%s\n", theme.Heading("Options"), strings.TrimRight(cmd.LocalFlags().FlagUsages(), "\n"))
	}
	if cmd.HasAvailableInheritedFlags() {
		fmt.Fprintf(output, "\n%s\n%s\n", theme.Heading("Global options"), strings.TrimRight(cmd.InheritedFlags().FlagUsages(), "\n"))
	}
	if cmd.HasAvailableSubCommands() {
		fmt.Fprintf(output, "\n%s\n  %s\n", theme.Heading("Learn more"), theme.Muted(fmt.Sprintf("Run '%s <command> --help' for command details.", cmd.CommandPath())))
	}
}

func commandsInGroup(commands []*cobra.Command, groupID string) []*cobra.Command {
	var result []*cobra.Command
	for _, command := range commands {
		if command.GroupID == groupID && command.IsAvailableCommand() {
			result = append(result, command)
		}
	}
	return result
}

// expandHelpCommands replaces namespace-only commands with their executable
// children so the root help documents the actual command paths.
func expandHelpCommands(commands []*cobra.Command) []*cobra.Command {
	var result []*cobra.Command
	for _, command := range commands {
		if command.Runnable() || !command.HasAvailableSubCommands() || command.Name() == "completion" {
			result = append(result, command)
			continue
		}
		for _, child := range command.Commands() {
			if child.IsAvailableCommand() {
				result = append(result, child)
			}
		}
	}
	return result
}

func printHelpCommands(output io.Writer, theme ui.Theme, context *cobra.Command, commands []*cobra.Command) {
	if len(commands) == 0 {
		return
	}
	sort.SliceStable(commands, func(i, j int) bool {
		return helpCommandName(context, commands[i]) < helpCommandName(context, commands[j])
	})
	width := 0
	for _, command := range commands {
		if value := lipgloss.Width(helpCommandName(context, command)); value > width {
			width = value
		}
	}
	for _, command := range commands {
		plainName := helpCommandName(context, command)
		name := theme.Brand(plainName)
		padding := width - lipgloss.Width(name) + 3
		if padding < 1 {
			padding = 1
		}
		fmt.Fprintf(output, "  %s%s%s\n", name, strings.Repeat(" ", padding), theme.Muted(command.Short))
	}
}

func helpCommandName(context, command *cobra.Command) string {
	if command.Parent() == context {
		return command.Name()
	}
	if command.Parent() != nil && command.Parent().Parent() == context {
		return command.Parent().Name() + " " + command.Name()
	}
	return command.CommandPath()
}

func renderBanner(output io.Writer, theme ui.Theme) {
	if theme.Symbol("unicode", "ascii") == "ascii" {
		fmt.Fprintf(output, "  [==]  %s\n        %s\n\n", theme.Brand("BOYLER"), theme.Muted("lightweight container engine"))
		return
	}
	fmt.Fprintf(output, "  ╭────╮   %s\n", theme.Brand("BOYLER"))
	fmt.Fprintf(output, "  │ ≋≋ │   %s\n", theme.Muted("lightweight container engine"))
	fmt.Fprintln(output, "  ╰─┬──╯")
	fmt.Fprintln(output, "    ╵")
	fmt.Fprintln(output)
}

func groupHasCommands(commands []*cobra.Command, groupID string) bool {
	for _, command := range commands {
		if command.GroupID == groupID && command.IsAvailableCommand() {
			return true
		}
	}
	return false
}

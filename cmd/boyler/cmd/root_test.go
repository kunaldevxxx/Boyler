package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootHelpContainsBrandAndGroups(t *testing.T) {
	var output bytes.Buffer
	previous := colorMode.value
	colorMode.value = "never"
	t.Cleanup(func() { colorMode.value = previous })

	rootCmd.SetOut(&output)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
	renderHelp(rootCmd, nil)

	for _, expected := range []string{
		"BOYLER",
		"Container lifecycle",
		"Images",
		"Observe",
		"System",
		"--color",
		"daemon status",
		"system info",
		"image inspect",
		"image prune",
		"image rm",
		"images",
		"rmi",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("help output does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestNestedHelpContainsSubcommands(t *testing.T) {
	previous := colorMode.value
	colorMode.value = "never"
	t.Cleanup(func() { colorMode.value = previous })
	for _, test := range []struct {
		command  *cobra.Command
		expected string
	}{{daemonCmd, "status"}, {systemCmd, "info"}} {
		var output bytes.Buffer
		test.command.SetOut(&output)
		renderHelp(test.command, nil)
		test.command.SetOut(nil)
		if !strings.Contains(output.String(), test.expected) || !strings.Contains(output.String(), "Commands") {
			t.Fatalf("%s help does not contain %q:\n%s", test.command.Name(), test.expected, output.String())
		}
	}
}

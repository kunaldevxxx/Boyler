package cmd

import (
	"bytes"
	"strings"
	"testing"
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
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("help output does not contain %q:\n%s", expected, output.String())
		}
	}
}

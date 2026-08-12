package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestThemeNeverKeepsOutputPlain(t *testing.T) {
	theme := NewTheme(&bytes.Buffer{}, ColorNever)
	if actual := theme.Gradient("BOYLER"); actual != "BOYLER" {
		t.Fatalf("plain gradient = %q", actual)
	}
	if actual := theme.Success("done"); actual != "done" {
		t.Fatalf("plain success = %q", actual)
	}
}

func TestThemeAlwaysRendersANSI(t *testing.T) {
	theme := NewTheme(&bytes.Buffer{}, ColorAlways)
	actual := theme.Gradient("BOYLER")
	if !strings.Contains(actual, "\x1b[") {
		t.Fatalf("forced color output does not contain ANSI: %q", actual)
	}
}

func TestValidColorMode(t *testing.T) {
	for _, mode := range []string{ColorAuto, ColorAlways, ColorNever} {
		if !ValidColorMode(mode) {
			t.Fatalf("expected %q to be valid", mode)
		}
	}
	if ValidColorMode("sometimes") {
		t.Fatal("unexpected valid color mode")
	}
}

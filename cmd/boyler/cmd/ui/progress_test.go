package ui

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHumanSize(t *testing.T) {
	tests := map[int64]string{
		0:       "0B",
		999:     "999B",
		1000:    "1.00kB",
		1500000: "1.50MB",
		-1:      "?",
	}
	for input, expected := range tests {
		if actual := humanSize(input); actual != expected {
			t.Errorf("humanSize(%d) = %q, want %q", input, actual, expected)
		}
	}
}

func TestDisplayImageAddsLatestTag(t *testing.T) {
	if actual := displayImage("alpine"); actual != "alpine:latest" {
		t.Fatalf("displayImage(alpine) = %q", actual)
	}
	if actual := displayImage("docker.io/acme/api:v2"); actual != "acme/api:v2" {
		t.Fatalf("displayImage(tagged image) = %q", actual)
	}
}

func TestProgressViewShowsPercentAndCompletion(t *testing.T) {
	theme := NewTheme(&bytes.Buffer{}, ColorNever)
	model := New(make(chan tea.Msg), theme)
	model.width = 70
	model.order = []string{"1234567890abcdef", "complete-layer"}
	model.layers["1234567890abcdef"] = &layer{progress: .5, current: 1500000, total: 3000000}
	model.layers["complete-layer"] = &layer{status: "download complete", progress: 1, current: 1000, total: 1000}

	view := model.View()
	for _, expected := range []string{"1234567890ab", "50%", "1.50MB / 3.00MB", "complete"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("progress output does not contain %q: %q", expected, view)
		}
	}
}

func TestProgressViewShowsCachedLayer(t *testing.T) {
	theme := NewTheme(&bytes.Buffer{}, ColorNever)
	model := New(make(chan tea.Msg), theme)
	model.order = []string{"cached-layer"}
	model.layers["cached-layer"] = &layer{
		status:   "Already exists",
		progress: 1,
		current:  1000,
		total:    1000,
	}
	if view := model.View(); !strings.Contains(view, "already exists") {
		t.Fatalf("cached progress output = %q", view)
	}
}

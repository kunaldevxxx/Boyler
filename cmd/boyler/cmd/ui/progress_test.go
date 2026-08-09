package ui

import "testing"

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

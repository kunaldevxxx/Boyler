package main

import (
	"strings"
	"testing"
)

func TestParseInvocation(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		id         string
		bundlePath string
		signal     string
	}{
		{name: "create", args: []string{"myrunc", "create", "container-1", "--bundle", "/tmp/bundle"}, id: "container-1", bundlePath: "/tmp/bundle"},
		{name: "run", args: []string{"myrunc", "run", "container-1"}, id: "container-1"},
		{name: "kill positional", args: []string{"myrunc", "kill", "container-1", "SIGKILL"}, id: "container-1", signal: "SIGKILL"},
		{name: "kill flag", args: []string{"myrunc", "kill", "container-1", "--signal", "15"}, id: "container-1", signal: "15"},
		{name: "delete", args: []string{"myrunc", "delete", "container-1"}, id: "container-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocation, err := parseInvocation(test.args)
			if err != nil {
				t.Fatal(err)
			}
			if invocation.info.id != test.id || invocation.info.bundlePath != test.bundlePath || invocation.info.sigNum != test.signal {
				t.Fatalf("parsed info = %#v", invocation.info)
			}
		})
	}
}

func TestParseInvocationRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing command", args: []string{"myrunc"}, want: "command is required"},
		{name: "unknown command", args: []string{"myrunc", "unknown", "id"}, want: "unknown command"},
		{name: "missing ID", args: []string{"myrunc", "run"}, want: "container ID is required"},
		{name: "unsafe ID", args: []string{"myrunc", "delete", "../host"}, want: "invalid container ID"},
		{name: "missing bundle", args: []string{"myrunc", "create", "id"}, want: "--bundle is required"},
		{name: "unknown flag", args: []string{"myrunc", "run", "id", "--force"}, want: "flag provided but not defined"},
		{name: "missing signal", args: []string{"myrunc", "kill", "id"}, want: "unsupported signal"},
		{name: "invalid signal", args: []string{"myrunc", "kill", "id", "SIGUSR1"}, want: "unsupported signal"},
		{name: "duplicate signal", args: []string{"myrunc", "kill", "id", "--signal", "9", "15"}, want: "either positionally or via --signal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseInvocation(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

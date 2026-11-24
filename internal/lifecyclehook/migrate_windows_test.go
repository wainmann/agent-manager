//go:build windows

package lifecyclehook

import (
	"slices"
	"testing"

	"github.com/YoanWai/agent-manager/internal/pwsh"
)

func TestMigratedArgvUsesEncodedPowerShell(t *testing.T) {
	command := "echo $env:AM_BODY"
	argv, err := migratedArgv(command)
	if err != nil {
		t.Fatal(err)
	}
	path, err := pwsh.Path()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{path, "-NoProfile", "-NonInteractive", "-EncodedCommand", pwsh.EncodedCommand(command)}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %#v, want %#v", argv, want)
	}
}

//go:build windows

package lifecyclehook

import "github.com/YoanWai/agent-manager/internal/pwsh"

// migratedArgv is the vector command_windows.go used: pwsh -NoProfile
// -NonInteractive -EncodedCommand, with the original command text encoded
// so its quotes survive the command line.
func migratedArgv(command string) ([]string, error) {
	path, err := pwsh.Path()
	if err != nil {
		return nil, err
	}
	return []string{path, "-NoProfile", "-NonInteractive", "-EncodedCommand", pwsh.EncodedCommand(command)}, nil
}

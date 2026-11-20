package mcpreg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/YoanWai/agent-manager/internal/tmux"
)

func TestApplyCopilotWritesConfigAndFlag(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{}
	command, err := Apply("copilot", "/opt/bin/agent-manager", dir, "copilot", env)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mcp-copilot.json")
	if want := "copilot --additional-mcp-config " + tmux.ShellQuote("@"+path); command != want {
		t.Fatalf("command = %q want %q", command, want)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]map[string]struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	server := parsed["mcpServers"]["agent-manager"]
	if server.Type != "local" || server.Command != "/opt/bin/agent-manager" || len(server.Args) != 1 || server.Args[0] != "mcp" {
		t.Fatalf("server = %+v", server)
	}
	if len(env) != 0 {
		t.Fatalf("env = %v, want untouched", env)
	}
}

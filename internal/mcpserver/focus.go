package mcpserver

import (
	"context"

	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addFocusSession(server *mcp.Server, noCaller error, sessions sessionCommands) {
	addTool(server, noCaller, &mcp.Tool{
		Name: "focus_session",
		Description: "Open a session in the user's Agent Manager window the way the enter key does, so the user lands on it: the groups above the session unfold, and a dialog the user is typing into stays open with the cursor moved under it. " +
			"It changes what the user is looking at, so call it only when the user asked to be taken to a session, such as one you handed their question to.",
		Annotations: toolAnnotations(false, false, false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args sessionTargetArgs) (*mcp.CallToolResult, sessioncmd.Session, error) {
		focused, err := sessions.Focus(args.SessionID, true)
		if err != nil {
			return nil, sessioncmd.Session{}, err
		}
		return textContent("focused " + sessioncmd.FormatSession(focused)), focused, nil
	})
}

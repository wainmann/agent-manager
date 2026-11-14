package mcpserver

import (
	"context"

	"github.com/YoanWai/agent-manager/internal/remote"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addDeleteSession(server *mcp.Server, noCaller error, sessionID string, sessions sessionCommands, remotes *remoteHosts) {
	addTool(server, noCaller, &mcp.Tool{
		Name: "delete_session",
		Description: "Remove a finished session the way the user's delete key does: its agent stops, its row and terminals go, and Agent Manager removes the worktree and am/ branch it created. " +
			"A worktree with uncommitted or untracked files, or commits on no remote and not on its base, stays with its branch, as does one another session still works in, and the result's notice says so. " +
			"Unlike kill_session the session cannot come back with revive_session, so ask first unless the user asked for it. To end yourself, call kill_self or archive_self.",
		Annotations: toolAnnotations(false, true, true),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args sessionTargetArgs) (*mcp.CallToolResult, sessioncmd.Deletion, error) {
		remove := func() (sessioncmd.Deletion, error) { return sessions.Delete(sessionID, args.SessionID) }
		if ref, ok := remote.ParseRef(args.SessionID); ok {
			remove = func() (sessioncmd.Deletion, error) { return remotes.delete(ctx, ref) }
		}
		deleted, err := remove()
		if err != nil {
			return nil, sessioncmd.Deletion{}, err
		}
		return textContent(sessioncmd.FormatDeletion(deleted)), deleted, nil
	})
}

func (h *remoteHosts) delete(ctx context.Context, ref remote.Ref) (sessioncmd.Deletion, error) {
	if _, err := h.ready(); err != nil {
		return sessioncmd.Deletion{}, err
	}
	deleted, err := h.client.Delete(ctx, ref)
	deleted.Session = qualifySession(ref.Host, deleted.Session)
	return deleted, err
}

package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addTool[In, Out any](server *mcp.Server, noCaller error, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, Out, error) {
		if noCaller != nil {
			var none Out
			return nil, none, noCaller
		}
		return handler(ctx, req, args)
	})
}

func toolAnnotations(readOnly, destructive, openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    readOnly,
		DestructiveHint: &destructive,
		IdempotentHint:  readOnly,
		OpenWorldHint:   &openWorld,
	}
}

func textContent(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}

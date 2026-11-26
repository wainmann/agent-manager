package mcpserver

type listSessionsArgs struct {
	IncludeArchived bool `json:"include_archived,omitempty" jsonschema:"also list archived sessions, which are otherwise left out apart from this session's own row; set it to find the id of one to restore or to match saved conversations"`
}

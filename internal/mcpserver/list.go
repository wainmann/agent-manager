package mcpserver

type listSessionsArgs struct {
	IncludeArchived bool `json:"include_archived,omitempty" jsonschema:"also list archived sessions, which are left out by default; set it to find the id of one to restore or to match saved conversations"`
}

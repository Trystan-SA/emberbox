package tools

import "github.com/Trystan-SA/emberbox/tool"

// RegisterDefaults registers all built-in tools (bash, file_read, file_write,
// file_edit, glob, grep, web_fetch) on the given registry. Existing entries
// with the same name are replaced.
func RegisterDefaults(r *tool.Registry) {
	r.Register(&BashTool{})
	r.Register(&FileReadTool{})
	r.Register(&FileWriteTool{})
	r.Register(&FileEditTool{})
	r.Register(&GlobTool{})
	r.Register(&GrepTool{})
	r.Register(&WebFetchTool{})
}

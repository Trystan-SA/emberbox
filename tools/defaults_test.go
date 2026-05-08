package tools

import (
	"testing"

	"github.com/Trystan-SA/emberbox/tool"
	"github.com/stretchr/testify/require"
)

func TestRegisterDefaults(t *testing.T) {
	r := tool.NewRegistry()
	RegisterDefaults(r)

	want := []string{"bash", "file_read", "file_write", "file_edit", "glob", "grep", "web_fetch"}
	for _, name := range want {
		_, ok := r.Get(name)
		require.True(t, ok, "expected %q to be registered", name)
	}
	require.Len(t, r.List(), len(want))
}

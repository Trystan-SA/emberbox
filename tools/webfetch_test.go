package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebFetchTool_Get(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello body"))
	}))
	defer srv.Close()

	in, err := json.Marshal(map[string]any{"url": srv.URL})
	require.NoError(t, err)

	res, err := (&WebFetchTool{}).Execute(context.Background(), in)
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Contains(t, res.Content, "hello body")
}

func TestWebFetchTool_MissingURL(t *testing.T) {
	in, err := json.Marshal(map[string]any{})
	require.NoError(t, err)

	res, err := (&WebFetchTool{}).Execute(context.Background(), in)
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestWebFetchTool_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	in, err := json.Marshal(map[string]any{"url": srv.URL})
	require.NoError(t, err)

	res, err := (&WebFetchTool{}).Execute(context.Background(), in)
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)
	// Non-2xx responses are not flagged as errors; the implementation returns
	// "HTTP <status>\n\n<body>" unconditionally regardless of status code.
	require.False(t, res.IsError)
	require.Contains(t, res.Content, "500")
}

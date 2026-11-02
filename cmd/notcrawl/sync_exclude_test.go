package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/notcrawl/internal/config"
	"github.com/openclaw/notcrawl/internal/notionmcp"
)

func TestSyncMCPCombinesConfigAndRepeatedExcludeFlags(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(dir, "notcrawl.db")
	cfg.Notion.MCP.AuthPath = filepath.Join(dir, "auth.json")
	cfg.Notion.MCP.Exclude = []string{"11111111-1111-1111-1111-111111111111"}
	if err := os.WriteFile(cfg.Notion.MCP.AuthPath, []byte(`{"tokens":{"access_token":"test-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "initialize":
			writeMainRPCResult(w, request.ID, map[string]any{"protocolVersion": "2025-03-26"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/list":
			writeMainRPCResult(w, request.ID, map[string]any{"tools": []map[string]any{
				{"name": "notion_fetch", "_meta": map[string]any{"connector_id": cfg.Notion.MCP.ConnectorID}},
				{"name": "notion_search", "_meta": map[string]any{"connector_id": cfg.Notion.MCP.ConnectorID}},
			}})
		default:
			t.Error("excluded page reached gateway")
			http.Error(w, "unexpected request", 400)
		}
	}))
	defer server.Close()
	cfg.Notion.MCP.BaseURL = server.URL
	original := newNotionMCPClient
	newNotionMCPClient = func(cfg config.Config) notionmcp.Client { c := original(cfg); c.AllowUnsafeBaseURL = true; return c }
	t.Cleanup(func() { newNotionMCPClient = original })
	var stdout, stderr bytes.Buffer
	err := runSync(context.Background(), &stdout, &stderr, cfg, []string{"--source", "notion-mcp", "--exclude", "22222222-2222-2222-2222-222222222222", "--exclude", "33333333-3333-3333-3333-333333333333", "--page", "11111111-1111-1111-1111-111111111111", "--page", "22222222-2222-2222-2222-222222222222", "--page", "33333333-3333-3333-3333-333333333333"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "candidates=0") || !strings.Contains(stdout.String(), "excluded: 3") {
		t.Fatalf("exclusions not combined: %s", stdout.String())
	}
}

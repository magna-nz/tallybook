package main

import (
	"github.com/magna-nz/tallybook/internal/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newApp opens the application layer every tool answers from. It is a thin
// wrapper over query.New so this package names the constructor the same way
// its tests and main always have.
func newApp() (*query.App, error) {
	return query.New()
}

// textResult wraps human-readable text as a tool result. The structured
// value is passed back separately by the typed handler.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// newServer builds the MCP server with every tool registered against a.
func newServer(a *query.App, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "tallybook", Version: version}, nil)
	registerTools(s, a)
	return s
}

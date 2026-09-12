package main

import (
	"context"

	"github.com/magna-nz/tallybook/internal/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// call wraps a tool body with the locking and freshness check every tool
// needs. The body returns the text a model reads and the structured value a
// client computes on. An error becomes an error result for that call only.
// When autoRefresh is false the body is responsible for scanning itself.
func call[In, Out any](a *query.App, autoRefresh bool, body func(In) (string, Out, error)) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		a.Lock()
		defer a.Unlock()
		var zero Out
		if err := ctx.Err(); err != nil {
			return nil, zero, err // cancelled while queued behind another call
		}
		if autoRefresh {
			a.EnsureFresh()
		}
		text, out, err := body(in)
		if err != nil {
			return nil, zero, err
		}
		return textResult(a.FreshnessNote() + text), out, nil
	}
}

// queryTool marks a tool that only reads. The lazy rescan such a tool may
// trigger writes only tallybook's own cache, which is not a change to the
// caller's environment in the sense the hint describes.
func queryTool() *mcp.ToolAnnotations {
	closed := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}
}

// refreshTool marks the one tool whose purpose is to write: it updates
// tallybook's database. Nothing is destroyed, and running it twice is the
// same as running it once.
func refreshTool() *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: true, OpenWorldHint: &no}
}

func registerTools(s *mcp.Server, a *query.App) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "report",
		Description: "What the coding agents on this machine cost over a window: the total, the split by tool and by model, and the list of findings about what would have been cheaper. Call finding with an id from the list for the full advice.",
		Annotations: queryTool(),
	}, call(a, true, a.Report))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "finding",
		Description: "One finding in full, addressed by its stable id from report: what happened, why it costs, what to change, what to expect, plus the evidence table and the patch if there is a file to change.",
		Annotations: queryTool(),
	}, call(a, true, a.Finding))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "changes",
		Description: "Every point where a sub-agent's model changed, with the real runs before and after compared and a verdict: keep, watch, revert, or too early. These are measured figures, not estimates.",
		Annotations: queryTool(),
	}, call(a, true, a.Changes))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "agents",
		Description: "Spend per sub-agent type: runs, the model it mostly used, average cost per run, the share of runs whose tool calls were all read-only, errored tool results, and runs where the requested model differed from the one actually used.",
		Annotations: queryTool(),
	}, call(a, true, a.Agents))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "sessions",
		Description: "Sessions in the window with what each cost, sorted by cost or by time, most expensive or most recent first. Includes sub-agent runs as their own rows.",
		Annotations: queryTool(),
	}, call(a, true, a.Sessions))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "session",
		Description: "One session turn by turn: model, effort, token counts and cost per model response. Accepts a full session id or a unique prefix. Never returns prompt text, tool output, or command lines; the database does not hold them.",
		Annotations: queryTool(),
	}, call(a, true, a.Session))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "prices",
		Description: "The price table every figure is computed from, in US dollars per million tokens, with the date it was last verified. Includes any overrides from the user's config.",
		Annotations: queryTool(),
	}, call(a, false, a.Prices))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "refresh",
		Description: "Rescan the transcript directories now instead of waiting for the automatic rescan, and report how many files were scanned, newly recorded, unchanged, or skipped. Only tallybook's own database is written.",
		Annotations: refreshTool(),
	}, call(a, false, a.Refresh))
}

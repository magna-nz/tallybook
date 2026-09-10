package report

import (
	"encoding/json"
	"io"
	"time"

	"github.com/magna-nz/tallybook/internal/findings"
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/pricing"
)

func writeJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// reportFindingJSON is the JSON shape of one line in the top-findings list.
type reportFindingJSON struct {
	Title      string  `json:"title"`
	SavingUSD  float64 `json:"savingUSD"`
	Share      float64 `json:"share"`
	Confidence string  `json:"confidence"`
	Direction  string  `json:"direction"`
}

// reportJSONDoc is the document ReportJSON writes.
type reportJSONDoc struct {
	Schema int `json:"schema"`

	Window struct {
		Since string  `json:"since,omitempty"`
		Until string  `json:"until"`
		Days  float64 `json:"days"`
		Label string  `json:"label"`
	} `json:"window"`

	Sessions  int `json:"sessions"`
	Subagents int `json:"subagents"`

	Plan        string                `json:"plan"`
	USD         float64               `json:"usd"`
	MainUSD     float64               `json:"mainUSD"`
	SubagentUSD float64               `json:"subagentUSD"`
	BySource    map[string]sourceJSON `json:"bySource"`

	Findings []reportFindingJSON `json:"findings"`

	SkippedFiles int `json:"skippedFiles"`
}

// ReportJSON writes ReportData as JSON.
func ReportJSON(w io.Writer, d ReportData) error {
	doc := reportJSONDoc{Schema: 1}
	if !d.Window.Since.IsZero() {
		doc.Window.Since = d.Window.Since.Format("2006-01-02")
	}
	doc.Window.Until = d.Window.Until.Format("2006-01-02")
	doc.Window.Days = d.Window.Days
	doc.Window.Label = d.Window.Label
	doc.Sessions = d.Sessions
	doc.Subagents = d.Subagents
	doc.Plan = string(d.Plan)
	doc.USD = d.Totals.USD
	doc.MainUSD = d.Totals.MainUSD
	doc.SubagentUSD = d.Totals.SubagentUSD
	doc.BySource = map[string]sourceJSON{}
	for src, st := range d.Totals.BySource {
		doc.BySource[string(src)] = sourceJSON{USD: st.USD, Sessions: st.Sessions, Turns: st.Turns}
	}
	doc.SkippedFiles = d.SkippedFiles

	for _, f := range d.Findings {
		doc.Findings = append(doc.Findings, reportFindingJSON{
			Title:      f.Title,
			SavingUSD:  f.SavingUSD,
			Share:      f.SavingShare,
			Confidence: string(f.Confidence),
			Direction:  string(f.Direction),
		})
	}

	return writeJSON(w, doc)
}

// findingJSONDoc is the document FindingJSON writes.
type findingJSONDoc struct {
	Schema int `json:"schema"`

	Index      int     `json:"index"`
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	SavingUSD  float64 `json:"savingUSD"`
	Share      float64 `json:"share"`
	Confidence string  `json:"confidence"`
	Direction  string  `json:"direction"`

	WhatHappened string `json:"whatHappened"`
	WhyItCosts   string `json:"whyItCosts"`
	WhatToChange string `json:"whatToChange"`
	WhatToExpect string `json:"whatToExpect"`

	Patch    string          `json:"patch,omitempty"`
	Evidence *findings.Table `json:"evidence,omitempty"`
}

// FindingJSON writes one finding as JSON.
func FindingJSON(w io.Writer, f findings.Finding, index int, mode FindingMode) error {
	doc := findingJSONDoc{
		Schema:       1,
		Index:        index,
		ID:           f.ID,
		Title:        f.Title,
		SavingUSD:    f.SavingUSD,
		Share:        f.SavingShare,
		Confidence:   string(f.Confidence),
		Direction:    string(f.Direction),
		WhatHappened: f.WhatHappened,
		WhyItCosts:   f.WhyItCosts,
		WhatToChange: f.WhatToChange,
		WhatToExpect: f.WhatToExpect,
		Patch:        f.Patch,
	}
	if mode.Evidence {
		doc.Evidence = &f.Evidence
	}
	return writeJSON(w, doc)
}

// agentRowJSON is the JSON shape of one ledger.AgentRow.
type agentRowJSON struct {
	Agent       string  `json:"agent"`
	Runs        int     `json:"runs"`
	Model       string  `json:"model"`
	AvgUSD      float64 `json:"avgUSD"`
	ReadOnlyPct float64 `json:"readOnlyPct"`
	Errors      int     `json:"errors"`
	Mismatched  int     `json:"mismatched"`
}

// agentsJSONDoc is the document AgentsJSON writes.
type agentsJSONDoc struct {
	Schema int            `json:"schema"`
	Agents []agentRowJSON `json:"agents"`
}

// AgentsJSON writes agent rollup rows as JSON.
func AgentsJSON(w io.Writer, rows []ledger.AgentRow) error {
	doc := agentsJSONDoc{Schema: 1}
	for _, r := range rows {
		doc.Agents = append(doc.Agents, agentRowJSON{
			Agent: r.Agent, Runs: r.Runs, Model: r.Model, AvgUSD: r.AvgUSD,
			ReadOnlyPct: r.ReadOnlyPct, Errors: r.Errors, Mismatched: r.Mismatched,
		})
	}
	return writeJSON(w, doc)
}

// sessionRowJSON is the JSON shape of one ledger.SessionCost.
type sessionRowJSON struct {
	ID        string  `json:"id"`
	Source    string  `json:"source"`
	Project   string  `json:"project"`
	StartedAt string  `json:"startedAt"`
	Turns     int     `json:"turns"`
	USD       float64 `json:"usd"`
	Known     bool    `json:"known"`
	AgentType string  `json:"agentType,omitempty"`
}

// sessionsJSONDoc is the document SessionsJSON writes.
type sessionsJSONDoc struct {
	Schema   int              `json:"schema"`
	Sessions []sessionRowJSON `json:"sessions"`
}

// SessionsJSON writes the first limit sessions as JSON, in the order given.
func SessionsJSON(w io.Writer, rows []ledger.SessionCost, limit int) error {
	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	doc := sessionsJSONDoc{Schema: 1}
	for _, r := range rows {
		doc.Sessions = append(doc.Sessions, sessionRowJSON{
			ID: r.ID, Source: string(r.Source), Project: r.Project, StartedAt: r.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
			Turns: r.Turns, USD: r.USD, Known: r.Known, AgentType: r.AgentType,
		})
	}
	return writeJSON(w, doc)
}

// turnJSON is the JSON shape of one model.Turn in a session's detail view.
type turnJSON struct {
	Index  int    `json:"index"`
	Time   string `json:"time"`
	Model  string `json:"model"`
	Input  int64  `json:"input"`
	Cached int64  `json:"cached"`
	Output int64  `json:"output"`
}

// sessionJSONDoc is the document SessionJSON writes.
type sessionJSONDoc struct {
	Schema int        `json:"schema"`
	ID     string     `json:"id"`
	USD    float64    `json:"usd"`
	Turns  []turnJSON `json:"turns"`
}

// SessionJSON writes one session's turn detail as JSON.
func SessionJSON(w io.Writer, id string, turns []model.Turn, cost float64) error {
	doc := sessionJSONDoc{Schema: 1, ID: id, USD: cost}
	for i, t := range turns {
		doc.Turns = append(doc.Turns, turnJSON{
			Index: i + 1, Time: t.Timestamp.Format(time.RFC3339), Model: t.Model,
			Input: t.Usage.Input, Cached: t.Usage.CacheRead, Output: t.Usage.Output,
		})
	}
	return writeJSON(w, doc)
}

// statusJSONDoc is the document StatusJSON writes.
type statusJSONDoc struct {
	Schema int `json:"schema"`

	DBPath  string `json:"dbPath"`
	DBBytes int64  `json:"dbBytes"`

	ClaudeRoots []string `json:"claudeRoots"`
	ClaudeCount int      `json:"claudeCount"`
	CodexRoots  []string `json:"codexRoots"`
	CodexCount  int      `json:"codexCount"`

	HaveIngest bool `json:"haveIngest"`
	Scanned    int  `json:"scanned"`
	Ingested   int  `json:"ingested"`
	Unchanged  int  `json:"unchanged"`
	Failed     int  `json:"failed"`

	Plan       string `json:"plan"`
	PlanReason string `json:"planReason"`

	PricesDated string `json:"pricesDated"`
}

// StatusJSON writes the status summary as JSON.
func StatusJSON(w io.Writer, d StatusData) error {
	doc := statusJSONDoc{
		Schema: 1, DBPath: d.DBPath, DBBytes: d.DBBytes,
		ClaudeRoots: d.ClaudeRoots, ClaudeCount: d.ClaudeCount,
		CodexRoots: d.CodexRoots, CodexCount: d.CodexCount,
		HaveIngest: d.HaveIngest, Plan: string(d.Plan), PlanReason: d.PlanReason,
		PricesDated: d.PricesDated,
	}
	if d.HaveIngest {
		doc.Scanned = d.LastIngest.Scanned
		doc.Ingested = d.LastIngest.Ingested
		doc.Unchanged = d.LastIngest.Unchanged
		doc.Failed = d.LastIngest.Failed
	}
	return writeJSON(w, doc)
}

// pricesJSONDoc is the document PricesJSON writes.
type pricesJSONDoc struct {
	Schema int              `json:"schema"`
	Models []priceModelJSON `json:"models"`
}

type priceModelJSON struct {
	ID           string  `json:"id"`
	Input        float64 `json:"input"`
	CacheRead    float64 `json:"cacheRead"`
	CacheWrite5m float64 `json:"cacheWrite5m"`
	CacheWrite1h float64 `json:"cacheWrite1h"`
	Output       float64 `json:"output"`
}

// PricesJSON writes the price table as JSON.
func PricesJSON(w io.Writer, table *pricing.Table) error {
	doc := pricesJSONDoc{Schema: 1}
	for _, id := range table.Models() {
		r, ok := table.Lookup(id)
		if !ok {
			continue
		}
		doc.Models = append(doc.Models, priceModelJSON{
			ID: id, Input: r.Input, CacheRead: r.CacheRead,
			CacheWrite5m: r.CacheWrite5m, CacheWrite1h: r.CacheWrite1h, Output: r.Output,
		})
	}
	return writeJSON(w, doc)
}

type sourceJSON struct {
	USD      float64 `json:"usd"`
	Sessions int     `json:"sessions"`
	Turns    int     `json:"turns"`
}

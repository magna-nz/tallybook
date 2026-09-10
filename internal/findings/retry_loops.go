package findings

import (
	"fmt"
	"sort"
)

// retryLoopRule finds sessions that failed their way through a task. It is
// the counterweight to the downgrade findings: these sessions needed a better
// brief or a stronger model, not a cheaper one. It never claims a saving.
type retryLoopRule struct{}

func (retryLoopRule) ID() string { return "retry-loops" }

// struggling is one session that kept hitting errors.
type struggling struct {
	id        string
	project   string
	agent     string
	tool      string
	errors    int
	calls     int
	cost      float64
	windowRun int // the worst run of same-tool errors inside 6 consecutive calls
}

// retryWindow is how many consecutive tool calls the run of errors has to
// fall inside to count as one struggle rather than bad luck spread thin.
const retryWindow = 6

func (retryLoopRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}

	threshold := in.Cfg.RetryThreshold
	if threshold <= 0 {
		threshold = 3
	}

	var hits []struggling
	for _, row := range rows {
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}
		nameByCall := map[string]string{}
		calls := 0
		for _, t := range turns {
			for _, tc := range t.ToolCalls {
				nameByCall[tc.ID] = tc.Name
				calls++
			}
		}

		results, err := in.Store.ToolResults(row.ID)
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			continue
		}

		names := make([]string, len(results))
		failed := make([]bool, len(results))
		errorsByTool := map[string]int{}
		errors := 0
		for i, r := range results {
			name := nameByCall[r.ToolCallID]
			if name == "" {
				name = "unknown tool"
			}
			names[i] = name
			failed[i] = r.IsError
			if r.IsError {
				errors++
				errorsByTool[name]++
			}
		}
		if errors == 0 {
			continue
		}

		worstRun, worstTool := longestSameToolRun(names, failed)
		burst := worstRun >= threshold
		rate := calls >= 8 && float64(errors) >= 0.25*float64(calls)
		if !burst && !rate {
			continue
		}

		tool := worstTool
		if !burst {
			tool = mostErroredTool(errorsByTool)
		}
		agent := row.AgentType
		if agent == "" {
			if row.AgentID != "" {
				agent = "unnamed agent"
			} else {
				agent = "main session"
			}
		}
		hits = append(hits, struggling{
			id: row.ID, project: row.Project, agent: agent, tool: tool,
			errors: errors, calls: calls, cost: costOf(in, turns), windowRun: worstRun,
		})
	}
	if len(hits) == 0 {
		return nil, nil
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].errors != hits[j].errors {
			return hits[i].errors > hits[j].errors
		}
		return hits[i].id < hits[j].id
	})

	var ids []string
	for i, h := range hits {
		if i == 3 {
			ids = append(ids, fmt.Sprintf("%s more", fmtInt(int64(len(hits)-3))))
			break
		}
		ids = append(ids, shortID(h.id))
	}

	f := &Finding{
		Direction:   Upgrade,
		Confidence:  Info,
		SavingUSD:   0,
		SavingShare: 0,
		Title: fmt.Sprintf("%s session%s %s under-powered",
			fmtInt(int64(len(hits))), plural(len(hits)), pick(len(hits), "looks", "look")),
	}

	f.WhatHappened = fmt.Sprintf(
		"%s %s tried the same kind of action %s or more times with errors in between, "+
			"before it worked or the session ended. The tool that failed most often was %s.",
		pick(len(hits), "Session", "Sessions"), joinList(ids), fmtInt(int64(threshold)), hits[0].tool)

	f.WhyItCosts = "This one is not a saving. It is the opposite of the findings above. " +
		"Retries burn tokens, and they usually mean the task was harder than the model or the instructions " +
		"could handle. Moving these to a cheaper model would make it worse."

	f.WhatToChange = "Look at what those sessions were doing. If they were sub-agents, give them a fuller brief: " +
		"the exact file, the surrounding code, and a command to check their work. " +
		"If they were main sessions, this may be the kind of task that is worth the strong model."

	f.WhatToExpect = "Nothing to change in settings. " +
		"This is a pointer to where your prompts need work, not your model choice."

	f.Evidence = Table{Columns: []string{"session", "project", "agent", "tool", "errors", "calls", "cost"}}
	for _, h := range hits {
		f.Evidence.Rows = append(f.Evidence.Rows, []string{
			shortID(h.id), h.project, h.agent, h.tool,
			fmtInt(int64(h.errors)), fmtInt(int64(h.calls)), fmtUSD(h.cost),
		})
	}
	return f, nil
}

// longestSameToolRun returns the most errors one tool racked up inside any
// window of retryWindow consecutive tool calls, and which tool that was.
func longestSameToolRun(names []string, failed []bool) (int, string) {
	best, bestTool := 0, ""
	for start := range names {
		end := start + retryWindow
		if end > len(names) {
			end = len(names)
		}
		counts := map[string]int{}
		for i := start; i < end; i++ {
			if failed[i] {
				counts[names[i]]++
			}
		}
		for _, tool := range sortedKeys(counts) {
			if counts[tool] > best {
				best, bestTool = counts[tool], tool
			}
		}
	}
	return best, bestTool
}

// mostErroredTool is the tool with the most failures over the whole session.
func mostErroredTool(errorsByTool map[string]int) string {
	best, bestN := "", 0
	for _, tool := range sortedKeys(errorsByTool) {
		if errorsByTool[tool] > bestN {
			best, bestN = tool, errorsByTool[tool]
		}
	}
	return best
}

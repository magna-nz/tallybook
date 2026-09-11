package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
)

// Filter narrows queries. Zero values mean "no constraint".
type Filter struct {
	Since, Until time.Time // on session StartedAt
	Project      string    // exact match on Session.Project, or prefix match if it ends with "/"
	Source       model.Source
}

// whereClause returns the SQL WHERE conditions (joined with AND, no leading
// "WHERE") and the matching args, for filtering the sessions table.
func (f Filter) whereClause() (string, []interface{}) {
	var conds []string
	var args []interface{}

	if !f.Since.IsZero() {
		conds = append(conds, "started_at >= ?")
		args = append(args, f.Since.UnixNano())
	}
	if !f.Until.IsZero() {
		conds = append(conds, "started_at <= ?")
		args = append(args, f.Until.UnixNano())
	}
	if f.Project != "" {
		// A trailing separator asks for everything under that directory.
		// Windows users type a backslash, so both are accepted, and both are
		// matched: the stored value is a session's working directory, so which
		// separator it holds depends on the machine that recorded it, not on
		// the one running the query.
		if strings.HasSuffix(f.Project, "/") || strings.HasSuffix(f.Project, `\`) {
			prefix := escapeLike(strings.TrimRight(f.Project, `/\`))
			conds = append(conds, "(project LIKE ? ESCAPE '!' OR project LIKE ? ESCAPE '!')")
			args = append(args, prefix+"/%", prefix+`\%`)
		} else {
			conds = append(conds, "project = ?")
			args = append(args, f.Project)
		}
	}
	if f.Source != "" {
		conds = append(conds, "source = ?")
		args = append(args, string(f.Source))
	}

	return strings.Join(conds, " AND "), args
}

// filteredSessionIDs returns the ids of sessions matching f.
func (s *Store) filteredSessionIDs(f Filter) ([]string, error) {
	where, args := f.whereClause()
	query := `SELECT id FROM sessions`
	if where != "" {
		query += " WHERE " + where
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: filter sessions: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan session id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// placeholders returns "?,?,...,?" with n placeholders.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := strings.Repeat("?,", n)
	return b[:len(b)-1]
}

func toArgs(ids []string) []interface{} {
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

// SessionRow is a session plus rollups. AgentType/RequestedModel/ResolvedModel
// are filled from the parent's agent_launches row when this is a sub-agent
// session (join on parent_session_id + agent_id); otherwise empty.
type SessionRow struct {
	model.Session
	Turns          int
	ToolCalls      int
	ToolErrors     int
	Usage          model.Usage // summed over turns
	Models         []string    // distinct, sorted
	AgentType      string
	RequestedModel string
	ResolvedModel  string
}

// Sessions returns the sessions matching f, ordered by StartedAt.
func (s *Store) Sessions(f Filter) ([]SessionRow, error) {
	ids, err := s.filteredSessionIDs(f)
	if err != nil {
		return nil, err
	}
	return s.sessionRowsByIDs(ids)
}

// Session returns the session with the given id, or nil, nil if it does not
// exist.
func (s *Store) Session(id string) (*SessionRow, error) {
	rows, err := s.sessionRowsByIDs([]string{id})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func (s *Store) sessionRowsByIDs(ids []string) ([]SessionRow, error) {
	if len(ids) == 0 {
		return []SessionRow{}, nil
	}
	ph := placeholders(len(ids))

	query := fmt.Sprintf(`
		SELECT
			s.id, s.source, s.path, s.project, s.git_branch, s.started_at, s.ended_at,
			s.cli_version, s.parent_session_id, s.agent_id,
			COALESCE(tu.turns, 0), COALESCE(tc.tool_calls, 0), COALESCE(te.tool_errors, 0),
			COALESCE(tu.input, 0), COALESCE(tu.cache_read, 0), COALESCE(tu.cache_write_5m, 0),
			COALESCE(tu.cache_write_1h, 0), COALESCE(tu.output, 0), COALESCE(tu.thinking, 0),
			al.subagent_type, al.requested_model, al.resolved_model
		FROM sessions s
		LEFT JOIN (
			SELECT session_id, COUNT(*) turns, SUM(input) input, SUM(cache_read) cache_read,
				SUM(cache_write_5m) cache_write_5m, SUM(cache_write_1h) cache_write_1h,
				SUM(output) output, SUM(thinking) thinking
			FROM turns GROUP BY session_id
		) tu ON tu.session_id = s.id
		LEFT JOIN (
			SELECT session_id, COUNT(*) tool_calls FROM tool_calls GROUP BY session_id
		) tc ON tc.session_id = s.id
		LEFT JOIN (
			SELECT session_id, SUM(is_error) tool_errors FROM tool_results GROUP BY session_id
		) te ON te.session_id = s.id
		LEFT JOIN agent_launches al
			ON al.session_id = s.parent_session_id AND al.agent_id = s.agent_id AND s.agent_id != ''
		WHERE s.id IN (%s)
		ORDER BY s.started_at`, ph)

	rows, err := s.db.Query(query, toArgs(ids)...)
	if err != nil {
		return nil, fmt.Errorf("store: query sessions: %w", err)
	}
	defer rows.Close()

	var result []SessionRow
	for rows.Next() {
		var r SessionRow
		var startedAt, endedAt int64
		var subagentType, requestedModel, resolvedModel sql.NullString

		err := rows.Scan(
			&r.ID, &r.Source, &r.Path, &r.Project, &r.GitBranch, &startedAt, &endedAt,
			&r.CLIVersion, &r.ParentSessionID, &r.AgentID,
			&r.Turns, &r.ToolCalls, &r.ToolErrors,
			&r.Usage.Input, &r.Usage.CacheRead, &r.Usage.CacheWrite5m,
			&r.Usage.CacheWrite1h, &r.Usage.Output, &r.Usage.Thinking,
			&subagentType, &requestedModel, &resolvedModel,
		)
		if err != nil {
			return nil, fmt.Errorf("store: scan session row: %w", err)
		}

		r.StartedAt = fromUnixNanos(startedAt)
		r.EndedAt = fromUnixNanos(endedAt)
		r.AgentType = subagentType.String
		r.RequestedModel = requestedModel.String
		r.ResolvedModel = resolvedModel.String

		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate session rows: %w", err)
	}

	if err := s.attachModels(ids, result); err != nil {
		return nil, err
	}

	return result, nil
}

// attachModels fills in the distinct, sorted Models slice for each row.
func (s *Store) attachModels(ids []string, result []SessionRow) error {
	if len(result) == 0 {
		return nil
	}
	ph := placeholders(len(ids))
	query := fmt.Sprintf(`
		SELECT DISTINCT session_id, model FROM turns
		WHERE session_id IN (%s) AND model IS NOT NULL AND model != ''
		ORDER BY session_id, model`, ph)

	rows, err := s.db.Query(query, toArgs(ids)...)
	if err != nil {
		return fmt.Errorf("store: query models: %w", err)
	}
	defer rows.Close()

	idxByID := make(map[string]int, len(result))
	for i, r := range result {
		idxByID[r.ID] = i
	}

	for rows.Next() {
		var sid, m string
		if err := rows.Scan(&sid, &m); err != nil {
			return fmt.Errorf("store: scan model row: %w", err)
		}
		if idx, ok := idxByID[sid]; ok {
			result[idx].Models = append(result[idx].Models, m)
		}
	}
	return rows.Err()
}

// Turns returns the turns of one session in timestamp order, each with its
// ToolCalls (and Agent launch data) attached.
func (s *Store) Turns(sessionID string) ([]model.Turn, error) {
	rows, err := s.db.Query(`
		SELECT id, ts, model, effort, input, cache_read, cache_write_5m, cache_write_1h, output, thinking, text_chars
		FROM turns WHERE session_id = ? ORDER BY ts`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: query turns: %w", err)
	}
	defer rows.Close()

	var turns []model.Turn
	indexByID := map[string]int{}
	for rows.Next() {
		var t model.Turn
		var ts int64
		err := rows.Scan(
			&t.ID, &ts, &t.Model, &t.Effort,
			&t.Usage.Input, &t.Usage.CacheRead, &t.Usage.CacheWrite5m, &t.Usage.CacheWrite1h,
			&t.Usage.Output, &t.Usage.Thinking, &t.TextChars,
		)
		if err != nil {
			return nil, fmt.Errorf("store: scan turn: %w", err)
		}
		t.SessionID = sessionID
		t.Timestamp = fromUnixNanos(ts)
		indexByID[t.ID] = len(turns)
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate turns: %w", err)
	}

	tcRows, err := s.db.Query(`
		SELECT turn_id, id, name, input_chars, class FROM tool_calls
		WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: query tool_calls: %w", err)
	}
	defer tcRows.Close()

	for tcRows.Next() {
		var turnID string
		var tc model.ToolCall
		if err := tcRows.Scan(&turnID, &tc.ID, &tc.Name, &tc.InputChars, &tc.Class); err != nil {
			return nil, fmt.Errorf("store: scan tool_call: %w", err)
		}
		if idx, ok := indexByID[turnID]; ok {
			turns[idx].ToolCalls = append(turns[idx].ToolCalls, tc)
		}
	}
	if err := tcRows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate tool_calls: %w", err)
	}

	alRows, err := s.db.Query(`
		SELECT tool_call_id, subagent_type, requested_model, resolved_model, agent_id, description
		FROM agent_launches WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: query agent_launches: %w", err)
	}
	defer alRows.Close()

	launches := map[string]model.AgentLaunch{}
	for alRows.Next() {
		var toolCallID string
		var al model.AgentLaunch
		err := alRows.Scan(&toolCallID, &al.SubagentType, &al.RequestedModel, &al.ResolvedModel, &al.AgentID, &al.Description)
		if err != nil {
			return nil, fmt.Errorf("store: scan agent_launch: %w", err)
		}
		launches[toolCallID] = al
	}
	if err := alRows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate agent_launches: %w", err)
	}

	for i := range turns {
		for j := range turns[i].ToolCalls {
			if al, ok := launches[turns[i].ToolCalls[j].ID]; ok {
				alCopy := al
				turns[i].ToolCalls[j].Agent = &alCopy
			}
		}
	}

	return turns, nil
}

// ToolResults returns the tool results recorded for a session, in timestamp
// order.
func (s *Store) ToolResults(sessionID string) ([]model.ToolResult, error) {
	rows, err := s.db.Query(`
		SELECT tool_call_id, ts, chars, is_error FROM tool_results
		WHERE session_id = ? ORDER BY ts`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: query tool_results: %w", err)
	}
	defer rows.Close()

	var out []model.ToolResult
	for rows.Next() {
		var tr model.ToolResult
		var ts int64
		var isError int
		if err := rows.Scan(&tr.ToolCallID, &ts, &tr.Chars, &isError); err != nil {
			return nil, fmt.Errorf("store: scan tool_result: %w", err)
		}
		tr.SessionID = sessionID
		tr.Timestamp = fromUnixNanos(ts)
		tr.IsError = isError != 0
		out = append(out, tr)
	}
	return out, rows.Err()
}

// ToolCounts returns, per session id in the filter, a map of tool name to
// count.
func (s *Store) ToolCounts(f Filter) (map[string]map[string]int, error) {
	ids, err := s.filteredSessionIDs(f)
	if err != nil {
		return nil, err
	}

	result := make(map[string]map[string]int, len(ids))
	for _, id := range ids {
		result[id] = map[string]int{}
	}
	if len(ids) == 0 {
		return result, nil
	}

	ph := placeholders(len(ids))
	query := fmt.Sprintf(`
		SELECT session_id,
			CASE WHEN class = '' THEN name ELSE name || '(' || class || ')' END AS label,
			COUNT(*) FROM tool_calls
		WHERE session_id IN (%s)
		GROUP BY session_id, label`, ph)

	rows, err := s.db.Query(query, toArgs(ids)...)
	if err != nil {
		return nil, fmt.Errorf("store: query tool counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sid, name string
		var count int
		if err := rows.Scan(&sid, &name, &count); err != nil {
			return nil, fmt.Errorf("store: scan tool count: %w", err)
		}
		if result[sid] == nil {
			result[sid] = map[string]int{}
		}
		result[sid][name] = count
	}
	return result, rows.Err()
}

// LaunchRow is one sub-agent launch seen from the parent, joined to the
// child session if it was ingested.
type LaunchRow struct {
	ParentSessionID string
	ToolCallID      string
	Timestamp       time.Time
	SubagentType    string
	RequestedModel  string
	ResolvedModel   string
	AgentID         string
	ChildSessionID  string // "" if the child transcript was not found
}

// Launches returns the sub-agent launches made by sessions matching f.
func (s *Store) Launches(f Filter) ([]LaunchRow, error) {
	ids, err := s.filteredSessionIDs(f)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []LaunchRow{}, nil
	}

	ph := placeholders(len(ids))
	query := fmt.Sprintf(`
		SELECT al.session_id, al.tool_call_id, t.ts, al.subagent_type, al.requested_model,
			al.resolved_model, al.agent_id, child.id
		FROM agent_launches al
		JOIN tool_calls tc ON tc.session_id = al.session_id AND tc.id = al.tool_call_id
		JOIN turns t ON t.session_id = al.session_id AND t.id = tc.turn_id
		LEFT JOIN sessions child
			ON child.parent_session_id = al.session_id AND child.agent_id = al.agent_id
		WHERE al.session_id IN (%s)
		ORDER BY t.ts`, ph)

	rows, err := s.db.Query(query, toArgs(ids)...)
	if err != nil {
		return nil, fmt.Errorf("store: query launches: %w", err)
	}
	defer rows.Close()

	var out []LaunchRow
	for rows.Next() {
		var lr LaunchRow
		var ts int64
		var childID sql.NullString
		err := rows.Scan(
			&lr.ParentSessionID, &lr.ToolCallID, &ts, &lr.SubagentType, &lr.RequestedModel,
			&lr.ResolvedModel, &lr.AgentID, &childID,
		)
		if err != nil {
			return nil, fmt.Errorf("store: scan launch: %w", err)
		}
		lr.Timestamp = fromUnixNanos(ts)
		if childID.Valid {
			lr.ChildSessionID = childID.String
		}
		out = append(out, lr)
	}
	return out, rows.Err()
}

// escapeLike neutralises the wildcards in a LIKE pattern. Without it a project
// directory called "my_app" would also match "myXapp", quietly folding another
// project's sessions into the report. "!" is the escape character, chosen
// because a backslash is ordinary in a Windows path.
func escapeLike(s string) string {
	r := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return r.Replace(s)
}

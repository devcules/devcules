package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// tracesRepo implements store.TracesRepo on the trace_calls table.
type tracesRepo struct {
	db *sql.DB
}

const traceColumns = `id, launch_id, at, provider, path, model, status, stream, duration_ms,
	input_tokens, output_tokens, redacted, question, reply, tool_calls`

// toolCallJSON is the stored shape of one tool call.
type toolCallJSON struct {
	Name string `json:"name"`
	Args string `json:"args"`
}

// Add stores c and returns its id.
func (r tracesRepo) Add(ctx context.Context, c store.TraceCall) (int64, error) {
	if c.LaunchID == "" {
		return 0, fmt.Errorf("store: trace launch id must not be empty")
	}
	redacted := c.Redacted
	if redacted == nil {
		redacted = map[string]int{}
	}
	total := 0
	for _, n := range redacted {
		total += n
	}
	tools := make([]toolCallJSON, 0, len(c.ToolCalls))
	for _, t := range c.ToolCalls {
		tools = append(tools, toolCallJSON{t.Name, t.Args})
	}
	rawRedacted, err := json.Marshal(redacted)
	if err != nil {
		return 0, fmt.Errorf("store: trace redacted: %w", err)
	}
	rawTools, err := json.Marshal(tools)
	if err != nil {
		return 0, fmt.Errorf("store: trace tool calls: %w", err)
	}
	stream := 0
	if c.Stream {
		stream = 1
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO trace_calls (launch_id, at, provider, path, model, status, stream, duration_ms,
		   input_tokens, output_tokens, redacted, redacted_total, question, reply, tool_calls)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.LaunchID, c.At.UTC().Format(nanoTime), c.Provider, c.Path, c.Model, c.Status, stream, c.DurationMS,
		c.InputTokens, c.OutputTokens, string(rawRedacted), total, c.Question, c.Reply, string(rawTools))
	if err != nil {
		return 0, fmt.Errorf("store: trace add for launch %q failed: %w", c.LaunchID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: trace add for launch %q failed: %w", c.LaunchID, err)
	}
	return id, nil
}

// Summaries sums calls per launch, latest call first.
func (r tracesRepo) Summaries(ctx context.Context, limit int) ([]store.TraceSession, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT launch_id, COUNT(*), SUM(input_tokens), SUM(output_tokens), SUM(redacted_total), MIN(at), MAX(at)
		 FROM trace_calls GROUP BY launch_id ORDER BY MAX(id) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: trace summaries failed: %w", err)
	}
	defer rows.Close()
	var out []store.TraceSession
	for rows.Next() {
		var s store.TraceSession
		var first, last string
		if err := rows.Scan(&s.LaunchID, &s.Calls, &s.InputTokens, &s.OutputTokens, &s.Redacted, &first, &last); err != nil {
			return nil, fmt.Errorf("store: trace summaries failed: %w", err)
		}
		if s.First, err = time.Parse(time.RFC3339Nano, first); err != nil {
			return nil, fmt.Errorf("store: trace summaries: %w", err)
		}
		if s.Last, err = time.Parse(time.RFC3339Nano, last); err != nil {
			return nil, fmt.Errorf("store: trace summaries: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: trace summaries failed: %w", err)
	}
	return out, nil
}

// Calls returns a launch's calls, oldest first.
func (r tracesRepo) Calls(ctx context.Context, launchID string) ([]store.TraceCall, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+traceColumns+` FROM trace_calls WHERE launch_id = ? ORDER BY id`, launchID)
	if err != nil {
		return nil, fmt.Errorf("store: trace calls failed: %w", err)
	}
	defer rows.Close()
	var out []store.TraceCall
	for rows.Next() {
		var c store.TraceCall
		var at, redacted, tools string
		var stream int
		if err := rows.Scan(&c.ID, &c.LaunchID, &at, &c.Provider, &c.Path, &c.Model, &c.Status, &stream, &c.DurationMS,
			&c.InputTokens, &c.OutputTokens, &redacted, &c.Question, &c.Reply, &tools); err != nil {
			return nil, fmt.Errorf("store: trace calls failed: %w", err)
		}
		c.Stream = stream == 1
		if c.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("store: trace %d at: %w", c.ID, err)
		}
		if err := json.Unmarshal([]byte(redacted), &c.Redacted); err != nil {
			return nil, fmt.Errorf("store: trace %d redacted: %w", c.ID, err)
		}
		var tc []toolCallJSON
		if err := json.Unmarshal([]byte(tools), &tc); err != nil {
			return nil, fmt.Errorf("store: trace %d tool calls: %w", c.ID, err)
		}
		for _, t := range tc {
			c.ToolCalls = append(c.ToolCalls, store.TraceToolCall{Name: t.Name, Args: t.Args})
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: trace calls failed: %w", err)
	}
	return out, nil
}

// DeleteBefore removes calls made before before.
func (r tracesRepo) DeleteBefore(ctx context.Context, before time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM trace_calls WHERE at < ?`, before.UTC().Format(nanoTime))
	if err != nil {
		return 0, fmt.Errorf("store: trace cleanup failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: trace cleanup failed: %w", err)
	}
	return int(n), nil
}

// nanoTime is RFC 3339 with a fixed nine-digit fraction: unlike
// RFC3339Nano it sorts as text, which range queries on at rely on.
// time.RFC3339Nano still parses it.
const nanoTime = "2006-01-02T15:04:05.000000000Z07:00"

package feedbacklab

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// Store interface for milestones, feedback, and analysis results
type Store interface {
	SaveMilestone(ctx context.Context, m *MilestoneSnapshot) error
	GetMilestone(ctx context.Context, id string) (*MilestoneSnapshot, error)
	ListMilestones(ctx context.Context) ([]MilestoneSnapshot, error)

	SaveResponse(ctx context.Context, r *FeedbackResponse) error
	ListResponses(ctx context.Context, milestoneID string) ([]FeedbackResponse, error)

	SaveAnalysis(ctx context.Context, a *AnalysisResult) error
	GetAnalysis(ctx context.Context, milestoneID string) (*AnalysisResult, error)

	Close() error
}

// MemoryStore is a concurrent, in-memory store for instant local testing
type MemoryStore struct {
	mu         sync.RWMutex
	milestones map[string]*MilestoneSnapshot
	responses  map[string][]*FeedbackResponse // milestoneID -> responses
	analyses   map[string]*AnalysisResult    // milestoneID -> analysis
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		milestones: make(map[string]*MilestoneSnapshot),
		responses:  make(map[string][]*FeedbackResponse),
		analyses:   make(map[string]*AnalysisResult),
	}
}

func (s *MemoryStore) SaveMilestone(ctx context.Context, m *MilestoneSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.milestones[m.ID] = m
	return nil
}

func (s *MemoryStore) GetMilestone(ctx context.Context, id string) (*MilestoneSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.milestones[id]
	if !ok {
		return nil, fmt.Errorf("milestone not found: %s", id)
	}
	return m, nil
}

func (s *MemoryStore) ListMilestones(ctx context.Context) ([]MilestoneSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]MilestoneSnapshot, 0, len(s.milestones))
	for _, m := range s.milestones {
		list = append(list, *m)
	}
	return list, nil
}

func (s *MemoryStore) SaveResponse(ctx context.Context, r *FeedbackResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[r.MilestoneID] = append(s.responses[r.MilestoneID], r)
	return nil
}

func (s *MemoryStore) ListResponses(ctx context.Context, milestoneID string) ([]FeedbackResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := s.responses[milestoneID]
	res := make([]FeedbackResponse, len(items))
	for i, it := range items {
		res[i] = *it
	}
	return res, nil
}

func (s *MemoryStore) SaveAnalysis(ctx context.Context, a *AnalysisResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.analyses[a.MilestoneID] = a
	return nil
}

func (s *MemoryStore) GetAnalysis(ctx context.Context, milestoneID string) (*AnalysisResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.analyses[milestoneID]
	if !ok {
		return nil, nil // No analysis yet
	}
	return a, nil
}

func (s *MemoryStore) Close() error {
	return nil
}

// SQLStore implements Store for both SQLite and PostgreSQL
type SQLStore struct {
	db     *sql.DB
	driver string // "sqlite" or "postgres"
}

func NewSQLStore(driverName, dataSourceName string) (*SQLStore, error) {
	db, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("open sql: %w", err)
	}

	store := &SQLStore{db: db, driver: driverName}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store, nil
}

func (s *SQLStore) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS milestones (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		title TEXT NOT NULL,
		summary TEXT NOT NULL,
		git_commit_sha TEXT NOT NULL,
		files_changed TEXT NOT NULL,
		diff_snippet TEXT NOT NULL,
		status TEXT NOT NULL,
		questions_json TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL
	);

	CREATE TABLE IF NOT EXISTS feedback_responses (
		id TEXT PRIMARY KEY,
		milestone_id TEXT NOT NULL REFERENCES milestones(id),
		author_name TEXT NOT NULL,
		author_role TEXT NOT NULL,
		author_email TEXT NOT NULL,
		answers_json TEXT NOT NULL,
		submitted_at TIMESTAMP NOT NULL
	);

	CREATE TABLE IF NOT EXISTS milestone_analyses (
		milestone_id TEXT PRIMARY KEY REFERENCES milestones(id),
		response_count INT NOT NULL,
		consensus_index REAL NOT NULL,
		overall_risk_tier TEXT NOT NULL,
		disposition TEXT NOT NULL,
		role_divergences_json TEXT NOT NULL,
		actionable_guidance_json TEXT NOT NULL,
		agent_prompt_delta TEXT NOT NULL,
		analyzed_at TIMESTAMP NOT NULL
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *SQLStore) SaveMilestone(ctx context.Context, m *MilestoneSnapshot) error {
	filesJSON, _ := json.Marshal(m.FilesChanged)
	qJSON, _ := json.Marshal(m.Questions)

	query := `
	INSERT INTO milestones (id, session_id, agent_id, title, summary, git_commit_sha, files_changed, diff_snippet, status, questions_json, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET status = excluded.status, summary = excluded.summary;
	`
	_, err := s.db.ExecContext(ctx, query,
		m.ID, m.SessionID, m.AgentID, m.Title, m.Summary, m.GitCommitSHA,
		string(filesJSON), m.DiffSnippet, m.Status, string(qJSON), m.CreatedAt.UTC(),
	)
	return err
}

func (s *SQLStore) GetMilestone(ctx context.Context, id string) (*MilestoneSnapshot, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, session_id, agent_id, title, summary, git_commit_sha, files_changed, diff_snippet, status, questions_json, created_at FROM milestones WHERE id = ?", id)

	var m MilestoneSnapshot
	var filesJSON, qJSON string
	err := row.Scan(
		&m.ID, &m.SessionID, &m.AgentID, &m.Title, &m.Summary, &m.GitCommitSHA,
		&filesJSON, &m.DiffSnippet, &m.Status, &qJSON, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(filesJSON), &m.FilesChanged)
	_ = json.Unmarshal([]byte(qJSON), &m.Questions)
	return &m, nil
}

func (s *SQLStore) ListMilestones(ctx context.Context) ([]MilestoneSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, session_id, agent_id, title, summary, git_commit_sha, files_changed, diff_snippet, status, questions_json, created_at FROM milestones ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []MilestoneSnapshot
	for rows.Next() {
		var m MilestoneSnapshot
		var filesJSON, qJSON string
		if err := rows.Scan(
			&m.ID, &m.SessionID, &m.AgentID, &m.Title, &m.Summary, &m.GitCommitSHA,
			&filesJSON, &m.DiffSnippet, &m.Status, &qJSON, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(filesJSON), &m.FilesChanged)
		_ = json.Unmarshal([]byte(qJSON), &m.Questions)
		list = append(list, m)
	}
	return list, rows.Err()
}

func (s *SQLStore) SaveResponse(ctx context.Context, r *FeedbackResponse) error {
	answersJSON, _ := json.Marshal(r.Answers)
	query := `
	INSERT INTO feedback_responses (id, milestone_id, author_name, author_role, author_email, answers_json, submitted_at)
	VALUES (?, ?, ?, ?, ?, ?, ?);
	`
	_, err := s.db.ExecContext(ctx, query,
		r.ID, r.MilestoneID, r.AuthorName, r.AuthorRole, r.AuthorEmail, string(answersJSON), r.SubmittedAt.UTC(),
	)
	return err
}

func (s *SQLStore) ListResponses(ctx context.Context, milestoneID string) ([]FeedbackResponse, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, milestone_id, author_name, author_role, author_email, answers_json, submitted_at FROM feedback_responses WHERE milestone_id = ? ORDER BY submitted_at ASC", milestoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []FeedbackResponse
	for rows.Next() {
		var r FeedbackResponse
		var answersJSON string
		if err := rows.Scan(&r.ID, &r.MilestoneID, &r.AuthorName, &r.AuthorRole, &r.AuthorEmail, &answersJSON, &r.SubmittedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(answersJSON), &r.Answers)
		res = append(res, r)
	}
	return res, rows.Err()
}

func (s *SQLStore) SaveAnalysis(ctx context.Context, a *AnalysisResult) error {
	divJSON, _ := json.Marshal(a.RoleDivergences)
	guidJSON, _ := json.Marshal(a.ActionableGuidance)

	query := `
	INSERT INTO milestone_analyses (milestone_id, response_count, consensus_index, overall_risk_tier, disposition, role_divergences_json, actionable_guidance_json, agent_prompt_delta, analyzed_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(milestone_id) DO UPDATE SET
		response_count = excluded.response_count,
		consensus_index = excluded.consensus_index,
		overall_risk_tier = excluded.overall_risk_tier,
		disposition = excluded.disposition,
		role_divergences_json = excluded.role_divergences_json,
		actionable_guidance_json = excluded.actionable_guidance_json,
		agent_prompt_delta = excluded.agent_prompt_delta,
		analyzed_at = excluded.analyzed_at;
	`
	_, err := s.db.ExecContext(ctx, query,
		a.MilestoneID, a.ResponseCount, a.ConsensusIndex, a.OverallRiskTier, string(a.Disposition),
		string(divJSON), string(guidJSON), a.AgentPromptDelta, a.AnalyzedAt.UTC(),
	)
	return err
}

func (s *SQLStore) GetAnalysis(ctx context.Context, milestoneID string) (*AnalysisResult, error) {
	row := s.db.QueryRowContext(ctx, "SELECT milestone_id, response_count, consensus_index, overall_risk_tier, disposition, role_divergences_json, actionable_guidance_json, agent_prompt_delta, analyzed_at FROM milestone_analyses WHERE milestone_id = ?", milestoneID)

	var a AnalysisResult
	var divJSON, guidJSON string
	err := row.Scan(
		&a.MilestoneID, &a.ResponseCount, &a.ConsensusIndex, &a.OverallRiskTier, &a.Disposition,
		&divJSON, &guidJSON, &a.AgentPromptDelta, &a.AnalyzedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(divJSON), &a.RoleDivergences)
	_ = json.Unmarshal([]byte(guidJSON), &a.ActionableGuidance)
	return &a, nil
}

func (s *SQLStore) Close() error {
	return s.db.Close()
}

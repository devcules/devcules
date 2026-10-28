package feedbacklab

import "time"

// QuestionType defines the sensory inquiry format
type QuestionType string

const (
	QuestionTypeLikert      QuestionType = "likert"       // 1-5 scale (e.g., Strongly Disagree -> Strongly Agree)
	QuestionTypePolarity    QuestionType = "polarity"     // Dilemma/Triad (e.g., Velocity vs Quality vs Security)
	QuestionTypeRisk        QuestionType = "risk"         // Risk tier (Low, Medium, High, Critical)
	QuestionTypeChoice      QuestionType = "choice"       // Single choice
	QuestionTypeMultiChoice QuestionType = "multichoice"  // Multiple selections
	QuestionTypeText        QuestionType = "text"         // Open-ended constructive inquiry
)

// Question represents a single sensemaking prompt
type Question struct {
	ID          string       `json:"id"`
	Type        QuestionType `json:"type"`
	Prompt      string       `json:"prompt"`
	Description string       `json:"description,omitempty"`
	Options     []string     `json:"options,omitempty"`     // For choice or polarity labels
	Required    bool         `json:"required"`
	Weight      float64      `json:"weight"`               // Importance weight in consensus (0.1 - 2.0)
	RoleTarget  string       `json:"role_target,omitempty"`// e.g. "engineering", "design", "product", "all"
}

// MilestoneSnapshot represents the agent's work state presented for review
type MilestoneSnapshot struct {
	ID           string     `json:"id"`
	SessionID    string     `json:"session_id"`
	AgentID      string     `json:"agent_id"`      // "llama-code", "claude-code", "codex"
	Title        string     `json:"title"`
	Summary      string     `json:"summary"`
	GitCommitSHA string     `json:"git_commit_sha"`
	FilesChanged []string   `json:"files_changed"`
	DiffSnippet  string     `json:"diff_snippet"`
	Status       string     `json:"status"`        // "awaiting_feedback", "in_analysis", "accepted", "revising"
	CreatedAt    time.Time  `json:"created_at"`
	Questions    []Question `json:"questions"`
}

// Answer is a single respondent's answer
type Answer struct {
	QuestionID  string      `json:"question_id"`
	ValueNumber *float64    `json:"value_number,omitempty"` // For Likert/Polarity scale 1-5
	ValueString string      `json:"value_string,omitempty"` // For choice or text
	ValueList   []string    `json:"value_list,omitempty"`   // For multichoice
}

// FeedbackResponse captures a teammate's feedback submission
type FeedbackResponse struct {
	ID          string    `json:"id"`
	MilestoneID string    `json:"milestone_id"`
	AuthorName  string    `json:"author_name"`
	AuthorRole  string    `json:"author_role"` // "Engineering", "Product", "Design", "QA/Security"
	AuthorEmail string    `json:"author_email"`
	Answers     []Answer  `json:"answers"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// RoleDivergence captures difference of opinion across functional roles
type RoleDivergence struct {
	QuestionID string             `json:"question_id"`
	Prompt     string             `json:"prompt"`
	RoleMeans  map[string]float64 `json:"role_means"` // role -> average score
	Divergence float64            `json:"divergence"` // standard deviation of role means
	IsPolarized bool              `json:"is_polarized"`
}

// DispositionType is the synthesized outcome for the agent
type DispositionType string

const (
	DispositionAccepted      DispositionType = "accepted"
	DispositionCourseCorrect DispositionType = "course_correct"
	DispositionNeedsClarify  DispositionType = "needs_clarification"
	DispositionRejected      DispositionType = "rejected"
)

// AnalysisResult encapsulates the calculated sensemaking metrics & agent instructions
type AnalysisResult struct {
	MilestoneID        string           `json:"milestone_id"`
	ResponseCount      int              `json:"response_count"`
	ConsensusIndex     float64          `json:"consensus_index"` // 0.0 (total deadlock) to 1.0 (unanimous agreement)
	OverallRiskTier    string           `json:"overall_risk_tier"`// "Low", "Medium", "High", "Critical"
	Disposition        DispositionType  `json:"disposition"`
	RoleDivergences    []RoleDivergence `json:"role_divergences"`
	ActionableGuidance []string         `json:"actionable_guidance"`
	AgentPromptDelta   string           `json:"agent_prompt_delta"` // Formatted markdown for Context v2
	AnalyzedAt         time.Time        `json:"analyzed_at"`
}

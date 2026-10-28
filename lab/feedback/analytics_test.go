package feedbacklab_test

import (
	"context"
	"testing"
	"time"

	feedbacklab "github.com/devcules/devcules/lab/feedback"
)

func TestSensemakingAnalyticsAndConsensus(t *testing.T) {
	milestone := &feedbacklab.MilestoneSnapshot{
		ID:    "ms-test",
		Title: "Test Migration",
		Questions: []feedbacklab.Question{
			{ID: "q1", Prompt: "Meets Requirements?", Type: feedbacklab.QuestionTypeLikert},
			{ID: "q2", Prompt: "Risk Level", Type: feedbacklab.QuestionTypeRisk},
		},
	}

	val5 := 5.0
	val1 := 1.0

	// Test 1: High consensus across roles
	alignedResponses := []feedbacklab.FeedbackResponse{
		{
			ID:          "r1",
			MilestoneID: "ms-test",
			AuthorRole:  "Engineering",
			Answers: []feedbacklab.Answer{
				{QuestionID: "q1", ValueNumber: &val5},
				{QuestionID: "q2", ValueString: "Low"},
			},
		},
		{
			ID:          "r2",
			MilestoneID: "ms-test",
			AuthorRole:  "Product",
			Answers: []feedbacklab.Answer{
				{QuestionID: "q1", ValueNumber: &val5},
				{QuestionID: "q2", ValueString: "Low"},
			},
		},
	}

	resAligned := feedbacklab.AnalyzeResponses(milestone, alignedResponses)
	if resAligned.ConsensusIndex < 0.9 {
		t.Fatalf("expected high consensus, got %f", resAligned.ConsensusIndex)
	}
	if resAligned.Disposition != feedbacklab.DispositionAccepted {
		t.Fatalf("expected accepted disposition, got %s", resAligned.Disposition)
	}

	// Test 2: Polarized opinions (Engineering loves it, DevOps reports critical risk)
	polarizedResponses := []feedbacklab.FeedbackResponse{
		{
			ID:          "r1",
			MilestoneID: "ms-test",
			AuthorRole:  "Engineering",
			Answers: []feedbacklab.Answer{
				{QuestionID: "q1", ValueNumber: &val5},
				{QuestionID: "q2", ValueString: "Low"},
			},
		},
		{
			ID:          "r2",
			MilestoneID: "ms-test",
			AuthorRole:  "QA/Security",
			Answers: []feedbacklab.Answer{
				{QuestionID: "q1", ValueNumber: &val1},
				{QuestionID: "q2", ValueString: "Critical"},
				{QuestionID: "q3_text", ValueString: "Blocks database connection pool!"},
			},
		},
	}

	resPolarized := feedbacklab.AnalyzeResponses(milestone, polarizedResponses)
	if resPolarized.OverallRiskTier != "Critical" && resPolarized.OverallRiskTier != "High" {
		t.Fatalf("expected elevated risk tier, got %s", resPolarized.OverallRiskTier)
	}
	if resPolarized.Disposition == feedbacklab.DispositionAccepted {
		t.Fatalf("expected unaccepted disposition due to critical risk and low consensus")
	}
	if len(resPolarized.AgentPromptDelta) == 0 {
		t.Fatalf("expected non-empty agent prompt delta")
	}
}

func TestMemoryStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := feedbacklab.NewMemoryStore()
	defer store.Close()

	m := &feedbacklab.MilestoneSnapshot{
		ID:        "ms-101",
		Title:     "Schema Add",
		Status:    "awaiting_feedback",
		CreatedAt: time.Now(),
	}

	if err := store.SaveMilestone(ctx, m); err != nil {
		t.Fatalf("save milestone: %v", err)
	}

	gotM, err := store.GetMilestone(ctx, "ms-101")
	if err != nil || gotM.Title != "Schema Add" {
		t.Fatalf("get milestone failed: %v", err)
	}

	resp := &feedbacklab.FeedbackResponse{
		ID:          "resp-1",
		MilestoneID: "ms-101",
		AuthorName:  "Jane",
		SubmittedAt: time.Now(),
	}
	if err := store.SaveResponse(ctx, resp); err != nil {
		t.Fatalf("save response: %v", err)
	}

	list, err := store.ListResponses(ctx, "ms-101")
	if err != nil || len(list) != 1 {
		t.Fatalf("list responses failed: len=%d, err=%v", len(list), err)
	}
}

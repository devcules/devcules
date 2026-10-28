package feedbacklab

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// AnalyzeResponses processes stakeholder feedback and synthesizes sensemaking insights
func AnalyzeResponses(milestone *MilestoneSnapshot, responses []FeedbackResponse) *AnalysisResult {
	if len(responses) == 0 {
		return &AnalysisResult{
			MilestoneID:        milestone.ID,
			ResponseCount:      0,
			ConsensusIndex:     0,
			OverallRiskTier:    "Unknown",
			Disposition:        DispositionNeedsClarify,
			ActionableGuidance: []string{"No stakeholder responses recorded yet."},
			AgentPromptDelta:   "Awaiting stakeholder feedback before proceeding.",
			AnalyzedAt:         time.Now(),
		}
	}

	// 1. Compute averages & variances per question and per role
	type qStats struct {
		values      []float64
		roleValues  map[string][]float64
		textAnswers []string
	}
	stats := make(map[string]*qStats)
	for _, q := range milestone.Questions {
		stats[q.ID] = &qStats{
			values:      make([]float64, 0),
			roleValues:  make(map[string][]float64),
			textAnswers: make([]string, 0),
		}
	}

	riskScores := make([]float64, 0)

	for _, r := range responses {
		role := strings.TrimSpace(r.AuthorRole)
		if role == "" {
			role = "General"
		}

		for _, ans := range r.Answers {
			st, ok := stats[ans.QuestionID]
			if !ok {
				continue
			}

			if ans.ValueNumber != nil {
				val := *ans.ValueNumber
				st.values = append(st.values, val)
				st.roleValues[role] = append(st.roleValues[role], val)
			}
			if ans.ValueString != "" {
				st.textAnswers = append(st.textAnswers, fmt.Sprintf("[%s - %s]: %s", r.AuthorName, role, ans.ValueString))
				// If risk choice
				switch strings.ToLower(ans.ValueString) {
				case "critical":
					riskScores = append(riskScores, 4.0)
				case "high":
					riskScores = append(riskScores, 3.0)
				case "medium":
					riskScores = append(riskScores, 2.0)
				case "low":
					riskScores = append(riskScores, 1.0)
				}
			}
		}
	}

	// 2. Measure consensus and role divergence
	totalVariance := 0.0
	measuredQuestions := 0
	var roleDivergences []RoleDivergence

	for _, q := range milestone.Questions {
		st := stats[q.ID]
		if len(st.values) < 2 {
			continue
		}

		// Calculate variance across all answers
		mean := meanOf(st.values)
		v := varianceOf(st.values, mean)
		totalVariance += v
		measuredQuestions++

		// Role-based breakdown
		roleMeans := make(map[string]float64)
		var roleMeanList []float64
		for role, vals := range st.roleValues {
			rm := meanOf(vals)
			roleMeans[role] = math.Round(rm*100) / 100
			roleMeanList = append(roleMeanList, rm)
		}

		roleDev := 0.0
		if len(roleMeanList) > 1 {
			roleDev = math.Sqrt(varianceOf(roleMeanList, meanOf(roleMeanList)))
		}

		isPolarized := roleDev > 0.75 // Substantial divergence between teams
		roleDivergences = append(roleDivergences, RoleDivergence{
			QuestionID:  q.ID,
			Prompt:      q.Prompt,
			RoleMeans:   roleMeans,
			Divergence:  math.Round(roleDev*100) / 100,
			IsPolarized: isPolarized,
		})
	}

	// Normalized consensus index: 1.0 is unanimous, 0.0 is maximum chaos
	consensusIndex := 1.0
	if measuredQuestions > 0 {
		avgVariance := totalVariance / float64(measuredQuestions)
		// On 1-5 scale, max variance is approx 4.0
		consensusIndex = math.Max(0.0, math.Min(1.0, 1.0-(avgVariance/3.0)))
	}
	consensusIndex = math.Round(consensusIndex*100) / 100

	// 3. Determine Overall Risk Tier
	avgRisk := 1.0
	if len(riskScores) > 0 {
		avgRisk = meanOf(riskScores)
	}
	overallRiskTier := "Low"
	if avgRisk >= 3.5 {
		overallRiskTier = "Critical"
	} else if avgRisk >= 2.5 {
		overallRiskTier = "High"
	} else if avgRisk >= 1.8 {
		overallRiskTier = "Medium"
	}

	// 4. Synthesize Disposition
	disposition := DispositionAccepted
	var guidance []string

	if overallRiskTier == "Critical" || overallRiskTier == "High" {
		if consensusIndex < 0.6 {
			disposition = DispositionCourseCorrect
			guidance = append(guidance, "High risk detected with polarized team opinions. Agent must halt destructive operations and apply course correction.")
		} else {
			disposition = DispositionNeedsClarify
			guidance = append(guidance, "Team agrees risk is elevated. Explicit operator review required before proceeding.")
		}
	} else if consensusIndex < 0.5 {
		disposition = DispositionNeedsClarify
		guidance = append(guidance, "Conflicting feedback across roles. Clarify specifications before next commit.")
	} else {
		disposition = DispositionAccepted
		guidance = append(guidance, "Consensus reached. Proposed changes satisfy milestone criteria.")
	}

	// Collect text feedback into guidance
	for _, q := range milestone.Questions {
		st := stats[q.ID]
		if len(st.textAnswers) > 0 {
			for _, comment := range st.textAnswers {
				guidance = append(guidance, comment)
			}
		}
	}

	// 5. Construct Context v2 Delta for the Agent
	var promptBuilder strings.Builder
	promptBuilder.WriteString(fmt.Sprintf("### Milestone Feedback Analysis (Milestone: %s)\n", milestone.Title))
	promptBuilder.WriteString(fmt.Sprintf("- **Team Consensus Index:** %.0f%%\n", consensusIndex*100))
	promptBuilder.WriteString(fmt.Sprintf("- **Assessed Risk Level:** %s\n", overallRiskTier))
	promptBuilder.WriteString(fmt.Sprintf("- **Synthesized Disposition:** %s\n\n", strings.ToUpper(string(disposition))))

	promptBuilder.WriteString("#### Critical Action Items from Stakeholders:\n")
	for _, g := range guidance {
		promptBuilder.WriteString(fmt.Sprintf("- %s\n", g))
	}

	if len(roleDivergences) > 0 {
		promptBuilder.WriteString("\n#### Key Alignment Signals:\n")
		for _, rd := range roleDivergences {
			if rd.IsPolarized {
				promptBuilder.WriteString(fmt.Sprintf("- ⚠️ **Role Divergence on '%s':** Roles disagree (Divergence: %.2f). Balance engineering feasibility with product constraints.\n", rd.Prompt, rd.Divergence))
			}
		}
	}

	return &AnalysisResult{
		MilestoneID:        milestone.ID,
		ResponseCount:      len(responses),
		ConsensusIndex:     consensusIndex,
		OverallRiskTier:    overallRiskTier,
		Disposition:        disposition,
		RoleDivergences:    roleDivergences,
		ActionableGuidance: guidance,
		AgentPromptDelta:   promptBuilder.String(),
		AnalyzedAt:         time.Now(),
	}
}

func meanOf(nums []float64) float64 {
	if len(nums) == 0 {
		return 0
	}
	sum := 0.0
	for _, n := range nums {
		sum += n
	}
	return sum / float64(len(nums))
}

func varianceOf(nums []float64, mean float64) float64 {
	if len(nums) <= 1 {
		return 0
	}
	sumSq := 0.0
	for _, n := range nums {
		diff := n - mean
		sumSq += diff * diff
	}
	return sumSq / float64(len(nums))
}

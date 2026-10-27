package questionbank

import (
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestImportMapsFrozenFactsAndKeepsMissingMetadataInDraft(t *testing.T) {
	q := frozenImportQuestion{ID: "source", QuestionNo: "OLD-3", QuestionType: "short_answer", AssessmentArchetype: "short_constructed", Score: 2,
		Stem: "Frozen stem", KnowledgePoints: []string{"legacy-label"},
		Answer:   &paper.ReadinessAnswerSnapshot{StandardAnswer: "Frozen answer"},
		Solution: &frozenImportSolution{RawText: "Frozen solution", Steps: []paper.SolutionStep{}, VerificationStatus: "human_confirmed"},
		Rubric:   &paper.ReadinessRubricSnapshot{MaxScore: 2, Points: []paper.RubricPoint{{ID: "p1", Description: "Frozen point", Score: 2, Required: true}}},
	}
	a := importAssessmentSnapshot{SubjectCode: "physics", EducationStage: "senior", ArchetypeCode: "short_constructed", Rubric: map[string]any{
		"max_score": 2, "points": []any{map[string]any{"id": "p1", "description": "Frozen point", "score": 2, "required": true}},
	}}
	if !scoringMatchesAssessment(q, a) {
		t.Fatal("equivalent frozen rubric facts did not match")
	}
	c, s, err := buildImportedFacts(q, a, ImportMapping{ItemCode: "NEW", KnowledgePoints: []string{"target.stable"}})
	if err != nil || c.Stem != "Frozen stem" || s.Answer.StandardAnswer != "Frozen answer" || s.Solution.RawText != "Frozen solution" {
		t.Fatalf("conversion: %+v %+v %v", c, s, err)
	}
	if c.Metadata.GradeScope != "unmapped" || c.Metadata.Copyright != "unknown" || len(s.Solution.SourceRefs) != 0 || c.KnowledgePoints[0] != "target.stable" {
		t.Fatal("mapping defaults or provenance separation failed")
	}
	if publishable(Version{Content: c, Scoring: s}, "question") == nil {
		t.Fatal("unmapped imported draft passed publication gate")
	}
	c.Metadata.GradeScope = "grade_10"
	c.Metadata.Copyright = "owned"
	if publishable(Version{Content: c, Scoring: s}, "question") != nil {
		t.Fatal("completed imported draft could not pass gate")
	}
	a.Rubric["max_score"] = 3
	if scoringMatchesAssessment(q, a) {
		t.Fatal("different frozen scoring silently matched")
	}
}

func TestImportPreservesFrozenBankOptionsAndAttachmentDigests(t *testing.T) {
	assetID := "00000000-0000-0000-0000-000000000123"
	q := frozenImportQuestion{
		ID: "source", QuestionNo: "OLD-4", QuestionType: "single_choice", AssessmentArchetype: "selected_response",
		Score: 1, Stem: "Frozen objective", Answer: &paper.ReadinessAnswerSnapshot{StandardAnswer: "B"},
		BankContent: map[string]any{
			"content": map[string]any{
				"options":  []string{"One", "Two", "Three"},
				"metadata": map[string]any{"grade_scope": "grade_8", "copyright": "licensed"},
			},
			"assets": []map[string]any{{
				"file_asset_id": assetID, "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"name": "diagram.svg", "content_type": "image/svg+xml",
			}},
		},
	}
	a := importAssessmentSnapshot{SubjectCode: "physics", EducationStage: "junior", ArchetypeCode: "selected_response"}
	content, scoring, err := buildImportedFacts(q, a, ImportMapping{ItemCode: "BANK-OLD-4"})
	if err != nil || len(content.Options) != 3 || content.Options[1] != "Two" || len(scoring.Assets) != 1 {
		t.Fatalf("frozen bank facts: %+v %+v %v", content, scoring, err)
	}
	if scoring.Assets[0].FileAssetID != assetID || scoring.Assets[0].SHA256 != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatal("attachment identity or digest was not preserved")
	}
	if issues := importIssues(content, scoring, MetadataSchema{Version: 1, Fields: []MetadataFieldDefinition{}}, "BANK-OLD-4"); len(issues) != 0 {
		t.Fatalf("complete frozen bank question had issues: %+v", issues)
	}
}

package paper

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

func TestReadinessPreviewUsesSameEffectiveFactsWithoutWritingConfiguration(t *testing.T) {
	for _, source := range []string{"default", "saved", "snapshot"} {
		t.Run(source, func(t *testing.T) {
			fact := assessmentFactFixture(t)
			fact.QuestionNo, fact.Source = "12(1)", source
			fact.Profile["education_stage"], fact.Profile["subject_code"] = "senior", "mathematics"
			data, _ := json.Marshal(fact)
			status, query, candidates := "configured", "assessment_effective_question_config", "FROM exam_class ec"
			if source == "snapshot" {
				status, query, candidates = "ready", "FROM exam_question_snapshot snapshot", "FROM exam_candidate_snapshot candidate"
			}
			steps := []paperBatch10Step{
				{kind: "query", fragment: query, columns: 2, rows: [][]driver.Value{{fact.QuestionID, data}}},
				{kind: "query", fragment: candidates, columns: 2},
			}
			if status == "ready" {
				steps = append(steps, paperBatch10Step{kind: "query", fragment: "configuration_hash=$3", columns: 1, rows: [][]driver.Value{{""}}})
			}
			script := &paperBatch10Script{steps: steps}
			db := sql.OpenDB(paperBatch10Connector{script: script})
			defer db.Close()
			result, err := assessmentReadinessHash(context.Background(), db, "tenant-1", "exam-1", status, ReadinessResult{Ready: true, ConfigurationHash: "content"})
			if err != nil || script.unmatched != nil || script.next != len(steps) || len(result.ScoringPreview) != 1 {
				t.Fatalf("preview did not remain a read-only part of readiness: %#v %v %#v", result, err, script)
			}
			preview := result.ScoringPreview[0]
			if preview.Source != source || preview.QuestionNo != fact.QuestionNo || preview.Profile.ID != "profile-1" || preview.Profile.EducationStage != "senior" || preview.Profile.Version != 1 || preview.ScoringPolicy.Mode != fact.Policy.Mode || preview.ProcessingMode != fact.Policy.Mode || preview.BlockingReason != "" {
				t.Fatalf("preview diverged from assessment facts: %#v", preview)
			}
		})
	}
}

func TestReadinessPreviewMetadataDoesNotInvalidateMaterializedDefaultHash(t *testing.T) {
	fact := assessmentFactFixture(t)
	fact.Source, fact.QuestionNo = "default", "1"
	expected := readinessAssessmentFactsHash([]readinessAssessmentFact{fact})
	for _, source := range []string{"saved", "snapshot"} {
		fact.Source, fact.QuestionNo, fact.AnswerScope = source, "1(1)", "grading_group"
		fact.BlockingReason, fact.BlockingSection = "显示原因", "questions"
		if readinessAssessmentFactsHash([]readinessAssessmentFact{fact}) != expected {
			t.Fatalf("display metadata changed the v3 assessment hash for %s", source)
		}
	}
}

func TestReadinessPreviewExplainsGroupMappingAndTeacherConfirmation(t *testing.T) {
	for _, mode := range []assessment.ScoringMode{assessment.ScoringRuleAuto, assessment.ScoringAIAssist, assessment.ScoringAIFastConfirm, assessment.ScoringDualHuman} {
		fact := assessmentFactFixture(t)
		fact.Policy.Mode, fact.AnswerScope, fact.Source = mode, "grading_group", "default"
		preview := buildReadinessScoringPreview([]readinessAssessmentFact{fact})[0]
		if preview.ScoringPolicy.Mode != mode || preview.ProcessingMode != mode {
			t.Fatalf("group must retain the per-question policy: %#v", preview)
		}
		if mode != assessment.ScoringDualHuman && !strings.Contains(preview.Reason, "按题号映射作答") {
			t.Fatalf("group observation mapping is unexplained: %#v", preview)
		}
		if (mode == assessment.ScoringAIAssist || mode == assessment.ScoringAIFastConfirm) && !strings.Contains(preview.Reason, "教师确认") {
			t.Fatalf("AI preview lost teacher confirmation: %#v", preview)
		}
		if mode == assessment.ScoringRuleAuto && !strings.Contains(preview.Reason, "核对规则、答案和答题区域") {
			t.Fatalf("rule preview promises unconditional scoring: %#v", preview)
		}
	}
}

func TestReadinessPreviewBlocksUnknownDefaultStageAtExamSettings(t *testing.T) {
	fact := assessmentFactFixture(t)
	fact.Source, fact.BlockingReason, fact.BlockingSection = "default", "请核对考试年级学段", "exam"
	result := withAssessmentProfileCheck(withReadinessAssessmentFacts(ReadinessResult{Ready: true}, []readinessAssessmentFact{fact}, nil), []readinessAssessmentFact{fact})
	if result.Ready || result.Checks[0].Passed || result.Checks[0].Section != "exam" || result.ScoringPreview[0].BlockingReason != fact.BlockingReason || result.ScoringPreview[0].BlockingSection != "exam" {
		t.Fatalf("unknown stage is not an actionable blocker: %#v", result)
	}
}

func TestReadinessPreviewEmptyQuestionsSerializeAsArray(t *testing.T) {
	result := withReadinessAssessmentFacts(ReadinessResult{}, nil, nil)
	raw, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), `"scoring_preview":[]`) {
		t.Fatalf("empty preview must be an array: %s %v", raw, err)
	}
}

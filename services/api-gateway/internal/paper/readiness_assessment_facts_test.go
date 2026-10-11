package paper

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

func assessmentFactFixture(t *testing.T) readinessAssessmentFact {
	t.Helper()
	var fact readinessAssessmentFact
	if err := json.Unmarshal(readinessFactJSON(t, `{"mode":"AI_ASSIST","require_evidence":true,"human_review_below_confidence":true}`), &fact); err != nil {
		t.Fatal(err)
	}
	return fact
}

func TestReadinessAssessmentHashTracksRiskEvidenceAndFrozenTemplateFacts(t *testing.T) {
	baseline := assessmentFactFixture(t)
	expected := readinessAssessmentFactsHash([]readinessAssessmentFact{baseline})
	changes := map[string]func(*readinessAssessmentFact){
		"risk":             func(f *readinessAssessmentFact) { f.RiskTier = assessment.RiskR3 },
		"evidence":         func(f *readinessAssessmentFact) { f.AllowedEvidence = []assessment.EvidenceType{"text_span"} },
		"profile identity": func(f *readinessAssessmentFact) { f.Profile["id"] = "profile-2" },
		"profile version":  func(f *readinessAssessmentFact) { f.Profile["version"] = float64(2) },
		"parser policy":    func(f *readinessAssessmentFact) { f.Profile["parser_policy"] = map[string]any{"parser": "two"} },
		"evidence policy": func(f *readinessAssessmentFact) {
			f.Profile["evidence_policy"] = map[string]any{"allowed_types": []any{"text_span"}}
		},
		"scoring defaults": func(f *readinessAssessmentFact) {
			f.Profile["scoring_default"] = map[string]any{"mode": "HUMAN_PRIMARY"}
		},
		"archetype identity": func(f *readinessAssessmentFact) { f.Archetype["code"] = "structured_steps" },
		"response schema":    func(f *readinessAssessmentFact) { f.Archetype["response_schema"] = map[string]any{"type": "steps"} },
		"archetype evidence": func(f *readinessAssessmentFact) { f.Archetype["evidence_types"] = []any{"text_span"} },
		"archetype defaults": func(f *readinessAssessmentFact) { f.Archetype["default_scoring_mode"] = "HUMAN_PRIMARY" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			fact := assessmentFactFixture(t)
			change(&fact)
			if readinessAssessmentFactsHash([]readinessAssessmentFact{fact}) == expected {
				t.Fatalf("%s did not invalidate the reviewed assessment", name)
			}
		})
	}
}

func TestReadinessAssessmentHashNormalizesEvidenceSetAndJSONLayout(t *testing.T) {
	first := assessmentFactFixture(t)
	first.AllowedEvidence = []assessment.EvidenceType{"text_span", "selected_option", "text_span"}
	second := assessmentFactFixture(t)
	second.AllowedEvidence = []assessment.EvidenceType{"selected_option", "text_span"}
	first.Profile["parser_policy"] = map[string]any{"a": true, "b": float64(2)}
	second.Profile["parser_policy"] = map[string]any{"b": float64(2), "a": true}
	if readinessAssessmentFactsHash([]readinessAssessmentFact{first}) != readinessAssessmentFactsHash([]readinessAssessmentFact{second}) {
		t.Fatal("equivalent evidence or JSON layout invalidated the review")
	}
}

func TestReadinessAssessmentHashFrozenV2CompatibilityDoesNotDowngradePreparation(t *testing.T) {
	fact := assessmentFactFixture(t)
	classes := []readinessCandidateClass{{StudentID: "student-1", ClassID: "class-1"}}
	base := ReadinessResult{ConfigurationHash: "paper-content"}
	v2 := withReadinessReviewFacts(base, []readinessScoringPolicy{{QuestionID: fact.QuestionID, Policy: fact.Policy}}, classes)
	for _, status := range []string{"draft", "configured", "ready", "collecting", "grading", "published", "archived"} {
		t.Run(status, func(t *testing.T) {
			query := "config.scoring_policy_json"
			candidateQuery := "FROM exam_class ec"
			frozen := status != "draft" && status != "configured"
			if frozen {
				query = "FROM exam_question_snapshot snapshot"
				candidateQuery = "FROM exam_candidate_snapshot candidate"
			}
			data, _ := json.Marshal(fact)
			steps := []paperBatch10Step{
				{kind: "query", fragment: query, columns: 2, rows: [][]driver.Value{{fact.QuestionID, data}}},
				{kind: "query", fragment: candidateQuery, columns: 2, rows: [][]driver.Value{{"student-1", "class-1"}}},
			}
			if frozen {
				steps = append(steps, paperBatch10Step{kind: "query", fragment: "configuration_hash=$4", columns: 1, rows: [][]driver.Value{{v2.ConfigurationHash}}})
			}
			script := &paperBatch10Script{steps: steps}
			db := sql.OpenDB(paperBatch10Connector{script: script})
			defer db.Close()
			result, err := assessmentReadinessHash(context.Background(), db, "tenant-1", "exam-1", status, base)
			if err != nil || script.unmatched != nil || script.next != len(steps) {
				t.Fatalf("unexpected readiness result: %#v %v %#v", result, err, script)
			}
			if (result.ConfigurationHash == v2.ConfigurationHash) != frozen {
				t.Fatalf("v2 compatibility incorrectly applied in %s", status)
			}
		})
	}
}

func TestReadinessConfirmationRejectsChangedRiskBeforeRosterWrites(t *testing.T) {
	// 两次计算保留相同评分方式，只改风险；原缺陷会接受第一次的 hash 并开始重建名单。
	steps := readinessReviewedConfigurationSteps(t)
	script := &paperBatch10Script{steps: append([]paperBatch10Step(nil), steps[2:]...)}
	db := sql.OpenDB(paperBatch10Connector{script: script})
	defer db.Close()
	reviewed, _, _, err := calculateReadiness(context.Background(), db, "tenant-1", "exam-1")
	if err != nil {
		t.Fatal(err)
	}
	for index := range steps {
		if steps[index].fragment != "config.scoring_policy_json" {
			continue
		}
		var fact readinessAssessmentFact
		if err := json.Unmarshal(steps[index].rows[0][1].([]byte), &fact); err != nil {
			t.Fatal(err)
		}
		fact.RiskTier = assessment.RiskR3
		data, _ := json.Marshal(fact)
		steps[index].rows = [][]driver.Value{{fact.QuestionID, data}}
	}
	store, proof := openPaperBatch10Store(t, steps)
	result, err := store.ConfirmReadiness(context.Background(), "tenant-1", "exam-1", "admin-1", ConfirmReadinessInput{ExpectedConfigurationHash: reviewed.ConfigurationHash})
	if !errors.Is(err, ErrReadinessChanged) || !result.Ready || result.Confirmed {
		t.Fatalf("risk change was frozen: %#v %v", result, err)
	}
	assertPaperBatch10Proof(t, proof, "rollback", result)
}

func TestReadinessConfirmationRollsBackTemplateChangeObservedByFreezeTrigger(t *testing.T) {
	// 模板维护不锁考试：准备读取 parser=one，ready 触发器冻结 parser=two。
	// 此脚本检查事务回滚，不冒充真实 PostgreSQL 触发器并发集成验证。
	steps := readinessReviewedConfigurationSteps(t)
	steps = append(steps,
		paperBatch10Step{kind: "query", fragment: "SELECT status", columns: 1, rows: [][]driver.Value{{"configured"}}},
		paperBatch10Step{kind: "exec", fragment: "DELETE FROM exam_candidate_snapshot", result: driver.RowsAffected(1)},
		paperBatch10Step{kind: "exec", fragment: "INSERT INTO exam_candidate_snapshot", result: driver.RowsAffected(1)},
		paperBatch10Step{kind: "query", fragment: "FROM exam_candidate_snapshot candidate", columns: 2, rows: [][]driver.Value{{"student-1", "class-1"}}},
	)
	// 确认逻辑会再次读取题目来建立导入伴随快照。
	questionReads := readinessReviewedConfigurationSteps(t)[4:11]
	steps = append(steps, questionReads...)
	var frozen readinessAssessmentFact
	if err := json.Unmarshal(readinessFactJSON(t, `{"mode":"DUAL_HUMAN","require_evidence":true,"human_review_below_confidence":true}`), &frozen); err != nil {
		t.Fatal(err)
	}
	frozen.Profile["parser_policy"] = map[string]any{"parser": "two"}
	data, _ := json.Marshal(frozen)
	steps = append(steps,
		paperBatch10Step{kind: "exec", fragment: "UPDATE exam_readiness_snapshot", result: driver.RowsAffected(0)},
		paperBatch10Step{kind: "exec", fragment: "UPDATE exam SET status = 'ready'", result: driver.RowsAffected(1)},
		paperBatch10Step{kind: "query", fragment: "FROM exam_question_snapshot snapshot", columns: 2, rows: [][]driver.Value{{frozen.QuestionID, data}}},
	)
	store, proof := openPaperBatch10Store(t, steps)
	result, err := store.ConfirmReadiness(context.Background(), "tenant-1", "exam-1", "admin-1", ConfirmReadinessInput{})
	if !errors.Is(err, ErrReadinessChanged) || !result.Ready || result.Confirmed {
		t.Fatalf("unreviewed template was frozen: %#v %v", result, err)
	}
	assertPaperBatch10Proof(t, proof, "rollback", result)
}

func TestReadinessMissingDefaultProfileIsPreparationBlockerWithoutReadError(t *testing.T) {
	for _, status := range []string{"draft", "configured"} {
		t.Run(status, func(t *testing.T) {
			fact := assessmentFactFixture(t)
			fact.Profile["id"] = nil
			data, _ := json.Marshal(fact)
			script := &paperBatch10Script{steps: []paperBatch10Step{
				{kind: "query", fragment: "config.scoring_policy_json", columns: 2, rows: [][]driver.Value{{fact.QuestionID, data}}},
				{kind: "query", fragment: "FROM exam_class ec", columns: 2, rows: [][]driver.Value{{"student-1", "class-1"}}},
			}}
			db := sql.OpenDB(paperBatch10Connector{script: script})
			defer db.Close()
			result, err := assessmentReadinessHash(context.Background(), db, "tenant-1", "exam-1", status, ReadinessResult{Ready: true, ConfigurationHash: "paper-content"})
			if err != nil || result.Ready || len(result.Checks) != 1 || result.Checks[0].Passed || result.Checks[0].Code != "assessment_profiles" || script.unmatched != nil {
				t.Fatalf("missing template should block preparation without hiding checks: %#v %v", result, err)
			}
		})
	}
}

func TestReadinessHistoricalPassedV1WithoutAssessmentSnapshotKeepsConfirmationHash(t *testing.T) {
	// 更早历史考试可能没有评估快照；只能凭确切 passed 内容摘要保留旧确认，不能扩展到准备阶段。
	script := &paperBatch10Script{steps: []paperBatch10Step{
		{kind: "query", fragment: "FROM exam_question_snapshot snapshot", columns: 2},
		{kind: "query", fragment: "FROM exam_candidate_snapshot candidate", columns: 2, rows: [][]driver.Value{{"student-1", "class-1"}}},
		{kind: "query", fragment: "configuration_hash=$3", columns: 1, rows: [][]driver.Value{{"historical-content"}}},
	}}
	db := sql.OpenDB(paperBatch10Connector{script: script})
	defer db.Close()
	result, err := assessmentReadinessHash(context.Background(), db, "tenant-1", "exam-1", "ready", ReadinessResult{Ready: true, ConfigurationHash: "historical-content"})
	if err != nil || !result.Ready || result.ConfigurationHash != "historical-content" || script.unmatched != nil || script.next != len(script.steps) {
		t.Fatalf("historical confirmation was lost: %#v %v", result, err)
	}
}

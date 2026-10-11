package paper

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

func TestReadinessScoringPolicyHashTracksAllPolicyChanges(t *testing.T) {
	threshold := .8
	baseline := readinessScoringPolicy{QuestionID: "question-1", Policy: assessment.ScoringPolicy{
		Mode: assessment.ScoringHumanPrimary, ConfidenceThreshold: &threshold, RequireEvidence: true, HumanReviewBelowConfidence: true,
	}}
	original := withReadinessReviewFacts(ReadinessResult{ConfigurationHash: "same-paper-and-roster"}, []readinessScoringPolicy{baseline}, nil)
	changed := []readinessScoringPolicy{baseline, baseline, baseline, baseline}
	changed[0].Policy.Mode = assessment.ScoringDualHuman
	newThreshold := .9
	changed[1].Policy.ConfidenceThreshold = &newThreshold
	changed[2].Policy.RequireEvidence = false
	changed[3].Policy.HumanReviewBelowConfidence = false
	for index, policy := range changed {
		updated := withReadinessReviewFacts(ReadinessResult{ConfigurationHash: "same-paper-and-roster"}, []readinessScoringPolicy{policy}, nil)
		if original.ConfigurationHash == updated.ConfigurationHash {
			t.Fatalf("policy change %d did not invalidate reviewed hash", index)
		}
	}
}

func TestReadinessScoringPolicyHashCanonicalizesQuestionOrder(t *testing.T) {
	first := readinessScoringPolicy{QuestionID: "question-1", Policy: assessment.ScoringPolicy{Mode: assessment.ScoringRuleAuto}}
	second := readinessScoringPolicy{QuestionID: "question-2", Policy: assessment.ScoringPolicy{Mode: assessment.ScoringHumanPrimary}}
	before := withReadinessReviewFacts(ReadinessResult{ConfigurationHash: "same"}, []readinessScoringPolicy{second, first}, nil)
	after := withReadinessReviewFacts(ReadinessResult{ConfigurationHash: "same"}, []readinessScoringPolicy{first, second}, nil)
	if before.ConfigurationHash != after.ConfigurationHash {
		t.Fatal("database question order changed readiness hash")
	}
}

func TestReadinessReviewHashTracksTransferWithinSelectedClasses(t *testing.T) {
	base := ReadinessResult{ConfigurationHash: "same-student-identities-and-selected-classes"}
	before := withReadinessReviewFacts(base, nil, []readinessCandidateClass{{StudentID: "student-1", ClassID: "class-1"}})
	after := withReadinessReviewFacts(base, nil, []readinessCandidateClass{{StudentID: "student-1", ClassID: "class-2"}})
	if before.ConfigurationHash == after.ConfigurationHash {
		t.Fatal("transfer within selected classes did not invalidate reviewed roster")
	}
}

func TestReadinessReviewHashCanonicalizesRosterAndKeepsReviewedMapping(t *testing.T) {
	base := ReadinessResult{ConfigurationHash: "same"}
	classes := []readinessCandidateClass{{StudentID: "student-2", ClassID: "class-2"}, {StudentID: "student-1", ClassID: "class-1"}}
	before := withReadinessReviewFacts(base, nil, classes)
	after := withReadinessReviewFacts(base, nil, []readinessCandidateClass{classes[1], classes[0]})
	classes[0].ClassID = "changed-after-read"
	if before.ConfigurationHash != after.ConfigurationHash || before.candidateClasses[1].ClassID != "class-2" {
		t.Fatal("roster order or later mutation changed the reviewed mapping")
	}
}

func TestReadinessScoringPolicyDefaultsRemainStableWhenMaterialized(t *testing.T) {
	// 脚本分别模拟缺省计算和触发器落库后返回的相同策略；不冒充真实 PostgreSQL 触发器集成验证。
	for _, mode := range []string{"RULE_AUTO", "AI_ASSIST", "HUMAN_PRIMARY", "DUAL_HUMAN", "MANUAL_ONLY"} {
		t.Run(mode, func(t *testing.T) {
			implicit := `{"mode":"` + mode + `","require_evidence":true,"human_review_below_confidence":true}`
			materialized := `{"human_review_below_confidence":true,"confidence_threshold":null,"require_evidence":true,"mode":"` + mode + `"}`
			before := scriptedAssessmentReadinessHash(t, "configured", implicit, nil)
			after := scriptedAssessmentReadinessHash(t, "ready", materialized, boolPointer(false))
			if before.ConfigurationHash != after.ConfigurationHash {
				t.Fatalf("materializing default policy changed hash: %s != %s", before.ConfigurationHash, after.ConfigurationHash)
			}
		})
	}
}

func TestReadinessScoringPolicyLegacyHashOnlySurvivesFrozenPassedSnapshot(t *testing.T) {
	policy := `{"mode":"HUMAN_PRIMARY","require_evidence":true,"human_review_below_confidence":true}`
	for _, status := range []string{"draft", "configured", "ready", "collecting", "processing", "grading", "scored", "published", "archived"} {
		t.Run(status, func(t *testing.T) {
			var existing *bool
			if status != "draft" && status != "configured" {
				existing = boolPointer(true)
			}
			result := scriptedAssessmentReadinessHash(t, status, policy, existing)
			legacyAllowed := existing != nil
			if (result.ConfigurationHash == "legacy-paper-and-roster") != legacyAllowed {
				t.Fatalf("unexpected legacy hash behavior in %s: %s", status, result.ConfigurationHash)
			}
		})
	}
	withoutPassed := scriptedAssessmentReadinessHash(t, "ready", policy, boolPointer(false))
	if withoutPassed.ConfigurationHash == "legacy-paper-and-roster" {
		t.Fatal("unconfirmed legacy hash bypassed current scoring policy")
	}
}

func TestReadinessConfirmationRollsBackClassTransferDuringRebuild(t *testing.T) {
	// 学籍不受考试行锁约束：准备摘要读到 class-1，重建名册已变为 class-2，但学生 ID 没变。
	// 脚本验证仓储事务的回滚边界，不代替 PostgreSQL 并发或触发器集成测试。
	steps := readinessReviewedConfigurationSteps(t)
	steps = append(steps,
		paperBatch10Step{kind: "query", fragment: "SELECT status", columns: 1, rows: [][]driver.Value{{"configured"}}},
		paperBatch10Step{kind: "exec", fragment: "DELETE FROM exam_candidate_snapshot", result: driver.RowsAffected(1)},
		paperBatch10Step{kind: "exec", fragment: "INSERT INTO exam_candidate_snapshot", result: driver.RowsAffected(1)},
		paperBatch10Step{kind: "query", fragment: "FROM exam_candidate_snapshot candidate", columns: 2, rows: [][]driver.Value{{"student-1", "class-2"}}},
	)
	store, script := openPaperBatch10Store(t, steps)
	result, err := store.ConfirmReadiness(context.Background(), "tenant-1", "exam-1", "admin-1", ConfirmReadinessInput{})
	if !errors.Is(err, ErrReadinessChanged) || !result.Ready || result.Confirmed {
		t.Fatalf("changed class was frozen: %#v %v", result, err)
	}
	assertPaperBatch10Proof(t, script, "rollback", result)
}

func TestReadinessConfirmationRejectsStalePolicyHashBeforeRebuild(t *testing.T) {
	store, script := openPaperBatch10Store(t, readinessReviewedConfigurationSteps(t))
	result, err := store.ConfirmReadiness(context.Background(), "tenant-1", "exam-1", "admin-1", ConfirmReadinessInput{ExpectedConfigurationHash: "stale-before-policy-change"})
	if !errors.Is(err, ErrReadinessChanged) || !result.Ready || result.Confirmed {
		t.Fatalf("stale policy was frozen: %#v %v", result, err)
	}
	assertPaperBatch10Proof(t, script, "rollback", result)
}

func TestReadinessConfirmationKeepsIdentityCheckWhenClassProjectionOmitsCandidate(t *testing.T) {
	// 历史年级已删除时，当前身份集合与可重建的归属列表可能不同；两份空归属不能覆盖原身份校验。
	steps := readinessReviewedConfigurationSteps(t)
	steps[len(steps)-1].rows = nil
	steps = append(steps,
		paperBatch10Step{kind: "query", fragment: "SELECT status", columns: 1, rows: [][]driver.Value{{"configured"}}},
		paperBatch10Step{kind: "exec", fragment: "DELETE FROM exam_candidate_snapshot", result: driver.RowsAffected(1)},
		paperBatch10Step{kind: "exec", fragment: "INSERT INTO exam_candidate_snapshot", result: driver.RowsAffected(0)},
		paperBatch10Step{kind: "query", fragment: "FROM exam_candidate_snapshot candidate", columns: 2},
	)
	store, script := openPaperBatch10Store(t, steps)
	result, err := store.ConfirmReadiness(context.Background(), "tenant-1", "exam-1", "admin-1", ConfirmReadinessInput{})
	if !errors.Is(err, ErrReadinessChanged) || !result.Ready || result.Confirmed {
		t.Fatalf("mismatched identity list was frozen: %#v %v", result, err)
	}
	assertPaperBatch10Proof(t, script, "rollback", result)
}

func readinessReviewedConfigurationSteps(t *testing.T) []paperBatch10Step {
	t.Helper()
	_, _, templates := readinessHashFixture()
	layout, err := json.Marshal(templates[0].Layout)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return []paperBatch10Step{
		{kind: "exec", fragment: "pg_advisory_xact_lock", result: driver.RowsAffected(1)},
		{kind: "query", fragment: "FOR UPDATE", columns: 1, rows: [][]driver.Value{{"configured"}}},
		{kind: "query", fragment: "SELECT e.total_score", columns: 4, rows: [][]driver.Value{{float64(1), "configured", []byte(`["class-1","class-2"]`), []byte(`["student-1"]`)}}},
		{kind: "query", fragment: "FROM exam_paper p", columns: 13, rows: [][]driver.Value{{"paper-1", "tenant-1", "exam-1", "file-1", int64(1), "ready", PaperPurposeQuestion, "paper.pdf", "application/pdf", int64(1), "sha-1", "private", "paper.pdf"}}},
		{kind: "query", fragment: "FROM question", columns: 12, rows: [][]driver.Value{{"question-1", "tenant-1", "exam-1", "paper-1", "1", "single_choice", float64(1), "Choose", []byte(`[]`), []byte(`null`), int64(1), "active"}}},
		{kind: "query", fragment: "SELECT source_type", columns: 9, rows: [][]driver.Value{{"manual", "", "", "", nil, "", "", []byte(`[]`), "manual"}}},
		{kind: "query", fragment: "SELECT COALESCE(paper_import_id", columns: 3, rows: [][]driver.Value{{"", "", []byte(`[]`)}}},
		{kind: "query", fragment: "SELECT COALESCE((SELECT archetype_code", columns: 1, rows: [][]driver.Value{{"selected_response"}}},
		{kind: "query", fragment: "FROM question_answer_key", columns: 9, rows: [][]driver.Value{{"answer-1", "question-1", "v1", []byte(`"A"`), []byte(`[]`), []byte(`null`), "", "", []byte(`[]`)}}},
		{kind: "query", fragment: "FROM question_rubric", columns: 11},
		{kind: "query", fragment: "FROM question_solution", columns: 8},
		{kind: "query", fragment: "FROM answer_sheet_template", columns: 16, rows: [][]driver.Value{{"template-1", "tenant-1", "exam-1", "paper-1", int64(1), int64(1), "template", "locked", int64(1), layout, "content-hash", "admin-1", "admin-1", now, now, now}}},
		{kind: "query", fragment: "FROM paper_import_job", columns: 4},
		{kind: "query", fragment: "config.scoring_policy_json", columns: 2, rows: [][]driver.Value{{"question-1", readinessFactJSON(t, `{"mode":"DUAL_HUMAN","require_evidence":true,"human_review_below_confidence":true}`)}}},
		{kind: "query", fragment: "FROM exam_class ec", columns: 2, rows: [][]driver.Value{{"student-1", "class-1"}}},
	}
}

func boolPointer(value bool) *bool { return &value }

func scriptedAssessmentReadinessHash(t *testing.T, status, policy string, legacy *bool) ReadinessResult {
	t.Helper()
	factQuery := "config.scoring_policy_json"
	if status != "draft" && status != "configured" && status != "" {
		factQuery = "FROM exam_question_snapshot snapshot"
	}
	script := &paperBatch10Script{steps: []paperBatch10Step{{kind: "query", fragment: factQuery, columns: 2, rows: [][]driver.Value{{"question-1", readinessFactJSON(t, policy)}}}}}
	candidateQuery := "FROM exam_candidate_snapshot candidate"
	if status == "draft" || status == "configured" {
		candidateQuery = "FROM exam_class ec"
	}
	script.steps = append(script.steps, paperBatch10Step{kind: "query", fragment: candidateQuery, columns: 2, rows: [][]driver.Value{{"student-1", "class-1"}}})
	if legacy != nil {
		script.steps = append(script.steps, paperBatch10Step{kind: "query", fragment: "configuration_hash=$3", columns: 1, rows: [][]driver.Value{{historicalHashForTest(*legacy)}}})
	}
	db := sql.OpenDB(paperBatch10Connector{script: script})
	t.Cleanup(func() { _ = db.Close() })
	result, err := assessmentReadinessHash(context.Background(), db, "tenant-1", "exam-1", status, ReadinessResult{ConfigurationHash: "legacy-paper-and-roster"})
	if err != nil || script.unmatched != nil || script.next != len(script.steps) {
		t.Fatalf("hash result=%#v err=%v script=%#v", result, err, script)
	}
	return result
}

func historicalHashForTest(legacy bool) string {
	if legacy {
		return "legacy-paper-and-roster"
	}
	return ""
}

func readinessFactJSON(t *testing.T, policy string) []byte {
	t.Helper()
	fact := readinessAssessmentFact{QuestionID: "question-1", RiskTier: assessment.RiskR2, AllowedEvidence: []assessment.EvidenceType{"selected_option"},
		Profile:   map[string]any{"id": "profile-1", "code": "junior-mathematics", "version": float64(1), "parser_policy": map[string]any{"parser": "one"}, "evidence_policy": map[string]any{"allowed_types": []any{"selected_option"}}, "scoring_default": map[string]any{"mode": "AI_ASSIST"}},
		Archetype: map[string]any{"code": "selected_response", "response_schema": map[string]any{"type": "option"}, "evidence_types": []any{"selected_option"}, "default_scoring_mode": "RULE_AUTO"}}
	if err := json.Unmarshal([]byte(policy), &fact.Policy); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(fact)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

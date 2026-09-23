package answergroup

import (
	"os"
	"strings"
	"testing"
)

func TestV2CandidateQuarantineMigrationClosesLegacyWriteWindow(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000163_answer_group_v2_candidate_quarantine.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := string(raw)
	for _, required := range []string{
		"guard_answer_group_active_candidate_version",
		"trg_answer_group_active_candidate_version",
		"NEW.candidate_kind <> 'group_score' OR NEW.status <> 'active'",
		"NEW.algorithm_version <> 'deterministic-complete-link-v2'",
		"deterministic-complete-link-v2",
		"normalized-char-bigram-v2",
		"ERRCODE = '23514'",
		"SET status = 'manual_required'",
		"candidate.algorithm_version <> 'deterministic-complete-link-v2'",
	} {
		if !strings.Contains(sqlText, required) {
			t.Fatalf("v2 candidate quarantine migration is missing %q", required)
		}
	}
	triggerAt := strings.Index(sqlText, "CREATE TRIGGER trg_answer_group_active_candidate_version")
	backfillAt := strings.Index(sqlText, "UPDATE answer_group_automation_candidate AS candidate")
	if triggerAt < 0 || backfillAt < 0 || triggerAt > backfillAt {
		t.Fatal("the active-candidate trigger must be installed before the quarantine backfill")
	}
}

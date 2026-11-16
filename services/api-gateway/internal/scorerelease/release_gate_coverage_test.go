package scorerelease

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
)

func sqlGateCounts(prepared bool, reviewCount, unconfirmedCount int64) []studentBatch50Step {
	count := func(query string, value int64) studentBatch50Step {
		return studentBatch50Step{fragment: query, args: []any{tenant, exam}, columns: 1, rows: [][]driver.Value{{value}}}
	}
	steps := []studentBatch50Step{
		count("SELECT CASE WHEN EXISTS", 0),
		count("FROM review_task", reviewCount),
		count("FROM arbitration_task", 0),
		count("FROM ocr_task", 0),
	}
	if prepared {
		steps = append(steps, count("AND NOT EXISTS (SELECT 1 FROM automatic_final af", 0))
	} else {
		steps = append(steps, count("AND NOT EXISTS (SELECT 1 FROM final_grade fg", 0))
	}
	steps = append(steps,
		count("WITH roster AS", 0),
		count("sub.actual_page_count < sub.expected_page_count", 0),
		count("SELECT CASE WHEN EXISTS (SELECT 1 FROM submission_grade", 0),
	)
	if !prepared {
		steps = append(steps, count("status NOT IN ('confirmed', 'published', 'locked')", unconfirmedCount))
	}
	steps = append(steps, count("BOOL_OR(ABS", 0))
	return append(steps, count("WHERE ABS(sg.total_score", 0))
}

func TestSQLPublicationGateSeparatesPreparationFromActualRelease(t *testing.T) {
	t.Run("preflight may account for automatic preparation", func(t *testing.T) {
		// 只读预览可计入会自动产生的终评分数，但未完成复核仍然阻断。
		store := studentBatch50Store(t, sqlGateCounts(true, 2, 0))
		tx, err := store.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()

		gate, err := store.gateTx(t.Context(), tx, tenant, exam, "", nil, true)
		if err != nil || gate.Passed || len(gate.Blocking) != 1 ||
			gate.Blocking[0].Code != "unfinished_review_tasks" || gate.Blocking[0].Count != 2 || gate.Blocking[0].ActionRoute != "review" {
			t.Fatalf("preflight hid unfinished work: gate=%+v err=%v", gate, err)
		}
	})

	t.Run("publication must check confirmed grades and frozen snapshot", func(t *testing.T) {
		const draftID = "draft-id"
		steps := append(sqlGateCounts(false, 0, 1),
			studentBatch50Step{fragment: "AND NOT EXISTS (SELECT 1 FROM score_release_question", args: []any{tenant, draftID}, columns: 1, rows: [][]driver.Value{{int64(2)}}},
			studentBatch50Step{fragment: "abs(item.total_score", args: []any{tenant, draftID}, columns: 1, rows: [][]driver.Value{{int64(3)}}},
		)
		store := studentBatch50Store(t, steps)
		tx, err := store.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()

		gate, err := store.gateTx(t.Context(), tx, tenant, exam, draftID, []GateIssue{{Code: "low_risk_warning", ActionRoute: "quality"}}, false)
		if err != nil || gate.Passed || len(gate.Blocking) != 3 || len(gate.Warnings) != 1 ||
			gate.Counts["grades_not_confirmed"] != 1 || gate.Counts["release_snapshot_incomplete"] != 5 || gate.Counts["low_risk_warning"] != 1 {
			t.Fatalf("release gate lost blockers or their counts: gate=%+v err=%v", gate, err)
		}
	})
}

func TestSQLPublicationGatePropagatesCollectionAndFrozenSnapshotReadFailures(t *testing.T) {
	failure := errors.New("test database read error")
	snapshotSteps := append(sqlGateCounts(false, 0, 0),
		studentBatch50Step{fragment: "AND NOT EXISTS (SELECT 1 FROM score_release_question", args: []any{tenant, "draft-id"}, err: failure},
	)
	tests := []struct {
		name  string
		steps []studentBatch50Step
	}{
		{name: "collection", steps: []studentBatch50Step{{fragment: "SELECT CASE WHEN EXISTS", args: []any{tenant, exam}, err: failure}}},
		{name: "snapshot", steps: snapshotSteps},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := studentBatch50Store(t, tc.steps)
			tx, err := store.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()

			gate, err := store.gateTx(t.Context(), tx, tenant, exam, "draft-id", nil, false)
			if !errors.Is(err, failure) || gate.Passed || gate.Counts != nil {
				t.Fatalf("query error was normalized to a gate result: gate=%+v err=%v", gate, err)
			}
		})
	}
}

type publicationDashboardReader struct{ qualitydashboard.Dashboard }

func (r publicationDashboardReader) Get(context.Context, string, string) (qualitydashboard.Dashboard, error) {
	return r.Dashboard, nil
}

func TestQualityWarningsBlockReleaseOnlyWhenTheirRiskRequiresIt(t *testing.T) {
	findings := []struct {
		code     string
		question string
		blocked  bool
	}{
		{code: "calibration_missing", question: "high", blocked: true},
		{code: "seed_missing", question: "high", blocked: true},
		{code: "gold_missing", question: "high", blocked: true},
		{code: "answer_group_sampling_pending", question: "high", blocked: true},
		{code: "drift_warning", question: "high", blocked: false},
		{code: "calibration_missing", question: "low", blocked: false},
		{code: "backmark_pending", question: "low", blocked: true},
	}
	dashboard := qualitydashboard.Dashboard{
		Questions: []qualitydashboard.QuestionQuality{
			{Question: qualitydashboard.Question{ID: "high", RiskTier: "R3"}},
			{Question: qualitydashboard.Question{ID: "low", RiskTier: "R1"}},
		},
		Blocking: []qualitydashboard.Finding{{Code: "invalid_rubric", Reason: "rubric requires review"}},
	}
	for _, finding := range findings {
		dashboard.Warnings = append(dashboard.Warnings, qualitydashboard.Finding{Code: finding.code, QuestionID: finding.question, Reason: "review evidence"})
	}
	store := NewPostgresStore(nil, publicationDashboardReader{dashboard})
	issues, err := store.dashboardIssues(t.Context(), tenant, exam)
	if err != nil || len(issues) != len(findings)+1 || issues[0].Code != "invalid_rubric" || !issues[0].Blocking {
		t.Fatalf("quality blockers were suppressed: issues=%+v err=%v", issues, err)
	}
	for index, finding := range findings {
		issue := issues[index+1]
		if issue.Code != finding.code || issue.Blocking != finding.blocked || issue.ActionRoute != "quality" || issue.Count != 1 {
			t.Errorf("question=%s code=%s should block=%t: %+v", finding.question, finding.code, finding.blocked, issue)
		}
	}
}

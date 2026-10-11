package review

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"testing"
	"time"
)

func TestArbitrationContextKeepsFrozenMaterialsAfterSourceChanges(t *testing.T) {
	original := reviewContext()
	frozen := contextForArbitration(original)
	original.Question.Score = 99
	original.Rubric.Points[0].Description = "later standard"
	if frozen.Question.Score != 5 || frozen.Rubric.Points[0].Description != "main idea" {
		t.Fatalf("frozen materials changed: %#v", frozen)
	}
	// 返回的嵌套点位也不归调用方所有，修改读取结果不能改变下一次任务读取。
	stored := ArbitrationTask{Context: frozen}
	returned := cloneArbitration(stored)
	returned.Context.Rubric.Points[0].Score = 99
	if stored.Context.Rubric.Points[0].Score != 5 {
		t.Fatal("caller changed stored rubric")
	}
	payload, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["question"] == nil || decoded["rubric"] == nil {
		t.Fatalf("missing material fields: %s", payload)
	}
}

func TestPostgresLegacyArbitrationReadUsesSnapshotRubricAndSavedAnswer(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	steps := []reviewBatch34Step{{name: "legacy arbitration", fragment: "FROM arbitration_task", columns: 26,
		args: []any{"tenant", "legacy"}, rows: [][]driver.Value{{
			"legacy", "tenant", "session", "exam", "question", "Q1", "segment", "submission", "anonymous",
			"first", "second", float64(5), float64(2), float64(3), "difference", "assigned", "arbitrator",
			nil, "", "", false, []byte(`{"raw_answer":"saved answer"}`), int64(1), "manager", now, now,
		}},
	}}
	contextSteps := reviewBatch34ContextSteps()
	// 现行细则行为空，但考试快照有冻结评分点；回读必须采用快照，不能依赖可编辑细则。
	contextSteps[0].rows[0][4] = []byte(`{"rubric_snapshot":{"id":"frozen","question_id":"question","version":"v1","status":"approved","max_score":10.5,"points":[{"id":"p1","description":"frozen rule","score":10.5,"required":true}],"deductions":[],"examples":[]}}`)
	steps = append(steps, contextSteps...)
	store, script := openReviewBatch34Store(t, steps)
	task, err := store.GetArbitrationTask(context.Background(), "tenant", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if task.Context.Question == nil || task.Context.Question.Score != 10.5 || task.Context.Rubric == nil || task.Context.Rubric.ID != "frozen" || task.Context.Rubric.Points[0].Description != "frozen rule" {
		t.Fatalf("wrong legacy standards: %#v", task.Context)
	}
	if task.Context.RawAnswer != "saved answer" {
		t.Fatal("saved answer was replaced")
	}
	if script.next != len(script.steps) || script.commits != 0 || script.rolls != 0 {
		t.Fatalf("unexpected material read operations: %v", script.events)
	}
}

func TestLegacyArbitrationReadHydratesStandardsWithoutReplacingSavedAnswers(t *testing.T) {
	store := NewMemoryStore()
	frozen := reviewContext()
	frozen.RawAnswer = "newer answer must not replace saved text"
	store.AddContext(tenantID, "segment", frozen)
	store.arbitrations[key(tenantID, "legacy")] = ArbitrationTask{
		ID: "legacy", TenantID: tenantID, AnswerSegmentID: "segment", AssignedTo: "arbitrator",
		Context: ReviewContext{RawAnswer: "original answer", OCRText: "original OCR"},
	}
	task, err := store.GetArbitrationTask(context.Background(), tenantID, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if task.Context.Question == nil || task.Context.Question.Score != 5 || task.Context.Rubric == nil || task.Context.Rubric.MaxScore != 5 {
		t.Fatalf("legacy standards missing: %#v", task.Context)
	}
	if task.Context.RawAnswer != "original answer" || task.Context.OCRText != "original OCR" {
		t.Fatal("legacy answer was replaced during material hydration")
	}
	if _, err = store.GetArbitrationTask(context.Background(), "other-tenant", "legacy"); err != ErrNotFound {
		t.Fatalf("cross-tenant read: %v", err)
	}
}

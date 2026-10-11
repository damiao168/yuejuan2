package paper

import (
	"context"
	"errors"
	"testing"
)

func readinessConfirmationFixture() *MemoryStore {
	s := NewMemoryStore()
	s.SetReadinessContext("exam-1", 10, 1, 30, "configured")
	q := advisoryQuestion("question-1", "1", "short_answer", 10, true)
	s.questions[q.ID] = q
	s.papers["paper-1"] = Paper{ID: "paper-1", TenantID: "tenant-1", ExamID: "exam-1", FileAssetID: "file-1", Status: "ready"}
	s.templates["template-1"] = AnswerSheetTemplate{ID: "template-1", TenantID: "tenant-1", ExamID: "exam-1", ExamPaperID: "paper-1", Status: "locked", PageCount: 1,
		Layout: TemplateLayout{Pages: []TemplatePage{{PageNo: 1, Width: 1000, Height: 1400, QuestionRegions: []LayoutRegion{{ID: "region-1", QuestionID: q.ID, X: 0.1, Y: 0.1, Width: 0.8, Height: 0.3}}}}}}
	return s
}

func TestReadinessConfirmationRejectsChangedReviewedConfigurationWithoutFreezing(t *testing.T) {
	ctx := context.Background()
	s := readinessConfirmationFixture()
	reviewed, err := s.Readiness(ctx, "tenant-1", "exam-1")
	if err != nil || !reviewed.Ready {
		t.Fatalf("fixture not ready: %#v %v", reviewed, err)
	}
	// 模拟复核之后另一管理员提交了题干修改；测试内存锁的比较分支，不代替数据库并发集成验证。
	s.mu.Lock()
	q := s.questions["question-1"]
	q.Stem = "核对之后改动的题干"
	s.questions[q.ID] = q
	s.mu.Unlock()
	current, err := s.ConfirmReadiness(ctx, "tenant-1", "exam-1", "admin-1", ConfirmReadinessInput{ExpectedConfigurationHash: reviewed.ConfigurationHash})
	if !errors.Is(err, ErrReadinessChanged) || current.Confirmed || current.ConfigurationHash == reviewed.ConfigurationHash || s.ExamStatus("exam-1") != "configured" {
		t.Fatalf("stale confirmation froze or hid change: %#v %v", current, err)
	}
	if _, ok := s.readiness["exam-1"]; ok {
		t.Fatal("conflict wrote a confirmation snapshot")
	}
	confirmed, err := s.ConfirmReadiness(ctx, "tenant-1", "exam-1", "admin-1", ConfirmReadinessInput{ExpectedConfigurationHash: current.ConfigurationHash})
	if err != nil || !confirmed.Confirmed || s.ExamStatus("exam-1") != "ready" {
		t.Fatalf("matching confirmation failed: %#v %v", confirmed, err)
	}
}

func TestReadinessConfirmationKeepsLegacyEmptyInput(t *testing.T) {
	s := readinessConfirmationFixture()
	confirmed, err := s.ConfirmReadiness(context.Background(), "tenant-1", "exam-1", "admin-1", ConfirmReadinessInput{})
	if err != nil || !confirmed.Confirmed {
		t.Fatalf("legacy confirmation failed: %#v %v", confirmed, err)
	}
}

package calibration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type batch41GoldReader struct {
	items []goldpaper.GoldPaper
	calls int
}

func (r *batch41GoldReader) ListActiveApproved(context.Context, string, string, string) ([]goldpaper.GoldPaper, error) {
	r.calls++
	return r.items, nil
}

type batch41QuestionReader struct {
	calls []string
	fail  bool
}

func (r *batch41QuestionReader) QuestionForSnapshot(_ context.Context, _, _, question, snapshot string) (paper.Question, error) {
	r.calls = append(r.calls, snapshot)
	if r.fail && snapshot == "snapshot-a" {
		return paper.Question{}, context.DeadlineExceeded
	}
	return paper.Question{ID: question, Stem: snapshot}, nil
}

func TestBatch41CreateSessionPreservesGoldReadOrderAndFirstError(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "sort_after_reads"
		minimum := 2
		itemCount := 2
		if fail {
			name, minimum, itemCount = "read_error_precedes_incomplete_set", 4, 3
		}
		t.Run(name, func(t *testing.T) {
			assertGoldReadOrderAndFrozenSession(t, fail, minimum, itemCount)
		})
	}
}

// 读取错误发生时仍检查首错和未落库；成功后才核对排序后的冻结样本。
func assertGoldReadOrderAndFrozenSession(t *testing.T, fail bool, minimum, itemCount int) {
	t.Helper()
	service, gold, store := testService(t, minimum)
	ctx := context.Background()
	items, err := gold.ListActiveApproved(ctx, "tenant-a", "exam-1", "question-1")
	if err != nil || len(items) < itemCount {
		t.Fatalf("gold fixture: %d %v", len(items), err)
	}
	items = items[:itemCount]
	items[0].ID, items[1].ID = "gold-z", "gold-a"
	items[0].Versions[0].ExamQuestionSnapshotID = "snapshot-z"
	items[1].Versions[0].ExamQuestionSnapshotID = "snapshot-a"
	if fail {
		items[2].Versions[0].ExamQuestionSnapshotID = "snapshot-after-error"
	}
	goldReader := &batch41GoldReader{items: items}
	questionReader := &batch41QuestionReader{fail: fail}
	service.gold = goldReader
	service.WithQuestionSource(questionReader)
	session, err := service.CreateSession(ctx, "tenant-a", "exam-1", "question-1", "grader-1")
	if goldReader.calls != 1 || !reflect.DeepEqual(questionReader.calls, []string{"snapshot-z", "snapshot-a"}) {
		t.Fatalf("read order: gold=%d snapshots=%v", goldReader.calls, questionReader.calls)
	}
	if fail {
		// 数量也不足时仍先返回快照读取错误，不能保存部分会话。
		if !errors.Is(err, context.DeadlineExceeded) || len(store.sessions) != 0 {
			t.Fatalf("partial session persisted or error replaced: %d %v", len(store.sessions), err)
		}
		return
	}
	digest := sha256.Sum256([]byte("gold-a\x001\x00snapshot-a\x00gold-z\x001\x00snapshot-z\x00"))
	if err != nil || len(session.Samples) != 2 || session.SnapshotID != "snapshot-a" || session.GoldSetHash != hex.EncodeToString(digest[:]) {
		t.Fatalf("frozen reference set: %#v %v", session, err)
	}
	if session.Samples[0].GoldPaperID != "gold-a" || session.Samples[1].GoldPaperID != "gold-z" || session.Samples[0].Question == nil || session.Samples[0].Question.Stem != "snapshot-a" {
		t.Fatalf("reference sort or material mismatch: %#v", session.Samples)
	}
}

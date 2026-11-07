package workspace

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
)

// 生产工作区靠类型断言取得发布读取能力；签名漂移时在这里编译失败，而不是静默退回按状态显示。
var _ publishedExamReader = (*exam.PostgresStore)(nil)

type fakePublishedExamReader struct {
	fakeExamReader
	published map[string]bool
	err       error
	requests  [][]string
}

func (f *fakePublishedExamReader) PublishedExamIDs(_ context.Context, tenantID string, examIDs []string) (map[string]bool, error) {
	f.requests = append(f.requests, append([]string(nil), examIDs...))
	if tenantID != f.item.TenantID {
		return nil, errors.New("release read used another tenant")
	}
	return f.published, f.err
}

// 学生端发布不改 exam.status。工作区必须按发布记录显示“已发布”，读取失败时不能断言未发布或再次提示发布。
func TestWorkspaceUsesStudentReleaseInsteadOfExamStatus(t *testing.T) {
	for _, scenario := range []struct {
		name, status         string
		published            map[string]bool
		readErr              error
		wantStatus, wantStep string
		wantSummary          string
		wantAction           string
		wantReads            int
		wantWarning          bool
	}{
		{"released after finalization", "finalized", map[string]bool{"exam-1": true}, nil, "published", "current", "成绩已发布", "view_results", 1, false},
		{"released while stage stayed in review", "reviewing", map[string]bool{"exam-1": true}, nil, "published", "current", "成绩已发布", "view_results", 1, false},
		{"finalized without release", "finalized", map[string]bool{}, nil, "finalized", "current", "成绩已确认，等待发布", "release_scores", 1, false},
		{"grading without release", "grading", map[string]bool{}, nil, "grading", "pending", "成绩尚未发布；发布检查没有可量化的统一分母", "continue_grading", 1, false},
		{"release read fails", "finalized", nil, errors.New("release source down"), "finalized", "current", "暂无法确认成绩是否已发布", "view_results", 1, true},
		{"legacy published skips release read", "published", nil, nil, "published", "current", "成绩已发布", "view_results", 0, false},
		{"legacy archive keeps priority", "archived", nil, nil, "archived", "completed", "考试已归档", "", 0, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reader := &fakePublishedExamReader{
				fakeExamReader: fakeExamReader{item: exam.Exam{ID: "exam-1", TenantID: "tenant-1", Status: scenario.status}},
				published:      scenario.published, err: scenario.readErr,
			}
			service := NewService(Dependencies{Exams: reader, Papers: fakePaperReader{}, Submissions: fakeSubmissionReader{}, Reviews: fakeReviewReader{}})
			result, err := service.Get(context.Background(), auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, "exam-1")
			if err != nil {
				t.Fatal(err)
			}
			assertWorkspaceStudentRelease(t, result, reader.requests, scenario.wantStatus, scenario.wantStep, scenario.wantSummary, scenario.wantAction, scenario.wantReads, scenario.wantWarning)
		})
	}
}

func assertWorkspaceStudentRelease(t *testing.T, result Projection, requests [][]string, wantStatus, wantStep, wantSummary, wantAction string, wantReads int, wantWarning bool) {
	t.Helper()
	if len(requests) != wantReads || (wantReads > 0 && !reflect.DeepEqual(requests[0], []string{"exam-1"})) {
		t.Fatalf("release reads=%v, want %d read of the resolved exam", requests, wantReads)
	}
	if result.ExamStatus != wantStatus || len(result.Stages) != 5 || result.Stages[4].State != wantStep {
		t.Fatalf("status=%q stages=%#v, want %q with results stage %q", result.ExamStatus, result.Stages, wantStatus, wantStep)
	}
	if len(result.StageProgress) != 5 || result.StageProgress[4].Summary != wantSummary {
		t.Fatalf("results summary=%#v, want %q", result.StageProgress, wantSummary)
	}
	codes := []string{}
	for _, action := range result.NextActions {
		codes = append(codes, action.Code)
	}
	if (wantAction == "" && len(codes) != 0) || (wantAction != "" && !reflect.DeepEqual(codes, []string{wantAction})) {
		t.Fatalf("next actions=%v, want %q", codes, wantAction)
	}
	if noticeListHasCode(result.Warnings, "score_release_unavailable") != wantWarning {
		t.Fatalf("release warning=%#v, want %t", result.Warnings, wantWarning)
	}
}

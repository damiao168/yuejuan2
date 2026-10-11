package schoolassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/workspace"
)

type batch40AuditRecorder struct{ calls int }

func (a *batch40AuditRecorder) Audit(_ context.Context, _ auth.AuditEvent) error {
	a.calls++
	return nil
}

func TestBatch40QueryEmitFailureStopsLaterSourcesAndCompletion(t *testing.T) {
	for _, stopAt := range []string{"source_started", "source_finished"} {
		t.Run(stopAt, func(t *testing.T) {
			assertQueryEmitFailureStopsSources(t, stopAt)
		})
	}
}

func TestBatch40SearchLaterPageFailureKeepsPartialCandidates(t *testing.T) {
	now := time.Now().UTC()
	items := make([]exam.Exam, 0, examSearchPageSize+1)
	for index := 0; index <= examSearchPageSize; index++ {
		item := toolTestExamItem(fmt.Sprintf("%08d-3333-4333-8333-333333333333", index+1), now.Add(-time.Duration(index)*time.Minute))
		item.Name = "其他考试"
		if index == 0 {
			item.Name = "目标考试"
		}
		items = append(items, item)
	}
	// 第一页已有一个可信候选，第二页读失败，不能丢掉事实或将有限结果表述为完整检索。
	reader := &toolTestExams{items: items, failAt: 2}
	service := NewService(Dependencies{Exams: reader, Resources: toolTestResourceStore(items)})
	output, err := service.ExecuteTool(toolTestContext(t, "school_admin", "exam:manage"), toolTestRequest, ToolSearchExams, json.RawMessage(`{"query":"目标","limit":10}`))
	if err != nil || reader.listCalls != 2 || output.Status != "partial" || output.Result.Status != "partial" || output.Scanned != examSearchPageSize || len(output.Exams) != 1 || len(output.Result.Actions) != 1 || len(output.Result.Sources) != 1 || len(output.Result.Facts) != 1 {
		t.Fatalf("known candidates lost after later failure: output=%+v calls=%d err=%v", output, reader.listCalls, err)
	}
	source, fact := output.Result.Sources[0], output.Result.Facts[0]
	if source.Status != "partial" || source.Note == "" || output.Exams[0].ID != items[0].ID || output.Result.Actions[0].ID != output.Exams[0].ActionID || !fact.Available || fact.Value == nil || *fact.Value != 1 || len(output.Result.Limitations) < 2 {
		t.Fatalf("partial source lost evidence or limitations: output=%+v", output)
	}
}

// 每个失败边界使用独立依赖计数，保留 started/finished 的读取差异和成功审计禁止条件。
func assertQueryEmitFailureStopsSources(t *testing.T, stopAt string) {
	t.Helper()
	item := toolTestExamItem(toolTestExam, time.Now().UTC())
	reader := &toolTestExams{items: []exam.Exam{item}}
	w := &toolTestWorkspace{projection: workspace.Projection{ExamID: toolTestExam}}
	gate, current := &toolTestGate{}, &toolTestCurrent{}
	audit := &batch40AuditRecorder{}
	service := NewService(Dependencies{Exams: reader, Resources: toolTestResourceStore(reader.items), Workspaces: w, Gates: gate, CurrentReleases: current, Audit: audit})
	wantErr := errors.New("stream write failed")
	events := []string{}
	// 在首个来源边界模拟流写入失败，保护后续读取与成功审计都必须停止的顺序。
	result, err := service.Query(toolTestContext(t, "school_admin", "score:manage"), QueryRequest{RequestID: toolTestRequest, Question: "检查学生端发布", PresetIntent: IntentPublishDiagnosis, ContextHint: ContextHint{ExamID: toolTestExam, ReleaseChannel: "student_portal"}}, func(event Event) error {
		events = append(events, event.Type)
		if event.RequestID != toolTestRequest || event.Seq != len(events) {
			t.Fatalf("event identity changed: %+v", event)
		}
		if event.Type == stopAt {
			return wantErr
		}
		return nil
	})
	wantEvents := []string{"scope_resolved", "source_started"}
	wantReads := 0
	if stopAt == "source_finished" {
		wantEvents = append(wantEvents, "source_finished")
		wantReads = 1
	}
	if !errors.Is(err, wantErr) || result.RequestID != "" || !reflect.DeepEqual(events, wantEvents) || w.calls != wantReads || gate.calls != 0 || current.calls != 0 || audit.calls != 0 {
		t.Fatalf("failed stream continued: err=%v result=%+v events=%v workspace=%d gate=%d current=%d audit=%d", err, result, events, w.calls, gate.calls, current.calls, audit.calls)
	}
}

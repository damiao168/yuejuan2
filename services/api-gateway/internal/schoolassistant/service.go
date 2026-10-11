package schoolassistant

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type Service struct{ deps Dependencies }

func NewService(deps Dependencies) *Service { return &Service{deps: deps} }

func (s *Service) Capabilities(ctx context.Context) (Capabilities, error) {
	user, scope, err := principal(ctx)
	if err != nil {
		return Capabilities{}, err
	}
	result := Capabilities{Mode: "school_business", ModelRequired: false, Intents: []Capability{}}
	for _, item := range []Capability{
		{Intent: IntentExamProgress, Label: "查看考试进度", RequiredPermission: requiredPermission(IntentExamProgress)},
		{Intent: IntentPublishDiagnosis, Label: "为什么不能向学生发布", RequiredPermission: requiredPermission(IntentPublishDiagnosis)},
	} {
		item.Available = auth.HasPermission(user, item.RequiredPermission)
		if !item.Available {
			item.Reason = "当前账号缺少业务查询权限。"
		}
		if !scope.TenantWide && !scope.IsSchoolWide() && len(scope.ExamIDs) == 0 {
			item.Available = false
			item.Reason = "首期整场考试统计不支持当前班级或任务范围。"
		}
		if s.deps.Exams == nil || s.deps.Workspaces == nil || (item.Intent == IntentPublishDiagnosis && (s.deps.Gates == nil || s.deps.CurrentReleases == nil)) {
			item.Available = false
			item.Reason = "业务读取服务暂未就绪。"
		}
		result.Intents = append(result.Intents, item)
	}
	return result, nil
}

// Query 供编排测试或内部调用使用；HTTP 入口另在发送 SSE 头前完成 prepare。
// emit 的写入失败与取消会立即结束后续读取，不把半成品作为完整结果返回。
func (s *Service) Query(ctx context.Context, input QueryRequest, emit func(Event) error) (Result, error) {
	q, err := s.prepare(ctx, input)
	if err != nil {
		return Result{}, err
	}
	return s.queryPrepared(ctx, q, emit)
}

func (s *Service) queryPrepared(ctx context.Context, q preparedQuery, emit func(Event) error) (Result, error) {
	result := Result{RequestID: q.request.RequestID, Intent: q.request.PresetIntent, Status: "completed", Scope: q.scope,
		Facts: []Fact{}, Blocks: []Block{}, Sources: []Source{}, Actions: []Action{}, Limitations: []string{}}
	seq := 0
	send := func(event Event) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		seq++
		event.RequestID, event.Seq = q.request.RequestID, seq
		if emit != nil {
			return emit(event)
		}
		return nil
	}
	if err := send(Event{Type: "scope_resolved", Scope: &result.Scope}); err != nil {
		return Result{}, err
	}
	if q.clarification != "" {
		result.Status = "needs_clarification"
		result.Blocks = append(result.Blocks, Block{Type: "notice", Text: q.clarification})
	} else {
		if err := s.readQuerySources(ctx, q, &result, send); err != nil {
			return Result{}, err
		}
		result.Limitations = append(result.Limitations, "各来源按当前状态分别读取，未跨服务锁定快照；处理完成后请重新查询。")
	}
	if err := send(Event{Type: "result", Result: &result}); err != nil {
		return Result{}, err
	}
	if err := send(Event{Type: "done", Status: result.Status}); err != nil {
		return Result{}, err
	}
	s.audit(ctx, q, result.Status)
	return result, nil
}

// readQuerySources 保持工作区、完整门禁、当前发布指针的读取顺序，任一事件失败后不读下一来源。
func (s *Service) readQuerySources(ctx context.Context, q preparedQuery, result *Result, send func(Event) error) error {
	if err := readQuerySource(ctx, q, result, Source{ID: "workspace", Kind: "workspace", Label: "考试工作区"}, func(source *Source) { s.readWorkspace(ctx, q, source, result) }, send); err != nil {
		return err
	}
	if q.request.PresetIntent == IntentPublishDiagnosis {
		if err := readQuerySource(ctx, q, result, Source{ID: "release_gate", Kind: "release_gate", Label: "完整发布门禁"}, func(source *Source) { s.readGate(ctx, q, source, result) }, send); err != nil {
			return err
		}
		if err := readQuerySource(ctx, q, result, Source{ID: "current_release", Kind: "current_release", Label: "学生端当前发布版本"}, func(source *Source) { s.readCurrentRelease(ctx, q, source, result) }, send); err != nil {
			return err
		}
	}
	return nil
}

// readQuerySource 在 started 发送成功后计时并读取；取消或事件写入失败不能形成完整来源结果。
func readQuerySource(ctx context.Context, q preparedQuery, result *Result, source Source, read func(*Source), send func(Event) error) error {
	if err := send(Event{Type: "source_started", SourceID: source.ID, Label: source.Label}); err != nil {
		return err
	}
	started := time.Now()
	source.Status, source.ReadAt, source.Consistency, source.ScopeLabel = "available", started.UTC(), "source_local", q.scope.ScopeLabel
	read(&source)
	if err := ctx.Err(); err != nil {
		return err
	}
	result.Sources = append(result.Sources, source)
	return send(Event{Type: "source_finished", Source: &source, DurationMS: time.Since(started).Milliseconds()})
}

func (s *Service) readCurrentRelease(ctx context.Context, q preparedQuery, source *Source, result *Result) {
	if s.deps.CurrentReleases == nil {
		sourceUnavailable(source, result, "当前发布版本暂不可用。")
		return
	}
	detail, err := s.deps.CurrentReleases.CurrentPublished(ctx, q.user.TenantID, q.scope.ExamID)
	if errors.Is(err, scorerelease.ErrNotFound) {
		source.Status, source.Note = "empty", "学生端尚无当前生效发布版本。"
		result.Blocks = append(result.Blocks, Block{Type: "notice", Title: "学生端当前版本", Text: source.Note})
		return
	}
	if err != nil {
		sourceUnavailable(source, result, "暂时无法核实学生端当前发布版本。")
		return
	}
	// current 指针是唯一生效依据；不能用最大版本或最近创建的草稿推断学生看到的成绩。
	if detail.Release.TenantID != q.user.TenantID || detail.Release.ExamID != q.scope.ExamID || detail.Release.Status != scorerelease.StatusPublished {
		sourceUnavailable(source, result, "当前发布版本返回的资源范围或状态不一致。")
		return
	}
	source.ReleaseID, source.ReleaseVersion = detail.Release.ID, &detail.Release.Version
	result.Blocks = append(result.Blocks, Block{Type: "notice", Title: "学生端当前版本", Text: currentReleaseText(detail.Release.Version)})
}

func sourceUnavailable(source *Source, result *Result, note string) {
	source.Status, source.Note = "unavailable", note
	result.Status = "partial"
	result.Limitations = append(result.Limitations, note)
}

func (s *Service) audit(ctx context.Context, q preparedQuery, status string) {
	if s.deps.Audit == nil {
		return
	}
	// 仅审计任务和资源标识；问题、业务明细和整段回答不会进入审计或幂等回执。
	err := s.deps.Audit.Audit(ctx, auth.AuditEvent{TenantID: q.user.TenantID, ActorID: q.user.ID, Action: "school_assistant.queried", TargetType: "exam", TargetID: q.scope.ExamID,
		RequestID: q.request.RequestID, AfterValue: map[string]any{"intent": q.request.PresetIntent, "status": status, "exam_id": q.scope.ExamID}})
	if err != nil {
		slog.WarnContext(ctx, "school assistant audit failed", "request_id", q.request.RequestID)
	}
}

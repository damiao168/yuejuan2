package processing

import (
	"context"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type Service struct {
	store   Store
	runtime workerruntime.Store
}

func NewService(store Store, runtime workerruntime.Store) *Service {
	return &Service{store: store, runtime: runtime}
}

// Summary 是只读操作。来源变化由持久化后台投影器处理，因此 GET 不获取写锁，也不启动流水线工作。
func (s *Service) Summary(ctx context.Context, tenantID, examID string) (Summary, error) {
	if s == nil || s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(examID) == "" {
		return Summary{}, ErrInvalidInput
	}
	return s.store.Summary(ctx, tenantID, examID)
}

func (s *Service) ListExceptions(ctx context.Context, tenantID string, filter ExceptionFilter) (ListResult, error) {
	if s == nil || s.store == nil || strings.TrimSpace(tenantID) == "" {
		return ListResult{}, ErrInvalidInput
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 25
	}
	if filter.Severity != "" && !filter.Severity.Valid() || filter.Stage != "" && !filter.Stage.Valid() || filter.Status != "" && !filter.Status.Valid() {
		return ListResult{}, ErrInvalidInput
	}
	return s.store.ListExceptions(ctx, tenantID, filter)
}

func (s *Service) Assign(ctx context.Context, tenantID, exceptionID, actorID string, input AssignInput) (Exception, error) {
	if s == nil || s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(exceptionID) == "" || strings.TrimSpace(actorID) == "" || strings.TrimSpace(input.AssigneeID) == "" {
		return Exception{}, ErrInvalidInput
	}
	return s.store.AssignException(ctx, tenantID, exceptionID, actorID, input)
}

func (s *Service) Resolve(ctx context.Context, tenantID, exceptionID, actorID string, input ResolveInput) (Exception, error) {
	if s == nil || s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(exceptionID) == "" || strings.TrimSpace(actorID) == "" || strings.TrimSpace(input.Resolution) == "" {
		return Exception{}, ErrInvalidInput
	}
	return s.store.ResolveException(ctx, tenantID, exceptionID, actorID, input)
}

// Retry 委托现有 Worker Runtime，不在 API 进程内执行图像、OCR 或解析工作。
// 重复点击无害，因为 Worker Runtime 的 Requeue 自身检查状态。
func (s *Service) Retry(ctx context.Context, tenantID, exceptionID string) (workerruntime.Task, error) {
	if s == nil || s.store == nil || s.runtime == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(exceptionID) == "" {
		return workerruntime.Task{}, ErrInvalidInput
	}
	target, err := s.store.RetryTarget(ctx, tenantID, exceptionID)
	if err != nil {
		return workerruntime.Task{}, err
	}
	task, err := s.runtime.GetBySource(ctx, tenantID, target.SourceType, target.SourceID)
	if err != nil {
		return workerruntime.Task{}, err
	}
	if task.Status == workerruntime.StatusQueued || task.Status == workerruntime.StatusLeased || task.Status == workerruntime.StatusRunning {
		return task, nil
	}
	if task.Status != workerruntime.StatusFailed && task.Status != workerruntime.StatusDeadLetter {
		return workerruntime.Task{}, ErrRetryForbidden
	}
	if target.SourceType == "image_quality_run" {
		coordinator, ok := s.store.(interface {
			RetryImageQuality(context.Context, string, string, string) (workerruntime.Task, error)
		})
		if !ok {
			return workerruntime.Task{}, ErrRetryForbidden
		}
		return coordinator.RetryImageQuality(ctx, tenantID, target.SourceID, task.ID)
	}
	return s.runtime.Requeue(ctx, tenantID, task.ID)
}

// ParserQualityForSegment 是 A26 到 A14 的适配器。它返回与冻结学科、题型原型匹配的质量维度，
// 对缺失或不支持的解析器证据返回 nil，使 A14 放弃判断，而非把 OCR 文本质量当作数学或图示解析质量。
func (s *Service) ParserQualityForSegment(ctx context.Context, tenantID, segmentID string, subject assessment.SubjectCode, archetype string) (*float64, error) {
	if s == nil || s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(segmentID) == "" || !subject.Valid() {
		return nil, ErrInvalidInput
	}
	return s.store.ParserQualityForSegment(ctx, tenantID, segmentID, subject, archetype)
}

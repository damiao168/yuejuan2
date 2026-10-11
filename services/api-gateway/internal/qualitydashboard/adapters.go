package qualitydashboard

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/backmark"
	"edugrade-enterprise/services/api-gateway/internal/graderdrift"
)

// NewDriftReader 适配 A11 的聚合事件视图，不授予仪表盘访问 Seed 答案、Gold 参考或复核提交的权限。
func NewDriftReader(service *graderdrift.Service) DriftReader {
	if service == nil {
		return nil
	}
	return driftReader{service: service}
}

type driftReader struct{ service *graderdrift.Service }

func (r driftReader) GetDriftSummary(ctx context.Context, tenantID, examID, questionID string) (DriftSummary, error) {
	items, err := r.service.ListIncidents(ctx, tenantID, graderdrift.IncidentFilter{
		ExamID: examID, QuestionID: questionID, Limit: 500,
	})
	if err != nil {
		return DriftSummary{}, err
	}
	result := DriftSummary{Incidents: make([]Incident, 0, len(items))}
	for _, item := range items {
		if item.Status != graderdrift.IncidentOpen {
			continue
		}
		if item.Severity == graderdrift.SeverityCritical {
			result.OpenCritical++
		} else {
			result.OpenWarnings++
		}
		result.Incidents = append(result.Incidents, Incident{
			ID: item.ID, GraderRef: item.GraderID, Severity: string(item.Severity),
			Status: string(item.Status), Type: string(item.Type),
		})
	}
	return result, nil
}

// NewBackmarkReader 只公开批次进度和分数差异数量。
// 管理员仪表盘投影中刻意省略全部原始分数、新分数和身份。
func NewBackmarkReader(service *backmark.Service) BackmarkReader {
	if service == nil {
		return nil
	}
	return backmarkReader{service: service}
}

type backmarkReader struct{ service *backmark.Service }

func (r backmarkReader) GetBackmarkSummary(ctx context.Context, tenantID, examID, questionID string) (BackmarkSummary, error) {
	// List/Get 沿用回标服务的默认页；这里投影返回条目，不承诺覆盖批次的全部条目。
	batches, err := r.service.List(ctx, tenantID, examID, questionID)
	if err != nil {
		return BackmarkSummary{}, err
	}
	result := BackmarkSummary{}
	var compared, corrected int
	for _, batch := range batches {
		if batch.Status != backmark.BatchCompleted && batch.Status != backmark.BatchCancelled {
			result.OpenBatches++
		}
		summary, summaryErr := r.service.Get(ctx, tenantID, batch.ID)
		if summaryErr != nil {
			// 任一批次读取失败就丢弃已累计结果，调用方不能把部分投影当作完整摘要。
			return BackmarkSummary{}, summaryErr
		}
		batchCompared, batchCorrected := addBackmarkItems(&result, summary.Items)
		compared += batchCompared
		corrected += batchCorrected
	}
	result.CorrectionRate = ratio(corrected, compared)
	return result, nil
}

func addBackmarkItems(result *BackmarkSummary, items []backmark.Item) (compared, corrected int) {
	for _, item := range items {
		switch item.Status {
		case backmark.ItemPending, backmark.ItemInProgress:
			result.PendingItems++
		default:
			// 取消及未知状态也沿用完成计数；只有待处理和处理中属于 pending。
			result.CompletedItems++
		}
		if item.Diff != nil {
			compared++
			if *item.Diff != 0 {
				corrected++
			}
		}
	}
	return compared, corrected
}

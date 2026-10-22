package subjective

import "context"

// AIFailureReader 是 review 工作台使用的最小读取接口，避免跨包暴露运行和供应商细节。
// 参数中的快照与量规版本来自已经授权的 TaskContext；实现仍须重新校验租户和当前答案。
type AIFailureReader interface {
	CurrentAIFailure(context.Context, string, string, string, string, string, string) (string, error)
}

var _ AIFailureReader = (*MemoryStore)(nil)
var _ AIFailureReader = (*PostgresStore)(nil)

var teacherVisibleFailureKinds = map[string]struct{}{
	"output_truncated":          {},
	"finish_reason_missing":     {},
	"finish_reason_unknown":     {},
	"provider_refused":          {},
	"content_filtered":          {},
	"unsupported_generation":    {},
	"schema_invalid":            {},
	"evidence_invalid":          {},
	"capability_unavailable":    {},
	"response_envelope_invalid": {},
}

func teacherVisibleFailureKind(run GradingRun) string {
	if run.Status != RunFailed && run.Status != RunConflict {
		return ""
	}
	// 历史失败没有诊断时仍应停用建议；未知类别不解释为某种具体供应商行为。
	if run.ExecutionDiagnostics == nil {
		return "output_incomplete"
	}
	if _, ok := teacherVisibleFailureKinds[run.ExecutionDiagnostics.FailureKind]; !ok {
		return "output_incomplete"
	}
	return run.ExecutionDiagnostics.FailureKind
}

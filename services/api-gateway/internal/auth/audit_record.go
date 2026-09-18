package auth

import (
	"context"
	"os"

	"edugrade-enterprise/services/api-gateway/internal/logger"
)

// auditFailureLogger 专用于记录审计写入失败，直接输出到 stderr，
// 便于运维通过日志采集（level=error, message=audit_write_failed）配置告警。
var auditFailureLogger = logger.New(os.Stderr, "error")

// RecordAudit 写入一条审计事件，并保证失败不会被静默丢弃。
//
// 设计约定：
//   - 审计失败绝不阻断业务请求：调用方在业务成功后记录审计，此时即便
//     审计存储不可用，也不应让已完成的业务操作对用户报错或回滚；
//   - 但失败必须可被运维告警发现：所有写入失败都会以 error 级别输出
//     message=audit_write_failed 的结构化日志（含 action / target / tenant），
//     运维侧应对该日志配置监控告警，避免审计链路长期悄然中断。
func RecordAudit(ctx context.Context, sink AuditRecorder, event AuditEvent) {
	if sink == nil {
		return
	}
	if err := sink.Audit(ctx, event); err != nil {
		auditFailureLogger.Error(ctx, "audit_write_failed", map[string]any{
			"error":       err.Error(),
			"action":      event.Action,
			"target_type": event.TargetType,
			"target_id":   event.TargetID,
			"tenant_id":   event.TenantID,
		})
	}
}

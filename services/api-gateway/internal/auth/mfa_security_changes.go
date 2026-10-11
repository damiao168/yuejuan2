package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"edugrade-enterprise/services/api-gateway/internal/logger"
)

const mfaSecurityAggregate = "auth_mfa"
const notificationAwaitingChannel = "awaiting_channel"

// MFASecurityChange 是版本化事实，不是认证授权；其白名单载荷不接受任意审计映射或凭据数据。
type MFASecurityChange struct {
	SchemaVersion        int       `json:"schema_version"`
	EventID              string    `json:"event_id"`
	TenantID             string    `json:"tenant_id"`
	SubjectUserID        string    `json:"subject_user_id"`
	ActorUserID          string    `json:"actor_user_id"`
	EventType            string    `json:"event_type"`
	Operation            string    `json:"operation"`
	AuthMethod           string    `json:"auth_method"`
	NotificationRequired bool      `json:"notification_required"`
	OccurredAt           time.Time `json:"occurred_at"`
}

type mfaAuditMetadata struct {
	IPAddress, UserAgent, RequestID string
}
type mfaAuditMetadataKey struct{}

func newMFASecurityChange(tenantID, userID, eventType, method string, now time.Time) (MFASecurityChange, error) {
	change := MFASecurityChange{SchemaVersion: 1, TenantID: tenantID, SubjectUserID: userID, ActorUserID: userID,
		EventType: eventType, AuthMethod: method, NotificationRequired: true, OccurredAt: now.UTC()}
	switch eventType {
	case "auth.mfa_enabled":
		if method != "totp" {
			return change, ErrMFAInvalid
		}
		change.Operation = "mfa.totp.enable"
	case "auth.mfa_recovery_rotated":
		if method != "totp" {
			return change, ErrMFAInvalid
		}
		change.Operation = MFAOperationRotateRecovery
	case "auth.mfa_recovery_used":
		if method != "recovery_code" {
			return change, ErrMFAInvalid
		}
		change.Operation = MFAOperationDisable
	case "auth.mfa_disabled":
		if method != "totp" && method != "recovery_code" {
			return change, ErrMFAInvalid
		}
		change.Operation = MFAOperationDisable
	default:
		return change, ErrMFAInvalid
	}
	if tenantID == "" || userID == "" || now.IsZero() {
		return change, ErrMFAInvalid
	}
	change.EventID = uuid.NewString()
	return change, nil
}

func mfaSecurityAudit(ctx context.Context, change MFASecurityChange) AuditEvent {
	metadata, _ := ctx.Value(mfaAuditMetadataKey{}).(mfaAuditMetadata)
	after := map[string]any{"event_id": change.EventID, "auth_method": change.AuthMethod,
		"operation": change.Operation, "notification_required": true}
	if user, ok := UserFromContext(ctx); ok && user.TenantID == change.TenantID && user.ID == change.SubjectUserID {
		after["risk_level"] = string(normalizeRiskLevel(user.CurrentRiskLevel))
		after["risk_action"] = string(normalizeRiskAction(user.CurrentRiskAction))
		after["risk_policy_version"] = normalizeRiskPolicyVersion(user.RiskPolicyVersion)
	}
	return AuditEvent{TenantID: change.TenantID, ActorID: change.ActorUserID, Action: change.EventType,
		TargetType: "user", TargetID: change.SubjectUserID, AfterValue: after,
		IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent, RequestID: metadata.RequestID}
}

// 只为已有审计采集元数据。设备和 IP 提示是未受信任的描述，刻意不纳入通知事实。
func (h *Handler) mfaSecurityContext(ctx context.Context, ipAddress, userAgent string) context.Context {
	return context.WithValue(ctx, mfaAuditMetadataKey{}, mfaAuditMetadata{
		IPAddress: ipAddress, UserAgent: userAgent, RequestID: logger.RequestID(ctx),
	})
}

func recordMFASecurityChangeSQL(ctx context.Context, tx *sql.Tx, tenantID, userID, eventType, method string, now time.Time) error {
	change, err := newMFASecurityChange(tenantID, userID, eventType, method, now)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(change)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO event_outbox(id,tenant_id,aggregate_type,aggregate_id,event_type,payload,occurred_at)
 VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5,$6::jsonb,$7::timestamptz)`, change.EventID, tenantID, mfaSecurityAggregate, userID, eventType, string(payload), now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO auth_security_notification_intent(event_id,tenant_id,user_id,created_at)
 VALUES($1::uuid,$2::uuid,$3::uuid,$4::timestamptz)`, change.EventID, tenantID, userID, now); err != nil {
		return err
	}
	event := mfaSecurityAudit(ctx, change)
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(tenant_id,actor_id,action,target_type,target_id,after_value,ip_address,user_agent,request_id,created_at)
 VALUES($1::uuid,$2::uuid,$3,$4,$5::uuid,$6::jsonb,$7,$8,$9,$10::timestamptz)`,
		event.TenantID, event.ActorID, event.Action, event.TargetType, event.TargetID, auditJSON(event.AfterValue), event.IPAddress, event.UserAgent, event.RequestID, now)
	return err
}

type memorySecurityNotificationIntent struct {
	Change MFASecurityChange
	Status string
}

// 调用方持有 s.mu；修改 MFA 状态前先创建并验证变更。
func (s *MemoryStore) recordMFASecurityChange(ctx context.Context, change MFASecurityChange) {
	s.mfaNotificationIntents = append(s.mfaNotificationIntents, memorySecurityNotificationIntent{Change: change, Status: notificationAwaitingChannel})
	s.appendAudit(mfaSecurityAudit(ctx, change), change.OccurredAt)
}

// Package releasegate 管理 A18 成绩发布周围的策略与证据层。
// 它不创建或修改成绩发布，A18 仍是公开成绩的唯一边界。
// 本包记录应用的版本化质量策略、允许的警告确认，以及每次发布前重算的不可变结果。
package releasegate

import (
	"context"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

var (
	ErrNotFound          = errors.New("release gate resource not found")
	ErrInvalidInput      = errors.New("invalid release gate input")
	ErrForbiddenWaiver   = errors.New("release gate waiver is not allowed")
	ErrGateBlocked       = errors.New("release gate is blocked")
	ErrEvidenceImmutable = errors.New("release gate evidence is immutable")
)

const (
	PolicyActive    = "active"
	PolicyRetired   = "retired"
	EvidencePreview = "preview"
	EvidencePublish = "publish"
	WaiverRequested = "requested"
	WaiverApproved  = "approved"
	WaiverRejected  = "rejected"

	DefaultPolicyVersion = "A20.core.v1"
)

// Policy 的范围有限。基础阻断项由 A18 计算；
// 此版本化策略可要求确认明确列出的警告，但绝不能降低阻断项级别。
type Policy struct {
	ID                            string    `json:"id"`
	TenantID                      string    `json:"tenant_id"`
	ExamID                        string    `json:"exam_id"`
	Version                       string    `json:"version"`
	Status                        string    `json:"status"`
	RequireWarningAcknowledgement bool      `json:"require_warning_acknowledgement"`
	WaivableWarningCodes          []string  `json:"waivable_warning_codes"`
	CreatedBy                     string    `json:"created_by"`
	CreatedAt                     time.Time `json:"created_at"`
}

// Evaluation 是应用策略后的有效门禁。BaseGate 完整保留，包括全部阻断项。
// PendingWarningCodes 属于策略决策层，绝不能使来源阻断项变得可豁免。
type Evaluation struct {
	Policy              Policy            `json:"policy"`
	BaseGate            scorerelease.Gate `json:"base_gate"`
	Passed              bool              `json:"passed"`
	PendingWarningCodes []string          `json:"pending_warning_codes"`
	AppliedWaiverIDs    []string          `json:"applied_waiver_ids"`
	GeneratedAt         time.Time         `json:"generated_at"`
}

// Evidence 是只追加的取证证据。预览为管理员提供稳定对象，用于申请警告豁免；
// 发布证据行始终在发布前立即重新计算。
type Evidence struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	ExamID     string     `json:"exam_id"`
	ReleaseID  string     `json:"release_id,omitempty"`
	Phase      string     `json:"phase"`
	Evaluation Evaluation `json:"evaluation"`
	Hash       string     `json:"hash"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Waiver 只是针对某一不可变预览证据行中警告的申请。
// 批准在存储中另记为只追加的决定，调用方只看到当前决定投影。
type Waiver struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	ExamID         string     `json:"exam_id"`
	PolicyID       string     `json:"policy_id,omitempty"`
	EvidenceID     string     `json:"evidence_id"`
	IssueCode      string     `json:"issue_code"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status"`
	RequestedBy    string     `json:"requested_by"`
	RequestedAt    time.Time  `json:"requested_at"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	DecisionReason string     `json:"decision_reason,omitempty"`
}

type CreatePolicyInput struct {
	Version                       string   `json:"version"`
	RequireWarningAcknowledgement bool     `json:"require_warning_acknowledgement"`
	WaivableWarningCodes          []string `json:"waivable_warning_codes"`
}

type RequestWaiverInput struct {
	EvidenceID string `json:"evidence_id"`
	IssueCode  string `json:"issue_code"`
	Reason     string `json:"reason"`
}

type DecideWaiverInput struct {
	Approve bool   `json:"approve"`
	Reason  string `json:"reason"`
}

// BaseGateReader 是 A18 的标准安全检查清单。
// 实现必须根据来源事实计算，不能接受浏览器提供的门禁。
type BaseGateReader interface {
	Gate(context.Context, string, string) (scorerelease.Gate, error)
}

// RegradeBlockerReader 只公开 A19 与发布相关的状态。
// 不能通过发布另一个版本绕过待处理修正计划。
type RegradeBlockerReader interface {
	BlockingRegradeCount(context.Context, string, string) (int, error)
}

type Store interface {
	CreatePolicy(context.Context, string, string, string, CreatePolicyInput) (Policy, error)
	ActivePolicy(context.Context, string, string) (Policy, error)
	GetEvidence(context.Context, string, string) (Evidence, error)
	AppendEvidence(context.Context, Evidence) (Evidence, error)
	RequestWaiver(context.Context, Waiver) (Waiver, error)
	GetWaiver(context.Context, string, string) (Waiver, error)
	DecideWaiver(context.Context, string, string, string, DecideWaiverInput) (Waiver, error)
	ApprovedWaivers(context.Context, string, string, string) ([]Waiver, error)
}

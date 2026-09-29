package onboarding

import (
	"context"
	"errors"
	"strings"
)

var ErrUnsupportedActor = errors.New("onboarding readiness is unavailable for this role")

type TenantReader interface {
	HasManagedTenant(ctx context.Context) (bool, error)
}

type OrganizationReader interface {
	Summary(ctx context.Context, tenantID string, scope ResourceScope) (OrganizationSummary, error)
}

type UserReader interface {
	HasBusinessUser(ctx context.Context, tenantID string, scope ResourceScope) (bool, error)
}

type ExamReader interface {
	HasExam(ctx context.Context, tenantID string, scope ResourceScope) (bool, error)
}

type SystemReadinessReader interface {
	Summary(ctx context.Context) (SystemReadinessSummary, error)
}

type GovernanceReader interface {
	DataPolicy(ctx context.Context, tenantID string) (DataPolicySummary, error)
}

type Dependencies struct {
	Tenants      TenantReader
	Organization OrganizationReader
	Users        UserReader
	Exams        ExamReader
	System       SystemReadinessReader
	Governance   GovernanceReader
}

type Service struct{ deps Dependencies }

func NewService(deps Dependencies) *Service { return &Service{deps: deps} }

// Readiness 按角色选择平台或学校视角；其他角色直接拒绝，避免把管理状态泄露给普通用户。
func (s *Service) Readiness(ctx context.Context, actor Actor) (OnboardingReadiness, error) {
	if hasRole(actor.Roles, "platform_admin") {
		return s.platformReadiness(ctx, actor), nil
	}
	if hasRole(actor.Roles, "tenant_admin") || hasRole(actor.Roles, "school_admin") {
		return s.schoolReadiness(ctx, actor), nil
	}
	return OnboardingReadiness{}, ErrUnsupportedActor
}

func (s *Service) platformReadiness(ctx context.Context, actor Actor) OnboardingReadiness {
	checks := make([]ReadinessCheck, 0, 5)
	systemSummary, systemErr := s.deps.System.Summary(ctx)
	if systemErr != nil {
		checks = append(checks, unavailableCheck("system_core", SeverityBlocking, "暂时无法读取系统状态", "/system/status"))
	} else if systemSummary.CoreReady {
		checks = append(checks, ReadinessCheck{Key: "system_core", State: CheckReady, Severity: SeverityBlocking, Title: "系统运行正常", Description: "数据库、缓存与文件存储等关键依赖可用。", ActionPath: "/system/status"})
	} else {
		checks = append(checks, ReadinessCheck{Key: "system_core", State: CheckAction, Severity: SeverityBlocking, Title: "系统关键依赖需要处理", Description: "请先在系统运维中检查关键服务。", ActionCode: "check_system", ActionPath: "/system/status"})
	}

	// 空状态只兼容历史调用；明确的非 active 状态必须阻断平台初始化。
	adminReady := strings.EqualFold(strings.TrimSpace(actor.Status), "active") || strings.TrimSpace(actor.Status) == ""
	checks = append(checks, booleanCheck("platform_admin", adminReady, SeverityBlocking, "平台管理员有效", "平台管理员账号需要恢复为可用状态。", "check_account", "/account/sessions"))

	hasTenant, tenantErr := s.deps.Tenants.HasManagedTenant(ctx)
	if tenantErr != nil {
		checks = append(checks, unavailableCheck("first_school", SeverityBlocking, "暂时无法读取学校状态", "/platform/schools"))
	} else {
		checks = append(checks, booleanCheck("first_school", hasTenant, SeverityBlocking, "已创建第一所学校", "创建学校以及第一位学校管理员。", "create_school", "/platform/getting-started"))
	}

	if systemErr != nil {
		checks = append(checks, unavailableCheck("ai_mode", SeverityRecommended, "暂时无法读取 AI 状态", "/platform/model-config"))
	} else {
		checks = append(checks, aiCheck(systemSummary))
	}

	policy, policyErr := s.deps.Governance.DataPolicy(ctx, actor.TenantID)
	if policyErr != nil {
		checks = append(checks, unavailableCheck("data_policy", SeverityRecommended, "暂时无法读取数据处理策略", "/system/models"))
	} else {
		checks = append(checks, dataPolicyCheck(policy))
	}

	return summarize("platform", checks)
}

func (s *Service) schoolReadiness(ctx context.Context, actor Actor) OnboardingReadiness {
	type result struct {
		key, title, description, action string
		ready                           bool
		err                             error
	}
	results := []result{}
	organization, organizationErr := s.deps.Organization.Summary(ctx, actor.TenantID, actor.Scope)
	results = append(results, result{"school", "学校资料", "填写学校或考试机构资料。", "/organization/setup", organization.HasSchool, organizationErr})
	results = append(results, result{"teaching_structure", "年级与班级", "至少创建一个年级和班级。", "/organization/setup", organization.HasTeachingStructure, organizationErr})
	results = append(results, result{"students", "学生", "导入或创建学生名单。", "/organization/setup", organization.HasStudents, organizationErr})
	hasStaff, err := s.deps.Users.HasBusinessUser(ctx, actor.TenantID, actor.Scope)
	results = append(results, result{"staff", "教师与管理员", "创建至少一位学校管理员或教学人员。", "/organization/setup", hasStaff, err})

	checks := make([]ReadinessCheck, 0, 5)
	for _, item := range results {
		if item.err != nil {
			checks = append(checks, unavailableCheck(item.key, SeverityBlocking, "暂时无法读取"+item.title, item.action))
			continue
		}
		checks = append(checks, booleanCheck(item.key, item.ready, SeverityBlocking, item.title, item.description, "continue_school_setup", item.action))
	}

	hasExam, examErr := s.deps.Exams.HasExam(ctx, actor.TenantID, actor.Scope)
	if examErr != nil {
		checks = append(checks, unavailableCheck("first_exam", SeverityRecommended, "暂时无法读取考试状态", "/exams/new"))
	} else if hasExam {
		checks = append(checks, ReadinessCheck{Key: "first_exam", State: CheckReady, Severity: SeverityRecommended, Title: "已创建第一场考试", ActionPath: "/exams"})
	} else {
		checks = append(checks, ReadinessCheck{Key: "first_exam", State: CheckAction, Severity: SeverityRecommended, Title: "创建第一场考试", Description: "基础启用完成后，建议创建第一场正式考试。", ActionCode: "create_exam", ActionPath: "/exams/new"})
	}
	return summarize("school", checks)
}

func aiCheck(summary SystemReadinessSummary) ReadinessCheck {
	metadata := map[string]any{"mode": summary.AIMode, "configured": summary.AIConfigured, "available": summary.AIAvailable}
	if summary.AIModel != "" {
		metadata["model"] = summary.AIModel
	}
	if !summary.AIConfigured {
		return ReadinessCheck{Key: "ai_mode", State: CheckAction, Severity: SeverityRecommended, Title: "选择 AI 工作模式", Description: "可以配置本地或第三方模型，也可以继续使用人工阅卷。", ActionCode: "configure_ai", ActionPath: "/platform/model-config", Metadata: metadata}
	}
	if !summary.AIAvailable {
		return ReadinessCheck{Key: "ai_mode", State: CheckWarning, Severity: SeverityRecommended, Title: "AI 阅卷当前不可用", Description: "这不会阻断人工阅卷，请检查模型运行状态。", ActionCode: "check_ai", ActionPath: "/platform/model-config", Metadata: metadata}
	}
	title := "AI 阅卷已准备"
	if summary.AIMode == "local" {
		title = "当前使用本地模型"
	} else if summary.AIMode == "external" {
		title = "当前使用第三方模型"
	}
	return ReadinessCheck{Key: "ai_mode", State: CheckReady, Severity: SeverityRecommended, Title: title, Description: "模型状态已确认。", ActionPath: "/platform/model-config", Metadata: metadata}
}

func dataPolicyCheck(policy DataPolicySummary) ReadinessCheck {
	metadata := map[string]any{
		"external_enabled": policy.ExternalEnabled, "text_export_enabled": policy.TextExportEnabled,
		"image_export_enabled": policy.ImageExportEnabled, "fallback_mode": policy.FallbackMode,
	}
	if policy.ExternalEnabled && (policy.TextExportEnabled || policy.ImageExportEnabled) {
		return ReadinessCheck{Key: "data_policy", State: CheckWarning, Severity: SeverityRecommended, Title: "当前允许部分答题数据发送到外部 AI", Description: "请确认文本与图片外发范围符合学校的数据政策。", ActionCode: "review_data_policy", ActionPath: "/system/models", Metadata: metadata}
	}
	return ReadinessCheck{Key: "data_policy", State: CheckReady, Severity: SeverityRecommended, Title: "答题数据保持在本地", Description: "当前模型策略未开启答题文本或图片外发。", ActionPath: "/system/models", Metadata: metadata}
}

func booleanCheck(key string, ready bool, severity Severity, title, description, actionCode, actionPath string) ReadinessCheck {
	if ready {
		return ReadinessCheck{Key: key, State: CheckReady, Severity: severity, Title: title, ActionPath: actionPath}
	}
	return ReadinessCheck{Key: key, State: CheckAction, Severity: severity, Title: title, Description: description, ActionCode: actionCode, ActionPath: actionPath}
}

func unavailableCheck(key string, severity Severity, title, actionPath string) ReadinessCheck {
	return ReadinessCheck{Key: key, State: CheckUnavailable, Severity: severity, Title: title, Description: "系统不会把无法确认的状态标记为已完成，请稍后重试。", ActionCode: "retry", ActionPath: actionPath}
}

func summarize(scope string, checks []ReadinessCheck) OnboardingReadiness {
	result := OnboardingReadiness{Scope: scope, ReadyForUse: true, Checks: checks}
	// 只有 blocking 检查计入 ReadyForUse；recommended 检查只产生提示，不阻断基础使用。
	for _, check := range checks {
		if check.Severity == SeverityBlocking {
			result.TotalRequired++
			if check.State == CheckReady {
				result.CompletedCount++
			} else {
				result.ReadyForUse = false
			}
		}
	}
	// NextAction 优先选择第一个可执行检查，前端据此把管理员带到最短处理路径。
	for _, check := range checks {
		if check.State != CheckReady && check.ActionCode != "" && check.ActionPath != "" && (check.Severity == SeverityBlocking || result.ReadyForUse) {
			result.NextAction = &NextAction{Code: check.ActionCode, Path: check.ActionPath}
			break
		}
	}
	return result
}

func hasRole(roles []string, target string) bool {
	for _, role := range roles {
		if role == target {
			return true
		}
	}
	return false
}

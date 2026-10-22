package subjective

import (
	"context"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

// Grade 将资格准入、模型调用、确定性结算和运行状态更新串成一条链。
// 证据或结算存在不确定结果时按对应失败路径返回，不能继续写入成功成绩。
func (h *Handler) Grade(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.requireAvailable(w, r) {
		return
	}
	var input GradeRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	ctx, policy, requestID, err := h.prepareDirectGrade(r.Context(), user.TenantID, r.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if input.IdempotencyKey != "" && h.replayGrade(w, r, user.TenantID, requestID) {
		return
	}
	run, runErr := h.store.GetOrCreateRun(r.Context(), user.TenantID, user.ID, runInputFor(ctx, "", policy, requestID))
	if runErr != nil {
		writeStoreError(w, r, runErr)
		return
	}
	runID := run.ID
	// 同一幂等请求的终态失败不能再开一轮推理；新尝试须通过已有管理入口生成新运行。
	if run.Status == RunFailed || run.Status == RunConflict {
		writeAgentFailureResponse(w, r, &GradingAgentError{Code: run.ErrorCode, ExecutionDiagnostics: run.ExecutionDiagnostics}, false)
		return
	}
	decision, allowed, decisionErr := h.decideEligibility(r.Context(), user.TenantID, runID, ctx, policy)
	if decisionErr != nil {
		writeStoreError(w, r, decisionErr)
		return
	}
	settlement := GradeSettlementInput{TenantID: user.TenantID, RunID: runID, Context: ctx, Policy: policy, Decision: decision}
	if !allowed {
		h.writeDirectGradeAbstention(w, r, user.ID, settlement)
		return
	}
	promptGuard := InspectPromptInjection(ctx.AnswerText)
	adapterInput, activeAdapter, usedMathV2, err := h.buildAdapterInput(r.Context(), user.TenantID, requestID, ctx, policy, promptGuard, decision.OutputConstraint)
	if err != nil {
		h.writeDirectGradeInputFailure(w, r, settlement, err)
		return
	}
	output, err := activeAdapter.Grade(r.Context(), adapterInput)
	output.RequestID = requestID
	if err != nil {
		h.writeDirectGradeAdapterFailure(w, r, user.ID, settlement, output, promptGuard, err)
		return
	}
	settlement.MathRun = usedMathV2
	if err := bindImageGradeModel(&ctx, policy, adapterInput.ImageModel); err != nil {
		h.writeDirectGradeInputFailure(w, r, settlement, err)
		return
	}
	settlement.Context = ctx
	if settleErr := h.gradeSettlementPipeline().Settle(r.Context(), settlement, &output); settleErr != nil {
		h.writeDirectGradeSettlementFailure(w, r, user.ID, settlement, output, settleErr)
		return
	}
	grade, err := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, successfulGrade(ctx, policy, runID, output))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if _, updateErr := h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunSucceeded, GradeID: grade.ID, ErrorCode: "", ExecutionDiagnostics: diagnosticsFromOutput(output, "")}); updateErr != nil {
		writeStoreError(w, r, updateErr)
		return
	}
	h.auditAction(r, "subjective.ai_grade_created", "ai_grade", grade.ID, "create subjective ai grade")
	httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
}

// 先验证策略并准备证据，再计算请求身份；重试回放也沿用这一步得到的当前证据版本。
func (h *Handler) prepareDirectGrade(ctx context.Context, tenantID, segmentID string, input GradeRequest) (Context, ModelPolicy, string, error) {
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return Context{}, ModelPolicy{}, "", err
	}
	policy := NormalizePolicy(input.ModelPolicy)
	if governed, ok := h.adapter.(GovernedPolicyProvider); ok {
		policy = governed.Policy()
	}
	if err := ValidatePolicy(policy); err != nil {
		return Context{}, policy, "", err
	}
	value, err := h.store.LoadContext(ctx, tenantID, segmentID)
	if err != nil {
		return value, policy, "", err
	}
	if err := h.prepareMathEvidence(ctx, tenantID, &value); err != nil {
		return value, policy, "", err
	}
	if !IsSupportedQuestionType(value.Question.QuestionType) {
		return value, policy, "", ErrUnsupportedQuestionType
	}
	requestID, err := stableRequestID(tenantID, value, policy, input.IdempotencyKey)
	return value, policy, requestID, err
}

func (h *Handler) writeDirectGradeAbstention(w http.ResponseWriter, r *http.Request, actorID string, input GradeSettlementInput) {
	grade, err := h.store.CreateGrade(r.Context(), input.TenantID, actorID, failedGrade(input.Context, input.Policy, input.RunID, "ai_eligibility_abstained", eligibilityAbstentionOutput(input.Policy, input.Decision)))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if _, err := h.store.UpdateRun(r.Context(), input.TenantID, input.RunID, UpdateRunInput{Status: RunFailed, GradeID: grade.ID, ErrorCode: "ai_eligibility_abstained"}); err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "subjective.ai_eligibility_abstained", "subjective_grading_run", input.RunID, "external AI was not admitted")
	httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade, "eligibility_decision": input.Decision})
}

// 输入证据冲突与证据缺失保持各自响应；运行状态只尽力记录，不替换原失败结果。
func (h *Handler) writeDirectGradeInputFailure(w http.ResponseWriter, r *http.Request, input GradeSettlementInput, err error) {
	if isMathRevisionConflict(err) {
		_, _ = h.store.UpdateRun(r.Context(), input.TenantID, input.RunID, UpdateRunInput{Status: RunConflict, ErrorCode: "math_evidence_version_conflict"})
		writeStoreError(w, r, err)
		return
	}
	_, _ = h.store.UpdateRun(r.Context(), input.TenantID, input.RunID, UpdateRunInput{Status: RunFailed, ErrorCode: "math_evidence_unavailable"})
	h.auditAction(r, "subjective.math_human_review_required", "subjective_grading_run", input.RunID, "math evidence or active crop is unavailable")
	httpx.JSON(w, http.StatusUnprocessableEntity, mathReviewPayload(input.Context, AdapterOutput{}))
}

func (h *Handler) writeDirectGradeAdapterFailure(w http.ResponseWriter, r *http.Request, actorID string, input GradeSettlementInput, output AdapterOutput, guard PromptGuard, err error) {
	var agentErr *GradingAgentError
	if errors.As(err, &agentErr) {
		h.writeAgentFailure(w, r, input.TenantID, input.RunID, agentErr)
		return
	}
	ApplyPromptGuard(&output, guard)
	grade, createErr := h.store.CreateGrade(r.Context(), input.TenantID, actorID, failedGrade(input.Context, input.Policy, input.RunID, err.Error(), output))
	if createErr != nil {
		writeStoreError(w, r, createErr)
		return
	}
	if _, updateErr := h.store.UpdateRun(r.Context(), input.TenantID, input.RunID, UpdateRunInput{Status: RunFailed, GradeID: grade.ID, ErrorCode: "adapter_failed", ExecutionDiagnostics: diagnosticsFromOutput(output, "schema_invalid")}); updateErr != nil {
		writeStoreError(w, r, updateErr)
		return
	}
	h.auditAction(r, "subjective.ai_grade_failed", "ai_grade", grade.ID, "subjective adapter failed")
	httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
}

// 数学结算失败和校准失败先按原路径返回；普通输出校验失败才保存失败成绩与运行状态。
func (h *Handler) writeDirectGradeSettlementFailure(w http.ResponseWriter, r *http.Request, actorID string, input GradeSettlementInput, output AdapterOutput, err error) {
	if input.MathRun {
		h.writeMathSettlementFailure(w, r, input.TenantID, input.RunID, input.Context, output, err)
		return
	}
	if errors.Is(err, ErrGradeCalibration) {
		writeStoreError(w, r, err)
		return
	}
	grade, createErr := h.store.CreateGrade(r.Context(), input.TenantID, actorID, failedGrade(input.Context, input.Policy, input.RunID, err.Error(), output))
	if createErr != nil {
		writeStoreError(w, r, createErr)
		return
	}
	if _, updateErr := h.store.UpdateRun(r.Context(), input.TenantID, input.RunID, UpdateRunInput{Status: RunFailed, GradeID: grade.ID, ErrorCode: "invalid_model_output", ExecutionDiagnostics: diagnosticsFromOutput(output, "schema_invalid")}); updateErr != nil {
		writeStoreError(w, r, updateErr)
		return
	}
	h.auditAction(r, "subjective.ai_grade_failed", "ai_grade", grade.ID, "subjective adapter output failed schema validation")
	httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
}

// 证据准备和请求身份计算在回放前完成；只有未找到结果才继续创建运行，查询失败不能当作首次请求。
func (h *Handler) replayGrade(w http.ResponseWriter, r *http.Request, tenantID, requestID string) bool {
	existing, err := h.store.GetGradeByAdapterRequestID(r.Context(), tenantID, requestID)
	if err == nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"grade": existing, "idempotent_replay": true})
		return true
	}
	if !errors.Is(err, ErrNotFound) {
		writeStoreError(w, r, err)
		return true
	}
	return false
}

// 服务不可用时先保存运行失败状态；保存失败仍优先返回存储错误，不生成失败成绩。
func (h *Handler) writeAgentFailure(w http.ResponseWriter, r *http.Request, tenantID, runID string, agentErr *GradingAgentError) {
	code, _ := agentFailureHTTP(agentErr)
	if _, err := h.store.UpdateRun(r.Context(), tenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: code, ExecutionDiagnostics: agentErr.ExecutionDiagnostics}); err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "subjective.ai_service_unavailable", "subjective_grading_run", runID, code)
	writeAgentFailureResponse(w, r, agentErr, false)
}

func agentFailureHTTP(agentErr *GradingAgentError) (string, int) {
	if agentErr.Code == "adapter_not_configured" {
		return "ai_service_not_configured", http.StatusServiceUnavailable
	}
	if agentErr.ExecutionDiagnostics != nil && agentErr.ExecutionDiagnostics.FailureKind != "" {
		return "ai_output_incomplete", http.StatusBadGateway
	}
	return "ai_service_unavailable", http.StatusServiceUnavailable
}

// 只返回结构化白名单；老师不能从失败响应收到半截正文、上游 request ID 或内部地址。
func writeAgentFailureResponse(w http.ResponseWriter, r *http.Request, agentErr *GradingAgentError, recorded bool) {
	code, status := agentFailureHTTP(agentErr)
	w.Header().Set(httpx.ErrorCodeHeader, code)
	httpx.JSON(w, status, map[string]any{"request_id": logger.RequestID(r.Context()), "trace_id": logger.TraceID(r.Context()),
		"error": httpx.ErrorBody{Code: code, Message: "AI 建议未完成，请继续人工评分"}, "execution_diagnostics": agentErr.ExecutionDiagnostics,
		"retryable": agentErrorRetryable(agentErr), "task_failure_recorded": recorded})
}

// 数学结算失败不生成成绩；状态更新仍为尽力记录，响应保留原来的复核、版本冲突或校验错误。
func (h *Handler) writeMathSettlementFailure(w http.ResponseWriter, r *http.Request, tenantID, runID string, value Context, output AdapterOutput, err error) {
	if errors.Is(err, ErrMathHumanReviewRequired) {
		_, _ = h.store.UpdateRun(r.Context(), tenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: "math_human_review_required", ExecutionDiagnostics: diagnosticsFromOutput(output, "evidence_invalid")})
		h.auditAction(r, "subjective.math_human_review_required", "subjective_grading_run", runID, "math scoring remained unresolved")
		httpx.JSON(w, http.StatusUnprocessableEntity, mathReviewPayload(value, output))
		return
	}
	if isMathRevisionConflict(err) {
		_, _ = h.store.UpdateRun(r.Context(), tenantID, runID, UpdateRunInput{Status: RunConflict, ErrorCode: "math_evidence_version_conflict", ExecutionDiagnostics: diagnosticsFromOutput(output, "evidence_invalid")})
		httpx.Error(w, r, http.StatusConflict, "math_evidence_version_conflict", "math evidence changed during grading")
		return
	}
	_, _ = h.store.UpdateRun(r.Context(), tenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: "invalid_model_output", ExecutionDiagnostics: diagnosticsFromOutput(output, "schema_invalid")})
	writeStoreError(w, r, err)
}

func successfulGrade(ctx Context, policy ModelPolicy, runID string, output AdapterOutput) Grade {
	graderType := LLMGraderType
	if output.Mock {
		graderType = MockGraderType
	}
	grade := Grade{
		AnswerSegmentID:        ctx.SegmentID,
		QuestionID:             ctx.Question.ID,
		QuestionNo:             ctx.Question.QuestionNo,
		QuestionType:           ctx.Question.QuestionType,
		AnswerVersion:          ctx.AnswerVersion,
		GraderType:             graderType,
		ModelVersion:           firstNonEmpty(output.ModelVersion, policy.ModelVersion),
		ModelConfigID:          ctx.modelConfigID,
		ModelName:              ctx.modelName,
		PromptVersion:          firstNonEmpty(output.PromptVersion, policy.PromptVersion),
		RubricVersion:          firstNonEmpty(output.RubricVersion, ctx.Rubric.Version),
		DeliveryMode:           firstNonEmpty(output.DeliveryMode, "teacher_review"),
		CapabilityProfile:      output.CapabilityProfile,
		AdapterRequestID:       output.RequestID,
		RunID:                  runID,
		AdapterName:            output.Telemetry.Adapter,
		ProviderKey:            output.Telemetry.Provider,
		DeploymentKey:          output.Telemetry.Deployment,
		DeploymentRegion:       output.Telemetry.Region,
		AdapterAttempts:        output.Telemetry.Attempts,
		AdapterLatencyMS:       output.Telemetry.ElapsedMS,
		AdapterRepairAttempted: output.Telemetry.RepairAttempted,
		InputTokens:            output.Telemetry.Usage.InputTokens,
		CachedInputTokens:      output.Telemetry.Usage.CachedInputTokens,
		OutputTokens:           output.Telemetry.Usage.OutputTokens,
		ReasoningTokens:        output.Telemetry.Usage.ReasoningTokens,
		TotalTokens:            output.Telemetry.Usage.TotalTokens,
		SuggestedScore:         output.SuggestedScore,
		MaxScore:               ctx.Question.Score,
		Confidence:             output.Confidence,
		MatchedPoints:          output.MatchedPoints,
		MissingPoints:          output.MissingPoints,
		Evidence:               output.Evidence,
		RiskFlags:              output.RiskFlags,
		NeedsHumanReview:       output.NeedsHumanReview,
		StudentFeedback:        output.StudentFeedback,
		TeacherNote:            output.TeacherNote,
		Mock:                   output.Mock,
		Status:                 "succeeded",
		RawOutput:              output.RawOutput,
	}
	if ctx.MathEvidence != nil {
		grade.MathArtifactID = ctx.MathEvidence.ArtifactID
		grade.MathArtifactVersion = ctx.MathEvidence.ArtifactVersion
		grade.MathCorrectionRevision = ctx.MathEvidence.CorrectionRevision
		grade.MathScoringVersion = ctx.MathEvidence.ScoringVersion
	}
	return grade
}

func failedGrade(ctx Context, policy ModelPolicy, runID string, reason string, output AdapterOutput) Grade {
	riskFlags := appendFlag(output.RiskFlags, "invalid_model_output")
	if output.Mock {
		riskFlags = appendFlag(riskFlags, "mock_llm_output")
	}
	graderType := LLMGraderType
	if output.Mock {
		graderType = MockGraderType
	}
	raw := output.RawOutput
	if raw == nil {
		raw = map[string]any{}
	}
	raw["failure_reason"] = reason
	grade := Grade{
		AnswerSegmentID:        ctx.SegmentID,
		QuestionID:             ctx.Question.ID,
		QuestionNo:             ctx.Question.QuestionNo,
		QuestionType:           ctx.Question.QuestionType,
		AnswerVersion:          ctx.AnswerVersion,
		GraderType:             graderType,
		ModelVersion:           firstNonEmpty(output.ModelVersion, policy.ModelVersion),
		ModelConfigID:          ctx.modelConfigID,
		ModelName:              ctx.modelName,
		PromptVersion:          firstNonEmpty(output.PromptVersion, policy.PromptVersion),
		RubricVersion:          firstNonEmpty(output.RubricVersion, ctx.Rubric.Version),
		DeliveryMode:           "teacher_review",
		CapabilityProfile:      output.CapabilityProfile,
		AdapterRequestID:       output.RequestID,
		RunID:                  runID,
		AdapterName:            output.Telemetry.Adapter,
		ProviderKey:            output.Telemetry.Provider,
		DeploymentKey:          output.Telemetry.Deployment,
		DeploymentRegion:       output.Telemetry.Region,
		AdapterAttempts:        output.Telemetry.Attempts,
		AdapterLatencyMS:       output.Telemetry.ElapsedMS,
		AdapterRepairAttempted: output.Telemetry.RepairAttempted,
		InputTokens:            output.Telemetry.Usage.InputTokens,
		CachedInputTokens:      output.Telemetry.Usage.CachedInputTokens,
		OutputTokens:           output.Telemetry.Usage.OutputTokens,
		ReasoningTokens:        output.Telemetry.Usage.ReasoningTokens,
		TotalTokens:            output.Telemetry.Usage.TotalTokens,
		SuggestedScore:         0,
		MaxScore:               ctx.Question.Score,
		Confidence:             0,
		MatchedPoints:          []grading.PointResult{},
		MissingPoints:          []grading.PointResult{},
		Evidence:               []grading.Evidence{},
		RiskFlags:              riskFlags,
		NeedsHumanReview:       true,
		StudentFeedback:        "",
		TeacherNote:            "Adapter output failed schema validation; no valid subjective score was produced.",
		Mock:                   output.Mock,
		Status:                 "failed",
		FailureReason:          reason,
		RawOutput:              raw,
	}
	if ctx.MathEvidence != nil {
		grade.MathArtifactID = ctx.MathEvidence.ArtifactID
		grade.MathArtifactVersion = ctx.MathEvidence.ArtifactVersion
		grade.MathCorrectionRevision = ctx.MathEvidence.CorrectionRevision
		grade.MathScoringVersion = ctx.MathEvidence.ScoringVersion
	}
	return grade
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

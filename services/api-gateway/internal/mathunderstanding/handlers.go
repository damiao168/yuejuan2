package mathunderstanding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type Handler struct {
	artifacts   Store
	corrections CorrectionStore
	pilotGates  PilotGateStore
	reviews     reviewAssignmentStore
	audit       auth.AuditRecorder
	runtime     workerruntime.Store
	rubrics     FrozenRubricSource
	crops       ActiveMathCropSource
}

type reviewAssignmentStore interface {
	HasActiveAssignment(ctx context.Context, tenantID string, reviewerID string, answerSegmentID string) (bool, error)
}

func NewHandler(artifacts Store, corrections CorrectionStore, pilotGates PilotGateStore, reviews reviewAssignmentStore, audit auth.AuditRecorder) *Handler {
	h := &Handler{artifacts: artifacts, corrections: corrections, pilotGates: pilotGates, reviews: reviews, audit: audit}
	if source, ok := artifacts.(FrozenRubricSource); ok {
		h.rubrics = source
	}
	if source, ok := artifacts.(ActiveMathCropSource); ok {
		h.crops = source
	}
	return h
}

func (h *Handler) WithRuntime(runtime workerruntime.Store) *Handler {
	h.runtime = runtime
	return h
}

func RegisterRoutes(mux *http.ServeMux, handler *Handler, requireWork, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/math-answer-segments/{segmentId}/understanding", requireWork(handler.GetLatest))
	mux.Handle("GET /api/v1/math-answer-segments/{segmentId}/rubric-score", requireWork(handler.GetRubricScore))
	mux.Handle("GET /api/v1/math-understanding/{artifactId}/corrections", requireWork(handler.ListCorrections))
	mux.Handle("POST /api/v1/math-understanding/{artifactId}/corrections", requireWork(handler.CreateCorrection))
	mux.Handle("GET /api/v1/math-understanding/training-export", requireManage(handler.ExportTraining))
	mux.Handle("GET /api/v1/math-pilot-gates", requireManage(handler.ListPilotGates))
	mux.Handle("POST /api/v1/math-pilot-gates/evaluate", requireManage(handler.EvaluatePilotGate))
}

func RegisterRuntimeRoutes(mux *http.ServeMux, handler *Handler, requireWorker func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/internal/math-understanding/tasks/{taskId}/input", requireWorker(handler.GetRuntimeInput))
	mux.Handle("POST /api/v1/internal/math-understanding/tasks/{taskId}/complete", requireWorker(handler.CompleteRuntimeTask))
	mux.Handle("GET /api/v1/internal/math-verification/tasks/{taskId}/input", requireWorker(handler.GetVerificationRuntimeInput))
	mux.Handle("POST /api/v1/internal/math-verification/tasks/{taskId}/complete", requireWorker(handler.CompleteVerificationRuntimeTask))
	mux.Handle("POST /api/v1/internal/math-verification/tasks/{taskId}/fail", requireWorker(handler.FailVerificationRuntimeTask))
}

type completeRuntimeRequest struct {
	LeaseToken string              `json:"lease_token"`
	DurationMS int                 `json:"duration_ms"`
	Artifact   CreateArtifactInput `json:"artifact"`
}

type completeVerificationRuntimeRequest struct {
	LeaseToken         string             `json:"lease_token"`
	DurationMS         int                `json:"duration_ms"`
	ArtifactID         string             `json:"artifact_id"`
	ArtifactVersion    int64              `json:"artifact_version"`
	CorrectionRevision int64              `json:"correction_revision"`
	Verifications      []MathVerification `json:"verifications"`
}

const runtimeSolutionBuilderVersion = "math-runtime-step-segmenter-v2"

func (h *Handler) GetRuntimeInput(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok || h.runtime == nil {
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil || task.QueueName != "math-understanding" || task.SourceType != "answer_segment" || task.SourceID == "" {
		httpx.Error(w, r, http.StatusNotFound, "math_runtime_task_not_found", "math understanding task was not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"task": map[string]any{
			"id": task.ID, "answer_segment_id": task.SourceID,
			"exam_question_snapshot_id": task.Payload["exam_question_snapshot_id"],
			"subject_code":              task.Payload["subject_code"], "region_kind": task.Payload["region_kind"],
			"input_hash": task.Payload["input_hash"],
		},
		"image_url": "/api/v1/internal/answer-segments/" + task.SourceID + "/image",
	})
}

func (h *Handler) CompleteRuntimeTask(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok || h.runtime == nil {
		return
	}
	var input completeRuntimeRequest
	if !decodeMathJSON(w, r, &input) {
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil || task.QueueName != "math-understanding" || task.SourceType != "answer_segment" {
		httpx.Error(w, r, http.StatusNotFound, "math_runtime_task_not_found", "math understanding task was not found")
		return
	}
	if strings.TrimSpace(input.LeaseToken) == "" || input.DurationMS < 0 || input.Artifact.AnswerSegmentID != task.SourceID ||
		input.Artifact.ExamQuestionSnapshotID != stringPayload(task.Payload, "exam_question_snapshot_id") ||
		input.Artifact.SubjectCode != stringPayload(task.Payload, "subject_code") ||
		input.Artifact.InputHash != stringPayload(task.Payload, "input_hash") {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_math_runtime_result", "math runtime result does not match its immutable task input")
		return
	}
	preparedArtifact, err := prepareRuntimeArtifact(input.Artifact)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	artifact, err := h.artifacts.CreateArtifact(r.Context(), user.TenantID, preparedArtifact)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	verificationTaskID := ""
	if len(artifact.Formulas) > 0 {
		verificationTask, taskErr := h.enqueueVerificationTask(r.Context(), user.ID, artifact, 0)
		if taskErr != nil {
			httpx.Error(w, r, http.StatusInternalServerError, "math_verification_enqueue_failed", "symbolic verification could not be scheduled")
			return
		}
		verificationTaskID = verificationTask.ID
	}
	_, err = h.runtime.Complete(r.Context(), user.TenantID, task.ID, workerruntime.CompleteInput{
		LeaseToken: input.LeaseToken, ResultSchemaVersion: "math-understanding-result-v2",
		Result: map[string]any{
			"artifact_id": artifact.ID, "artifact_version": artifact.Version, "input_hash": artifact.InputHash,
			"verification_task_id": verificationTaskID,
		},
		DurationMS: input.DurationMS,
	})
	if err != nil {
		httpx.Error(w, r, http.StatusConflict, "math_runtime_completion_failed", "math runtime lease is no longer valid")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"artifact": artifact})
}

func (h *Handler) GetVerificationRuntimeInput(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok || h.runtime == nil {
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil || !isVerificationTask(task) {
		httpx.Error(w, r, http.StatusNotFound, "math_verification_task_not_found", "math verification task was not found")
		return
	}
	artifact, contract, correctionRevision, err := h.verificationTaskContract(r.Context(), user.TenantID, task)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"task": map[string]any{
			"id": task.ID, "artifact_id": artifact.ID, "artifact_version": artifact.Version,
			"input_hash": artifact.InputHash, "correction_revision": correctionRevision,
		},
		"contract": contract,
	})
}

func (h *Handler) CompleteVerificationRuntimeTask(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok || h.runtime == nil {
		return
	}
	var input completeVerificationRuntimeRequest
	if !decodeMathJSON(w, r, &input) {
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil || !isVerificationTask(task) {
		httpx.Error(w, r, http.StatusNotFound, "math_verification_task_not_found", "math verification task was not found")
		return
	}
	artifact, contract, correctionRevision, err := h.verificationTaskContract(r.Context(), user.TenantID, task)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	if strings.TrimSpace(input.LeaseToken) == "" || input.DurationMS < 0 || input.ArtifactID != artifact.ID ||
		input.ArtifactVersion != artifact.Version || input.CorrectionRevision != correctionRevision {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_math_verification_result", "math verification result does not match its immutable task input")
		return
	}
	verifiedContract, quality, err := applySymbolicVerifications(contract, input.Verifications, correctionRevision)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	derived, superseded, err := h.completeVerifiedRuntime(r.Context(), task, artifact, correctionRevision, verifiedContract, quality, input.LeaseToken, input.DurationMS)
	if err != nil {
		httpx.Error(w, r, http.StatusConflict, "math_verification_completion_failed", "math verification lease is no longer valid")
		return
	}
	if superseded {
		httpx.JSON(w, http.StatusOK, map[string]any{"superseded": true})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"artifact": derived, "superseded": false})
}

func (h *Handler) FailVerificationRuntimeTask(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok || h.runtime == nil {
		return
	}
	var input workerruntime.FailInput
	if !decodeMathJSON(w, r, &input) {
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil || !isVerificationTask(task) {
		httpx.Error(w, r, http.StatusNotFound, "math_verification_task_not_found", "math verification task was not found")
		return
	}
	updated, err := h.runtime.Fail(r.Context(), user.TenantID, task.ID, input)
	if err != nil {
		httpx.Error(w, r, http.StatusConflict, "math_verification_failure_rejected", "math verification lease is no longer valid")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"task": updated})
}

func (h *Handler) verificationTaskContract(ctx context.Context, tenantID string, task workerruntime.Task) (Artifact, CreateArtifactInput, int64, error) {
	artifact, err := h.artifacts.GetArtifact(ctx, tenantID, task.SourceID)
	if err != nil {
		return Artifact{}, CreateArtifactInput{}, 0, err
	}
	version, versionOK := int64Payload(task.Payload, "artifact_version")
	revision, revisionOK := int64Payload(task.Payload, "correction_revision")
	if !versionOK || !revisionOK || revision < 0 || stringPayload(task.Payload, "artifact_id") != artifact.ID || version != artifact.Version ||
		stringPayload(task.Payload, "input_hash") != artifact.InputHash {
		return Artifact{}, CreateArtifactInput{}, 0, ErrInvalidInput
	}
	contract := cloneInput(artifact.CreateArtifactInput)
	if revision > 0 {
		correction, correctionErr := h.corrections.GetCorrection(ctx, tenantID, artifact.ID, revision)
		if correctionErr != nil {
			return Artifact{}, CreateArtifactInput{}, 0, correctionErr
		}
		if !correctionMatchesArtifact(correction.CorrectedContract, artifact) {
			return Artifact{}, CreateArtifactInput{}, 0, ErrInvalidInput
		}
		contract = cloneInput(correction.CorrectedContract)
	}
	return artifact, contract, revision, nil
}

func (h *Handler) enqueueVerificationTask(ctx context.Context, actorID string, artifact Artifact, correctionRevision int64) (workerruntime.Task, error) {
	key := "math-verification:" + artifact.ID + ":" + strconv.FormatInt(artifact.Version, 10) + ":" + strconv.FormatInt(correctionRevision, 10)
	return h.runtime.CreateTask(ctx, artifact.TenantID, actorID, workerruntime.CreateTaskInput{
		TaskType: "evidence_verify", QueueName: "math-verification", SourceType: "math_understanding_artifact", SourceID: artifact.ID,
		Priority: 75, PayloadSchemaVersion: "math-verification-task-v1", IdempotencyKey: key, DedupeKey: key,
		MaxAttempts: 3, RetryBackoffSeconds: 30,
		Payload: map[string]any{
			"artifact_id": artifact.ID, "artifact_version": artifact.Version, "input_hash": artifact.InputHash,
			"correction_revision": correctionRevision,
		},
	})
}

func isVerificationTask(task workerruntime.Task) bool {
	return task.QueueName == "math-verification" && task.TaskType == "evidence_verify" &&
		task.SourceType == "math_understanding_artifact" && task.SourceID != "" &&
		task.PayloadSchemaVersion == "math-verification-task-v1"
}

func int64Payload(payload map[string]any, key string) (int64, bool) {
	switch value := payload[key].(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		converted := int64(value)
		return converted, float64(converted) == value
	case json.Number:
		converted, err := value.Int64()
		return converted, err == nil
	default:
		return 0, false
	}
}

func applySymbolicVerifications(contract CreateArtifactInput, checks []MathVerification, correctionRevision int64) (CreateArtifactInput, map[string]any, error) {
	if len(checks) == 0 || len(checks) > 2048 {
		return CreateArtifactInput{}, nil, ErrInvalidInput
	}
	contract = cloneInput(contract)
	retained := make([]MathVerification, 0, len(contract.Verifications)+len(checks))
	knownIDs := map[string]bool{}
	criticalConfidence := contract.SolutionGraph.OverallConfidence
	requiresReview := false
	for _, check := range contract.Verifications {
		if check.Kind == "syntax" {
			retained = append(retained, check)
			knownIDs[check.ID] = true
			if check.Status == "uncertain" || check.Status == "contradicted" {
				criticalConfidence = min(criticalConfidence, check.Confidence)
				if check.Status == "contradicted" {
					criticalConfidence = 0
				}
				requiresReview = true
			}
		}
	}
	statusCounts := map[string]int{"verified": 0, "contradicted": 0, "uncertain": 0, "not_applicable": 0}
	activeFormulas := map[string]bool{}
	for _, step := range contract.SolutionGraph.Steps {
		for _, id := range step.FormulaIDs {
			activeFormulas[id] = true
		}
	}
	checkedFormulas := map[string]bool{}
	for _, check := range checks {
		if check.Engine != "sympy" || !set("equivalence", "constraint")[check.Kind] || knownIDs[check.ID] || !validSymbolicBinding(contract, check) {
			return CreateArtifactInput{}, nil, ErrInvalidInput
		}
		knownIDs[check.ID] = true
		if activeFormulas[check.FormulaID] {
			checkedFormulas[check.FormulaID] = true
		}
		if fromID, ok := check.Details["from_formula_id"].(string); ok {
			if activeFormulas[fromID] {
				checkedFormulas[fromID] = true
			}
		}
		statusCounts[check.Status]++
		if check.Status == "contradicted" {
			criticalConfidence = 0
			requiresReview = true
		} else if check.Status == "uncertain" {
			if check.Confidence < criticalConfidence {
				criticalConfidence = check.Confidence
			}
			requiresReview = true
		}
		retained = append(retained, check)
	}
	contract.Verifications = retained
	contract.SolutionGraph.RequiresHumanReview = contract.SolutionGraph.RequiresHumanReview || requiresReview
	if err := ValidateCreateArtifact(contract); err != nil {
		return CreateArtifactInput{}, nil, err
	}
	quality := map[string]any{
		"verification_count": len(checks), "verified_count": statusCounts["verified"],
		"contradicted_count": statusCounts["contradicted"], "uncertain_count": statusCounts["uncertain"],
		"not_applicable_count": statusCounts["not_applicable"], "critical_confidence": criticalConfidence,
		"engine": "sympy", "correction_revision": correctionRevision,
		"formula_count": len(contract.Formulas), "active_formula_count": len(activeFormulas), "checked_formula_count": len(checkedFormulas),
		"unchecked_formula_count": len(activeFormulas) - len(checkedFormulas),
	}
	return contract, quality, nil
}

func validSymbolicBinding(contract CreateArtifactInput, check MathVerification) bool {
	steps := map[string]SolutionStep{}
	for _, step := range contract.SolutionGraph.Steps {
		steps[step.ID] = step
	}
	contains := func(ids []string, id string) bool {
		for _, candidate := range ids {
			if candidate == id {
				return true
			}
		}
		return false
	}
	if check.StepID != "" && !contains(steps[check.StepID].FormulaIDs, check.FormulaID) {
		return false
	}
	if check.Kind != "equivalence" {
		return true
	}
	fromStep, _ := check.Details["from_step_id"].(string)
	fromFormula, _ := check.Details["from_formula_id"].(string)
	if fromStep == "" || fromFormula == "" || check.StepID == "" || check.FormulaID == "" || !contains(steps[fromStep].FormulaIDs, fromFormula) {
		return false
	}
	for _, edge := range contract.SolutionGraph.Edges {
		if edge.Kind == "derives" && edge.FromStepID == fromStep && edge.ToStepID == check.StepID {
			return true
		}
	}
	return false
}

func prepareRuntimeArtifact(input CreateArtifactInput) (CreateArtifactInput, error) {
	relations := input.Relations
	if len(relations) == 0 {
		relations = BuildSpatialRelations(input.Blocks, SpatialGraphOptions{})
	}

	formulaModelVersion := "text-only"
	if len(input.Formulas) > 0 {
		formulaModelVersion = strings.TrimSpace(input.SolutionGraph.FormulaModelVersion)
		for index := 0; formulaModelVersion == "" && index < len(input.Formulas); index++ {
			formulaModelVersion = strings.TrimSpace(input.Formulas[index].RecognitionVersion)
		}
	}

	graph, err := BuildSolutionGraph(SolutionBuildInput{
		AnswerSegmentID:     input.AnswerSegmentID,
		Blocks:              input.Blocks,
		Formulas:            input.Formulas,
		Relations:           relations,
		Verifications:       input.Verifications,
		BuilderVersion:      runtimeSolutionBuilderVersion,
		FormulaModelVersion: formulaModelVersion,
	})
	if err != nil {
		return CreateArtifactInput{}, err
	}
	graph.RequiresHumanReview = graph.RequiresHumanReview || input.SolutionGraph.RequiresHumanReview

	input.Relations = relations
	input.SolutionGraph = graph
	return input, nil
}

func stringPayload(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

type evaluatePilotGateRequest struct {
	SubjectCode  string       `json:"subject_code"`
	BenchmarkRef string       `json:"benchmark_ref"`
	Metrics      PilotMetrics `json:"metrics"`
	Policy       PilotPolicy  `json:"policy"`
}

func (h *Handler) EvaluatePilotGate(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	var input evaluatePilotGateRequest
	if !decodeMathJSON(w, r, &input) {
		return
	}
	item, err := h.pilotGates.CreatePilotGate(r.Context(), user.TenantID, input.SubjectCode, input.BenchmarkRef, input.Metrics, input.Policy, user.ID)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	h.auditEvent(r, user, "math_understanding.pilot_gate_evaluated", item.ID, map[string]any{"subject_code": item.SubjectCode, "passed": item.Decision.Passed, "scope": item.Decision.Scope})
	httpx.JSON(w, http.StatusCreated, map[string]any{"evaluation": item})
}

func (h *Handler) ListPilotGates(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	subject := r.URL.Query().Get("subject")
	if subject != "" && !set("mathematics", "physics", "chemistry")[subject] {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_subject", "math pilot gates require an enabled subject")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.pilotGates.ListPilotGates(r.Context(), user.TenantID, subject, limit)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"evaluations": items})
}

func (h *Handler) GetLatest(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	item, err := h.artifacts.GetLatestArtifact(r.Context(), user.TenantID, r.PathValue("segmentId"))
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	if !h.canAccess(r, user, item.AnswerSegmentID) {
		httpx.Error(w, r, http.StatusForbidden, "math_evidence_forbidden", "math evidence is limited to assigned review work")
		return
	}
	effective, err := resolveEffectiveContract(r.Context(), h.corrections, user.TenantID, item)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	corrections, err := h.corrections.ListCorrections(r.Context(), user.TenantID, item.ID)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"artifact":            item,
		"effective_artifact":  effective.EffectiveContract,
		"correction_revision": effective.CorrectionRevision,
		"corrected":           effective.Corrected,
		"corrections":         corrections,
	})
}
func (h *Handler) ListCorrections(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	artifact, err := h.artifacts.GetArtifact(r.Context(), user.TenantID, r.PathValue("artifactId"))
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	if !h.canAccess(r, user, artifact.AnswerSegmentID) {
		httpx.Error(w, r, http.StatusForbidden, "math_evidence_forbidden", "math evidence is limited to assigned review work")
		return
	}
	items, err := h.corrections.ListCorrections(r.Context(), user.TenantID, artifact.ID)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"corrections": items})
}
func (h *Handler) CreateCorrection(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	artifact, err := h.artifacts.GetArtifact(r.Context(), user.TenantID, r.PathValue("artifactId"))
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	if !h.canAccess(r, user, artifact.AnswerSegmentID) {
		httpx.Error(w, r, http.StatusForbidden, "math_correction_forbidden", "only the assigned reviewer or quality manager may correct math evidence")
		return
	}
	var input CreateCorrectionInput
	if !decodeMathJSON(w, r, &input) {
		return
	}
	item, err := h.corrections.CreateCorrection(r.Context(), user.TenantID, artifact.ID, user.ID, input)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	verificationStatus, verificationTaskID := "not_required", ""
	if len(item.CorrectedContract.Formulas) > 0 {
		verificationStatus = "unavailable"
		if h.runtime != nil {
			task, taskErr := h.enqueueVerificationTask(r.Context(), user.ID, artifact, item.Revision)
			verificationStatus = "failed"
			if taskErr == nil {
				verificationStatus, verificationTaskID = "queued", task.ID
			}
		}
	}
	h.auditEvent(r, user, "math_understanding.corrected", artifact.ID, map[string]any{"revision": item.Revision, "operation_count": len(item.Operations)})
	// Saving the append-only correction and scheduling verification are distinct
	// outcomes. Never report "running" when the queue is absent or failed.
	response := map[string]any{"correction": item, "verification_status": verificationStatus}
	if verificationTaskID != "" {
		response["verification_task_id"] = verificationTaskID
	}
	httpx.JSON(w, http.StatusCreated, response)
}
func (h *Handler) ExportTraining(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	subject := r.URL.Query().Get("subject")
	if !set("mathematics", "physics", "chemistry")[subject] {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_subject", "math training export requires an enabled subject")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.corrections.ExportCorrections(r.Context(), user.TenantID, subject, limit)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	safe := make([]map[string]any, 0, len(items))
	for _, item := range items {
		contract := cloneInput(item.CorrectedContract)
		contract.AnswerSegmentID = ""
		contract.ExamQuestionSnapshotID = ""
		digest := sha256.Sum256([]byte(user.TenantID + ":" + item.AnswerSegmentID))
		safe = append(safe, map[string]any{"sample_key": hex.EncodeToString(digest[:]), "subject_code": subject, "operations": item.Operations, "corrected_contract": contract, "created_at": item.CreatedAt})
	}
	h.auditEvent(r, user, "math_understanding.training_exported", subject, map[string]any{"count": len(safe)})
	httpx.JSON(w, http.StatusOK, map[string]any{"samples": safe})
}

func (h *Handler) canAccess(r *http.Request, user auth.User, segmentID string) bool {
	for _, permission := range user.Permissions {
		if permission == "review:manage" {
			return true
		}
	}
	if h.reviews == nil {
		return false
	}
	allowed, err := h.reviews.HasActiveAssignment(r.Context(), user.TenantID, user.ID, segmentID)
	return err == nil && allowed
}
func (h *Handler) auditEvent(r *http.Request, user auth.User, action, targetID string, after map[string]any) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "math_understanding", TargetID: targetID, AfterValue: after, Reason: action, IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}
func currentMathUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return user, ok
}
func decodeMathJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must use the documented math evidence contract")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}
func writeMathError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "math_understanding_not_found", "math understanding artifact was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_math_understanding", "math understanding input is invalid")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "math_understanding_revision_conflict", "math understanding changed; reload before correcting")
	case errors.Is(err, ErrInvalidPilotGate):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_math_pilot_gate", "math pilot gate evidence or policy is invalid")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "math_understanding_failed", "math understanding operation failed")
	}
}

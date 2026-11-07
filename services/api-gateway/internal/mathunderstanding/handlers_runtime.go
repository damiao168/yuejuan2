package mathunderstanding

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

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
	// 输入响应与图片下载之间仍可能发生重裁切；这里提前拒绝陈旧任务，worker还须校验下载字节，完成事务再次核对。
	if h.crops != nil {
		hash, err := h.crops.LoadActiveMathCropHash(r.Context(), user.TenantID, task.SourceID)
		if err != nil {
			writeMathError(w, r, err)
			return
		}
		if !sameCropHash(stringPayload(task.Payload, "input_hash"), hash) {
			writeMathError(w, r, ErrRevisionConflict)
			return
		}
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
	// 服务端重新构建解题步骤和连线；worker 已给出的空间关系会保留，缺失时才根据几何位置补算。
	preparedArtifact, err := prepareRuntimeArtifact(input.Artifact)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	artifact, verificationTaskID, err := h.completeRuntimeArtifact(r.Context(), recognitionRuntimeCompletion{
		Task: task, ActorID: user.ID, Input: preparedArtifact, LeaseToken: input.LeaseToken, DurationMS: input.DurationMS,
	})
	if err != nil {
		if errors.Is(err, workerruntime.ErrConflict) || errors.Is(err, workerruntime.ErrLeaseExpired) || errors.Is(err, workerruntime.ErrLeaseMismatch) || errors.Is(err, workerruntime.ErrInvalidTransition) {
			httpx.Error(w, r, http.StatusConflict, "math_runtime_completion_failed", "math runtime lease or accepted result no longer matches")
		} else {
			writeMathError(w, r, err)
		}
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"artifact": artifact, "verification_task_id": verificationTaskID})
}

func (h *Handler) GetVerificationRuntimeInput(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok || h.runtime == nil {
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil || !isVerificationTask(task) {
		httpx.Error(w, r, http.StatusNotFound, "math_verification_task_not_found", verificationTaskNotFound)
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
		httpx.Error(w, r, http.StatusNotFound, "math_verification_task_not_found", verificationTaskNotFound)
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
	derived, superseded, err := h.completeVerifiedRuntime(r.Context(), verifiedRuntimeCompletion{
		Task: task, Parent: artifact, CorrectionRevision: correctionRevision, Contract: verifiedContract,
		Quality: quality, LeaseToken: input.LeaseToken, DurationMS: input.DurationMS,
	})
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
		httpx.Error(w, r, http.StatusNotFound, "math_verification_task_not_found", verificationTaskNotFound)
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
	// 任务绑定的是创建时的修订号，不能改读“最新修订”；否则相同任务重试会得到不同输入。
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

func stringPayload(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

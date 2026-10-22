package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func imageInputProtocol(value Context) string {
	if value.InputVersionID != "" {
		return ImageProtocolVersion
	}
	return ""
}

// ScoringFailure 只向协调器暴露受控错误码；配置故障不能被当成可无限重发的临时错误。
func (e *GradingAgentError) ScoringFailure() (string, bool) { return e.Code, e.Retryable }

// ObserveStudentAnswers 只消费学生图和成员题干。标准答案、量规和参考解法在此入口没有传输字段。
func (h *Handler) ObserveStudentAnswers(ctx context.Context, tenantID string, input grading.ScoringObservationInput) (json.RawMessage, error) {
	store, ok := h.store.(*PostgresStore)
	if !ok {
		return nil, ErrInvalidInput
	}
	plan, err := store.loadFrozenImageAIPlan(ctx, tenantID, input.ScoringRunID)
	if err != nil {
		return nil, err
	}
	adapter, model, err := h.imageAdapterForPlan(ctx, tenantID, plan)
	if err != nil {
		return nil, err
	}
	members, err := store.imageObservationMembers(ctx, tenantID, input)
	if err != nil {
		return nil, err
	}
	crop, err := h.activeCrops.ResolveForObservation(ctx, tenantID, input.SegmentID, members[0].QuestionID)
	if err != nil {
		return nil, err
	}
	if crop.SHA256 != input.CropSHA256 {
		return nil, ErrWorkerResultVersionConflict
	}
	request, err := imageRequest(AdapterInput{RequestID: input.RequestID, InputVersionID: input.InputVersionID, CropSHA256: input.CropSHA256, ActiveCrop: &crop})
	if err != nil {
		return nil, err
	}
	request.Members, request.ModelPolicy = members, adapter.Policy()
	request.ManagedModel = model
	request.Rubric, request.FrozenReference = nil, nil
	response, err := adapter.imageRequest(ctx, "/grading/observe-image-v3", request)
	if err != nil {
		return nil, err
	}
	if response.Observation.ModelBinding != imageModelBinding(model) {
		return nil, ErrInvalidModelOutput
	}
	// 同一观察保留全量来源，投影只改变成员形状，不能把整区文本复制成每题答案。
	projected := []map[string]any{}
	evidence := []map[string]any{}
	segmentByQuestion := map[string]string{}
	for _, member := range input.Members {
		segmentByQuestion[member.QuestionID] = member.SegmentID
	}
	for _, member := range response.Observation.Members {
		ids := []string{}
		for _, item := range member.Evidence {
			ids = append(ids, item.EvidenceID)
			evidence = append(evidence, map[string]any{"id": item.EvidenceID, "question_id": member.QuestionID, "text": item.Text, "bbox": item.BBox, "crop_sha256": input.CropSHA256})
		}
		projected = append(projected, map[string]any{"answer_segment_id": segmentByQuestion[member.QuestionID], "question_id": member.QuestionID, "presence": member.Presence, "association": member.Association, "confidence": member.Confidence, "answer": member.Answer, "evidence_ids": ids})
	}
	return json.Marshal(map[string]any{"input_version_id": input.InputVersionID, "members": projected, "observations": evidence, "student_observation": response.Observation, "model_invoked": response.Telemetry.Attempts > 0, "telemetry": response.Telemetry})
}

func (s *PostgresStore) imageObservationMembers(ctx context.Context, tenantID string, input grading.ScoringObservationInput) ([]ObservationMember, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT iv.members_json FROM answer_input_version iv JOIN answer_segment seg ON seg.tenant_id=iv.tenant_id AND seg.id=iv.answer_segment_id AND seg.deleted_at IS NULL AND seg.crop_sha256=iv.crop_sha256 AND seg.crop_file_asset_id=iv.crop_file_asset_id WHERE iv.tenant_id=$1::uuid AND iv.id=$2::uuid AND iv.answer_segment_id=$3::uuid AND iv.crop_sha256=$4`, tenantID, input.InputVersionID, input.SegmentID, input.CropSHA256).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkerResultVersionConflict
	}
	if err != nil {
		return nil, err
	}
	var frozen []grading.ObservationMember
	if json.Unmarshal(raw, &frozen) != nil || len(frozen) == 0 || len(frozen) != len(input.Members) {
		return nil, ErrInvalidInput
	}
	expected := map[string]string{}
	for _, member := range input.Members {
		if expected[member.QuestionID] != "" {
			return nil, ErrInvalidInput
		}
		expected[member.QuestionID] = member.SegmentID
	}
	result := []ObservationMember{}
	for _, member := range frozen {
		if expected[member.QuestionID] != member.SegmentID {
			return nil, ErrInvalidInput
		}
		delete(expected, member.QuestionID)
		var stem string
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(rule_snapshot->'questions'->($3::text)->'question'->>'stem','') FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, input.ScoringRunID, member.QuestionID).Scan(&stem); err != nil {
			return nil, err
		}
		result = append(result, ObservationMember{QuestionID: member.QuestionID, QuestionNo: member.QuestionNo, QuestionType: member.QuestionType, QuestionText: stem})
	}
	return result, nil
}

func (s *PostgresStore) loadFrozenImageContext(ctx context.Context, tenantID string, input grading.ScoringAIInput) (Context, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT sr.rule_snapshot->'questions'->(item.question_id::text) FROM scoring_run_item item JOIN scoring_run sr ON sr.tenant_id=item.tenant_id AND sr.id=item.scoring_run_id AND sr.deleted_at IS NULL WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND item.answer_segment_id=$3::uuid AND item.input_version_id=$4::uuid AND item.assessment_snapshot_id=$5::uuid AND item.processing_status NOT IN ('superseded','cancelled')`, tenantID, input.ScoringRunID, input.SegmentID, input.InputVersionID, input.AssessmentSnapshotID).Scan(&raw)
	if err != nil {
		return Context{}, err
	}
	var frozen struct {
		Question   paper.Question                  `json:"question"`
		Rubric     *paper.Rubric                   `json:"rubric"`
		Assessment assessment.ExamQuestionSnapshot `json:"assessment_snapshot"`
		AnswerKey  *paper.AnswerKey                `json:"answer_key"`
		Hash       string                          `json:"hash"`
	}
	if json.Unmarshal(raw, &frozen) != nil || frozen.Question.ID == "" || frozen.Assessment.ID != input.AssessmentSnapshotID {
		return Context{}, ErrWorkerResultVersionConflict
	}
	if rubric, ok := rubricFromAssessmentSnapshot(frozen.Assessment.RubricSnapshot, frozen.Question.ID); ok {
		frozen.Rubric = &rubric
	}
	if frozen.Rubric == nil || len(frozen.Rubric.Points) == 0 {
		return Context{}, ErrRubricMissing
	}
	value := Context{SegmentID: input.SegmentID, Question: frozen.Question, Rubric: *frozen.Rubric, AssessmentSnapshot: frozen.Assessment, Subject: string(frozen.Assessment.SubjectCode), GradeLevel: string(frozen.Assessment.EducationStage), AnswerImageRef: map[string]any{"answer_segment_id": input.SegmentID, "crop_sha256": input.CropSHA256}}
	value.AIPlan, err = s.loadFrozenImageAIPlan(ctx, tenantID, input.ScoringRunID)
	if err != nil {
		return Context{}, err
	}
	value.AIPlan = imagePlanForQuestion(value.AIPlan, value.Question.QuestionType)
	if frozen.AnswerKey != nil {
		value.ReferenceContext = &GradingReferenceContext{Source: "scoring_run.rule_snapshot", SnapshotHash: frozen.Hash, StandardAnswer: frozen.AnswerKey.StandardAnswer, EquivalentAnswers: frozen.AnswerKey.EquivalentAnswers}
	}
	return value, nil
}

// ScheduleScoringItem 在一个事务里冻结图像上下文、创建运行和 runtime；浏览器关闭不影响接续。
// 本轮只使用原图，不能把队列期间变化的 OCR 正文拼入已冻结的输入身份。
func (h *Handler) ScheduleScoringItem(ctx context.Context, tenantID, actorID string, input grading.ScoringAIInput) (string, string, error) {
	store, ok := h.store.(*PostgresStore)
	if !ok {
		return "", "", ErrInvalidInput
	}
	value, err := store.loadFrozenImageContext(ctx, tenantID, input)
	if err != nil {
		return "", "", err
	}
	governed, model, err := h.imageAdapterForPlan(ctx, tenantID, value.AIPlan)
	if err != nil {
		return "", "", err
	}
	if value.AssessmentSnapshot.ID != input.AssessmentSnapshotID || requestedScoringMode(value) != assessment.ScoringAIAssist {
		return "", "", ErrWorkerResultVersionConflict
	}
	if len(value.Rubric.Deductions) > 0 {
		return "", "", &GradingAgentError{Code: "image_rubric_deductions_unsupported"}
	}
	if value.ReferenceMaterials != nil && value.ReferenceMaterials.Mode == "reference_images" {
		return "", "", &GradingAgentError{Code: "reference_material_image_v3_not_supported"}
	}
	var observed struct {
		StudentObservation StudentObservation `json:"student_observation"`
	}
	if json.Unmarshal(input.Observation, &observed) != nil {
		return "", "", ErrInvalidInput
	}
	if observed.StudentObservation.ModelBinding != imageModelBinding(model) {
		return "", "", ErrWorkerResultVersionConflict
	}
	// 分组观察在该题评分时只传本题证据；其它题的可见步骤不能替本题得分。
	selected := []ObservedMember{}
	for _, member := range observed.StudentObservation.Members {
		if member.QuestionID == value.Question.ID {
			selected = append(selected, member)
		}
	}
	observed.StudentObservation.Members = selected
	member := ObservationMember{QuestionID: value.Question.ID, QuestionNo: value.Question.QuestionNo, QuestionType: value.Question.QuestionType, QuestionText: value.Question.Stem}
	if err := ValidateStudentObservation(observed.StudentObservation, input.InputVersionID, input.CropSHA256, []ObservationMember{member}); err != nil {
		return "", "", err
	}
	value.InputVersionID, value.CropSHA256, value.AnswerVersion = input.InputVersionID, input.CropSHA256, input.InputVersionID
	value.AnswerText, value.OCRConfidence, value.MathEvidence = "", nil, nil
	value.Observation = &observed.StudentObservation
	return store.scheduleImageRun(ctx, tenantID, actorID, input, value, governed.Policy())
}

func (s *PostgresStore) loadFrozenImageAIPlan(ctx context.Context, tenantID, scoringRunID string) (*grading.GradingAIPlan, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(rule_snapshot->'ai_plan','null'::jsonb) FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, scoringRunID).Scan(&raw); err != nil {
		return nil, err
	}
	var plan *grading.GradingAIPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func (s *PostgresStore) scheduleImageRun(ctx context.Context, tenantID, actorID string, input grading.ScoringAIInput, value Context, policy ModelPolicy) (string, string, error) {
	if input.IdempotencyKey == "" || ValidatePolicy(policy) != nil {
		return "", "", ErrInvalidInput
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var runStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, input.ScoringRunID).Scan(&runStatus); err != nil {
		return "", "", err
	}
	if runStatus == "cancelling" || runStatus == "cancelled" {
		return "", "", ErrBatchCancelled
	}
	var itemID string
	// 锁当前计划成员，输入变化或 superseded 后不能以旧回调创建收费任务。
	err = tx.QueryRowContext(ctx, `SELECT item.id::text FROM scoring_run_item item JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.id=item.answer_segment_id AND seg.crop_sha256=iv.crop_sha256 AND seg.crop_file_asset_id=iv.crop_file_asset_id AND seg.deleted_at IS NULL WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND item.answer_segment_id=$3::uuid AND item.input_version_id=$4::uuid AND item.assessment_snapshot_id=$5::uuid AND iv.crop_sha256=$6 AND item.processing_status NOT IN ('superseded','cancelled') FOR UPDATE OF item`, tenantID, input.ScoringRunID, input.SegmentID, input.InputVersionID, input.AssessmentSnapshotID, input.CropSHA256).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrWorkerResultVersionConflict
	}
	if err != nil {
		return "", "", err
	}
	// 图像输入用同一个 UUID 绑定新旧版本字段；旧 answer_version 是 text，须先固定参数类型再转文本。
	_, err = tx.ExecContext(ctx, `INSERT INTO subjective_grading_run(tenant_id,answer_segment_id,answer_version,question_id,exam_question_snapshot_id,rubric_version,model_version,prompt_version,min_confidence,request_id,status,attempt_count,input_version_id,input_mode,input_protocol,image_context_json) VALUES($1::uuid,$2::uuid,$3::uuid::text,$4::uuid,$5::uuid,$6,$7,$8,$9,$10,'queued',0,$3::uuid,'image','grading-image-v3',$11::jsonb) ON CONFLICT(tenant_id,request_id) DO NOTHING`, tenantID, value.SegmentID, value.InputVersionID, value.Question.ID, value.AssessmentSnapshot.ID, value.Rubric.Version, policy.ModelVersion, policy.PromptVersion, policy.MinConfidence, input.IdempotencyKey, raw)
	if err != nil {
		return "", "", err
	}
	var runID string
	var same bool
	err = tx.QueryRowContext(ctx, `SELECT id::text,input_version_id=$3::uuid AND exam_question_snapshot_id=$4::uuid AND image_context_json=$5::jsonb AND model_version=$6 AND prompt_version=$7 FROM subjective_grading_run WHERE tenant_id=$1::uuid AND request_id=$2`, tenantID, input.IdempotencyKey, input.InputVersionID, input.AssessmentSnapshotID, raw, policy.ModelVersion, policy.PromptVersion).Scan(&runID, &same)
	if err != nil {
		return "", "", err
	}
	if !same {
		return "", "", ErrIdempotencyConflict
	}
	task, err := workerruntime.CreateTaskInTx(ctx, tx, tenantID, actorID, workerruntime.CreateTaskInput{TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: runID, PayloadSchemaVersion: "subjective-grade-v1", Payload: map[string]any{"run_id": runID, "input_version_id": input.InputVersionID, "answer_segment_id": value.SegmentID}, IdempotencyKey: input.IdempotencyKey, DedupeKey: "image-subjective:" + runID, MaxAttempts: 3, RetryBackoffSeconds: 30})
	if err != nil {
		return "", "", err
	}
	_, err = tx.ExecContext(ctx, `UPDATE scoring_run_item SET subjective_run_id=$3::uuid,runtime_task_id=$4::uuid,stage='score',processing_status='queued',reason_code='ai_suggestion_pending',next_action='wait',revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND subjective_run_id IS NULL`, tenantID, itemID, runID, task.ID)
	if err != nil {
		return "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return runID, task.ID, nil
}

// LoadRunContext 对图像运行读取入队时的冻结内容，并再次核对当前裁图及活跃计划。
// 返回的观察只是模型观察，不会升级为旧数学 v2 的 verified artifact。
func (s *PostgresStore) LoadRunContext(ctx context.Context, tenantID string, run GradingRun) (Context, error) {
	var raw []byte
	var current bool
	err := s.db.QueryRowContext(ctx, `SELECT r.image_context_json,EXISTS(SELECT 1 FROM scoring_run_item item JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.id=item.answer_segment_id AND seg.crop_sha256=iv.crop_sha256 AND seg.crop_file_asset_id=iv.crop_file_asset_id AND seg.deleted_at IS NULL WHERE item.tenant_id=r.tenant_id AND item.answer_segment_id=r.answer_segment_id AND item.input_version_id=r.input_version_id AND item.assessment_snapshot_id=r.exam_question_snapshot_id AND item.processing_status NOT IN ('superseded','cancelled')) FROM subjective_grading_run r WHERE r.tenant_id=$1::uuid AND r.id=$2::uuid`, tenantID, run.ID).Scan(&raw, &current)
	if err != nil {
		return Context{}, err
	}
	if len(raw) == 0 {
		return s.LoadContext(ctx, tenantID, run.AnswerSegmentID)
	}
	current, err = imageBindingCurrentInTx(ctx, s.db, tenantID, run)
	if err != nil {
		return Context{}, err
	}
	if !current {
		return Context{}, ErrWorkerResultVersionConflict
	}
	var value Context
	if json.Unmarshal(raw, &value) != nil || value.InputVersionID != run.AnswerVersion || value.AssessmentSnapshot.ID != run.ExamQuestionSnapshotID {
		return Context{}, ErrWorkerResultVersionConflict
	}
	if value.Observation == nil {
		return Context{}, ErrWorkerResultVersionConflict
	}
	if err := ValidateStudentObservation(*value.Observation, value.InputVersionID, value.CropSHA256, []ObservationMember{{QuestionID: run.QuestionID}}); err != nil {
		return Context{}, err
	}
	return value, nil
}

func imageBindingCurrentInTx(ctx context.Context, tx rowQueryer, tenantID string, run GradingRun) (bool, error) {
	var current bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM subjective_grading_run r JOIN scoring_run_item item ON item.tenant_id=r.tenant_id AND item.subjective_run_id=r.id JOIN scoring_run sr ON sr.tenant_id=item.tenant_id AND sr.id=item.scoring_run_id AND sr.status NOT IN ('cancelling','cancelled') AND sr.deleted_at IS NULL JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id AND iv.id=r.input_version_id JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.id=item.answer_segment_id AND seg.crop_sha256=iv.crop_sha256 AND seg.crop_file_asset_id=iv.crop_file_asset_id AND seg.deleted_at IS NULL JOIN LATERAL(SELECT id FROM exam_question_snapshot WHERE tenant_id=r.tenant_id AND question_id=r.question_id ORDER BY snapshot_version DESC,id DESC LIMIT 1) snapshot ON snapshot.id=r.exam_question_snapshot_id WHERE r.tenant_id=$1::uuid AND r.id=$2::uuid AND item.assessment_snapshot_id=r.exam_question_snapshot_id AND item.processing_status NOT IN ('superseded','cancelled'))`, tenantID, run.ID).Scan(&current)
	return current, err
}

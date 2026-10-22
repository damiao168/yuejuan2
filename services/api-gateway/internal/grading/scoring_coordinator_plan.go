package grading

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

const scoringCoordinatorQueue = "scoring-coordinator"

// ObservationMember 只含成员身份和题干提示；不允许在观察协议里混入标准答案或量规。
type ObservationMember struct {
	SegmentID    string `json:"answer_segment_id"`
	QuestionID   string `json:"question_id"`
	QuestionNo   string `json:"question_no"`
	QuestionType string `json:"question_type"`
	QuestionText string `json:"question_text,omitempty"`
}

// ScoringObservationInput 绑定已冻结的学生输入，跨题共区必须一次返回独立成员映射。
type ScoringObservationInput struct {
	ScoringRunID   string
	RequestID      string
	InputVersionID string
	SegmentID      string
	CropSHA256     string
	Members        []ObservationMember
}

// ScoringAIInput 接续已持久化的观察，评分规则和量规从本轮快照取值。
type ScoringAIInput struct {
	ScoringRunID, SegmentID, InputVersionID, CropSHA256, AssessmentSnapshotID, IdempotencyKey string
	Observation                                                                               json.RawMessage
}

// ScoringImageExecutor 由装配层接入主观服务，避免 grading 与 subjective 相互导入。
// 观察请求不含标准答案；调度的 AI 任务必须使用本轮冻结快照。
type ScoringImageExecutor interface {
	Observe(context.Context, string, ScoringObservationInput) (json.RawMessage, error)
	Schedule(context.Context, string, string, ScoringAIInput) (string, string, error)
}

func scoringDigest(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// 计划摘要同时绑定题目、配置和当前裁图；GET 只读，启动命令再在考试锁内比较。
func scoringPlanHash(ctx context.Context, q scoringReadinessQueryer, tenantID, examID string) (string, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT jsonb_build_object('questions',COALESCE((SELECT jsonb_agg(jsonb_build_object('question',to_jsonb(q),'snapshot',to_jsonb(eqs),'answer_key',to_jsonb(ak),'rule',to_jsonb(sr),'draft_rules',(SELECT jsonb_agg(to_jsonb(draft) ORDER BY draft.version) FROM scoring_rule draft WHERE draft.tenant_id=q.tenant_id AND draft.question_id=q.id AND draft.status='draft' AND draft.deleted_at IS NULL)) ORDER BY q.id) FROM question q LEFT JOIN exam_question_snapshot eqs ON eqs.tenant_id=q.tenant_id AND eqs.question_id=q.id AND eqs.exam_id=q.exam_id LEFT JOIN LATERAL(SELECT * FROM question_answer_key WHERE tenant_id=q.tenant_id AND question_id=q.id AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1)ak ON true LEFT JOIN LATERAL(SELECT * FROM scoring_rule WHERE tenant_id=q.tenant_id AND question_id=q.id AND status='published' AND deleted_at IS NULL ORDER BY version DESC LIMIT 1)sr ON true WHERE q.tenant_id=$1::uuid AND q.exam_id=$2::uuid AND q.deleted_at IS NULL),'[]'::jsonb),'inputs',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',seg.id,'crop',seg.crop_file_asset_id,'hash',seg.crop_sha256,'registration',seg.registration_run_id,'template',seg.template_content_hash,'answer',(SELECT to_jsonb(answer) FROM answer_segment_answer answer WHERE answer.tenant_id=seg.tenant_id AND answer.answer_segment_id=seg.id AND answer.deleted_at IS NULL ORDER BY answer.created_at DESC,answer.id DESC LIMIT 1)) ORDER BY seg.id) FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.deleted_at IS NULL),'[]'::jsonb))`, tenantID, examID).Scan(&raw)
	if err != nil {
		return "", err
	}
	var modelPlan []byte
	// 预览摘要同时绑定本场确认方案和连接版本；确认后换模型须重新核对路线。
	err = q.QueryRowContext(ctx, `SELECT jsonb_build_object('exam_plan',(SELECT to_jsonb(p) FROM exam_grading_ai_plan p WHERE p.tenant_id=$1::uuid AND p.exam_id=$2::uuid),'school_selection',(SELECT to_jsonb(s) FROM tenant_grading_model_selection s WHERE s.tenant_id=$1::uuid),'models',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'updated_at',m.updated_at,'status',m.status) ORDER BY m.id) FROM managed_model_api_config m WHERE m.tenant_id=$1::uuid AND m.deleted_at IS NULL),'[]'::jsonb))`, tenantID, examID).Scan(&modelPlan)
	if err != nil {
		return "", err
	}
	return scoringDigest(map[string]json.RawMessage{"rules_and_inputs": raw, "ai_plan": modelPlan}), nil
}

// 同一共区的成员共享不可变输入和一次观察；题目计划仍分别保存，局部归属失败不会污染其他题。
func prepareScoringPlanTx(ctx context.Context, tx *sql.Tx, tenantID, runID string, segments []scoringRunSegment) ([]scoringRunSegment, error) {
	groups := map[string][]int{}
	for i, seg := range segments {
		var area map[string]any
		if err := json.Unmarshal(seg.area, &area); err != nil {
			return nil, err
		}
		scope, _ := area["answer_scope"].(string)
		key := seg.id
		if scope == "grading_group" {
			key = seg.submissionID + ":" + seg.cropID + ":" + seg.cropHash
		}
		groups[key] = append(groups[key], i)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		indexes := groups[key]
		sort.Slice(indexes, func(i, j int) bool { return segments[indexes[i]].id < segments[indexes[j]].id })
		first := segments[indexes[0]]
		scope := "question"
		if len(indexes) > 1 {
			scope = "grading_group"
		} else {
			var area map[string]any
			_ = json.Unmarshal(first.area, &area)
			if area["answer_scope"] == "grading_group" {
				scope = "grading_group"
			}
		}
		members := []ObservationMember{}
		for _, i := range indexes {
			seg := segments[i]
			members = append(members, ObservationMember{SegmentID: seg.id, QuestionID: seg.questionID, QuestionNo: seg.no, QuestionType: seg.kind})
		}
		membersRaw, _ := json.Marshal(members)
		answerVersions := []string{}
		for _, i := range indexes {
			answerVersions = append(answerVersions, segments[i].answerID)
		}
		var registrationID string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(registration_run_id::text,'') FROM answer_segment WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, first.id).Scan(&registrationID); err != nil {
			return nil, err
		}
		hash := scoringDigest([]any{tenantID, first.submissionID, first.cropID, first.cropHash, first.templateHash, registrationID, scope, members, answerVersions})
		var inputID string
		// 输入不可变，因此冲突时只读取既有身份，不能使用 ON CONFLICT UPDATE 穿过不可变触发器。
		if err := tx.QueryRowContext(ctx, `INSERT INTO answer_input_version(tenant_id,answer_segment_id,submission_id,crop_file_asset_id,crop_sha256,registration_run_id,template_content_hash,answer_scope,members_json,input_hash) SELECT $1::uuid,seg.id,seg.submission_id,seg.crop_file_asset_id,seg.crop_sha256,seg.registration_run_id,seg.template_content_hash,$3,$4::jsonb,$5 FROM answer_segment seg WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid ON CONFLICT(tenant_id,input_hash) DO NOTHING RETURNING id::text`, tenantID, first.id, scope, membersRaw, hash).Scan(&inputID); err != nil {
			if err != sql.ErrNoRows {
				return nil, err
			}
			if err = tx.QueryRowContext(ctx, `SELECT id::text FROM answer_input_version WHERE tenant_id=$1::uuid AND input_hash=$2`, tenantID, hash).Scan(&inputID); err != nil {
				return nil, err
			}
		}
		for _, i := range indexes {
			seg := &segments[i]
			seg.inputVersionID = inputID
			if err := tx.QueryRowContext(ctx, `INSERT INTO scoring_run_item(tenant_id,scoring_run_id,answer_segment_id,question_id,input_version_id,assessment_snapshot_id,frozen_rule_id,route) SELECT $1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,(sr.rule_snapshot->'questions'->($4::text)->'assessment_snapshot'->>'id')::uuid,NULLIF($6,'')::uuid,'prepare' FROM scoring_run sr WHERE sr.tenant_id=$1::uuid AND sr.id=$2::uuid RETURNING id::text,assessment_snapshot_id::text`, tenantID, runID, seg.id, seg.questionID, inputID, seg.ruleID).Scan(&seg.planItemID, &seg.snapshotID); err != nil {
				return nil, fmt.Errorf("create scoring item: %w", err)
			}
		}
	}
	return segments, nil
}

func queueScoringObservationTx(ctx context.Context, tx *sql.Tx, tenantID, runID, actorID string, seg scoringRunSegment, route string) error {
	key := "scoring-observation:" + runID + ":" + seg.inputVersionID
	task, err := workerruntime.CreateTaskInTx(ctx, tx, tenantID, actorID, workerruntime.CreateTaskInput{TaskType: "ocr", QueueName: scoringCoordinatorQueue, SourceType: "answer_input_version", SourceID: seg.inputVersionID, PayloadSchemaVersion: "scoring-observation-task-v1", Payload: map[string]any{"scoring_run_id": runID, "input_version_id": seg.inputVersionID}, IdempotencyKey: key, DedupeKey: key, MaxAttempts: 3, RetryBackoffSeconds: 30})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE scoring_run_item SET route=$3,stage='observe',processing_status='queued',observation_task_id=$4::uuid,runtime_task_id=$4::uuid,reason_code='observation_pending',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, seg.planItemID, route, task.ID)
	return err
}

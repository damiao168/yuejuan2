// Package taskmaterials 读取任务指定快照下的题目材料，不负责授予对象访问权限。
package taskmaterials

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
)

type QuestionReader interface {
	QuestionForSnapshot(context.Context, string, string, string, string) (paper.Question, error)
}
type PostgresReader struct{ DB *sql.DB }

// QuestionForSnapshot 由已校验任务/管理对象的调用方使用。题干在考试冻结后不可改；
// 参考答案和解析只取该快照之前的版本，不用当前题库的新标准替换历史任务依据。
func (s PostgresReader) QuestionForSnapshot(ctx context.Context, tenantID, examID, questionID, snapshotID string) (paper.Question, error) {
	var raw []byte
	err := s.DB.QueryRowContext(ctx, `SELECT to_jsonb(q) || jsonb_build_object('answer_key',to_jsonb(k),'solution',to_jsonb(sol),'rubric',snap.rubric_snapshot)
FROM exam_question_snapshot snap
JOIN question q ON q.tenant_id=snap.tenant_id AND q.id=snap.question_id AND q.exam_id=snap.exam_id AND q.deleted_at IS NULL
LEFT JOIN LATERAL (SELECT id,question_id,answer_version,standard_answer,equivalent_answers,tolerance FROM question_answer_key
 WHERE tenant_id=q.tenant_id AND question_id=q.id AND deleted_at IS NULL AND created_at<=snap.created_at ORDER BY created_at DESC,id DESC LIMIT 1) k ON true
LEFT JOIN LATERAL (SELECT id,question_id,solution_version,raw_text,steps,source_refs,verification_status FROM question_solution
 WHERE tenant_id=q.tenant_id AND question_id=q.id AND deleted_at IS NULL AND created_at<=snap.created_at ORDER BY created_at DESC,id DESC LIMIT 1) sol ON true
WHERE snap.tenant_id=$1::uuid AND snap.exam_id=$2::uuid AND snap.question_id=$3::uuid AND snap.id=$4::uuid`, tenantID, examID, questionID, snapshotID).Scan(&raw)
	if err != nil {
		return paper.Question{}, err
	}
	var question paper.Question
	if err = json.Unmarshal(raw, &question); err != nil {
		return paper.Question{}, err
	}
	return question, nil
}

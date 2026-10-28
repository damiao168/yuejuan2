package backmark

import (
	"context"
	"encoding/json"
)

// AssignedQueueSummaries 沿用本人尚未完成的分派范围；名称仅辅助定位，领取时仍重验分派与修订。
func (s *PostgresStore) AssignedQueueSummaries(ctx context.Context, tenantID, graderID string, ids []string) (map[string]QueueSummary, error) {
	raw, _ := json.Marshal(ids)
	rows, err := s.db.QueryContext(ctx, `SELECT i.id::text,e.name,e.subject,t.question_no,t.anonymous_code
FROM backmark_item i
JOIN review_task t ON t.tenant_id=i.tenant_id AND t.id=i.review_task_id
JOIN exam e ON e.tenant_id=t.tenant_id AND e.id=t.exam_id
WHERE i.tenant_id=$1::uuid AND i.reassigned_to=$2::uuid AND i.status IN ('pending','in_progress')
AND i.id::text IN (SELECT jsonb_array_elements_text($3::jsonb))`, tenantID, graderID, raw)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]QueueSummary{}
	for rows.Next() {
		var id string
		var value QueueSummary
		if err := rows.Scan(&id, &value.ExamName, &value.Subject, &value.QuestionNo, &value.AnonymousCode); err != nil {
			return nil, err
		}
		out[id] = value
	}
	return out, rows.Err()
}

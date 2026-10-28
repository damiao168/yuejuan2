package regrade

import (
	"context"
	"encoding/json"
)

// AssignedQueueSummaries 仅查询本人当前可领取的独立任务；不连接原成绩或原阅卷记录。
func (s *PostgresStore) AssignedQueueSummaries(ctx context.Context, tenantID, reviewerID string, ids []string) (map[string]QueueSummary, error) {
	raw, _ := json.Marshal(ids)
	// 独立任务使用自身派生的匿名码，不借用原评队列或考号来关联学生身份。
	rows, err := s.db.QueryContext(ctx, `SELECT i.id::text,e.name,e.subject,q.question_no,'复评-'||substr(md5(i.id::text),1,12)
FROM regrade_item i
JOIN regrade_job j ON j.tenant_id=i.tenant_id AND j.id=i.job_id
JOIN exam e ON e.tenant_id=j.tenant_id AND e.id=j.exam_id
JOIN question q ON q.tenant_id=j.tenant_id AND q.exam_id=j.exam_id AND q.id=j.question_id
WHERE i.tenant_id=$1::uuid AND i.assigned_to=$2::uuid AND j.status IN ('running','diff_review')
AND (i.status='pending' OR (i.status='claimed' AND i.claimed_by=$2::uuid))
AND i.id::text IN (SELECT jsonb_array_elements_text($3::jsonb))`, tenantID, reviewerID, raw)
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

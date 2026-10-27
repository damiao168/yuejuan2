package answergroup

import (
	"context"
	"database/sql"
)

// 分组表示只对构建时的成员答案有效；任一成员缺失、失去资格或内容改变，都要求重建并重新抽样。
func (s *MemoryStore) groupSourcesCurrent(group Group, source []SourceAnswer) bool {
	return groupSourcesCurrent(group, eligibleSourceAnswers(source, s.policy), s.provider)
}

func groupSourcesCurrent(group Group, source []SourceAnswer, provider RepresentationProvider) bool {
	current := make(map[string]SourceAnswer, len(source))
	for _, answer := range source {
		if answer.SnapshotID == group.ExamQuestionSnapshotID {
			current[answer.SegmentID] = answer
		}
	}
	for _, member := range group.Members {
		answer, ok := current[member.SegmentID]
		if !ok || answer.SubmissionID != member.SubmissionID || provider.Represent(answer.AnswerText).Hash != member.RepresentationHash {
			return false
		}
	}
	return len(group.Members) == group.MemberCount
}

func (s *PostgresStore) validateConfirmationSources(ctx context.Context, tx *sql.Tx, tenantID string, group Group) error {
	// 锁顺序与答案写入触发器一致。回读必须是锁后的另一条语句，才能看到等待期间已提交的新答案。
	rows, err := tx.QueryContext(ctx, `SELECT segment.id FROM answer_group_member member
JOIN answer_segment segment ON segment.tenant_id=member.tenant_id AND segment.id=member.segment_id AND segment.deleted_at IS NULL
WHERE member.tenant_id=$1::uuid AND member.group_id=$2::uuid
ORDER BY segment.id FOR UPDATE OF segment`, tenantID, group.ID)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		count++
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if count != group.MemberCount {
		return ErrRevisionConflict
	}
	answers, err := s.sourceAnswersWith(ctx, tx, tenantID, group.ExamID, group.QuestionID)
	if err != nil {
		return err
	}
	if !groupSourcesCurrent(group, eligibleSourceAnswers(answers, s.policy), s.provider) {
		return ErrRevisionConflict
	}
	return nil
}

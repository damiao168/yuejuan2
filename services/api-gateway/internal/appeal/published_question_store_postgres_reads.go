package appeal

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"errors"
)

func (s *PublishedQuestionAppealPostgresStore) GetPublishedQuestionAppeal(ctx context.Context, tenantID, appealID string) (PublishedQuestionAppeal, error) {
	item, err := scanPublishedQuestionAppeal(s.db.QueryRowContext(ctx, `
SELECT `+publishedQuestionAppealColumns("")+`
FROM question_appeal
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, appealID))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishedQuestionAppeal{}, ErrNotFound
	}
	return item, err
}

func (s *PublishedQuestionAppealPostgresStore) GetPublishedQuestionAppealContext(ctx context.Context, tenantID, appealID string) (PublishedQuestionAppealContext, error) {
	appeal, err := s.GetPublishedQuestionAppeal(ctx, tenantID, appealID)
	if err != nil {
		return PublishedQuestionAppealContext{}, err
	}
	context := PublishedQuestionAppealContext{Appeal: appeal, RubricSnapshot: map[string]any{}, ReleaseHistory: []QuestionAppealReleaseVersion{}}
	var rubricJSON []byte
	err = s.db.QueryRowContext(ctx, `
SELECT segment.id::text, grade.source, snapshot.rubric_snapshot_json
FROM final_grade grade
JOIN answer_segment segment
  ON segment.tenant_id = grade.tenant_id AND segment.id = grade.answer_segment_id AND segment.deleted_at IS NULL
LEFT JOIN exam_question_snapshot snapshot
  ON snapshot.tenant_id = grade.tenant_id AND snapshot.id = grade.exam_question_snapshot_id
WHERE grade.tenant_id = $1 AND grade.id = $2::uuid AND grade.deleted_at IS NULL
`, tenantID, appeal.SourceFinalGradeID).Scan(&context.AnswerSegmentID, &context.SourceType, &rubricJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PublishedQuestionAppealContext{}, err
	}
	if err == nil {
		context.AnswerImageReady = context.AnswerSegmentID != ""
		if len(rubricJSON) > 0 {
			if json.Unmarshal(rubricJSON, &context.RubricSnapshot) != nil {
				return PublishedQuestionAppealContext{}, ErrInvalidInput
			}
		}
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, version, source, status, reason, published_at
FROM score_release
WHERE tenant_id = $1 AND exam_id = $2::uuid
ORDER BY version DESC
`, tenantID, appeal.ExamID)
	if err != nil {
		return PublishedQuestionAppealContext{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var version QuestionAppealReleaseVersion
		var publishedAt sql.NullTime
		if err := rows.Scan(&version.ID, &version.Version, &version.Source, &version.Status, &version.Reason, &publishedAt); err != nil {
			return PublishedQuestionAppealContext{}, err
		}
		if publishedAt.Valid {
			value := publishedAt.Time.UTC()
			version.PublishedAt = &value
		}
		context.ReleaseHistory = append(context.ReleaseHistory, version)
	}
	if err := rows.Err(); err != nil {
		return PublishedQuestionAppealContext{}, err
	}
	// 先释放历史列表连接，再用结案共用规则核对候选。列表展示的旧检查结果不能替代提交时的事务校验。
	if err := rows.Close(); err != nil {
		return PublishedQuestionAppealContext{}, err
	}
	if appeal.Status == QuestionAppealUpheldPendingRegrade && appeal.RegradeJobID != "" {
		for index := range context.ReleaseHistory {
			version := &context.ReleaseHistory[index]
			if version.Status != "published" || version.Source != "regrade" || version.Version <= appeal.SourceReleaseVersion {
				continue
			}
			eligible, err := successorPublishedReleaseExists(ctx, s.db, tenantID, appeal, version.ID)
			if err != nil {
				return PublishedQuestionAppealContext{}, err
			}
			version.EligibleForResolution = eligible
		}
	}
	return context, nil
}

func (s *PublishedQuestionAppealPostgresStore) ListPublishedQuestionAppeals(ctx context.Context, tenantID string, filter QuestionAppealFilter) ([]PublishedQuestionAppeal, error) {
	accessArgs, err := filter.Access.SQLArgs(tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+publishedQuestionAppealColumns("")+`
FROM question_appeal
WHERE tenant_id = $1
  AND ($2 = '' OR exam_id::text = $2)
  AND ($3 = '' OR student_id::text = $3)
  AND ($4 = '' OR assigned_to::text = $4)
  AND ($5 = '' OR status = $5)
  AND `+auth.ExamCollectionPredicate("question_appeal.exam_id", 6)+`
ORDER BY created_at DESC, id DESC
`, append([]any{tenantID, filter.ExamID, filter.StudentID, filter.AssignedTo, filter.Status}, accessArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PublishedQuestionAppeal{}
	for rows.Next() {
		item, err := scanPublishedQuestionAppeal(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PublishedQuestionAppealPostgresStore) ListPublishedQuestionAppealEvents(ctx context.Context, tenantID, appealID string) ([]PublishedQuestionAppealEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, appeal_id::text, event_type, actor_id::text, payload, created_at
FROM question_appeal_event
WHERE tenant_id = $1 AND appeal_id = $2::uuid
ORDER BY created_at, id
`, tenantID, appealID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []PublishedQuestionAppealEvent{}
	for rows.Next() {
		var item PublishedQuestionAppealEvent
		var payload []byte
		if err := rows.Scan(&item.ID, &item.AppealID, &item.Type, &item.ActorID, &payload, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			return nil, err
		}
		events = append(events, item)
	}
	return events, rows.Err()
}

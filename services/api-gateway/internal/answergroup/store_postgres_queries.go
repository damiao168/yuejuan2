package answergroup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func (s *PostgresStore) List(ctx context.Context, tenantID, examID, questionID string) ([]Group, error) {
	if s.db == nil || !validIDs(tenantID, examID, questionID) {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text FROM answer_group WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid ORDER BY member_count DESC,id`, tenantID, examID, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return s.groupsByIDs(ctx, s.db, tenantID, ids)
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, groupID string) (Group, error) {
	if s.db == nil || !validIDs(tenantID, groupID) {
		return Group{}, ErrInvalidInput
	}
	return s.loadGroup(ctx, s.db, tenantID, groupID, false)
}

func (s *PostgresStore) Metrics(ctx context.Context, tenantID, examID, questionID string) (Metrics, error) {
	if s.db == nil || !validIDs(tenantID, examID, questionID) {
		return Metrics{}, ErrInvalidInput
	}
	var metric Metrics
	var weighted sql.NullFloat64
	var decided, rolledBack int
	err := s.db.QueryRowContext(ctx, `
SELECT count(grouping.id),COALESCE(sum(grouping.member_count),0),
       sum(grouping.homogeneity * grouping.member_count),
       count(*) FILTER (WHERE grouping.status IN ('confirmed','rolled_back')),
       count(*) FILTER (WHERE grouping.status='rolled_back'),
       COALESCE(sum(GREATEST(grouping.member_count-COALESCE(decision.sample_size,grouping.member_count),0))
         FILTER (WHERE grouping.status='confirmed'),0)
FROM answer_group grouping
LEFT JOIN answer_group_decision decision ON decision.tenant_id=grouping.tenant_id AND decision.group_id=grouping.id
WHERE grouping.tenant_id=$1::uuid AND grouping.exam_id=$2::uuid AND grouping.question_id=$3::uuid`, tenantID, examID, questionID).Scan(
		&metric.GroupCount, &metric.MemberCount, &weighted, &decided, &rolledBack, &metric.HumanActionsSaved,
	)
	if err != nil {
		return Metrics{}, err
	}
	if metric.MemberCount > 0 && weighted.Valid {
		metric.GroupHomogeneity = roundScore(weighted.Float64 / float64(metric.MemberCount))
	}
	if decided > 0 {
		metric.BatchOverrideRate = roundScore(float64(rolledBack) / float64(decided))
	}
	// A07 不从任意回滚原因推断审计后错误。后续审计抽样记录明确的评测数和错误数之前，该比率保持未知。
	metric.PostAuditEvidenceStatus = "not_collected"
	return metric, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *PostgresStore) sourceAnswers(ctx context.Context, tenantID, examID, questionID string) ([]SourceAnswer, error) {
	return s.sourceAnswersWith(ctx, s.db, tenantID, examID, questionID)
}

func (s *PostgresStore) sourceAnswersWith(ctx context.Context, db queryer, tenantID, examID, questionID string) ([]SourceAnswer, error) {
	rows, err := db.QueryContext(ctx, `
SELECT submission.id::text,segment.id::text,snapshot.id::text,snapshot.archetype_code,
       answer.answer_text,answer.source,answer.confidence
FROM answer_segment segment
JOIN submission ON submission.tenant_id=segment.tenant_id AND submission.id=segment.submission_id AND submission.deleted_at IS NULL
JOIN question ON question.tenant_id=segment.tenant_id AND question.id=segment.question_id AND question.exam_id=$2::uuid AND question.deleted_at IS NULL
JOIN exam_question_snapshot snapshot ON snapshot.tenant_id=question.tenant_id AND snapshot.exam_id=question.exam_id AND snapshot.question_id=question.id
JOIN LATERAL (
  SELECT answer_text,source,confidence FROM answer_segment_answer
  WHERE tenant_id=segment.tenant_id AND answer_segment_id=segment.id AND deleted_at IS NULL
  ORDER BY created_at DESC,id DESC LIMIT 1
) answer ON true
WHERE segment.tenant_id=$1::uuid AND segment.question_id=$3::uuid AND segment.deleted_at IS NULL
ORDER BY segment.id`, tenantID, examID, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceAnswer{}
	for rows.Next() {
		var item SourceAnswer
		var confidence sql.NullFloat64
		if err = rows.Scan(&item.SubmissionID, &item.SegmentID, &item.SnapshotID, &item.ArchetypeCode, &item.AnswerText, &item.Source, &confidence); err != nil {
			return nil, err
		}
		if confidence.Valid {
			value := confidence.Float64
			item.Confidence = &value
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) loadGroup(ctx context.Context, db queryer, tenantID, groupID string, lock bool) (Group, error) {
	query := `
SELECT id::text,tenant_id::text,exam_id::text,question_id::text,exam_question_snapshot_id::text,
       algorithm_version,representation_version,member_count,representative_submission_id::text,
       homogeneity,status,minimum_sample,created_at,updated_at
FROM answer_group WHERE tenant_id=$1::uuid AND id=$2::uuid`
	if lock {
		query += ` FOR UPDATE`
	}
	var group Group
	err := db.QueryRowContext(ctx, query, tenantID, groupID).Scan(
		&group.ID, &group.TenantID, &group.ExamID, &group.QuestionID, &group.ExamQuestionSnapshotID,
		&group.AlgorithmVersion, &group.RepresentationVersion, &group.MemberCount, &group.RepresentativeSubmissionID,
		&group.Homogeneity, &group.Status, &group.MinimumSample, &group.CreatedAt, &group.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT member.submission_id::text,member.segment_id::text,member.similarity,member.outlier_score,
       member.is_representative,member.is_boundary,member.is_outlier,member.representation_hash,
       COALESCE(sample.outcome,''),COALESCE(sample.reviewed_by::text,''),sample.reviewed_at
FROM answer_group_member member
LEFT JOIN answer_group_sample_review sample ON sample.tenant_id=member.tenant_id AND sample.group_id=member.group_id AND sample.segment_id=member.segment_id
WHERE member.tenant_id=$1::uuid AND member.group_id=$2::uuid
ORDER BY member.is_representative DESC,member.is_boundary DESC,member.outlier_score DESC,member.segment_id`, tenantID, groupID)
	if err != nil {
		return Group{}, err
	}
	for rows.Next() {
		var member Member
		var sampledAt sql.NullTime
		if err = rows.Scan(&member.SubmissionID, &member.SegmentID, &member.Similarity, &member.OutlierScore,
			&member.Representative, &member.Boundary, &member.Outlier, &member.RepresentationHash,
			&member.SampleStatus, &member.SampledBy, &sampledAt); err != nil {
			rows.Close()
			return Group{}, err
		}
		if sampledAt.Valid {
			member.SampledAt = &sampledAt.Time
		}
		group.Members = append(group.Members, member)
	}
	if err = rows.Close(); err != nil {
		return Group{}, err
	}
	var decision Decision
	var scoreRaw, rubricRaw []byte
	var confirmedBy, rollbackRef, rolledBackBy sql.NullString
	var confirmedAt, rolledBackAt sql.NullTime
	err = db.QueryRowContext(ctx, `
SELECT id::text,score_candidate_json,rubric_selection_json,sample_size,min_sample,
       confirmed_by::text,confirmed_at,revision,rollback_reference::text,rolled_back_by::text,rolled_back_at,rollback_reason
FROM answer_group_decision WHERE tenant_id=$1::uuid AND group_id=$2::uuid`, tenantID, groupID).Scan(
		&decision.ID, &scoreRaw, &rubricRaw, &decision.SampleSize, &decision.MinimumSample,
		&confirmedBy, &confirmedAt, &decision.Revision, &rollbackRef, &rolledBackBy, &rolledBackAt, &decision.RollbackReason,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Group{}, err
	}
	if err == nil {
		if err = json.Unmarshal(scoreRaw, &decision.ScoreCandidate); err != nil {
			return Group{}, fmt.Errorf("decode answer group score candidate: %w", err)
		}
		if err = json.Unmarshal(rubricRaw, &decision.RubricSelection); err != nil {
			return Group{}, fmt.Errorf("decode answer group rubric selection: %w", err)
		}
		decision.ConfirmedBy, decision.RollbackReference, decision.RolledBackBy = confirmedBy.String, rollbackRef.String, rolledBackBy.String
		if confirmedAt.Valid {
			decision.ConfirmedAt = &confirmedAt.Time
		}
		if rolledBackAt.Valid {
			decision.RolledBackAt = &rolledBackAt.Time
		}
		group.Decision = &decision
	}
	refreshReadiness(&group)
	return group, nil
}

func (s *PostgresStore) groupsByIDs(ctx context.Context, db queryer, tenantID string, ids []string) ([]Group, error) {
	out := make([]Group, 0, len(ids))
	for _, id := range ids {
		item, err := s.loadGroup(ctx, db, tenantID, id, false)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sortGroups(out)
	return out, nil
}

func (s *PostgresStore) listByBuildHash(ctx context.Context, db queryer, tenantID, examID, questionID, hash string) ([]Group, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text FROM answer_group WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid AND build_input_hash=$4 ORDER BY id`, tenantID, examID, questionID, hash)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return s.groupsByIDs(ctx, db, tenantID, ids)
}

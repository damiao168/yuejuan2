package answergroup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type PostgresStore struct {
	db       *sql.DB
	provider RepresentationProvider
	policy   Policy
}

func NewPostgresStore(db *sql.DB, provider RepresentationProvider, policy Policy) *PostgresStore {
	if provider == nil {
		provider = DeterministicTextProvider{}
	}
	if !policy.valid() {
		policy = DefaultPolicy()
	}
	return &PostgresStore{db: db, provider: provider, policy: policy}
}

func (s *PostgresStore) Build(ctx context.Context, tenantID, examID, questionID, actorID string, input BuildInput) ([]Group, error) {
	if s.db == nil || !validIDs(tenantID, examID, questionID, actorID) {
		return nil, ErrInvalidInput
	}
	algorithm := strings.TrimSpace(input.AlgorithmVersion)
	if algorithm == "" {
		algorithm = DefaultAlgorithmVersion
	}
	if algorithm != DefaultAlgorithmVersion {
		return nil, ErrInvalidInput
	}
	answers, err := s.sourceAnswers(ctx, tenantID, examID, questionID)
	if err != nil {
		return nil, err
	}
	eligibleAnswers := make([]SourceAnswer, 0, len(answers))
	for _, answer := range answers {
		if eligible(answer, s.policy) {
			eligibleAnswers = append(eligibleAnswers, answer)
		}
	}
	if len(eligibleAnswers) == 0 {
		return nil, ErrNoEligibleAnswers
	}
	archetype, snapshotID := eligibleAnswers[0].ArchetypeCode, eligibleAnswers[0].SnapshotID
	for _, answer := range eligibleAnswers {
		if answer.ArchetypeCode != archetype || answer.SnapshotID != snapshotID || snapshotID == "" {
			return nil, ErrInvalidInput
		}
	}
	inputHash := buildInputHash(eligibleAnswers, algorithm, s.provider.Version())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, tenantID+"/"+examID+"/"+questionID+"/"+inputHash); err != nil {
		return nil, err
	}
	if existing, existingErr := s.listByBuildHash(ctx, tx, tenantID, examID, questionID, inputHash); existingErr != nil {
		return nil, existingErr
	} else if len(existing) > 0 {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	}
	threshold := s.policy.ShortAnswerThreshold
	if archetype == ArchetypeExactText {
		threshold = s.policy.ExactTextThreshold
	}
	clusters := clusterAnswers(eligibleAnswers, s.provider, threshold)
	createdIDs := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		members, homogeneity := materializeMembers(cluster, s.policy.HighOutlierThreshold)
		groupID := uuid.NewString()
		representativeID := ""
		for _, member := range members {
			if member.Representative {
				representativeID = member.SubmissionID
				break
			}
		}
		minimum := minimumSample(len(members), homogeneity)
		if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_group(
  id,tenant_id,exam_id,question_id,exam_question_snapshot_id,build_input_hash,
  algorithm_version,representation_version,member_count,representative_submission_id,
  homogeneity,minimum_sample,status,created_by
) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10::uuid,$11,$12,'sampling',$13::uuid)`,
			groupID, tenantID, examID, questionID, snapshotID, inputHash, algorithm, s.provider.Version(), len(members), representativeID, homogeneity, minimum, actorID); err != nil {
			return nil, err
		}
		for _, member := range members {
			if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_group_member(
  tenant_id,group_id,submission_id,segment_id,representation_hash,similarity,outlier_score,
  is_representative,is_boundary,is_outlier
) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10)`,
				tenantID, groupID, member.SubmissionID, member.SegmentID, member.RepresentationHash,
				member.Similarity, member.OutlierScore, member.Representative, member.Boundary, member.Outlier); err != nil {
				return nil, err
			}
			if member.Outlier {
				payload, _ := json.Marshal(map[string]any{"reason": "high_outlier"})
				if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_group_automation_candidate(
  tenant_id,group_id,submission_id,segment_id,decision_revision,candidate_kind,
  score_candidate_json,rubric_selection_json,status,algorithm_version,created_by
) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,0,'individual_review',$5::jsonb,'{}'::jsonb,'manual_required',$6,$7::uuid)`,
					tenantID, groupID, member.SubmissionID, member.SegmentID, payload, algorithm, actorID); err != nil {
					return nil, err
				}
				if _, err = tx.ExecContext(ctx, `
INSERT INTO review_task(
  tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,
  anonymous_code,source,status,priority,reason_code,created_by
)
SELECT segment.tenant_id,question.exam_id,segment.question_id,segment.question_no,segment.id,segment.submission_id,
       COALESCE(NULLIF(submission.candidate_no,''),'ANON-' || left(segment.submission_id::text,8)),
       'answer_group_outlier','pending',100,'answer_group_high_outlier',$4::uuid
FROM answer_segment segment
JOIN question ON question.tenant_id=segment.tenant_id AND question.id=segment.question_id
JOIN submission ON submission.tenant_id=segment.tenant_id AND submission.id=segment.submission_id
WHERE segment.tenant_id=$1::uuid AND segment.id=$2::uuid AND segment.submission_id=$3::uuid
ON CONFLICT (tenant_id,answer_segment_id,source,grade_round)
WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL
  AND source <> 'ai_panel_disagreement'
DO NOTHING`, tenantID, member.SegmentID, member.SubmissionID, actorID); err != nil {
					return nil, err
				}
			}
		}
		createdIDs = append(createdIDs, groupID)
	}
	if err = insertOutbox(ctx, tx, tenantID, questionID, "answer_group.build_completed", map[string]any{
		"actor_id": actorID, "exam_id": examID, "question_id": questionID, "group_count": len(createdIDs),
		"eligible_count": len(eligibleAnswers), "algorithm_version": algorithm, "representation_version": s.provider.Version(),
	}); err != nil {
		return nil, err
	}
	created, err := s.groupsByIDs(ctx, tx, tenantID, createdIDs)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

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

func (s *PostgresStore) ReviewSample(ctx context.Context, tenantID, groupID, memberSegmentID, actorID string, input SampleReviewInput) (Group, error) {
	input.Outcome, input.Notes = strings.TrimSpace(input.Outcome), strings.TrimSpace(input.Notes)
	if s.db == nil || !validIDs(tenantID, groupID, memberSegmentID, actorID) ||
		(input.Outcome != SampleAccepted && input.Outcome != SampleRejected) || len(input.Notes) > 4000 {
		return Group{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer tx.Rollback()
	group, err := s.loadGroup(ctx, tx, tenantID, groupID, true)
	if err != nil {
		return Group{}, err
	}
	if group.Status == StatusConfirmed || group.Status == StatusRolledBack {
		return Group{}, ErrStateConflict
	}
	found := false
	for _, member := range group.Members {
		if member.SegmentID == memberSegmentID {
			found = true
			break
		}
	}
	if !found {
		return Group{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_group_sample_review(tenant_id,group_id,segment_id,outcome,notes,reviewed_by)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid)
ON CONFLICT(tenant_id,group_id,segment_id) DO UPDATE SET outcome=EXCLUDED.outcome,notes=EXCLUDED.notes,reviewed_by=EXCLUDED.reviewed_by,reviewed_at=now()`,
		tenantID, groupID, memberSegmentID, input.Outcome, input.Notes, actorID); err != nil {
		return Group{}, err
	}
	group, err = s.loadGroup(ctx, tx, tenantID, groupID, false)
	if err != nil {
		return Group{}, err
	}
	refreshReadiness(&group)
	if _, err = tx.ExecContext(ctx, `UPDATE answer_group SET status=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, groupID, group.Status); err != nil {
		return Group{}, err
	}
	if err = tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}

func (s *PostgresStore) PutDecision(ctx context.Context, tenantID, groupID, actorID string, input DecisionInput) (Group, error) {
	if s.db == nil || !validIDs(tenantID, groupID, actorID) || len(input.ScoreCandidate) == 0 || len(input.RubricSelection) == 0 || input.ExpectedRevision < 0 {
		return Group{}, ErrInvalidInput
	}
	scoreJSON, err := json.Marshal(input.ScoreCandidate)
	if err != nil {
		return Group{}, ErrInvalidInput
	}
	rubricJSON, err := json.Marshal(input.RubricSelection)
	if err != nil {
		return Group{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer tx.Rollback()
	group, err := s.loadGroup(ctx, tx, tenantID, groupID, true)
	if err != nil {
		return Group{}, err
	}
	if group.Status == StatusConfirmed || group.Status == StatusRolledBack {
		return Group{}, ErrStateConflict
	}
	if group.Decision == nil {
		if input.ExpectedRevision != 0 {
			return Group{}, ErrRevisionConflict
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO answer_group_decision(
  tenant_id,group_id,score_candidate_json,rubric_selection_json,sample_size,min_sample,created_by,updated_by
) VALUES($1::uuid,$2::uuid,$3::jsonb,$4::jsonb,$5,$6,$7::uuid,$7::uuid)`,
			tenantID, groupID, scoreJSON, rubricJSON, group.ReviewedSampleCount, group.MinimumSample, actorID)
	} else {
		if group.Decision.Revision != input.ExpectedRevision {
			return Group{}, ErrRevisionConflict
		}
		result, updateErr := tx.ExecContext(ctx, `
UPDATE answer_group_decision SET score_candidate_json=$4::jsonb,rubric_selection_json=$5::jsonb,
  sample_size=$6,revision=revision+1,updated_by=$7::uuid,updated_at=now()
WHERE tenant_id=$1::uuid AND group_id=$2::uuid AND revision=$3 AND confirmed_at IS NULL AND rolled_back_at IS NULL`,
			tenantID, groupID, input.ExpectedRevision, scoreJSON, rubricJSON, group.ReviewedSampleCount, actorID)
		if updateErr != nil {
			return Group{}, updateErr
		}
		if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
			return Group{}, ErrRevisionConflict
		}
	}
	if err != nil {
		return Group{}, err
	}
	group, err = s.loadGroup(ctx, tx, tenantID, groupID, false)
	if err != nil {
		return Group{}, err
	}
	refreshReadiness(&group)
	if _, err = tx.ExecContext(ctx, `UPDATE answer_group SET status=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, groupID, group.Status); err != nil {
		return Group{}, err
	}
	if err = tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}

func (s *PostgresStore) Confirm(ctx context.Context, tenantID, groupID, actorID string, input ConfirmInput) (Group, []Candidate, error) {
	if s.db == nil || !validIDs(tenantID, groupID, actorID) || input.ExpectedRevision <= 0 {
		return Group{}, nil, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, nil, err
	}
	defer tx.Rollback()
	group, err := s.loadGroup(ctx, tx, tenantID, groupID, true)
	if err != nil {
		return Group{}, nil, err
	}
	refreshReadiness(&group)
	if group.Decision == nil || group.Decision.Revision != input.ExpectedRevision {
		return Group{}, nil, ErrRevisionConflict
	}
	if !group.CanConfirm {
		return Group{}, nil, ErrSamplingIncomplete
	}
	now, rollback := time.Now().UTC(), uuid.NewString()
	created := make([]Candidate, 0, group.MemberCount)
	for _, member := range group.Members {
		candidateID := uuid.NewString()
		scoreJSON, _ := json.Marshal(group.Decision.ScoreCandidate)
		rubricJSON, _ := json.Marshal(group.Decision.RubricSelection)
		if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_group_automation_candidate(
  id,tenant_id,group_id,submission_id,segment_id,decision_revision,candidate_kind,
  score_candidate_json,rubric_selection_json,status,algorithm_version,rollback_reference,created_by
) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'group_score',$7::jsonb,$8::jsonb,'active',$9,$10::uuid,$11::uuid)`,
			candidateID, tenantID, groupID, member.SubmissionID, member.SegmentID, group.Decision.Revision,
			scoreJSON, rubricJSON, group.AlgorithmVersion, rollback, actorID); err != nil {
			return Group{}, nil, err
		}
		created = append(created, Candidate{ID: candidateID, GroupID: groupID, SubmissionID: member.SubmissionID,
			SegmentID: member.SegmentID, DecisionRevision: group.Decision.Revision, Kind: CandidateGroupScore,
			ScoreCandidate: cloneMap(group.Decision.ScoreCandidate), RubricSelection: cloneMap(group.Decision.RubricSelection),
			Status: CandidateActive, AlgorithmVersion: group.AlgorithmVersion, RollbackReference: rollback, CreatedAt: now})
	}
	result, err := tx.ExecContext(ctx, `
UPDATE answer_group_decision SET confirmed_by=$4::uuid,confirmed_at=$5,rollback_reference=$6::uuid,
  sample_size=$7,updated_by=$4::uuid,updated_at=$5
WHERE tenant_id=$1::uuid AND group_id=$2::uuid AND revision=$3 AND confirmed_at IS NULL`,
		tenantID, groupID, input.ExpectedRevision, actorID, now, rollback, group.ReviewedSampleCount)
	if err != nil {
		return Group{}, nil, err
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
		return Group{}, nil, ErrRevisionConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE answer_group SET status='confirmed',updated_at=$3 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, groupID, now); err != nil {
		return Group{}, nil, err
	}
	if err = insertOutbox(ctx, tx, tenantID, groupID, "answer_group.confirmed", map[string]any{
		"actor_id": actorID, "group_id": groupID, "affected_count": len(created),
		"algorithm_version": group.AlgorithmVersion, "representation_version": group.RepresentationVersion,
		"decision_revision": group.Decision.Revision, "rollback_reference": rollback,
	}); err != nil {
		return Group{}, nil, err
	}
	group, err = s.loadGroup(ctx, tx, tenantID, groupID, false)
	if err != nil {
		return Group{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return Group{}, nil, err
	}
	return group, created, nil
}

func (s *PostgresStore) Rollback(ctx context.Context, tenantID, groupID, actorID string, input RollbackInput) (Group, []Candidate, error) {
	input.RollbackReference, input.Reason = strings.TrimSpace(input.RollbackReference), strings.TrimSpace(input.Reason)
	if s.db == nil || !validIDs(tenantID, groupID, actorID, input.RollbackReference) || input.Reason == "" || len(input.Reason) > 4000 {
		return Group{}, nil, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, nil, err
	}
	defer tx.Rollback()
	group, err := s.loadGroup(ctx, tx, tenantID, groupID, true)
	if err != nil {
		return Group{}, nil, err
	}
	if group.Status != StatusConfirmed || group.Decision == nil || group.Decision.RollbackReference != input.RollbackReference {
		return Group{}, nil, ErrRollbackConflict
	}
	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `
UPDATE answer_group_automation_candidate SET status='rolled_back',rolled_back_at=$4
WHERE tenant_id=$1::uuid AND group_id=$2::uuid AND rollback_reference=$3::uuid AND status='active'
RETURNING id::text,submission_id::text,segment_id::text,decision_revision,candidate_kind,score_candidate_json,rubric_selection_json,algorithm_version,created_at`,
		tenantID, groupID, input.RollbackReference, now)
	if err != nil {
		return Group{}, nil, err
	}
	updated := []Candidate{}
	for rows.Next() {
		var candidate Candidate
		var scoreRaw, rubricRaw []byte
		if err = rows.Scan(&candidate.ID, &candidate.SubmissionID, &candidate.SegmentID, &candidate.DecisionRevision, &candidate.Kind, &scoreRaw, &rubricRaw, &candidate.AlgorithmVersion, &candidate.CreatedAt); err != nil {
			rows.Close()
			return Group{}, nil, err
		}
		candidate.GroupID, candidate.Status, candidate.RollbackReference = groupID, CandidateRolledBack, input.RollbackReference
		if err = json.Unmarshal(scoreRaw, &candidate.ScoreCandidate); err != nil {
			rows.Close()
			return Group{}, nil, err
		}
		if err = json.Unmarshal(rubricRaw, &candidate.RubricSelection); err != nil {
			rows.Close()
			return Group{}, nil, err
		}
		updated = append(updated, candidate)
	}
	if err = rows.Close(); err != nil {
		return Group{}, nil, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE answer_group_decision SET rolled_back_by=$4::uuid,rolled_back_at=$5,rollback_reason=$6,updated_by=$4::uuid,updated_at=$5
WHERE tenant_id=$1::uuid AND group_id=$2::uuid AND rollback_reference=$3::uuid`, tenantID, groupID, input.RollbackReference, actorID, now, input.Reason); err != nil {
		return Group{}, nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE answer_group SET status='rolled_back',updated_at=$3 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, groupID, now); err != nil {
		return Group{}, nil, err
	}
	if err = insertOutbox(ctx, tx, tenantID, groupID, "answer_group.rolled_back", map[string]any{
		"actor_id": actorID, "group_id": groupID, "affected_count": len(updated),
		"algorithm_version": group.AlgorithmVersion, "rollback_reference": input.RollbackReference, "reason": input.Reason,
	}); err != nil {
		return Group{}, nil, err
	}
	group, err = s.loadGroup(ctx, tx, tenantID, groupID, false)
	if err != nil {
		return Group{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return Group{}, nil, err
	}
	return group, updated, nil
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
	// A07 does not infer a post-audit error from an arbitrary rollback reason.
	// The rate stays structurally unknown until a later audit sampling story
	// records an explicit evaluated/error denominator.
	metric.PostAuditEvidenceStatus = "not_collected"
	return metric, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *PostgresStore) sourceAnswers(ctx context.Context, tenantID, examID, questionID string) ([]SourceAnswer, error) {
	rows, err := s.db.QueryContext(ctx, `
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

func insertOutbox(ctx context.Context, tx *sql.Tx, tenantID, aggregateID, eventType string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO event_outbox(tenant_id,aggregate_type,aggregate_id,event_type,payload) VALUES($1::uuid,'answer_group',$2::uuid,$3,$4::jsonb)`, tenantID, aggregateID, eventType, raw)
	return err
}

func validIDs(values ...string) bool {
	for _, value := range values {
		if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
			return false
		}
	}
	return true
}

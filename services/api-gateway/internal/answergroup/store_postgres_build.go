package answergroup

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"strings"
)

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
	eligibleAnswers := eligibleSourceAnswers(answers, s.policy)
	if len(eligibleAnswers) == 0 {
		return nil, ErrNoEligibleAnswers
	}
	archetype, snapshotID, err := groupBuildAnswerIdentity(eligibleAnswers)
	if err != nil {
		return nil, err
	}
	inputHash := buildInputHash(eligibleAnswers, algorithm, s.provider.Version())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// 同一租户题目的构建在事务内串行化，并在锁后再次查重，避免并发请求生成两批组。
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
	build := postgresGroupBuild{TenantID: tenantID, ExamID: examID, QuestionID: questionID, ActorID: actorID, SnapshotID: snapshotID, InputHash: inputHash, Algorithm: algorithm}
	createdIDs := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		groupID, err := s.createPostgresClusterGroup(ctx, tx, build, cluster)
		if err != nil {
			return nil, err
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

func groupBuildAnswerIdentity(answers []SourceAnswer) (string, string, error) {
	archetype, snapshotID := answers[0].ArchetypeCode, answers[0].SnapshotID
	for _, answer := range answers {
		if answer.ArchetypeCode != archetype || answer.SnapshotID != snapshotID || snapshotID == "" {
			return "", "", ErrInvalidInput
		}
	}
	return archetype, snapshotID, nil
}

type postgresGroupBuild struct {
	TenantID   string
	ExamID     string
	QuestionID string
	ActorID    string
	SnapshotID string
	InputHash  string
	Algorithm  string
}

func (s *PostgresStore) createPostgresClusterGroup(ctx context.Context, tx *sql.Tx, build postgresGroupBuild, cluster groupedAnswers) (string, error) {
	members, homogeneity := materializeMembers(cluster, s.policy.HighOutlierThreshold)
	groupID := uuid.NewString()
	representativeID := representativeSubmissionID(members)
	minimum := minimumSample(len(members), homogeneity)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO answer_group(
  id,tenant_id,exam_id,question_id,exam_question_snapshot_id,build_input_hash,
  algorithm_version,representation_version,member_count,representative_submission_id,
  homogeneity,minimum_sample,status,created_by
) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10::uuid,$11,$12,'sampling',$13::uuid)`,
		groupID, build.TenantID, build.ExamID, build.QuestionID, build.SnapshotID, build.InputHash, build.Algorithm, s.provider.Version(), len(members), representativeID, homogeneity, minimum, build.ActorID); err != nil {
		return "", err
	}
	if err := persistGroupMembers(ctx, tx, build.TenantID, groupID, build.Algorithm, build.ActorID, members); err != nil {
		return "", err
	}
	return groupID, nil
}

func representativeSubmissionID(members []Member) string {
	for _, member := range members {
		if member.Representative {
			return member.SubmissionID
		}
	}
	return ""
}

// 成员、离群候选和人工复核任务沿用构建事务；任一步失败均由调用方回滚整批组。
func persistGroupMembers(ctx context.Context, tx *sql.Tx, tenantID, groupID, algorithm, actorID string, members []Member) error {
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO answer_group_member(
  tenant_id,group_id,submission_id,segment_id,representation_hash,similarity,outlier_score,
  is_representative,is_boundary,is_outlier
) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10)`,
			tenantID, groupID, member.SubmissionID, member.SegmentID, member.RepresentationHash,
			member.Similarity, member.OutlierScore, member.Representative, member.Boundary, member.Outlier); err != nil {
			return err
		}
		if member.Outlier {
			payload, _ := json.Marshal(map[string]any{"reason": "high_outlier"})
			if _, err := tx.ExecContext(ctx, `
INSERT INTO answer_group_automation_candidate(
  tenant_id,group_id,submission_id,segment_id,decision_revision,candidate_kind,
  score_candidate_json,rubric_selection_json,status,algorithm_version,created_by
) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,0,'individual_review',$5::jsonb,'{}'::jsonb,'manual_required',$6,$7::uuid)`,
				tenantID, groupID, member.SubmissionID, member.SegmentID, payload, algorithm, actorID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
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
				return err
			}
		}
	}
	return nil
}

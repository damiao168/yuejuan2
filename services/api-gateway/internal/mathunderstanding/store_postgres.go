package mathunderstanding

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) CreateArtifact(ctx context.Context, tenantID string, input CreateArtifactInput) (Artifact, error) {
	if tenantID == "" || ValidateCreateArtifact(input) != nil {
		return Artifact{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Artifact{}, err
	}
	defer tx.Rollback()
	var lockedSegmentID string
	// 先核实题块与考试快照的归属，并锁住题块；并发写入同一题块时串行分配版本、切换当前记录。
	if err = tx.QueryRowContext(ctx, `
SELECT seg.id::text
FROM answer_segment seg
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
JOIN exam_question_snapshot snap ON snap.tenant_id=q.tenant_id AND snap.exam_id=q.exam_id AND snap.question_id=q.id
WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND snap.id=$3::uuid
  AND snap.profile_snapshot_json->>'subject_code'=$4
  AND snap.profile_snapshot_json->>'subject_code' IN ('mathematics','physics','chemistry')
  AND seg.deleted_at IS NULL
	FOR UPDATE OF seg`, tenantID, input.AnswerSegmentID, input.ExamQuestionSnapshotID, input.SubjectCode).Scan(&lockedSegmentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Artifact{}, ErrInvalidInput
		}
		return Artifact{}, err
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM math_understanding_artifact WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND input_hash=$3 AND stage='recognition' ORDER BY version DESC LIMIT 1`, tenantID, input.AnswerSegmentID, input.InputHash).Scan(&existingID)
	if err == nil {
		_ = tx.Rollback()
		return s.GetArtifact(ctx, tenantID, existingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, err
	}
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(version),0)+1 FROM math_understanding_artifact WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid`, tenantID, input.AnswerSegmentID).Scan(&version); err != nil {
		return Artifact{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE math_understanding_artifact SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, tenantID, input.AnswerSegmentID); err != nil {
		return Artifact{}, err
	}
	blocks, _ := json.Marshal(input.Blocks)
	formulas, _ := json.Marshal(input.Formulas)
	relations, _ := json.Marshal(input.Relations)
	graph, _ := json.Marshal(input.SolutionGraph)
	checks, _ := json.Marshal(input.Verifications)
	evidence, _ := json.Marshal(input.RubricEvidence)
	row := tx.QueryRowContext(ctx, `
INSERT INTO math_understanding_artifact(
 tenant_id,subject_code,answer_segment_id,exam_question_snapshot_id,version,input_hash,engine_version,
 blocks_json,formulas_json,relations_json,solution_graph_json,verifications_json,rubric_evidence_json,is_current)
VALUES($1::uuid,$2,$3::uuid,$4::uuid,$5,$6,$7,$8::jsonb,$9::jsonb,$10::jsonb,$11::jsonb,$12::jsonb,$13::jsonb,true)
RETURNING id::text,created_at`, tenantID, input.SubjectCode, input.AnswerSegmentID, input.ExamQuestionSnapshotID, version, input.InputHash, input.EngineVersion, blocks, formulas, relations, graph, checks, evidence)
	item := Artifact{TenantID: tenantID, AnswerSegmentID: input.AnswerSegmentID, ExamQuestionSnapshotID: input.ExamQuestionSnapshotID, Version: version, InputHash: input.InputHash, EngineVersion: input.EngineVersion, IsCurrent: true, Stage: "recognition", QualitySummary: map[string]any{}, CreateArtifactInput: cloneInput(input)}
	if err = row.Scan(&item.ID, &item.CreatedAt); err != nil {
		return Artifact{}, err
	}
	if err = tx.Commit(); err != nil {
		return Artifact{}, err
	}
	return item, nil
}

func (s *PostgresStore) CreateDerivedArtifact(ctx context.Context, tenantID, parentArtifactID string, correctionRevision int64, input CreateArtifactInput, qualitySummary map[string]any) (Artifact, error) {
	if tenantID == "" || parentArtifactID == "" || correctionRevision < 0 || ValidateCreateArtifact(input) != nil {
		return Artifact{}, ErrInvalidInput
	}
	qualitySummary = cloneMap(qualitySummary)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Artifact{}, err
	}
	defer tx.Rollback()
	item, err := s.createDerivedArtifactInTx(ctx, tx, tenantID, parentArtifactID, correctionRevision, input, qualitySummary)
	if err != nil {
		return Artifact{}, err
	}
	return item, tx.Commit()
}

func (s *PostgresStore) createDerivedArtifactInTx(ctx context.Context, tx *sql.Tx, tenantID, parentArtifactID string, correctionRevision int64, input CreateArtifactInput, qualitySummary map[string]any) (Artifact, error) {
	quality, err := json.Marshal(cloneMap(qualitySummary))
	if err != nil {
		return Artifact{}, ErrInvalidInput
	}
	var segmentID string
	if err := tx.QueryRowContext(ctx, `SELECT id::text FROM answer_segment WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, input.AnswerSegmentID).Scan(&segmentID); err != nil {
		return Artifact{}, err
	}
	var parent Artifact
	err = tx.QueryRowContext(ctx, `
SELECT id::text,tenant_id::text,subject_code,answer_segment_id::text,exam_question_snapshot_id::text,
       version,input_hash,engine_version,is_current
FROM math_understanding_artifact
WHERE tenant_id=$1::uuid AND id=$2::uuid
FOR UPDATE`, tenantID, parentArtifactID).Scan(
		&parent.ID, &parent.TenantID, &parent.SubjectCode, &parent.AnswerSegmentID,
		&parent.ExamQuestionSnapshotID, &parent.Version, &parent.InputHash, &parent.EngineVersion, &parent.IsCurrent,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, ErrNotFound
	}
	if err != nil {
		return Artifact{}, err
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `
SELECT id::text FROM math_understanding_artifact
WHERE tenant_id=$1::uuid AND parent_artifact_id=$2::uuid AND stage='verified' AND correction_revision=$3`,
		tenantID, parentArtifactID, correctionRevision).Scan(&existingID)
	if err == nil {
		return s.getArtifact(ctx, tx, tenantID, "id=$2::uuid", existingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, err
	}
	// 重试可复用上方查到的已有结果；创建新结果则要求父版本仍是当前版本，且输入来源没有改变。
	if !parent.IsCurrent || !sameArtifactBinding(input, parent) {
		return Artifact{}, ErrRevisionConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE math_understanding_artifact SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, tenantID, input.AnswerSegmentID); err != nil {
		return Artifact{}, err
	}
	blocks, _ := json.Marshal(input.Blocks)
	formulas, _ := json.Marshal(input.Formulas)
	relations, _ := json.Marshal(input.Relations)
	graph, _ := json.Marshal(input.SolutionGraph)
	checks, _ := json.Marshal(input.Verifications)
	evidence, _ := json.Marshal(input.RubricEvidence)
	item := Artifact{
		TenantID: tenantID, AnswerSegmentID: input.AnswerSegmentID, ExamQuestionSnapshotID: input.ExamQuestionSnapshotID,
		Version: parent.Version + 1, InputHash: input.InputHash, EngineVersion: input.EngineVersion,
		IsCurrent: true, Stage: "verified", ParentArtifactID: parent.ID, CorrectionRevision: correctionRevision,
		QualitySummary: cloneMap(qualitySummary), CreateArtifactInput: cloneInput(input),
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO math_understanding_artifact(
 tenant_id,subject_code,answer_segment_id,exam_question_snapshot_id,version,input_hash,engine_version,
 blocks_json,formulas_json,relations_json,solution_graph_json,verifications_json,rubric_evidence_json,is_current,
 stage,parent_artifact_id,correction_revision,quality_summary_json)
VALUES($1::uuid,$2,$3::uuid,$4::uuid,$5,$6,$7,$8::jsonb,$9::jsonb,$10::jsonb,$11::jsonb,$12::jsonb,$13::jsonb,true,
 'verified',$14::uuid,$15,$16::jsonb)
RETURNING id::text,created_at`, tenantID, input.SubjectCode, input.AnswerSegmentID, input.ExamQuestionSnapshotID,
		item.Version, input.InputHash, input.EngineVersion, blocks, formulas, relations, graph, checks, evidence,
		parentArtifactID, correctionRevision, quality)
	if err = row.Scan(&item.ID, &item.CreatedAt); err != nil {
		return Artifact{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetLatestArtifact(ctx context.Context, tenantID string, answerSegmentID string) (Artifact, error) {
	return s.getArtifact(ctx, s.db, tenantID, "answer_segment_id=$2::uuid AND is_current", answerSegmentID)
}

func (s *PostgresStore) GetArtifact(ctx context.Context, tenantID string, artifactID string) (Artifact, error) {
	return s.getArtifact(ctx, s.db, tenantID, "id=$2::uuid", artifactID)
}

type artifactQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *PostgresStore) getArtifact(ctx context.Context, queryer artifactQueryRower, tenantID, predicate, id string) (Artifact, error) {
	var item Artifact
	var blocks, formulas, relations, graph, checks, evidence, quality []byte
	err := queryer.QueryRowContext(ctx, `
SELECT id::text,tenant_id::text,subject_code,answer_segment_id::text,exam_question_snapshot_id::text,version,input_hash,engine_version,is_current,
 stage,COALESCE(parent_artifact_id::text,''),correction_revision,quality_summary_json,
 blocks_json,formulas_json,relations_json,solution_graph_json,verifications_json,rubric_evidence_json,created_at
FROM math_understanding_artifact
WHERE tenant_id=$1::uuid AND `+predicate, tenantID, id).Scan(
		&item.ID, &item.TenantID, &item.SubjectCode, &item.AnswerSegmentID, &item.ExamQuestionSnapshotID, &item.Version, &item.InputHash, &item.EngineVersion, &item.IsCurrent,
		&item.Stage, &item.ParentArtifactID, &item.CorrectionRevision, &quality,
		&blocks, &formulas, &relations, &graph, &checks, &evidence, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, ErrNotFound
	}
	if err != nil {
		return Artifact{}, err
	}
	item.CreateArtifactInput.AnswerSegmentID = item.AnswerSegmentID
	item.CreateArtifactInput.ExamQuestionSnapshotID = item.ExamQuestionSnapshotID
	item.CreateArtifactInput.InputHash = item.InputHash
	item.CreateArtifactInput.EngineVersion = item.EngineVersion
	if json.Unmarshal(quality, &item.QualitySummary) != nil || json.Unmarshal(blocks, &item.Blocks) != nil || json.Unmarshal(formulas, &item.Formulas) != nil || json.Unmarshal(relations, &item.Relations) != nil || json.Unmarshal(graph, &item.SolutionGraph) != nil || json.Unmarshal(checks, &item.Verifications) != nil || json.Unmarshal(evidence, &item.RubricEvidence) != nil {
		return Artifact{}, errors.New("decode math understanding artifact")
	}
	return item, nil
}

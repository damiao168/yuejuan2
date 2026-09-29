package assessment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) ListSubjectProfiles(ctx context.Context, tenantID string, stage EducationStage, subject SubjectCode) ([]SubjectProfile, error) {
	if tenantID == "" || (stage != "" && !stage.Valid()) || (subject != "" && !subject.Valid()) {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, code, education_stage, subject_code, version,
       status, parser_policy_json, evidence_policy_json, scoring_default_json,
       created_at, updated_at
FROM subject_profile
WHERE tenant_id = $1 AND status = 'active'
  AND ($2 = '' OR education_stage = $2)
  AND ($3 = '' OR subject_code = $3)
ORDER BY education_stage, subject_code, version DESC
`, tenantID, stage, subject)
	if err != nil {
		return nil, mapStoreError(err)
	}
	defer rows.Close()
	out := []SubjectProfile{}
	for rows.Next() {
		item, err := scanSubjectProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListQuestionArchetypes(ctx context.Context) ([]QuestionArchetype, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT code, response_schema_json, evidence_types_json, default_scoring_mode
FROM question_archetype
ORDER BY code
`)
	if err != nil {
		return nil, mapStoreError(err)
	}
	defer rows.Close()
	out := []QuestionArchetype{}
	for rows.Next() {
		item, err := scanQuestionArchetype(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetQuestionConfig(ctx context.Context, tenantID string, examID string, questionID string) (QuestionAssessmentConfig, error) {
	row := s.db.QueryRowContext(ctx, questionConfigSelect+`
WHERE config.tenant_id = $1 AND config.exam_id = $2 AND config.question_id = $3
`, tenantID, examID, questionID)
	item, err := scanQuestionConfig(row)
	if err != nil {
		return QuestionAssessmentConfig{}, mapStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) ConfigureQuestion(ctx context.Context, tenantID string, examID string, questionID string, input ConfigureQuestionInput) (QuestionAssessmentConfig, error) {
	if tenantID == "" || examID == "" || questionID == "" || input.SubjectProfileID == "" {
		return QuestionAssessmentConfig{}, ErrInvalidInput
	}
	if err := validateConfigureInput(input); err != nil {
		return QuestionAssessmentConfig{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return QuestionAssessmentConfig{}, err
	}
	defer tx.Rollback()

	var examStatus, examSubject string
	// 先锁考试再锁题目配置，状态确认与修订在同一事务中，避免考试已冻结仍接受旧页面保存。
	if err := tx.QueryRowContext(ctx, `
SELECT exam.status, exam.subject
FROM exam
JOIN question
  ON question.tenant_id = exam.tenant_id AND question.exam_id = exam.id
WHERE exam.tenant_id = $1 AND exam.id::text = $2
  AND question.id::text = $3
  AND exam.deleted_at IS NULL AND question.deleted_at IS NULL
  AND question.status <> 'deleted'
FOR UPDATE OF exam
`, tenantID, examID, questionID).Scan(&examStatus, &examSubject); err != nil {
		return QuestionAssessmentConfig{}, mapStoreError(err)
	}
	if examStatus != "draft" && examStatus != "configured" {
		return QuestionAssessmentConfig{}, ErrExamFrozen
	}

	profile, err := loadSubjectProfile(ctx, tx, tenantID, input.SubjectProfileID)
	if err != nil {
		return QuestionAssessmentConfig{}, mapStoreError(err)
	}
	if canonicalSubject, ok := NormalizeSubjectCode(examSubject); ok && canonicalSubject != profile.SubjectCode {
		return QuestionAssessmentConfig{}, ErrInvalidInput
	}
	archetype, err := loadQuestionArchetype(ctx, tx, input.ArchetypeCode)
	if err != nil {
		return QuestionAssessmentConfig{}, mapStoreError(err)
	}
	if err := validateAllowedEvidence(input.AllowedEvidenceTypes, profile, archetype, input.ScoringPolicy.RequireEvidence); err != nil {
		return QuestionAssessmentConfig{}, err
	}

	evidenceJSON, _ := json.Marshal(input.AllowedEvidenceTypes)
	policyJSON, _ := json.Marshal(input.ScoringPolicy)
	var existingRevision int64
	err = tx.QueryRowContext(ctx, `
SELECT revision
FROM question_assessment_config
WHERE tenant_id = $1 AND exam_id = $2 AND question_id = $3
FOR UPDATE
`, tenantID, examID, questionID).Scan(&existingRevision)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if input.ExpectedRevision != 0 {
			return QuestionAssessmentConfig{}, ErrRevisionConflict
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO question_assessment_config (
  tenant_id, exam_id, question_id, subject_profile_id, archetype_code,
  allowed_evidence_types, risk_tier, scoring_policy_json
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
`, tenantID, examID, questionID, input.SubjectProfileID, input.ArchetypeCode,
			evidenceJSON, input.RiskTier, policyJSON)
	case err != nil:
		return QuestionAssessmentConfig{}, mapStoreError(err)
	case existingRevision != input.ExpectedRevision:
		return QuestionAssessmentConfig{}, ErrRevisionConflict
	default:
		result, updateErr := tx.ExecContext(ctx, `
UPDATE question_assessment_config
SET subject_profile_id = $4, archetype_code = $5,
    allowed_evidence_types = $6, risk_tier = $7, scoring_policy_json = $8,
    revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND exam_id = $2 AND question_id = $3 AND revision = $9
`, tenantID, examID, questionID, input.SubjectProfileID, input.ArchetypeCode,
			evidenceJSON, input.RiskTier, policyJSON, input.ExpectedRevision)
		if updateErr != nil {
			err = updateErr
		} else if affected, _ := result.RowsAffected(); affected != 1 {
			return QuestionAssessmentConfig{}, ErrRevisionConflict
		}
	}
	if err != nil {
		return QuestionAssessmentConfig{}, mapStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return QuestionAssessmentConfig{}, mapStoreError(err)
	}
	return s.GetQuestionConfig(ctx, tenantID, examID, questionID)
}

func (s *PostgresStore) FreezeQuestionSnapshot(ctx context.Context, tenantID string, examID string, questionID string) (ExamQuestionSnapshot, error) {
	var status string
	if err := s.db.QueryRowContext(ctx, `
SELECT status FROM exam
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, examID).Scan(&status); err != nil {
		return ExamQuestionSnapshot{}, mapStoreError(err)
	}
	if status == "draft" || status == "configured" {
		return ExamQuestionSnapshot{}, ErrInvalidInput
	}
	if _, err := s.db.ExecContext(ctx, `SELECT assessment_freeze_question_snapshot($1::uuid, $2::uuid, $3::uuid)`, tenantID, examID, questionID); err != nil {
		return ExamQuestionSnapshot{}, mapStoreError(err)
	}
	return s.GetQuestionSnapshot(ctx, tenantID, examID, questionID)
}

func (s *PostgresStore) GetQuestionSnapshot(ctx context.Context, tenantID string, examID string, questionID string) (ExamQuestionSnapshot, error) {
	row := s.db.QueryRowContext(ctx, snapshotSelect+`
WHERE snapshot.tenant_id = $1 AND snapshot.exam_id = $2 AND snapshot.question_id = $3
ORDER BY snapshot.snapshot_version DESC
LIMIT 1
`, tenantID, examID, questionID)
	item, err := scanQuestionSnapshot(row)
	if err != nil {
		return ExamQuestionSnapshot{}, mapStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) CreateScoringEvidence(ctx context.Context, tenantID string, input CreateScoringEvidenceInput) (ScoringEvidence, error) {
	if tenantID == "" || ValidateScoringEvidence(input) != nil {
		return ScoringEvidence{}, ErrInvalidInput
	}
	payloadJSON, _ := json.Marshal(input.Payload)
	var bboxJSON any
	if input.BoundingBox != nil {
		bboxJSON, _ = json.Marshal(input.BoundingBox)
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO scoring_evidence (
  tenant_id, submission_id, question_id, exam_question_snapshot_id,
  evidence_type, source_artifact_id, rubric_criterion_key,
  payload_json, bbox_json, quality
)
SELECT $1, submission.id, question.id, snapshot.id,
       $5, artifact.id, NULLIF($7, ''), $8, $9, $10
FROM submission
JOIN exam_question_snapshot snapshot
  ON snapshot.tenant_id = submission.tenant_id AND snapshot.exam_id = submission.exam_id
JOIN question
  ON question.tenant_id = snapshot.tenant_id AND question.exam_id = snapshot.exam_id
 AND question.id = snapshot.question_id
JOIN file_asset artifact ON artifact.tenant_id = submission.tenant_id
WHERE submission.tenant_id = $1 AND submission.id::text = $2
  AND question.id::text = $3 AND snapshot.id::text = $4
  AND artifact.id::text = $6 AND artifact.deleted_at IS NULL
  AND snapshot.allowed_evidence_types ? $5
RETURNING id::text, tenant_id::text, submission_id::text, question_id::text,
          exam_question_snapshot_id::text, evidence_type, source_artifact_id::text,
          COALESCE(rubric_criterion_key, ''), payload_json, bbox_json, quality::float8,
          created_at
`, tenantID, input.SubmissionID, input.QuestionID, input.ExamQuestionSnapshotID,
		input.EvidenceType, input.SourceArtifactID, input.RubricCriterionKey, payloadJSON, bboxJSON, input.Quality)
	item, err := scanScoringEvidence(row)
	if err != nil {
		return ScoringEvidence{}, mapStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) ListScoringEvidence(ctx context.Context, tenantID string, submissionID string, questionID string) ([]ScoringEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, question_id::text,
       exam_question_snapshot_id::text, evidence_type, source_artifact_id::text,
       COALESCE(rubric_criterion_key, ''), payload_json, bbox_json, quality::float8,
       created_at
FROM scoring_evidence
WHERE tenant_id = $1 AND submission_id = $2 AND question_id = $3
ORDER BY created_at, id
`, tenantID, submissionID, questionID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	defer rows.Close()
	out := []ScoringEvidence{}
	for rows.Next() {
		item, err := scanScoringEvidence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

const questionConfigSelect = `
SELECT config.id::text, config.tenant_id::text, config.exam_id::text,
       config.question_id::text, config.subject_profile_id::text,
       profile.code, profile.version, profile.education_stage, profile.subject_code,
       config.archetype_code, config.allowed_evidence_types, config.risk_tier,
       config.scoring_policy_json, config.revision, config.created_at, config.updated_at
FROM question_assessment_config config
JOIN subject_profile profile
  ON profile.tenant_id = config.tenant_id AND profile.id = config.subject_profile_id
`

const snapshotSelect = `
SELECT snapshot.id::text, snapshot.tenant_id::text, snapshot.exam_id::text,
       snapshot.question_id::text, snapshot.snapshot_version,
       snapshot.subject_profile_id::text,
       snapshot.profile_snapshot_json ->> 'code', snapshot.subject_profile_version,
       snapshot.profile_snapshot_json ->> 'education_stage',
       snapshot.profile_snapshot_json ->> 'subject_code', snapshot.archetype_code,
       snapshot.allowed_evidence_types, snapshot.risk_tier,
       snapshot.profile_snapshot_json, snapshot.archetype_snapshot_json,
       snapshot.rubric_snapshot_json, snapshot.scoring_policy_snapshot_json,
       snapshot.content_hash, snapshot.created_at, snapshot.source_snapshot_json
FROM exam_question_snapshot snapshot
`

type scanner interface {
	Scan(dest ...any) error
}

func scanSubjectProfile(row scanner) (SubjectProfile, error) {
	var item SubjectProfile
	var parserJSON, evidenceJSON, scoringJSON []byte
	err := row.Scan(&item.ID, &item.TenantID, &item.Code, &item.EducationStage,
		&item.SubjectCode, &item.Version, &item.Status, &parserJSON, &evidenceJSON,
		&scoringJSON, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return SubjectProfile{}, err
	}
	if err := json.Unmarshal(parserJSON, &item.ParserPolicy); err != nil {
		return SubjectProfile{}, err
	}
	if err := json.Unmarshal(evidenceJSON, &item.EvidencePolicy); err != nil {
		return SubjectProfile{}, err
	}
	if err := json.Unmarshal(scoringJSON, &item.ScoringDefault); err != nil {
		return SubjectProfile{}, err
	}
	return item, nil
}

func scanQuestionArchetype(row scanner) (QuestionArchetype, error) {
	var item QuestionArchetype
	var schemaJSON, evidenceJSON []byte
	if err := row.Scan(&item.Code, &schemaJSON, &evidenceJSON, &item.DefaultScoringMode); err != nil {
		return QuestionArchetype{}, err
	}
	if err := json.Unmarshal(schemaJSON, &item.ResponseSchema); err != nil {
		return QuestionArchetype{}, err
	}
	if err := json.Unmarshal(evidenceJSON, &item.EvidenceTypes); err != nil {
		return QuestionArchetype{}, err
	}
	return item, nil
}

func scanQuestionConfig(row scanner) (QuestionAssessmentConfig, error) {
	var item QuestionAssessmentConfig
	var evidenceJSON, scoringJSON []byte
	if err := row.Scan(&item.ID, &item.TenantID, &item.ExamID, &item.QuestionID,
		&item.SubjectProfileID, &item.SubjectProfileCode, &item.SubjectProfileVersion,
		&item.EducationStage, &item.SubjectCode, &item.ArchetypeCode, &evidenceJSON,
		&item.RiskTier, &scoringJSON, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return QuestionAssessmentConfig{}, err
	}
	if err := json.Unmarshal(evidenceJSON, &item.AllowedEvidenceTypes); err != nil {
		return QuestionAssessmentConfig{}, err
	}
	if err := json.Unmarshal(scoringJSON, &item.ScoringPolicy); err != nil {
		return QuestionAssessmentConfig{}, err
	}
	return item, nil
}

func scanQuestionSnapshot(row scanner) (ExamQuestionSnapshot, error) {
	var item ExamQuestionSnapshot
	var evidenceJSON, profileJSON, archetypeJSON, rubricJSON, scoringJSON, sourceJSON []byte
	if err := row.Scan(&item.ID, &item.TenantID, &item.ExamID, &item.QuestionID,
		&item.SnapshotVersion, &item.SubjectProfileID, &item.SubjectProfileCode,
		&item.SubjectProfileVersion, &item.EducationStage, &item.SubjectCode,
		&item.ArchetypeCode, &evidenceJSON, &item.RiskTier, &profileJSON,
		&archetypeJSON, &rubricJSON, &scoringJSON, &item.ContentHash, &item.CreatedAt, &sourceJSON); err != nil {
		return ExamQuestionSnapshot{}, err
	}
	if err := json.Unmarshal(evidenceJSON, &item.AllowedEvidenceTypes); err != nil {
		return ExamQuestionSnapshot{}, err
	}
	for raw, destination := range map[*[]byte]*map[string]any{
		&profileJSON:   &item.ProfileSnapshot,
		&archetypeJSON: &item.ArchetypeSnapshot,
		&rubricJSON:    &item.RubricSnapshot,
		&sourceJSON:    &item.SourceSnapshot,
	} {
		if err := json.Unmarshal(*raw, destination); err != nil {
			return ExamQuestionSnapshot{}, err
		}
	}
	if err := json.Unmarshal(scoringJSON, &item.ScoringPolicySnapshot); err != nil {
		return ExamQuestionSnapshot{}, err
	}
	return item, nil
}

func scanScoringEvidence(row scanner) (ScoringEvidence, error) {
	var item ScoringEvidence
	var payloadJSON []byte
	var bboxJSON []byte
	var quality sql.NullFloat64
	if err := row.Scan(&item.ID, &item.TenantID, &item.SubmissionID, &item.QuestionID,
		&item.ExamQuestionSnapshotID, &item.EvidenceType, &item.SourceArtifactID,
		&item.RubricCriterionKey, &payloadJSON, &bboxJSON, &quality, &item.CreatedAt); err != nil {
		return ScoringEvidence{}, err
	}
	if err := json.Unmarshal(payloadJSON, &item.Payload); err != nil {
		return ScoringEvidence{}, err
	}
	if len(bboxJSON) > 0 && string(bboxJSON) != "null" {
		item.BoundingBox = &BoundingBox{}
		if err := json.Unmarshal(bboxJSON, item.BoundingBox); err != nil {
			return ScoringEvidence{}, err
		}
	}
	if quality.Valid {
		item.Quality = &quality.Float64
	}
	return item, nil
}

func loadSubjectProfile(ctx context.Context, tx *sql.Tx, tenantID string, profileID string) (SubjectProfile, error) {
	return scanSubjectProfile(tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, code, education_stage, subject_code, version,
       status, parser_policy_json, evidence_policy_json, scoring_default_json,
       created_at, updated_at
FROM subject_profile
WHERE tenant_id = $1 AND id::text = $2 AND status = 'active'
`, tenantID, profileID))
}

func loadQuestionArchetype(ctx context.Context, tx *sql.Tx, code string) (QuestionArchetype, error) {
	return scanQuestionArchetype(tx.QueryRowContext(ctx, `
SELECT code, response_schema_json, evidence_types_json, default_scoring_mode
FROM question_archetype WHERE code = $1
`, code))
}

func validateConfigureInput(input ConfigureQuestionInput) error {
	if !IsQuestionArchetype(input.ArchetypeCode) || input.ExpectedRevision < 0 {
		return ErrInvalidInput
	}
	if err := ValidateScoringPolicy(input.RiskTier, input.ArchetypeCode, input.ScoringPolicy); err != nil {
		return err
	}
	seen := map[EvidenceType]bool{}
	for _, evidenceType := range input.AllowedEvidenceTypes {
		if !evidenceType.Valid() || seen[evidenceType] {
			return ErrInvalidInput
		}
		seen[evidenceType] = true
	}
	return nil
}

// 可选证据取学科能力与题型能力的交集；只符合其中一个维度的证据不能写入题目配置。
func validateAllowedEvidence(selected []EvidenceType, profile SubjectProfile, archetype QuestionArchetype, requireEvidence bool) error {
	if requireEvidence && len(selected) == 0 {
		return ErrInvalidInput
	}
	profileAllowed := map[EvidenceType]bool{}
	switch raw := profile.EvidencePolicy["allowed_types"].(type) {
	case []any:
		for _, value := range raw {
			if text, ok := value.(string); ok {
				profileAllowed[EvidenceType(text)] = true
			}
		}
	case []string:
		for _, value := range raw {
			profileAllowed[EvidenceType(value)] = true
		}
	case []EvidenceType:
		for _, value := range raw {
			profileAllowed[value] = true
		}
	}
	archetypeAllowed := map[EvidenceType]bool{}
	for _, value := range archetype.EvidenceTypes {
		archetypeAllowed[value] = true
	}
	for _, value := range selected {
		if !profileAllowed[value] || !archetypeAllowed[value] {
			return ErrInvalidInput
		}
	}
	return nil
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	message := strings.ToLower(pgErr.Message)
	switch {
	case pgErr.Code == "55000" && strings.Contains(message, "frozen"):
		return ErrExamFrozen
	case pgErr.Code == "55000" && strings.Contains(message, "immutable"):
		return ErrSnapshotConflict
	case pgErr.Code == "23514" && strings.Contains(message, "r3"):
		return ErrPolicyViolation
	case pgErr.Code == "23514":
		return ErrInvalidInput
	case pgErr.Code == "23505":
		return ErrRevisionConflict
	case pgErr.Code == "23503":
		return ErrInvalidInput
	case pgErr.Code == "P0001" && strings.Contains(message, "missing"):
		return ErrNotFound
	default:
		return err
	}
}

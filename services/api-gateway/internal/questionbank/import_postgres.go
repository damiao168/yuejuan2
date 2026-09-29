package questionbank

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"regexp"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
)

var importCommandPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func (s *PostgresStore) loadImportAssessment(ctx context.Context, q queryer, tenantID, examID, questionID, snapshotID string) (importAssessmentSnapshot, error) {
	where := `snapshot.tenant_id=$1::uuid AND snapshot.exam_id=$2::uuid AND snapshot.question_id=$3::uuid`
	args := []any{tenantID, examID, questionID}
	order := ` ORDER BY snapshot.snapshot_version DESC LIMIT 1`
	if snapshotID != "" {
		where += ` AND snapshot.id=$4::uuid`
		args = append(args, snapshotID)
		order = ``
	}
	var out importAssessmentSnapshot
	var profile, archetype, rubric, policy []byte
	err := q.QueryRowContext(ctx, `
SELECT snapshot.id::text,snapshot.snapshot_version,snapshot.profile_snapshot_json,
       snapshot.archetype_snapshot_json,snapshot.rubric_snapshot_json,
       snapshot.scoring_policy_snapshot_json,snapshot.content_hash,snapshot.archetype_code,
       snapshot.profile_snapshot_json->>'subject_code',snapshot.profile_snapshot_json->>'education_stage'
FROM exam_question_snapshot snapshot WHERE `+where+order, args...).Scan(
		&out.ID, &out.Version, &profile, &archetype, &rubric, &policy,
		&out.ContentHash, &out.ArchetypeCode, &out.SubjectCode, &out.EducationStage,
	)
	if err != nil {
		return out, pgError(err)
	}
	for raw, target := range map[*[]byte]*map[string]any{
		&profile: &out.Profile, &archetype: &out.Archetype,
		&rubric: &out.Rubric, &policy: &out.ScoringPolicy,
	} {
		if err = json.Unmarshal(*raw, target); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *PostgresStore) prepareImport(ctx context.Context, q queryer, scope auth.AccessScope, selection ImportPreviewSelection, assessmentSnapshotID string) (preparedImport, error) {
	var prepared preparedImport
	if !validScope(scope) || !validID(selection.QuestionID) || !validID(selection.TargetBankID) || !validID(selection.SourceSnapshotID) {
		return prepared, ErrInvalidInput
	}
	bank, err := s.bank(ctx, q, scope, selection.TargetBankID, "create", "")
	if err != nil {
		return prepared, err
	}
	if bank.Status != "active" {
		return prepared, ErrLocked
	}
	var examID, configurationHash string
	var schemaVersion sql.NullInt16
	var sourceHash sql.NullString
	var raw []byte
	err = q.QueryRowContext(ctx, `
SELECT exam_id::text,configuration_hash,import_snapshot_schema_version,import_snapshot_hash,import_snapshot_json
FROM exam_readiness_snapshot
WHERE tenant_id=$1::uuid AND id=$2::uuid AND ($3 OR exam_id::text=ANY($4::text[]))
`, scope.TenantID, selection.SourceSnapshotID, scope.TenantWide, scope.ExamIDs).Scan(&examID, &configurationHash, &schemaVersion, &sourceHash, &raw)
	if err != nil {
		return prepared, pgError(err)
	}
	if !schemaVersion.Valid || schemaVersion.Int16 != 1 || !sourceHash.Valid || len(raw) == 0 {
		return prepared, ErrSourceUnavailable
	}
	var frozen frozenImportSnapshot
	// 题目、评分快照和哈希必须来自同一份冻结记录，不能用考试当前配置替换缺失的历史依据。
	if err = json.Unmarshal(raw, &frozen); err != nil || frozen.Version != 1 || frozen.ConfigurationHash != configurationHash || hashJSON(frozen) != sourceHash.String {
		return prepared, ErrSourceUnavailable
	}
	question, ok := frozenQuestion(frozen, selection.QuestionID)
	if !ok {
		return prepared, ErrNotFound
	}
	if !validID(question.AssessmentSnapshotID) || (assessmentSnapshotID != "" && assessmentSnapshotID != question.AssessmentSnapshotID) {
		return prepared, ErrSourceUnavailable
	}
	assessment, err := s.loadImportAssessment(ctx, q, scope.TenantID, examID, question.ID, question.AssessmentSnapshotID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return prepared, ErrSourceUnavailable
		}
		return prepared, err
	}
	if assessment.ContentHash != question.AssessmentSnapshotHash {
		return prepared, ErrSourceUnavailable
	}
	content, scoring, err := buildImportedFacts(question, assessment, selection.Mapping)
	if err != nil {
		return prepared, ErrSourceUnavailable
	}
	schema, err := s.metadataSchema(ctx, q, scope, bank.ID, bank.MetadataSchemaVersion, "create")
	if err != nil {
		return prepared, err
	}
	prepared = preparedImport{
		Content: content, Scoring: scoring, Schema: schema, Bank: bank,
		ScoringConsistent: scoringMatchesAssessment(question, assessment),
		Source: ImportSource{
			ExamID: examID, QuestionID: question.ID, QuestionNo: question.QuestionNo,
			ReadinessSnapshotID: selection.SourceSnapshotID, ConfigurationHash: configurationHash,
			SnapshotHash: sourceHash.String, AssessmentSnapshotID: assessment.ID,
			AssessmentSnapshotHash: assessment.ContentHash, AssessmentSnapshotVersion: assessment.Version,
			ProfileSnapshot: assessment.Profile, ArchetypeSnapshot: assessment.Archetype,
			ScoringPolicySnapshot:   assessment.ScoringPolicy,
			SourceBankItemID:        question.SourceBankItemID,
			SourceBankItemVersionID: question.SourceBankItemVersionID,
			SourceContentHash:       question.SourceContentHash,
		},
	}
	prepared.Issues = importIssues(content, scoring, schema, selection.Mapping.ItemCode)
	if !prepared.ScoringConsistent {
		prepared.Issues = append(prepared.Issues, ImportIssue{Code: "scoring_snapshot_mismatch", Field: "scoring_source", Message: "readiness 与 Assessment Snapshot 的 Rubric 事实不一致", Blocking: true})
	}
	return prepared, nil
}

func importStemSummary(value string) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= 240 {
		return value
	}
	runes := []rune(value)
	return string(runes[:240]) + "…"
}

func (s *PostgresStore) duplicateHints(ctx context.Context, q queryer, scope auth.AccessScope, bankID, contentHashValue, bundleHashValue, stem string) ([]ImportDuplicateHint, error) {
	// 相似题只作为提示；是否新建题目或追加版本，由确认请求明确选择。
	rows, err := q.QueryContext(ctx, `
SELECT i.id::text,i.item_code,v.id::text,v.version_no,v.content_hash,v.bundle_hash,v.content->>'stem'
FROM question_bank_item i
JOIN question_bank_item_version v ON v.tenant_id=i.tenant_id AND v.item_id=i.id
WHERE i.tenant_id=$1::uuid AND i.bank_id=$2::uuid AND i.kind='question' AND i.status='active'
  AND (v.content_hash=$3 OR v.bundle_hash=$4 OR lower(regexp_replace(btrim(v.content->>'stem'),'\s+',' ','g'))=lower(regexp_replace(btrim($5),'\s+',' ','g')))
ORDER BY (v.bundle_hash=$4) DESC,v.updated_at DESC,v.id
LIMIT 20
`, scope.TenantID, bankID, contentHashValue, bundleHashValue, stem)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hints := []ImportDuplicateHint{}
	for rows.Next() {
		var hint ImportDuplicateHint
		var stem string
		if err = rows.Scan(&hint.ItemID, &hint.ItemCode, &hint.VersionID, &hint.VersionNo, &hint.ContentHash, &hint.BundleHash, &stem); err != nil {
			return nil, err
		}
		if hint.BundleHash == bundleHashValue {
			hint.Kind = "exact_bundle"
		} else if hint.ContentHash == contentHashValue {
			hint.Kind = "same_content_different_scoring"
		} else {
			hint.Kind = "similar_content"
		}
		hint.StemSummary = importStemSummary(stem)
		hints = append(hints, hint)
	}
	return hints, rows.Err()
}

func (s *PostgresStore) PreviewImports(ctx context.Context, scope auth.AccessScope, in ImportPreviewInput) (ImportPreviewBatch, error) {
	if len(in.Selections) == 0 || len(in.Selections) > 50 {
		return ImportPreviewBatch{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return ImportPreviewBatch{}, err
	}
	defer tx.Rollback()
	out := ImportPreviewBatch{Items: make([]ImportPreviewItem, 0, len(in.Selections))}
	for _, selection := range in.Selections {
		item := ImportPreviewItem{QuestionID: selection.QuestionID, Status: "failed"}
		prepared, prepareErr := s.prepareImport(ctx, tx, scope, selection, "")
		if prepareErr != nil {
			item.ErrorCode, item.Retryable = importErrorCode(prepareErr)
			out.Items = append(out.Items, item)
			continue
		}
		contentHashValue := contentHash(prepared.Content)
		bundleHashValue := bundleHash(prepared.Content, prepared.Scoring)
		hints, hintErr := s.duplicateHints(ctx, tx, scope, selection.TargetBankID, contentHashValue, bundleHashValue, prepared.Content.Stem)
		if hintErr != nil {
			item.ErrorCode, item.Retryable = importErrorCode(hintErr)
			out.Items = append(out.Items, item)
			continue
		}
		preview := &ImportPreview{
			QuestionID: selection.QuestionID, Available: prepared.ScoringConsistent,
			Content: prepared.Content, Scoring: prepared.Scoring,
			ContentHash: contentHashValue, BundleHash: bundleHashValue,
			Source: prepared.Source, Issues: prepared.Issues, DuplicateHints: hints,
			SuggestedItemCode: suggestedImportCode(selection.QuestionID),
		}
		item.Status, item.Preview = "ready", preview
		out.Items = append(out.Items, item)
	}
	if err = tx.Commit(); err != nil {
		return ImportPreviewBatch{}, err
	}
	return out, nil
}

func (s *PostgresStore) validateImportAsset(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, asset Asset) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `
SELECT lifecycle_status='active' AND deleted_at IS NULL AND hash_sha256=$3 AND original_name=$4 AND content_type=$5
FROM file_asset WHERE tenant_id=$1::uuid AND id=$2::uuid FOR SHARE
`, scope.TenantID, asset.FileAssetID, asset.SHA256, asset.Name, asset.ContentType).Scan(&valid)
	if err != nil {
		return pgError(err)
	}
	if !valid {
		return ErrSourceUnavailable
	}
	return nil
}

func (s *PostgresStore) ImportQuestion(ctx context.Context, scope auth.AccessScope, questionID string, in ImportQuestionInput) (ImportResult, error) {
	if !validID(questionID) || !validID(in.TargetBankID) || !validID(in.SourceSnapshotID) || !validID(in.AssessmentSnapshotID) ||
		in.ScoringSource != "original_exam" || !oneOf(in.DedupDecision, "new_item", "new_version") ||
		in.ExpectedTargetRevision <= 0 || in.ExpectedTargetSchemaVersion <= 0 || !importCommandPattern.MatchString(in.CommandID) || commandreceipt.ID(ctx) != in.CommandID {
		return ImportResult{}, ErrInvalidInput
	}
	if in.DedupDecision == "new_item" {
		if !codePattern.MatchString(in.Mapping.ItemCode) || in.ExistingItemID != "" {
			return ImportResult{}, ErrInvalidInput
		}
	} else if !validID(in.ExistingItemID) || in.Mapping.ItemCode != "" {
		return ImportResult{}, ErrInvalidInput
	}
	selection := ImportPreviewSelection{QuestionID: questionID, TargetBankID: in.TargetBankID, SourceSnapshotID: in.SourceSnapshotID, Mapping: in.Mapping}
	return pgMutation(ctx, s, scope, "question_bank.import.from_question", questionID, in, "create", func(tx *sql.Tx) (ImportResult, error) {
		bank, err := s.bank(ctx, tx, scope, in.TargetBankID, "create", " FOR SHARE OF b")
		if err != nil {
			return ImportResult{}, err
		}
		if bank.MetadataSchemaVersion != in.ExpectedTargetSchemaVersion {
			return ImportResult{}, ErrConflict
		}
		prepared, err := s.prepareImport(ctx, tx, scope, selection, in.AssessmentSnapshotID)
		if err != nil {
			return ImportResult{}, err
		}
		if !prepared.ScoringConsistent {
			return ImportResult{}, ErrSourceUnavailable
		}
		// 评分来源不一致时拒绝导入；其余映射问题随草稿保存，交给后续编辑和发布检查处理。
		for _, asset := range prepared.Scoring.Assets {
			if err = s.validateImportAsset(ctx, tx, scope, asset); err != nil {
				return ImportResult{}, err
			}
		}
		contentHashValue := contentHash(prepared.Content)
		bundleHashValue := bundleHash(prepared.Content, prepared.Scoring)
		hints, err := s.duplicateHints(ctx, tx, scope, in.TargetBankID, contentHashValue, bundleHashValue, prepared.Content.Stem)
		if err != nil {
			return ImportResult{}, err
		}
		var item Item
		var version Version
		var linked any
		if in.DedupDecision == "new_item" {
			bank, lockErr := s.bank(ctx, tx, scope, in.TargetBankID, "create", " FOR SHARE OF b")
			if lockErr != nil {
				return ImportResult{}, lockErr
			}
			if bank.Status != "active" {
				return ImportResult{}, ErrLocked
			}
			if bank.Revision != in.ExpectedTargetRevision {
				return ImportResult{}, ErrConflict
			}
			item, err = scanItem(tx.QueryRowContext(ctx, `
INSERT INTO question_bank_item(tenant_id,bank_id,item_code,subject_code,grade_scope,created_by,kind)
VALUES($1::uuid,$2::uuid,$3,$4,$5,$6::uuid,'question') RETURNING `+itemCols,
				scope.TenantID, in.TargetBankID, in.Mapping.ItemCode,
				prepared.Content.Metadata.SubjectCode, prepared.Content.Metadata.GradeScope, scope.ActorID))
			if err != nil {
				return ImportResult{}, err
			}
			version, err = s.insertVersion(ctx, tx, scope, item.ID, 1, prepared.Schema.Version, nil, prepared.Content, prepared.Scoring)
		} else {
			item, err = s.item(ctx, tx, scope, in.ExistingItemID, "edit", " FOR UPDATE")
			if err != nil {
				return ImportResult{}, err
			}
			if item.BankID != in.TargetBankID || item.Kind != "question" || item.Status != "active" {
				return ImportResult{}, ErrNotFound
			}
			if item.Revision != in.ExpectedTargetRevision {
				return ImportResult{}, ErrConflict
			}
			var next int
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(version_no),0)+1 FROM question_bank_item_version WHERE tenant_id=$1::uuid AND item_id=$2::uuid`, scope.TenantID, item.ID).Scan(&next); err != nil {
				return ImportResult{}, err
			}
			version, err = s.insertVersion(ctx, tx, scope, item.ID, next, prepared.Schema.Version, nil, prepared.Content, prepared.Scoring)
			if err == nil {
				item, err = scanItem(tx.QueryRowContext(ctx, `UPDATE question_bank_item SET revision=revision+1 WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+itemCols, scope.TenantID, item.ID))
			}
			linked = item.ID
		}
		if err != nil {
			return ImportResult{}, err
		}
		if version.ContentHash != contentHashValue || version.BundleHash != bundleHashValue {
			// 同时核对 Go 预览和数据库生成的哈希，避免两端使用不同规则却接受同一份导入。
			return ImportResult{}, ErrConflict
		}
		mappingRaw, _ := json.Marshal(in.Mapping)
		if in.DedupDecision == "new_version" {
			filtered := []ImportIssue{}
			for _, issue := range prepared.Issues {
				if issue.Code != "item_code_required" {
					filtered = append(filtered, issue)
				}
			}
			prepared.Issues = filtered
		}
		issuesRaw, _ := json.Marshal(prepared.Issues)
		provenanceID := uuid.NewString()
		provenance := ImportProvenance{
			ID: provenanceID, TargetBankID: in.TargetBankID, TargetSchemaVersion: prepared.Schema.Version,
			Source: prepared.Source, ScoringSource: in.ScoringSource, DedupDecision: in.DedupDecision,
			LinkedItemID: in.ExistingItemID, Mapping: in.Mapping, CommandID: in.CommandID,
		}
		provenanceRaw, _ := json.Marshal(provenance)
		var createdAt sql.NullTime
		err = tx.QueryRowContext(ctx, `
INSERT INTO question_bank_import(
 tenant_id,target_bank_id,target_item_id,target_version_id,target_schema_version,
 source_exam_id,source_question_id,source_question_no,source_readiness_snapshot_id,
 source_configuration_hash,source_snapshot_hash,source_assessment_snapshot_id,source_assessment_hash,
 imported_content_hash,imported_bundle_hash,scoring_source,dedup_decision,linked_item_id,
 mapping,issues,command_id,created_by,id,provenance
) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7::uuid,$8,$9::uuid,$10,$11,$12::uuid,$13,$14,$15,$16,$17,$18::uuid,$19::jsonb,$20::jsonb,$21,$22::uuid,$23::uuid,$24::jsonb)
RETURNING id::text,created_at
`, scope.TenantID, in.TargetBankID, item.ID, version.ID, prepared.Schema.Version,
			prepared.Source.ExamID, prepared.Source.QuestionID, prepared.Source.QuestionNo,
			prepared.Source.ReadinessSnapshotID, prepared.Source.ConfigurationHash, prepared.Source.SnapshotHash,
			prepared.Source.AssessmentSnapshotID, prepared.Source.AssessmentSnapshotHash,
			contentHashValue, bundleHashValue, in.ScoringSource, in.DedupDecision, linked,
			mappingRaw, issuesRaw, in.CommandID, scope.ActorID, provenanceID, provenanceRaw).Scan(&provenanceID, &createdAt)
		if err != nil {
			return ImportResult{}, err
		}
		provenance.CreatedAt = createdAt.Time
		version.ImportProvenance = &provenance
		return ImportResult{
			Item: item, Version: version, DuplicateHints: hints, Issues: prepared.Issues,
			Provenance: provenance,
		}, nil
	})
}

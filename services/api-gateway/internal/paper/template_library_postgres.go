package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

const librarySelect = `SELECT id::text,tenant_id::text,school_id::text,name,page_count,layout,source_sha256,content_hash,reviewed_by::text,reviewed_at FROM answer_sheet_template_library`

func scanLibrary(row questionScanner) (AnswerSheetTemplateLibraryVersion, error) {
	var out AnswerSheetTemplateLibraryVersion
	var layout []byte
	err := row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.Name, &out.PageCount, &layout, &out.SourceSHA256, &out.ContentHash, &out.ReviewedBy, &out.ReviewedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(layout, &out.Layout); err != nil {
		return out, err
	}
	out.VersionNo = 1
	return out, nil
}

func libraryExamSchool(ctx context.Context, q postgresQueryer, scope auth.AccessScope, examID string) (string, error) {
	var schoolID string
	err := q.QueryRowContext(ctx, `SELECT school_id::text FROM exam WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, scope.TenantID, examID).Scan(&schoolID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	rows, err := q.QueryContext(ctx, `SELECT class_id::text FROM exam_class WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL`, scope.TenantID, examID)
	if err != nil {
		return "", err
	}
	classes := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		classes = append(classes, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if !auth.AllowsResourceBoundary(scope, auth.ResourceBoundary{TenantID: scope.TenantID, SchoolID: schoolID, ExamID: examID, ClassIDs: classes, ResourceType: "exam", ResourceID: examID}) {
		return "", ErrNotFound
	}
	return schoolID, nil
}

func (s *PostgresStore) ListTemplateLibrary(ctx context.Context, scope auth.AccessScope, examID string) ([]AnswerSheetTemplateLibraryVersion, error) {
	school, err := libraryExamSchool(ctx, s.db, scope, examID)
	if err != nil {
		return nil, err
	}
	return listLibrary(ctx, s.db, scope.TenantID, school, "")
}

func listLibrary(ctx context.Context, q postgresQueryer, tenantID, schoolID, hash string) ([]AnswerSheetTemplateLibraryVersion, error) {
	rows, err := q.QueryContext(ctx, librarySelect+` WHERE tenant_id=$1 AND school_id=$2::uuid AND ($3='' OR source_sha256=$3) ORDER BY reviewed_at DESC,id`, tenantID, schoolID, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnswerSheetTemplateLibraryVersion{}
	for rows.Next() {
		item, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func libraryTemplate(ctx context.Context, q postgresQueryer, tenantID, id string) (AnswerSheetTemplate, error) {
	var out AnswerSheetTemplate
	err := scanTemplate(q.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,exam_paper_id::text,version_no,revision,name,status,page_count,layout,content_hash,created_by::text,COALESCE(locked_by::text,''),locked_at,created_at,updated_at FROM answer_sheet_template WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, id), &out)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

func libraryQuestions(ctx context.Context, q postgresQueryer, tenantID, examID string) ([]Question, error) {
	rows, err := q.QueryContext(ctx, `SELECT id::text,question_no,question_type,status FROM question WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status<>'deleted' ORDER BY sort_order,id`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Question{}
	for rows.Next() {
		var item Question
		if err = rows.Scan(&item.ID, &item.QuestionNo, &item.QuestionType, &item.Status); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) PublishTemplateToLibrary(ctx context.Context, scope auth.AccessScope, id string, input PublishTemplateToLibraryInput) (AnswerSheetTemplateLibraryVersion, error) {
	if !libraryPublishInputValid(input) {
		return AnswerSheetTemplateLibraryVersion{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	defer tx.Rollback()
	source, err := libraryTemplate(ctx, tx, scope.TenantID, id)
	if err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	school, err := libraryExamSchool(ctx, tx, scope, source.ExamID)
	if err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	if source.Status != "locked" {
		return AnswerSheetTemplateLibraryVersion{}, ErrTemplateNotLocked
	}
	if source.Revision != input.ExpectedRevision {
		return AnswerSheetTemplateLibraryVersion{}, ErrConflict
	}
	// 同一已锁定版本的并发收录共用事务锁；响应丢失后的重试返回已经存在的不可变版本。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "library/"+id); err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	existing, err := scanLibrary(tx.QueryRowContext(ctx, librarySelect+` WHERE tenant_id=$1 AND source_template_id=$2::uuid AND source_revision=$3`, scope.TenantID, id, source.Revision))
	if err == nil {
		if existing.SchoolID != school {
			return AnswerSheetTemplateLibraryVersion{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	questions, err := libraryQuestions(ctx, tx, scope.TenantID, source.ExamID)
	if err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	layout, err := newLibraryLayout(source, questions)
	if err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	var file FileAssetInput
	var assetID string
	err = tx.QueryRowContext(ctx, `SELECT f.id::text,f.original_name,f.content_type,f.size_bytes,f.hash_sha256,f.storage_bucket,f.storage_key FROM exam_paper p JOIN file_asset f ON f.tenant_id=p.tenant_id AND f.id=p.file_asset_id WHERE p.tenant_id=$1 AND p.id=$2::uuid AND p.exam_id=$3::uuid AND p.purpose='answer_sheet' AND p.deleted_at IS NULL AND f.deleted_at IS NULL AND f.lifecycle_status='active' AND f.submission_id IS NULL AND f.exam_id=p.exam_id AND (f.school_id IS NULL OR f.school_id=$4::uuid) FOR SHARE OF p,f`, scope.TenantID, source.ExamPaperID, source.ExamID, school).Scan(&assetID, &file.OriginalName, &file.ContentType, &file.SizeBytes, &file.HashSHA256, &file.StorageBucket, &file.StorageKey)
	if errors.Is(err, sql.ErrNoRows) {
		return AnswerSheetTemplateLibraryVersion{}, ErrInvalidInput
	}
	if err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	if !validLibraryReference(file) {
		return AnswerSheetTemplateLibraryVersion{}, ErrInvalidInput
	}
	// 同对象可能已有其他资产别名；对象锁与删除触发器共用，不能只保护来源那一行。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "answer-sheet-reference/"+scope.TenantID+"/"+file.StorageBucket+"/"+file.StorageKey); err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	var deleting bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM file_asset WHERE tenant_id=$1 AND storage_bucket=$2 AND storage_key=$3 AND (deleted_at IS NOT NULL OR lifecycle_status IN ('pending_delete','delete_failed','deleted')))`, scope.TenantID, file.StorageBucket, file.StorageKey).Scan(&deleting); err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	if deleting {
		return AnswerSheetTemplateLibraryVersion{}, ErrInvalidInput
	}
	existing, err = scanLibrary(tx.QueryRowContext(ctx, librarySelect+` WHERE tenant_id=$1 AND school_id=$2::uuid AND source_sha256=$3 AND content_hash=$4`, scope.TenantID, school, file.HashSHA256, stableContentHash(layout)))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	var libraryID, referenceID string
	if err = tx.QueryRowContext(ctx, `SELECT gen_random_uuid()::text,gen_random_uuid()::text`).Scan(&libraryID, &referenceID); err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	// 库建立独立的学校参考资产；实例化时再复制元数据到目标考试，禁止搬走旧考试资产归属。
	_, err = tx.ExecContext(ctx, `INSERT INTO file_asset(id,tenant_id,school_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'answer_sheet_template_library',$4::uuid,$5,$6,$7,$8,$9,$10,'private',$11::uuid)`, referenceID, scope.TenantID, school, libraryID, file.OriginalName, file.ContentType, file.SizeBytes, file.HashSHA256, file.StorageBucket, file.StorageKey, scope.ActorID)
	if err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	raw, _ := json.Marshal(layout)
	_, err = tx.ExecContext(ctx, `INSERT INTO answer_sheet_template_library(id,tenant_id,school_id,source_template_id,source_revision,reference_file_asset_id,name,page_count,layout,source_sha256,content_hash,reviewed_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,$9::jsonb,$10,$11,$12::uuid)`, libraryID, scope.TenantID, school, id, source.Revision, referenceID, strings.TrimSpace(input.Name), source.PageCount, raw, file.HashSHA256, stableContentHash(layout), scope.ActorID)
	if err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	out, err := scanLibrary(tx.QueryRowContext(ctx, librarySelect+` WHERE tenant_id=$1 AND id=$2::uuid`, scope.TenantID, libraryID))
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return AnswerSheetTemplateLibraryVersion{}, err
	}
	return out, nil
}

func (s *PostgresStore) InstantiateTemplateLibrary(ctx context.Context, scope auth.AccessScope, examID, libraryID string) (TemplateLibraryInstance, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TemplateLibraryInstance{}, err
	}
	defer tx.Rollback()
	school, err := libraryExamSchool(ctx, tx, scope, examID)
	if err != nil {
		return TemplateLibraryInstance{}, err
	}
	out, err := instantiateLibraryTx(ctx, tx, scope, school, examID, libraryID, "")
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return TemplateLibraryInstance{}, err
	}
	return out, nil
}

func instantiateLibraryTx(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, school, examID, libraryID, paperID string) (TemplateLibraryInstance, error) {
	item, err := scanLibrary(tx.QueryRowContext(ctx, librarySelect+` WHERE tenant_id=$1 AND school_id=$2::uuid AND id=$3::uuid`, scope.TenantID, school, libraryID))
	if err != nil {
		return TemplateLibraryInstance{}, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, examID); err != nil {
		return TemplateLibraryInstance{}, err
	}
	var templateID string
	err = tx.QueryRowContext(ctx, `SELECT template_id::text FROM answer_sheet_template_library_instance WHERE tenant_id=$1 AND exam_id=$2::uuid AND library_id=$3::uuid`, scope.TenantID, examID, libraryID).Scan(&templateID)
	if err == nil {
		t, err := libraryTemplate(ctx, tx, scope.TenantID, templateID)
		if err != nil {
			return TemplateLibraryInstance{}, ErrConflict
		}
		p, err := libraryPaper(ctx, tx, scope.TenantID, examID, t.ExamPaperID)
		if err != nil {
			return TemplateLibraryInstance{}, err
		}
		return TemplateLibraryInstance{Template: t, Paper: p, Reused: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TemplateLibraryInstance{}, err
	}
	// 创建 paper、template 和幂等关联共用考试行锁与事务；任何题号映射失败都不留下半个实例。
	if err = ensureExamPaperMutableTx(ctx, tx, scope.TenantID, examID); err != nil {
		return TemplateLibraryInstance{}, err
	}
	questions, err := libraryQuestions(ctx, tx, scope.TenantID, examID)
	if err != nil {
		return TemplateLibraryInstance{}, err
	}
	layout, err := instantiateLibraryLayout(item, questions)
	if err != nil {
		return TemplateLibraryInstance{}, err
	}
	var p Paper
	if paperID != "" {
		p, err = libraryPaper(ctx, tx, scope.TenantID, examID, paperID)
		if err != nil {
			return TemplateLibraryInstance{}, err
		}
		if p.Purpose != PaperPurposeAnswerSheet || p.File.HashSHA256 != item.SourceSHA256 {
			return TemplateLibraryInstance{}, ErrInvalidInput
		}
	} else {
		p, err = createLibraryReferencePaper(ctx, tx, scope, item, examID)
		if err != nil {
			return TemplateLibraryInstance{}, err
		}
	}
	raw, _ := json.Marshal(layout)
	var t AnswerSheetTemplate
	err = scanTemplate(tx.QueryRowContext(ctx, `INSERT INTO answer_sheet_template(tenant_id,exam_id,exam_paper_id,version_no,name,page_count,layout,content_hash,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,(SELECT COALESCE(MAX(version_no),0)+1 FROM answer_sheet_template WHERE tenant_id=$1 AND exam_id=$2::uuid),$4,$5,$6::jsonb,$7,$8::uuid) RETURNING id::text,tenant_id::text,exam_id::text,exam_paper_id::text,version_no,revision,name,status,page_count,layout,content_hash,created_by::text,COALESCE(locked_by::text,''),locked_at,created_at,updated_at`, scope.TenantID, examID, p.ID, item.Name, item.PageCount, raw, stableContentHash(layout), scope.ActorID), &t)
	if err != nil {
		return TemplateLibraryInstance{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO answer_sheet_template_library_instance(tenant_id,exam_id,library_id,template_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, scope.TenantID, examID, libraryID, t.ID)
	if err != nil {
		return TemplateLibraryInstance{}, err
	}
	return TemplateLibraryInstance{Template: t, Paper: p}, nil
}

func libraryPaper(ctx context.Context, q postgresQueryer, tenantID, examID, id string) (Paper, error) {
	var p Paper
	err := q.QueryRowContext(ctx, `SELECT p.id::text,p.tenant_id::text,p.exam_id::text,p.file_asset_id::text,p.version_no,p.status,p.purpose,f.original_name,f.content_type,f.size_bytes,f.hash_sha256,f.storage_bucket,f.storage_key FROM exam_paper p JOIN file_asset f ON f.tenant_id=p.tenant_id AND f.id=p.file_asset_id WHERE p.tenant_id=$1 AND p.exam_id=$2::uuid AND p.id=$3::uuid AND p.deleted_at IS NULL AND f.deleted_at IS NULL AND f.lifecycle_status='active'`, tenantID, examID, id).Scan(&p.ID, &p.TenantID, &p.ExamID, &p.FileAssetID, &p.VersionNo, &p.Status, &p.Purpose, &p.File.OriginalName, &p.File.ContentType, &p.File.SizeBytes, &p.File.HashSHA256, &p.File.StorageBucket, &p.File.StorageKey)
	if errors.Is(err, sql.ErrNoRows) {
		return Paper{}, ErrNotFound
	}
	return p, err
}

func createLibraryReferencePaper(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, item AnswerSheetTemplateLibraryVersion, examID string) (Paper, error) {
	var p Paper
	p.TenantID, p.ExamID, p.Purpose, p.Status = scope.TenantID, examID, PaperPurposeAnswerSheet, "uploaded"
	err := tx.QueryRowContext(ctx, `SELECT f.original_name,f.content_type,f.size_bytes,f.hash_sha256,f.storage_bucket,f.storage_key FROM answer_sheet_template_library l JOIN file_asset f ON f.tenant_id=l.tenant_id AND f.id=l.reference_file_asset_id WHERE l.tenant_id=$1 AND l.id=$2::uuid AND f.school_id=$3::uuid AND f.exam_id IS NULL AND f.submission_id IS NULL AND f.deleted_at IS NULL AND f.lifecycle_status='active' FOR SHARE OF f`, scope.TenantID, item.ID, item.SchoolID).Scan(&p.File.OriginalName, &p.File.ContentType, &p.File.SizeBytes, &p.File.HashSHA256, &p.File.StorageBucket, &p.File.StorageKey)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrInvalidInput
	}
	if err != nil {
		return p, err
	}
	if !validLibraryReference(p.File) || p.File.HashSHA256 != item.SourceSHA256 {
		return p, ErrInvalidInput
	}
	if err = tx.QueryRowContext(ctx, `SELECT gen_random_uuid()::text,gen_random_uuid()::text,(SELECT COALESCE(MAX(version_no),0)+1 FROM exam_paper WHERE tenant_id=$1 AND exam_id=$2::uuid)`, scope.TenantID, examID).Scan(&p.ID, &p.FileAssetID, &p.VersionNo); err != nil {
		return p, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO file_asset(id,tenant_id,school_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'exam_paper',$5::uuid,$6,$7,$8,$9,$10,$11,'private',$12::uuid)`, p.FileAssetID, scope.TenantID, item.SchoolID, examID, p.ID, p.File.OriginalName, p.File.ContentType, p.File.SizeBytes, p.File.HashSHA256, p.File.StorageBucket, p.File.StorageKey, scope.ActorID)
	if err != nil {
		return p, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO exam_paper(id,tenant_id,exam_id,file_asset_id,version_no,status,uploaded_by,purpose) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,'uploaded',$6::uuid,'answer_sheet')`, p.ID, scope.TenantID, examID, p.FileAssetID, p.VersionNo, scope.ActorID)
	return p, err
}

func (s *PostgresStore) ResolveTemplateLibrary(ctx context.Context, scope auth.AccessScope, examID string, input ResolveTemplateLibraryInput) (TemplateLibraryResolution, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TemplateLibraryResolution{}, err
	}
	defer tx.Rollback()
	school, err := libraryExamSchool(ctx, tx, scope, examID)
	if err != nil {
		return TemplateLibraryResolution{}, err
	}
	p, err := libraryPaper(ctx, tx, scope.TenantID, examID, input.ExamPaperID)
	if err != nil {
		return TemplateLibraryResolution{}, err
	}
	if p.Purpose != PaperPurposeAnswerSheet || !validLibraryReference(p.File) {
		return TemplateLibraryResolution{}, ErrInvalidInput
	}
	candidates, err := listLibrary(ctx, tx, scope.TenantID, school, p.File.HashSHA256)
	if err != nil {
		return TemplateLibraryResolution{}, err
	}
	out := TemplateLibraryResolution{Status: "unknown", Candidates: candidates}
	if len(candidates) > 1 {
		out.Status = "ambiguous"
	}
	if len(candidates) == 1 {
		out.Status = "matched"
		instance, err := instantiateLibraryTx(ctx, tx, scope, school, examID, candidates[0].ID, p.ID)
		if err != nil {
			return TemplateLibraryResolution{}, err
		}
		out.Instance = &instance
	}
	if err = tx.Commit(); err != nil {
		return TemplateLibraryResolution{}, err
	}
	return out, nil
}

func (s *PostgresStore) TemplateLibraryMatchSources(ctx context.Context, scope auth.AccessScope, examID string, input ResolveTemplateLibraryInput) (TemplateLibraryMatchSources, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return TemplateLibraryMatchSources{}, err
	}
	defer tx.Rollback()
	school, err := libraryExamSchool(ctx, tx, scope, examID)
	if err != nil {
		return TemplateLibraryMatchSources{}, err
	}
	p, err := libraryPaper(ctx, tx, scope.TenantID, examID, input.ExamPaperID)
	if err != nil {
		return TemplateLibraryMatchSources{}, err
	}
	if p.Purpose != PaperPurposeAnswerSheet || !validLibraryReference(p.File) {
		return TemplateLibraryMatchSources{}, ErrInvalidInput
	}
	items, err := listLibrary(ctx, tx, scope.TenantID, school, "")
	if err != nil {
		return TemplateLibraryMatchSources{}, err
	}
	questions, err := libraryQuestions(ctx, tx, scope.TenantID, examID)
	if err != nil {
		return TemplateLibraryMatchSources{}, err
	}
	out := TemplateLibraryMatchSources{Source: TemplateLibraryMatchDocument{SourceSHA256: p.File.HashSHA256, File: p.File}, Candidates: []TemplateLibraryMatchDocument{}, Complete: true}
	for _, item := range items {
		if _, err := instantiateLibraryLayout(item, questions); err != nil {
			continue
		}
		if len(out.Candidates) == TemplateLibraryMatchCandidateLimit {
			out.Complete = false
			break
		}
		var file FileAssetInput
		err = tx.QueryRowContext(ctx, `SELECT f.original_name,f.content_type,f.size_bytes,f.hash_sha256,f.storage_bucket,f.storage_key FROM answer_sheet_template_library l JOIN file_asset f ON f.tenant_id=l.tenant_id AND f.id=l.reference_file_asset_id WHERE l.tenant_id=$1 AND l.id=$2::uuid AND f.school_id=$3::uuid AND f.exam_id IS NULL AND f.submission_id IS NULL AND f.deleted_at IS NULL AND f.lifecycle_status='active'`, scope.TenantID, item.ID, school).Scan(&file.OriginalName, &file.ContentType, &file.SizeBytes, &file.HashSHA256, &file.StorageBucket, &file.StorageKey)
		if errors.Is(err, sql.ErrNoRows) {
			return TemplateLibraryMatchSources{}, ErrConflict
		}
		if err != nil {
			return TemplateLibraryMatchSources{}, err
		}
		if !validLibraryReference(file) || file.HashSHA256 != item.SourceSHA256 {
			return TemplateLibraryMatchSources{}, ErrConflict
		}
		out.Candidates = append(out.Candidates, TemplateLibraryMatchDocument{LibraryID: item.ID, PageCount: item.PageCount, SourceSHA256: item.SourceSHA256, Layout: item.Layout, File: file})
	}
	if err = tx.Commit(); err != nil {
		return TemplateLibraryMatchSources{}, err
	}
	return out, nil
}

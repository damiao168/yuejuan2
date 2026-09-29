package questionbank

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type scanner interface{ Scan(...any) error }

const bankCols = `b.id::text,b.tenant_id::text,b.school_id::text,b.name,b.description,b.status,b.revision,b.metadata_schema_version,b.created_by::text,b.created_at,b.updated_at`
const itemCols = `id::text,tenant_id::text,bank_id::text,item_code,subject_code,grade_scope,current_published_version_id::text,status,revision,created_by::text,created_at,kind`
const versionCols = `id::text,tenant_id::text,item_id::text,version_no,schema_version,revision,workflow_status,source_version_id::text,author_id::text,content_hash,content,created_at,updated_at,scoring,bundle_hash`

func scanBank(row scanner) (b Bank, err error) {
	err = row.Scan(&b.ID, &b.TenantID, &b.SchoolID, &b.Name, &b.Description, &b.Status, &b.Revision, &b.MetadataSchemaVersion, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt)
	return b, pgError(err)
}
func scanItem(row scanner) (i Item, err error) {
	err = row.Scan(&i.ID, &i.TenantID, &i.BankID, &i.ItemCode, &i.SubjectCode, &i.GradeScope, &i.CurrentPublishedVersionID, &i.Status, &i.Revision, &i.CreatedBy, &i.CreatedAt, &i.Kind)
	return i, pgError(err)
}
func scanVersion(row scanner) (v Version, err error) {
	var raw, scoring []byte
	err = row.Scan(&v.ID, &v.TenantID, &v.ItemID, &v.VersionNo, &v.SchemaVersion, &v.Revision, &v.WorkflowStatus, &v.SourceVersionID, &v.AuthorID, &v.ContentHash, &raw, &v.CreatedAt, &v.UpdatedAt, &scoring, &v.BundleHash)
	if err != nil {
		return v, pgError(err)
	}
	err = json.Unmarshal(raw, &v.Content)
	if err == nil {
		err = json.Unmarshal(scoring, &v.Scoring)
	}
	v.HashScope = "draft_content_v1"
	v.BundleSchemaVersion = 2
	return
}
func pgError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code == "23505" {
			return ErrConflict
		}
		if pg.Code == "23503" || pg.Code == "23514" || pg.Code == "22P02" {
			return ErrInvalidInput
		}
	}
	return err
}
func (s *PostgresStore) bank(ctx context.Context, q queryer, scope auth.AccessScope, id, action, lock string) (Bank, error) {
	if !validScope(scope) || !validID(id) {
		return Bank{}, ErrNotFound
	}
	return scanBank(q.QueryRowContext(ctx, `SELECT `+bankCols+` FROM question_bank b WHERE b.tenant_id=$1::uuid AND b.id=$2::uuid AND ($3 OR b.school_id::text=ANY($4::text[])) AND question_bank_actor_has_action(b.tenant_id,b.id,$5::uuid,$6) AND ($6='manage' OR question_bank_actor_has_action(b.tenant_id,b.id,$5::uuid,'read'))`+lock, scope.TenantID, id, scope.TenantWide, scope.SchoolIDs, scope.ActorID, action))
}
func (s *PostgresStore) GetBank(ctx context.Context, scope auth.AccessScope, id string) (Bank, error) {
	b, err := s.bank(ctx, s.db, scope, id, "read", "")
	if errors.Is(err, ErrNotFound) {
		return s.bank(ctx, s.db, scope, id, "manage", "")
	}
	return b, err
}
func (s *PostgresStore) item(ctx context.Context, q queryer, scope auth.AccessScope, id, action, lock string) (Item, error) {
	if !validScope(scope) || !validID(id) {
		return Item{}, ErrNotFound
	}
	i, err := scanItem(q.QueryRowContext(ctx, `SELECT `+itemCols+` FROM question_bank_item WHERE tenant_id=$1::uuid AND id=$2::uuid`, scope.TenantID, id))
	if err != nil {
		return i, err
	}
	if _, err = s.bank(ctx, q, scope, i.BankID, action, ""); err != nil {
		return Item{}, err
	}
	if lock != "" {
		return scanItem(q.QueryRowContext(ctx, `SELECT `+itemCols+` FROM question_bank_item WHERE tenant_id=$1::uuid AND id=$2::uuid`+lock, scope.TenantID, id))
	}
	return i, nil
}
func (s *PostgresStore) GetItem(ctx context.Context, scope auth.AccessScope, id string) (Item, error) {
	return s.item(ctx, s.db, scope, id, "read", "")
}
func (s *PostgresStore) version(ctx context.Context, q queryer, scope auth.AccessScope, id, action string) (Version, error) {
	if !validScope(scope) || !validID(id) {
		return Version{}, ErrNotFound
	}
	v, err := scanVersion(q.QueryRowContext(ctx, `SELECT `+versionCols+` FROM question_bank_item_version WHERE tenant_id=$1::uuid AND id=$2::uuid`, scope.TenantID, id))
	if err != nil {
		return v, err
	}
	_, err = s.item(ctx, q, scope, v.ItemID, action, "")
	if err != nil {
		return Version{}, err
	}
	return v, nil
}
func (s *PostgresStore) GetVersion(ctx context.Context, scope auth.AccessScope, id string) (Version, error) {
	v, err := s.version(ctx, s.db, scope, id, "read")
	if err != nil {
		return v, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT (SELECT id::text FROM question_bank_answer_version WHERE tenant_id=$1 AND version_id=$2 AND content_revision=$3),(SELECT id::text FROM question_bank_rubric_version WHERE tenant_id=$1 AND version_id=$2 AND content_revision=$3)`, scope.TenantID, id, v.Revision).Scan(&v.AnswerVersionID, &v.RubricVersionID)
	if err == nil {
		var provenanceRaw []byte
		var createdAt sql.NullTime
		sourceErr := s.db.QueryRowContext(ctx, `WITH RECURSIVE ancestry AS (
	  SELECT id,source_version_id FROM question_bank_item_version WHERE tenant_id=$1::uuid AND id=$2::uuid
	  UNION ALL SELECT v.id,v.source_version_id FROM question_bank_item_version v JOIN ancestry a ON a.source_version_id=v.id WHERE v.tenant_id=$1::uuid
	 ) SELECT i.provenance,i.created_at FROM ancestry a JOIN question_bank_import i ON i.tenant_id=$1::uuid AND i.target_version_id=a.id LIMIT 1`, scope.TenantID, id).Scan(&provenanceRaw, &createdAt)
		if sourceErr == nil {
			var provenance ImportProvenance
			err = json.Unmarshal(provenanceRaw, &provenance)
			provenance.CreatedAt = createdAt.Time
			v.ImportProvenance = &provenance
		} else if !errors.Is(sourceErr, sql.ErrNoRows) {
			err = sourceErr
		}
	}
	return v, err
}

// Every mutation and its receipt commit together; the command lock is acquired
// before bank/item locks. Replays still check the actor's current content ACL.
func pgMutation[T any](ctx context.Context, s *PostgresStore, scope auth.AccessScope, op, target string, input any, action string, fn func(*sql.Tx) (T, error)) (out T, err error) {
	if !validScope(scope) {
		return out, auth.ErrForbidden
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	replay, err := commandreceipt.Load(ctx, tx, scope.TenantID, scope.ActorID, op, target, input, &out)
	if err != nil {
		return out, err
	}
	if replay {
		switch v := any(out).(type) {
		case Bank:
			_, err = s.bank(ctx, tx, scope, v.ID, action, "")
		case ItemResult:
			_, err = s.bank(ctx, tx, scope, v.Item.BankID, action, "")
		case Version:
			_, err = s.item(ctx, tx, scope, v.ItemID, action, "")
		case MaterializeResult:
			err = s.checkExam(ctx, tx, scope, v.ExamID)
			if err == nil {
				for _, id := range v.SourceVersionIDs {
					if _, err = s.version(ctx, tx, scope, id, "read"); err != nil {
						break
					}
				}
			}
		case MetadataSchema:
			_, err = s.bank(ctx, tx, scope, v.BankID, "manage", "")
		case ACLDocument:
			_, err = s.bank(ctx, tx, scope, v.BankID, "manage", "")
		case Item:
			_, err = s.item(ctx, tx, scope, v.ID, action, "")
		case ImportResult:
			_, err = s.bank(ctx, tx, scope, v.Provenance.TargetBankID, "create", "")
			if err == nil && !scope.AllowsExam(v.Provenance.Source.ExamID) {
				err = ErrNotFound
			}
			if err == nil && v.Provenance.LinkedItemID != "" {
				_, err = s.item(ctx, tx, scope, v.Provenance.LinkedItemID, "edit", "")
			}
		}
		if err != nil {
			var zero T
			return zero, err
		}
		return out, tx.Commit()
	}
	out, err = fn(tx)
	if err != nil {
		return out, pgError(err)
	}
	switch v := any(out).(type) {
	case Version:
		err = s.scoringIDs(ctx, tx, scope.TenantID, &v)
		out = any(v).(T)
	case ItemResult:
		err = s.scoringIDs(ctx, tx, scope.TenantID, &v.Version)
		out = any(v).(T)
	case ImportResult:
		err = s.scoringIDs(ctx, tx, scope.TenantID, &v.Version)
		out = any(v).(T)
	}
	if err != nil {
		return out, err
	}
	var aggregate string
	facts := map[string]any{"operation": op, "command_id": commandreceipt.ID(ctx)}
	switch v := any(out).(type) {
	case Bank:
		aggregate = v.ID
		facts["revision"] = v.Revision
	case ItemResult:
		aggregate = v.Item.ID
		facts["version_id"], facts["content_hash"], facts["revision"] = v.Version.ID, v.Version.ContentHash, v.Version.Revision
	case Version:
		aggregate = v.ID
		facts["item_id"], facts["content_hash"], facts["revision"] = v.ItemID, v.ContentHash, v.Revision
	case MaterializeResult:
		aggregate = v.ExamID
		facts["source_version_ids"], facts["revision"] = v.SourceVersionIDs, v.Revision
	case MetadataSchema:
		aggregate = v.BankID
		facts["schema_version"] = v.Version
	case ACLDocument:
		aggregate = v.BankID
		facts["revision"] = v.Revision
	case Item:
		aggregate = v.ID
		facts["bank_id"], facts["revision"], facts["status"] = v.BankID, v.Revision, v.Status
	case ImportResult:
		aggregate = v.Item.ID
		facts["version_id"], facts["content_hash"], facts["bundle_hash"] = v.Version.ID, v.Version.ContentHash, v.Version.BundleHash
		facts["source_exam_id"], facts["source_question_id"] = v.Provenance.Source.ExamID, v.Provenance.Source.QuestionID
		facts["source_snapshot_hash"], facts["source_assessment_hash"] = v.Provenance.Source.SnapshotHash, v.Provenance.Source.AssessmentSnapshotHash
	}
	// Only fingerprints and identities go to audit/outbox, never stems/answers.
	facts["resource_id"] = aggregate
	payload, _ := json.Marshal(facts)
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(tenant_id,actor_id,action,target_type,target_id,after_value) VALUES($1::uuid,$2::uuid,$3,'question_bank',$4::uuid,$5::jsonb)`, scope.TenantID, scope.ActorID, op, aggregate, payload)
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO event_outbox(tenant_id,aggregate_type,aggregate_id,event_type,payload) VALUES($1::uuid,'question_bank',$2::uuid,$3,$4::jsonb)`, scope.TenantID, aggregate, op, payload)
	if err != nil {
		return out, err
	}
	if err = commandreceipt.Save(ctx, tx, scope.TenantID, scope.ActorID, op, target, input, out); err != nil {
		return out, err
	}
	err = tx.Commit()
	return
}
func (s *PostgresStore) CreateBank(ctx context.Context, scope auth.AccessScope, input CreateBankInput) (Bank, error) {
	in, err := normalizeBank(input)
	if err != nil {
		return Bank{}, err
	}
	if !validScope(scope) || !scope.AllowsSchool(in.SchoolID) {
		return Bank{}, auth.ErrForbidden
	}
	return pgMutation(ctx, s, scope, "question_bank.create", "", input, "read", func(tx *sql.Tx) (Bank, error) {
		b, err := scanBank(tx.QueryRowContext(ctx, `INSERT INTO question_bank AS b(tenant_id,school_id,name,description,created_by) SELECT $1::uuid,$2::uuid,$3,$4,$5::uuid FROM school WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='active' AND deleted_at IS NULL RETURNING `+bankCols, scope.TenantID, in.SchoolID, in.Name, in.Description, scope.ActorID))
		if err != nil {
			return b, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO question_bank_acl(tenant_id,bank_id,user_id,action) SELECT $1::uuid,$2::uuid,$3::uuid,action FROM unnest(ARRAY['read','create','edit','manage']) AS action`, scope.TenantID, b.ID, scope.ActorID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO question_bank_metadata_schema(tenant_id,bank_id,version,created_by) VALUES($1,$2,1,$3)`, scope.TenantID, b.ID, scope.ActorID)
		}
		return b, err
	})
}
func (s *PostgresStore) UpdateBank(ctx context.Context, scope auth.AccessScope, id string, in UpdateBankInput) (Bank, error) {
	if in.ExpectedRevision <= 0 || !oneOf(in.Status, "active", "archived") {
		return Bank{}, ErrInvalidInput
	}
	normalized, err := normalizeBank(CreateBankInput{SchoolID: "00000000-0000-0000-0000-000000000001", Name: in.Name, Description: in.Description})
	if err != nil {
		return Bank{}, err
	}
	return pgMutation(ctx, s, scope, "question_bank.update", id, in, "manage", func(tx *sql.Tx) (Bank, error) {
		b, err := s.bank(ctx, tx, scope, id, "manage", " FOR UPDATE OF b")
		if err != nil {
			return b, err
		}
		if b.Revision != in.ExpectedRevision {
			return Bank{}, ErrConflict
		}
		return scanBank(tx.QueryRowContext(ctx, `UPDATE question_bank AS b SET name=$3,description=$4,status=$5,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+bankCols, scope.TenantID, id, normalized.Name, normalized.Description, in.Status))
	})
}
func (s *PostgresStore) CreateItem(ctx context.Context, scope auth.AccessScope, bankID string, input CreateItemInput) (ItemResult, error) {
	if input.Kind == "" {
		input.Kind = "question"
	}
	if !oneOf(input.Kind, "question", "rubric_template") {
		return ItemResult{}, ErrInvalidInput
	}
	content, err := normalizeContent(input.Content)
	if err != nil || !codePattern.MatchString(input.ItemCode) {
		return ItemResult{}, ErrInvalidInput
	}
	op := "question_bank.item.create"
	if input.Kind == "rubric_template" {
		op = "question_bank.template.create"
	}
	return pgMutation(ctx, s, scope, op, bankID, input, "create", func(tx *sql.Tx) (ItemResult, error) {
		b, err := s.bank(ctx, tx, scope, bankID, "create", " FOR SHARE OF b")
		if err != nil {
			return ItemResult{}, err
		}
		if b.Status != "active" {
			return ItemResult{}, ErrLocked
		}
		schema, err := s.metadataSchema(ctx, tx, scope, bankID, b.MetadataSchemaVersion, "create")
		if err != nil {
			return ItemResult{}, err
		}
		if err = validationErr(validateContentMetadata(schema, content, true)); err != nil {
			return ItemResult{}, err
		}
		i, err := scanItem(tx.QueryRowContext(ctx, `INSERT INTO question_bank_item(tenant_id,bank_id,item_code,subject_code,grade_scope,created_by,kind) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6::uuid,$7) RETURNING `+itemCols, scope.TenantID, bankID, input.ItemCode, content.Metadata.SubjectCode, content.Metadata.GradeScope, scope.ActorID, input.Kind))
		if err != nil {
			return ItemResult{}, err
		}
		v, err := s.insertVersion(ctx, tx, scope, i.ID, 1, b.MetadataSchemaVersion, nil, content, emptyScoring())
		return ItemResult{Item: i, Version: v}, err
	})
}
func (s *PostgresStore) insertVersion(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, itemID string, no, schemaVersion int, source *string, content Content, scoring Scoring) (Version, error) {
	// 整包哈希及答案、Rubric 子版本由数据库触发器维护，定义见迁移 000141。
	raw, _ := json.Marshal(content)
	scoreRaw, _ := json.Marshal(scoring)
	return scanVersion(tx.QueryRowContext(ctx, `INSERT INTO question_bank_item_version(tenant_id,item_id,version_no,schema_version,source_version_id,author_id,content,content_hash,scoring) VALUES($1::uuid,$2::uuid,$3,$4,$5::uuid,$6::uuid,$7::jsonb,$8,$9::jsonb) RETURNING `+versionCols, scope.TenantID, itemID, no, schemaVersion, source, scope.ActorID, raw, contentHash(content), scoreRaw))
}
func (s *PostgresStore) CreateVersion(ctx context.Context, scope auth.AccessScope, itemID string, in CreateVersionInput) (Version, error) {
	if !validID(in.SourceVersionID) {
		return Version{}, ErrInvalidInput
	}
	return pgMutation(ctx, s, scope, "question_bank.version.create", itemID, in, "edit", func(tx *sql.Tx) (Version, error) {
		i, err := s.item(ctx, tx, scope, itemID, "edit", "")
		if err != nil {
			return Version{}, err
		}
		b, err := s.bank(ctx, tx, scope, i.BankID, "edit", " FOR SHARE OF b")
		if err != nil {
			return Version{}, err
		}
		i, err = s.item(ctx, tx, scope, itemID, "edit", " FOR UPDATE")
		if err != nil {
			return Version{}, err
		}
		if b.Status != "active" || i.Status != "active" {
			return Version{}, ErrLocked
		}
		source, err := s.version(ctx, tx, scope, in.SourceVersionID, "edit")
		if err != nil {
			return Version{}, err
		}
		if source.ItemID != itemID {
			return Version{}, ErrInvalidInput
		}
		var no int
		// 此处已持有题目行锁，才能安全地用 MAX+1 分配同一题目的下一版本号。
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(version_no),0)+1 FROM question_bank_item_version WHERE tenant_id=$1::uuid AND item_id=$2::uuid`, scope.TenantID, itemID).Scan(&no)
		if err != nil {
			return Version{}, err
		}
		content, err := normalizeContent(source.Content)
		if err != nil {
			return Version{}, err
		}
		return s.insertVersion(ctx, tx, scope, itemID, no, b.MetadataSchemaVersion, &source.ID, content, source.Scoring)
	})
}
func (s *PostgresStore) UpdateVersion(ctx context.Context, scope auth.AccessScope, id string, input UpdateVersionInput) (Version, error) {
	content, err := normalizeContent(input.Content)
	if err != nil || input.ExpectedRevision <= 0 {
		return Version{}, ErrInvalidInput
	}
	return pgMutation(ctx, s, scope, "question_bank.version.update", id, input, "edit", func(tx *sql.Tx) (Version, error) {
		v, err := s.version(ctx, tx, scope, id, "edit")
		if err != nil {
			return Version{}, err
		}
		i, err := s.item(ctx, tx, scope, v.ItemID, "edit", "")
		if err != nil {
			return Version{}, err
		}
		b, err := s.bank(ctx, tx, scope, i.BankID, "edit", " FOR SHARE OF b")
		if err != nil {
			return Version{}, err
		}
		if b.Status != "active" || i.Status != "active" || v.WorkflowStatus != "draft" {
			return Version{}, ErrLocked
		}
		schema, err := s.metadataSchema(ctx, tx, scope, i.BankID, v.SchemaVersion, "edit")
		if err != nil {
			return Version{}, err
		}
		if err = validationErr(validateContentMetadata(schema, content, true)); err != nil {
			return Version{}, err
		}
		raw, _ := json.Marshal(content)
		result, err := scanVersion(tx.QueryRowContext(ctx, `UPDATE question_bank_item_version SET content=$3::jsonb,content_hash=$4,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND revision=$5 AND workflow_status='draft' RETURNING `+versionCols, scope.TenantID, id, raw, contentHash(content), input.ExpectedRevision))
		if errors.Is(err, ErrNotFound) {
			return Version{}, ErrConflict
		}
		return result, err
	})
}

func (s *PostgresStore) ListBanks(ctx context.Context, scope auth.AccessScope, filter Filter) (page BankPage, err error) {
	f := cleanFilter(filter)
	page = BankPage{Banks: []Bank{}, Limit: f.Limit, Offset: f.Offset}
	if !validScope(scope) {
		return page, auth.ErrForbidden
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	where := ` FROM question_bank b WHERE b.tenant_id=$1::uuid AND ($2 OR b.school_id::text=ANY($3::text[])) AND (question_bank_actor_has_action(b.tenant_id,b.id,$4::uuid,'read') OR question_bank_actor_has_action(b.tenant_id,b.id,$4::uuid,'manage')) AND (b.name ILIKE '%'||$5||'%' OR b.description ILIKE '%'||$5||'%')`
	args := []any{scope.TenantID, scope.TenantWide, scope.SchoolIDs, scope.ActorID, f.Query}
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+bankCols+where+` ORDER BY b.created_at DESC,b.id LIMIT $6 OFFSET $7`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		b, e := scanBank(rows)
		if e != nil {
			rows.Close()
			return page, e
		}
		page.Banks = append(page.Banks, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	return page, tx.Commit()
}
func (s *PostgresStore) ListItems(ctx context.Context, scope auth.AccessScope, bankID string, filter Filter) (page ItemPage, err error) {
	f := cleanFilter(filter)
	page = ItemPage{Items: []Item{}, Limit: f.Limit, Offset: f.Offset}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	if _, err = s.bank(ctx, tx, scope, bankID, "read", ""); err != nil {
		return page, err
	}
	kind := f.Kind
	if kind == "" {
		kind = "question"
	}
	where := ` FROM question_bank_item WHERE tenant_id=$1::uuid AND bank_id=$2::uuid AND item_code ILIKE '%'||$3||'%' AND kind=$4`
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+where, scope.TenantID, bankID, f.Query, kind).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+itemCols+where+` ORDER BY created_at DESC,id LIMIT $5 OFFSET $6`, scope.TenantID, bankID, f.Query, kind, f.Limit, f.Offset)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		i, e := scanItem(rows)
		if e != nil {
			rows.Close()
			return page, e
		}
		page.Items = append(page.Items, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	return page, tx.Commit()
}
func (s *PostgresStore) ListVersions(ctx context.Context, scope auth.AccessScope, itemID string, filter Filter) (page VersionPage, err error) {
	f := cleanFilter(filter)
	page = VersionPage{Versions: []Version{}, Limit: f.Limit, Offset: f.Offset}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	if _, err = s.item(ctx, tx, scope, itemID, "read", ""); err != nil {
		return page, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM question_bank_item_version WHERE tenant_id=$1::uuid AND item_id=$2::uuid`, scope.TenantID, itemID).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+versionCols+` FROM question_bank_item_version WHERE tenant_id=$1::uuid AND item_id=$2::uuid ORDER BY version_no DESC LIMIT $3 OFFSET $4`, scope.TenantID, itemID, f.Limit, f.Offset)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		v, e := scanVersion(rows)
		if e != nil {
			rows.Close()
			return page, e
		}
		page.Versions = append(page.Versions, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	return page, tx.Commit()
}

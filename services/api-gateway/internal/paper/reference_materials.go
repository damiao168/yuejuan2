package paper

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	_ "image/jpeg"
	"time"
)

const referenceCropTransform = "reference-crop-png-v2"

// ReferenceMaterialsService 只复用已登记的来源与文件存储；请求中的不透明句柄
// 每次都从数据库重新解析，不接受客户端指定对象地址或学生原卷。
type ReferenceMaterialsService struct {
	db      *sql.DB
	assets  files.Store
	objects files.ObjectStorage
	bucket  string
}

func NewReferenceMaterialsService(db *sql.DB, assets files.Store, objects files.ObjectStorage, bucket string) *ReferenceMaterialsService {
	if db == nil || assets == nil || objects == nil {
		return nil
	}
	return &ReferenceMaterialsService{db, assets, objects, bucket}
}

func referenceHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s", len([]byte(p)), p)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Get 固定考试后通过确认快照找清单，草稿才读最新修订；历史 NULL 只读投影未记录。
func (s *ReferenceMaterialsService) Get(ctx context.Context, tenant, exam, question string) (ReferenceMaterials, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT e.status FROM exam e JOIN question q ON q.tenant_id=e.tenant_id AND q.exam_id=e.id WHERE e.tenant_id=$1 AND e.id=$2 AND q.id=$3 AND q.deleted_at IS NULL`, tenant, exam, question).Scan(&status)
	if err != nil {
		return ReferenceMaterials{}, err
	}
	if status == "draft" || status == "configured" {
		return loadReferenceMaterials(ctx, s.db, tenant, exam, question, "")
	}
	var id sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT q->'reference_materials'->>'manifest_id' FROM exam_readiness_snapshot r CROSS JOIN LATERAL jsonb_array_elements(r.import_snapshot_json->'questions') q WHERE r.tenant_id=$1 AND r.exam_id=$2 AND r.status='passed' AND q->>'id'=$3 ORDER BY r.confirmed_at DESC LIMIT 1`, tenant, exam, question).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !id.Valid {
		return ReferenceMaterials{Mode: "legacy_unrecorded", Frozen: true, Items: []ReferenceMaterialItem{}}, nil
	}
	if err != nil {
		return ReferenceMaterials{}, err
	}
	m, err := loadReferenceMaterials(ctx, s.db, tenant, exam, question, id.String)
	m.Frozen = true
	return m, err
}

func loadReferenceMaterials(ctx context.Context, q postgresQueryer, tenant, exam, question, id string) (ReferenceMaterials, error) {
	m := ReferenceMaterials{Mode: "legacy_unrecorded", Items: []ReferenceMaterialItem{}}
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT id::text,mode,revision,content_hash,authorization_revision,use_state,created_by::text,created_at FROM question_reference_manifest WHERE tenant_id=$1 AND exam_id=$2 AND question_id=$3 AND ($4='' OR id::text=$4) ORDER BY revision DESC LIMIT 1`, tenant, exam, question, id).Scan(&m.ID, &m.Mode, &m.Revision, &m.ContentHash, &m.AuthorizationRevision, &m.UseState, &m.CreatedBy, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if id != "" {
			return m, ErrNotFound
		}
		return m, nil
	}
	if err != nil {
		return m, err
	}
	rows, err := q.QueryContext(ctx, `SELECT payload,derived_file_asset_id::text,source_file_asset_id::text,page_file_asset_id::text FROM question_reference_manifest_item WHERE tenant_id=$1 AND manifest_id=$2 ORDER BY ordinal`, tenant, m.ID)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ReferenceMaterialItem
		var derived, source, page string
		if err = rows.Scan(&raw, &derived, &source, &page); err != nil {
			return m, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return m, err
		}
		item.FileAssetID = derived
		item.Source.SourceAssetID = source
		item.Source.PageAssetID = page
		m.Items = append(m.Items, item)
	}
	return m, rows.Err()
}

func (s *ReferenceMaterialsService) Save(ctx context.Context, scope auth.AccessScope, exam, question string, input SaveReferenceMaterialsInput) (ReferenceMaterials, error) {
	m := ReferenceMaterials{}
	if !referenceCommandValid(input.CommandID) || input.ExpectedRevision < 0 || !(input.Mode == "text_only" && len(input.Items) == 0 || input.Mode == "reference_images" && len(input.Items) > 0 && len(input.Items) <= 4) {
		return m, ErrInvalidInput
	}
	ctx = commandreceipt.WithID(ctx, input.CommandID)
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return m, e
	}
	defer tx.Rollback()
	identity := commandreceipt.Identity{TenantID: scope.TenantID, ActorID: scope.ActorID, Operation: "reference.save", TargetID: exam + "/" + question}
	if found, e := commandreceipt.Load(ctx, tx, identity, input, &m); e != nil || found {
		return m, e
	}
	if e = ensureExamPaperMutableTx(ctx, tx, scope.TenantID, exam); e != nil {
		return m, e
	}
	previous, e := loadReferenceMaterials(ctx, tx, scope.TenantID, exam, question, "")
	if e != nil {
		return m, e
	}
	if previous.Revision != input.ExpectedRevision {
		return m, ErrConflict
	}
	m = ReferenceMaterials{ID: uuid.NewString(), Mode: input.Mode, Revision: previous.Revision + 1, AuthorizationRevision: 1, UseState: "active", CreatedBy: scope.ActorID, CreatedAt: time.Now().UTC(), Items: []ReferenceMaterialItem{}}
	seen := map[string]bool{}
	roles := map[string]int{}
	parts := []string{"material-manifest-v1", input.Mode}
	grants := []map[string]any{}
	inheritedParent := ""
	// 同一事务按资产身份加共享锁；保存只接受 active 的本题派生图及匹配的来源摘要。
	for i, entry := range input.Items {
		if seen[entry.CropRef] || entry.Ordinal != i+1 || (entry.Role != "question_diagram" && entry.Role != "reference_solution") {
			return m, ErrInvalidInput
		}
		seen[entry.CropRef] = true
		roles[entry.Role]++
		if roles[entry.Role] > 2 || entry.Role == "question_diagram" && roles["reference_solution"] > 0 {
			return m, ErrInvalidInput
		}
		var raw []byte
		var derived string
		e = tx.QueryRowContext(ctx, `SELECT reference_crop_json,id::text FROM file_asset WHERE tenant_id=$1 AND id::text=$2 AND exam_id=$3 AND reference_crop_json->>'question_id'=$4 AND owner_type='question_reference_crop' AND lifecycle_status='active' AND deleted_at IS NULL FOR SHARE`, scope.TenantID, entry.CropRef, exam, question).Scan(&raw, &derived)
		var metadata struct {
			Item       ReferenceMaterialItem `json:"item"`
			SourceID   string                `json:"source_id"`
			PageID     string                `json:"page_id"`
			SourceHash string                `json:"source_hash"`
			QuestionID string                `json:"question_id"`
		}
		inherited := false
		if errors.Is(e, sql.ErrNoRows) && previous.ID != "" {
			// 已获目标考试授权的继承裁图可重排或保留；不重新要求操作者拥有原始文件权限。
			for _, old := range previous.Items {
				if old.CropRef == entry.CropRef {
					binding := &ReferenceMaterialImport{SourceExamID: exam, SourceQuestionID: question, ReferenceMaterialBinding: *referenceMaterialBinding(previous), Items: previous.Items}
					if err := LockReferenceMaterialImportTx(ctx, tx, scope.TenantID, binding); err != nil {
						return m, err
					}
					metadata.Item, metadata.SourceID, metadata.PageID, metadata.SourceHash = old, old.Source.SourceAssetID, old.Source.PageAssetID, old.Source.SourceHash
					derived = old.FileAssetID
					inherited, inheritedParent, e = true, previous.ID, nil
					break
				}
			}
		}
		if e != nil {
			return m, e
		}
		if !inherited {
			if e = json.Unmarshal(raw, &metadata); e != nil {
				return m, e
			}
		}
		item := metadata.Item
		item.FileAssetID = derived
		item.ID = uuid.NewString()
		item.Role = entry.Role
		item.Ordinal = entry.Ordinal
		item.Required = true
		item.Source.SourceAssetID = metadata.SourceID
		item.Source.PageAssetID = metadata.PageID
		item.Source.SourceHash = metadata.SourceHash
		if !inherited {
			if e = referenceSourceCurrentTx(ctx, tx, scope.TenantID, exam, item.Source); e != nil {
				return m, e
			}
		}
		if !inherited {
			if reader, ok := s.assets.(interface {
				GetScopedTx(context.Context, *sql.Tx, auth.AccessScope, string) (files.FileAsset, error)
			}); ok {
				if _, e = reader.GetScopedTx(ctx, tx, scope, metadata.SourceID); e != nil {
					return m, e
				}
			} else {
				return m, ErrInvalidInput
			}
		}
		for _, ref := range []struct{ id, hash string }{{metadata.SourceID, metadata.SourceHash}, {metadata.PageID, item.Source.SHA256}} {
			var actual string
			e = tx.QueryRowContext(ctx, `SELECT hash_sha256 FROM file_asset WHERE tenant_id=$1 AND id=$2 AND lifecycle_status='active' AND deleted_at IS NULL FOR SHARE`, scope.TenantID, ref.id).Scan(&actual)
			if e != nil {
				return m, e
			}
			if actual != ref.hash {
				return m, ErrConflict
			}
		}
		m.Items = append(m.Items, item)
		parts = append(parts, item.Role, fmt.Sprint(item.Ordinal), item.Source.PageRef, item.Source.SourceHash, item.Source.SHA256, item.SHA256, fmt.Sprint(item.Box.X), fmt.Sprint(item.Box.Y), fmt.Sprint(item.Box.Width), fmt.Sprint(item.Box.Height), fmt.Sprint(item.SizeBytes), referenceCropTransform)
		grants = append(grants, map[string]any{"source_kind": item.Source.Kind, "source_file_asset_id": metadata.SourceID, "source_sha256": metadata.SourceHash, "source_version": item.Source.Identity, "exam_id": exam, "authorized_by": scope.ActorID, "purpose": "frozen_exam_reference"})
	}
	m.ContentHash = referenceHash(parts...)
	inputJSON, _ := json.Marshal(input)
	grantFacts := map[string]any{"schema_version": 1, "exam_id": exam, "sources": grants}
	if inheritedParent != "" {
		grantFacts["parent_manifest_id"] = inheritedParent
	}
	grantJSON, _ := json.Marshal(grantFacts)
	inserted, e := tx.ExecContext(ctx, `INSERT INTO question_reference_manifest(id,tenant_id,exam_id,question_id,revision,previous_manifest_id,mode,content_hash,command_id,request_hash,created_by,created_at,exam_use_grant_json,origin_manifest_id) SELECT $1,$2,$3,q.id,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13,NULLIF($14,'')::uuid FROM question q WHERE q.tenant_id=$2 AND q.exam_id=$3 AND q.id=$4 AND q.deleted_at IS NULL`, m.ID, scope.TenantID, exam, question, m.Revision, previous.ID, m.Mode, m.ContentHash, input.CommandID, referenceHash(string(inputJSON)), scope.ActorID, m.CreatedAt, grantJSON, inheritedParent)
	if e != nil {
		return m, e
	}
	// INSERT SELECT 没有匹配题目时不会报 SQL 错误，不能因此提交一个不存在清单的成功回执。
	if count, err := inserted.RowsAffected(); err != nil {
		return m, err
	} else if count != 1 {
		return m, ErrNotFound
	}
	for _, item := range m.Items {
		raw, _ := json.Marshal(item)
		_, e = tx.ExecContext(ctx, `INSERT INTO question_reference_manifest_item(id,tenant_id,manifest_id,ordinal,role,source_file_asset_id,page_file_asset_id,derived_file_asset_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, item.ID, scope.TenantID, m.ID, item.Ordinal, item.Role, item.Source.SourceAssetID, item.Source.PageAssetID, item.FileAssetID, raw)
		if e != nil {
			return m, e
		}
	}
	if e = commandreceipt.Save(ctx, tx, identity, input, m); e != nil {
		return m, e
	}
	return m, tx.Commit()
}

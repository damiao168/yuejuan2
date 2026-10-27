package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ReferenceMaterialImport 保留原冻结清单及展示信息；它不是通用附件，也不接受评分编辑请求重绑。
// 使用权由题库版本的独立授权记录授予，来源清单撤回仍实时阻断所有派生使用。
type ReferenceMaterialImport struct {
	SourceExamID     string `json:"source_exam_id"`
	SourceQuestionID string `json:"source_question_id"`
	ReferenceMaterialBinding
	Items []ReferenceMaterialItem `json:"items"`
}

// LoadReferenceMaterialImport 只解析冻结的准确绑定，预览和正式导入使用同一套内容核验。
func LoadReferenceMaterialImport(ctx context.Context, q postgresQueryer, tenant, exam, question string, binding *ReferenceMaterialBinding) (*ReferenceMaterialImport, error) {
	if binding == nil || binding.Mode != "reference_images" {
		return nil, ErrInvalidInput
	}
	m, err := loadReferenceMaterials(ctx, q, tenant, exam, question, binding.ID)
	if err != nil {
		return nil, err
	}
	if m.Mode != binding.Mode || m.Revision != binding.Revision || m.ContentHash != binding.ContentHash || len(m.Items) != binding.ImageCount || len(m.Items) < 1 || len(m.Items) > 4 {
		return nil, ErrConflict
	}
	available, err := referenceMaterialsAvailable(ctx, q, tenant, exam, Question{ID: question, ReferenceMaterials: binding})
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, ErrConflict
	}
	return &ReferenceMaterialImport{SourceExamID: exam, SourceQuestionID: question, ReferenceMaterialBinding: *binding, Items: m.Items}, nil
}

// LockReferenceMaterialImportTx 锁定父授权链及引用的文件，撤回或隔离若先提交则拒绝本次转移。
// 只取来源清单共享锁，不写来源考试；归档历史可以继续只读复用。
func LockReferenceMaterialImportTx(ctx context.Context, tx *sql.Tx, tenant string, imported *ReferenceMaterialImport) error {
	if imported == nil {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE chain AS (
 SELECT id,origin_manifest_id FROM question_reference_manifest WHERE tenant_id=$1 AND id=$2
 UNION SELECT m.id,m.origin_manifest_id FROM question_reference_manifest m JOIN chain c ON m.id=c.origin_manifest_id WHERE m.tenant_id=$1
) SELECT m.id::text FROM question_reference_manifest m JOIN chain c ON c.id=m.id WHERE m.tenant_id=$1 ORDER BY m.id FOR SHARE OF m`, tenant, imported.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT f.id::text FROM file_asset f WHERE f.tenant_id=$1 AND f.id IN(
 SELECT unnest(ARRAY[i.source_file_asset_id,i.page_file_asset_id,i.derived_file_asset_id]) FROM question_reference_manifest_item i WHERE i.tenant_id=$1 AND i.manifest_id=$2
) ORDER BY f.id FOR SHARE`, tenant, imported.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	_, err = LoadReferenceMaterialImport(ctx, tx, tenant, imported.SourceExamID, imported.SourceQuestionID, &imported.ReferenceMaterialBinding)
	return err
}

// MaterializeBankReferenceTx 为本次明确复制动作创建目标考试的清单授权，图片及父授权身份保持不变。
// 调用方已检查目标题库版本读取权与目标考试编辑权，数据库同时要求准确版本的授权记录。
func MaterializeBankReferenceTx(ctx context.Context, tx *sql.Tx, tenant, exam, question, actor, version string, imported *ReferenceMaterialImport) (*ReferenceMaterialBinding, error) {
	if imported == nil {
		return nil, nil
	}
	if err := LockReferenceMaterialImportTx(ctx, tx, tenant, imported); err != nil {
		return nil, err
	}
	full, err := LoadReferenceMaterialImport(ctx, tx, tenant, imported.SourceExamID, imported.SourceQuestionID, &imported.ReferenceMaterialBinding)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	grants := []map[string]any{}
	for _, item := range full.Items {
		grants = append(grants, map[string]any{"source_kind": item.Source.Kind, "source_file_asset_id": item.Source.SourceAssetID, "source_sha256": item.Source.SourceHash, "source_version": item.Source.Identity, "exam_id": exam, "authorized_by": actor, "purpose": "frozen_exam_reference"})
	}
	grantJSON, _ := json.Marshal(map[string]any{"schema_version": 1, "exam_id": exam, "parent_manifest_id": full.ID, "bank_version_id": version, "sources": grants})
	command := "bank-reference-" + uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO question_reference_manifest(id,tenant_id,exam_id,question_id,revision,mode,content_hash,command_id,request_hash,created_by,created_at,exam_use_grant_json,origin_manifest_id) VALUES($1,$2,$3,$4,1,'reference_images',$5,$6,$7,$8,$9,$10,$11)`, id, tenant, exam, question, full.ContentHash, command, referenceHash(command, version, full.ID), actor, time.Now().UTC(), grantJSON, full.ID); err != nil {
		return nil, err
	}
	for _, item := range full.Items {
		item.ID = uuid.NewString()
		raw, _ := json.Marshal(item)
		if _, err = tx.ExecContext(ctx, `INSERT INTO question_reference_manifest_item(id,tenant_id,manifest_id,ordinal,role,source_file_asset_id,page_file_asset_id,derived_file_asset_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, item.ID, tenant, id, item.Ordinal, item.Role, item.Source.SourceAssetID, item.Source.PageAssetID, item.FileAssetID, raw); err != nil {
			return nil, fmt.Errorf("copy bank reference item: %w", err)
		}
	}
	return &ReferenceMaterialBinding{ID: id, Mode: full.Mode, Revision: 1, ContentHash: full.ContentHash, ImageCount: len(full.Items)}, nil
}

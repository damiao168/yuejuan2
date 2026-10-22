package paper

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
)

// 这里只核对已登记的版本和文件事实，不读取对象内容，也不把历史空绑定补成 text_only。
func referenceMaterialsAvailable(ctx context.Context, q postgresQueryer, tenant, exam string, question Question) (bool, error) {
	b := question.ReferenceMaterials
	if b == nil {
		return true, nil
	}
	var available bool
	err := q.QueryRowContext(ctx, `SELECT m.use_state='active' AND m.content_hash=$5 AND m.revision=$6 AND m.mode=$7
 AND (SELECT count(*) FROM question_reference_manifest_item i WHERE i.tenant_id=m.tenant_id AND i.manifest_id=m.id)=$8
 AND NOT EXISTS(SELECT 1 FROM question_reference_manifest_item i JOIN file_asset f ON f.tenant_id=i.tenant_id AND f.id IN(i.source_file_asset_id,i.page_file_asset_id,i.derived_file_asset_id)
 WHERE i.tenant_id=m.tenant_id AND i.manifest_id=m.id AND (f.lifecycle_status<>'active' OR f.deleted_at IS NOT NULL
 OR (f.id=i.source_file_asset_id AND f.hash_sha256<>i.payload->'source'->>'source_sha256')
 OR (f.id=i.page_file_asset_id AND f.hash_sha256<>i.payload->'source'->>'sha256')
 OR (f.id=i.derived_file_asset_id AND f.hash_sha256<>i.payload->>'sha256')))
 FROM question_reference_manifest m WHERE m.tenant_id=$1 AND m.exam_id=$2 AND m.question_id=$3 AND m.id=$4`, tenant, exam, question.ID, b.ID, b.ContentHash, b.Revision, b.Mode, b.ImageCount).Scan(&available)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return available, err
}

func withReferenceReadiness(ctx context.Context, q postgresQueryer, tenant, exam string, questions []Question, result ReadinessResult) (ReadinessResult, error) {
	for _, question := range questions {
		if question.ReferenceMaterials != nil {
			result.referenceQuestions = append(result.referenceQuestions, question)
		}
	}
	if len(result.referenceQuestions) == 0 {
		return result, nil
	}
	check := ReadinessCheck{Code: "reference_materials", Label: "本题参考材料", Passed: true, Severity: "blocker", Section: "questions", Message: "参考材料版本与文件状态可用"}
	for _, question := range result.referenceQuestions {
		available, err := referenceMaterialsAvailable(ctx, q, tenant, exam, question)
		if err != nil {
			return result, err
		}
		if !available {
			check.Passed = false
			check.Message = "参考材料已撤回或文件不可用，请核对本题材料"
			result.Ready = false
		}
	}
	result.Checks = append(result.Checks, check)
	return result, nil
}

// 确认和开考在考试锁内，按清单 ID、文件 ID 顺序持共享锁至提交。
// 撤回或隔离若先完成则阻断；若后取得锁，冻结保留原内容而后续试改仍受实时授权约束。
func lockReadinessReferenceMaterials(ctx context.Context, tx *sql.Tx, tenant, exam string, questions []Question) error {
	ids := []string{}
	for _, question := range questions {
		if question.ReferenceMaterials != nil {
			ids = append(ids, question.ReferenceMaterials.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	for _, id := range ids {
		var actual string
		if err := tx.QueryRowContext(ctx, `SELECT id::text FROM question_reference_manifest WHERE tenant_id=$1 AND exam_id=$2 AND id=$3 FOR SHARE`, tenant, exam, id).Scan(&actual); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM file_asset WHERE tenant_id=$1 AND id IN(SELECT unnest(ARRAY[i.source_file_asset_id,i.page_file_asset_id,i.derived_file_asset_id]) FROM question_reference_manifest_item i WHERE i.tenant_id=$1 AND i.manifest_id::text=ANY(string_to_array($2,','))) ORDER BY id FOR SHARE`, tenant, strings.Join(ids, ","))
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
	for _, question := range questions {
		available, e := referenceMaterialsAvailable(ctx, tx, tenant, exam, question)
		if e != nil {
			return e
		}
		if !available {
			return ErrNotReady
		}
	}
	return nil
}

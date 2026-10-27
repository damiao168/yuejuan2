package questionbank

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// 正式导入命令已核对来源考试和目标题库权限；授权只能绑定该命令创建的准确题库版本。
func (s *PostgresStore) grantImportedReferences(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, bank, version, provenance string, binding *paper.ReferenceMaterialImport) error {
	if binding == nil {
		return nil
	}
	raw, _ := json.Marshal(binding)
	_, err := tx.ExecContext(ctx, `INSERT INTO question_bank_reference_grant(tenant_id,version_id,bank_id,source_manifest_id,source_exam_id,source_question_id,reference_materials,origin_import_id,authorized_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, scope.TenantID, version, bank, binding.ID, binding.SourceExamID, binding.SourceQuestionID, raw, provenance, scope.ActorID)
	return err
}

func (s *PostgresStore) copyReferenceGrant(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, source, target string, binding *paper.ReferenceMaterialImport) error {
	if binding == nil {
		return nil
	}
	if err := paper.LockReferenceMaterialImportTx(ctx, tx, scope.TenantID, binding); err != nil {
		return ErrSourceUnavailable
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO question_bank_reference_grant(tenant_id,version_id,bank_id,source_manifest_id,source_exam_id,source_question_id,reference_materials,origin_import_id,source_version_id,authorized_by)
 SELECT tenant_id,$3,bank_id,source_manifest_id,source_exam_id,source_question_id,reference_materials,origin_import_id,version_id,$4 FROM question_bank_reference_grant WHERE tenant_id=$1 AND version_id=$2`, scope.TenantID, source, target, scope.ActorID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return ErrSourceUnavailable
	}
	return nil
}

// 评分编辑可以省略只读引用，不能删除或替换已导入的授权链，也不能给普通草稿注入清单句柄。
func preserveImportedReferences(current, incoming Scoring) (Scoring, error) {
	if incoming.ReferenceMaterials != nil && !reflect.DeepEqual(current.ReferenceMaterials, incoming.ReferenceMaterials) {
		return incoming, ErrInvalidInput
	}
	incoming.ReferenceMaterials = copyScoring(current).ReferenceMaterials
	return incoming, nil
}

func validateReferenceGrant(ctx context.Context, q queryer, tenant, version string, binding *paper.ReferenceMaterialImport) error {
	if binding == nil {
		return nil
	}
	raw, _ := json.Marshal(binding)
	var valid bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM question_bank_reference_grant WHERE tenant_id=$1 AND version_id=$2 AND reference_materials=$3::jsonb)`, tenant, version, raw).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrSourceUnavailable
	}
	return nil
}

// ReferenceMaterials 先执行题库的版本读取边界，再核对独立授权及原冻结清单。
// 题库读者只获得本版本已授予的裁图，不因此获得来源考试或原始文件的浏览权限。
func (s *PostgresStore) ReferenceMaterials(ctx context.Context, scope auth.AccessScope, version string) (*paper.ReferenceMaterialImport, error) {
	v, err := s.version(ctx, s.db, scope, version, "read")
	if err != nil {
		return nil, err
	}
	if v.Scoring.ReferenceMaterials == nil {
		return nil, ErrNotFound
	}
	if err = validateReferenceGrant(ctx, s.db, scope.TenantID, version, v.Scoring.ReferenceMaterials); err != nil {
		return nil, err
	}
	full, err := paper.LoadReferenceMaterialImport(ctx, s.db, scope.TenantID, v.Scoring.ReferenceMaterials.SourceExamID, v.Scoring.ReferenceMaterials.SourceQuestionID, &v.Scoring.ReferenceMaterials.ReferenceMaterialBinding)
	if err != nil {
		return nil, ErrSourceUnavailable
	}
	return full, nil
}

func (h *Handler) WithReferenceMaterials(service *paper.ReferenceMaterialsService) *Handler {
	h.referenceMaterials = service
	return h
}

func (h *Handler) GetReferenceMaterialImage(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	reader, ok := h.store.(interface {
		ReferenceMaterials(context.Context, auth.AccessScope, string) (*paper.ReferenceMaterialImport, error)
	})
	if !ok || h.referenceMaterials == nil {
		writeError(w, r, ErrUnavailable)
		return
	}
	binding, err := reader.ReferenceMaterials(r.Context(), scope, r.PathValue("versionId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	_, images, err := h.referenceMaterials.Resolve(r.Context(), scope.TenantID, binding.SourceExamID, binding.SourceQuestionID, &binding.ReferenceMaterialBinding)
	if err != nil {
		writeError(w, r, ErrSourceUnavailable)
		return
	}
	for _, image := range images {
		if image.Item.ID == r.PathValue("itemId") {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(image.Data)
			return
		}
	}
	writeError(w, r, ErrNotFound)
}

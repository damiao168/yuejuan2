package paper

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"errors"
)

func (s *ReferenceMaterialsService) ForSnapshot(ctx context.Context, tenant, exam, question, snapshot string) (ReferenceMaterials, error) {
	var id sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT item->'reference_materials'->>'manifest_id' FROM exam_readiness_snapshot r JOIN exam_question_snapshot snap ON snap.tenant_id=r.tenant_id AND snap.exam_id=r.exam_id CROSS JOIN LATERAL jsonb_array_elements(r.import_snapshot_json->'questions') item WHERE r.tenant_id=$1 AND r.exam_id=$2 AND snap.question_id=$3::uuid AND snap.id=$4 AND r.status='passed' AND item->>'id'=$3::text AND item->>'assessment_snapshot_id'=snap.id::text AND item->>'assessment_snapshot_hash'=snap.content_hash ORDER BY r.confirmed_at DESC LIMIT 1`, tenant, exam, question, snapshot).Scan(&id)
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
func (s *ReferenceMaterialsService) FrozenImage(ctx context.Context, tenant, exam, question, snapshot, itemID string) ([]byte, error) {
	m, err := s.ForSnapshot(ctx, tenant, exam, question, snapshot)
	if err != nil {
		return nil, err
	}
	if m.Mode != "reference_images" || m.UseState != "active" {
		return nil, ErrConflict
	}
	// 完整清单重新核验，不能从 item ID 直接扩大为任意文件读取。
	_, images, err := s.Resolve(ctx, tenant, exam, question, referenceMaterialBinding(m))
	if err != nil {
		return nil, err
	}
	for _, image := range images {
		if image.Item.ID == itemID {
			return image.Data, nil
		}
	}
	return nil, ErrNotFound
}
func (s *ReferenceMaterialsService) ScoringRunSnapshot(ctx context.Context, scope auth.AccessScope, run, question string) (string, string, error) {
	var exam, snapshot string
	err := s.db.QueryRowContext(ctx, `SELECT sr.exam_id::text,snap.id::text FROM scoring_run sr JOIN question q ON q.tenant_id=sr.tenant_id AND q.exam_id=sr.exam_id JOIN LATERAL(SELECT id FROM exam_question_snapshot s WHERE s.tenant_id=q.tenant_id AND s.exam_id=q.exam_id AND s.question_id=q.id ORDER BY snapshot_version DESC LIMIT 1) snap ON true WHERE sr.tenant_id=$1 AND sr.id=$2 AND q.id=$3 AND sr.deleted_at IS NULL AND q.deleted_at IS NULL`, scope.TenantID, run, question).Scan(&exam, &snapshot)
	if err != nil {
		return "", "", err
	}
	if !scope.AllowsExam(exam) {
		return "", "", files.ErrForbidden
	}
	return exam, snapshot, nil
}

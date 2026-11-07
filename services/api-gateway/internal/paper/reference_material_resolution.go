package paper

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/files"
)

type ResolvedReferenceImage struct {
	Item ReferenceMaterialItem
	Data []byte
}

// Resolve 只接收已冻结的准确绑定；考试使用权不随原题库个人浏览角色变化，
// 显式撤回或源/page/派生资产隔离仍阻断。读取后再次核验，不能通过同名对象换图。
func (s *ReferenceMaterialsService) Resolve(ctx context.Context, tenant, exam, question string, binding *ReferenceMaterialBinding) (ReferenceMaterials, []ResolvedReferenceImage, error) {
	if binding == nil {
		return ReferenceMaterials{}, nil, nil
	}
	m, err := loadReferenceMaterials(ctx, s.db, tenant, exam, question, binding.ID)
	if err != nil {
		return m, nil, err
	}
	if m.UseState != "active" || m.ContentHash != binding.ContentHash || m.Revision != binding.Revision || m.Mode != binding.Mode || len(m.Items) != binding.ImageCount {
		return m, nil, ErrConflict
	}
	var authorizationActive bool
	if err = s.db.QueryRowContext(ctx, `SELECT reference_authorization_active($1,$2)`, tenant, m.ID).Scan(&authorizationActive); err != nil {
		return m, nil, err
	}
	if !authorizationActive {
		return m, nil, ErrConflict
	}
	out := make([]ResolvedReferenceImage, 0, len(m.Items))
	for _, item := range m.Items {
		for _, ref := range []struct{ id, hash string }{{item.Source.SourceAssetID, item.Source.SourceHash}, {item.Source.PageAssetID, item.Source.SHA256}, {item.FileAssetID, item.SHA256}} {
			a, e := s.assets.Get(ctx, tenant, ref.id)
			if e != nil {
				return m, nil, e
			}
			if a.Lifecycle != files.LifecycleActive || a.DeletedAt != nil || a.HashSHA256 != ref.hash {
				return m, nil, ErrConflict
			}
		}
		a, e := s.assets.Get(ctx, tenant, item.FileAssetID)
		if e != nil {
			return m, nil, e
		}
		data, e := s.readImage(ctx, a)
		if e != nil {
			return m, nil, e
		}
		out = append(out, ResolvedReferenceImage{item, data})
	}
	latest, err := loadReferenceMaterials(ctx, s.db, tenant, exam, question, m.ID)
	if err != nil {
		return m, nil, err
	}
	if latest.AuthorizationRevision != m.AuthorizationRevision || latest.UseState != "active" {
		return m, nil, ErrConflict
	}
	if err = s.db.QueryRowContext(ctx, `SELECT reference_authorization_active($1,$2)`, tenant, m.ID).Scan(&authorizationActive); err != nil {
		return m, nil, err
	}
	if !authorizationActive {
		return m, nil, ErrConflict
	}
	return m, out, nil
}

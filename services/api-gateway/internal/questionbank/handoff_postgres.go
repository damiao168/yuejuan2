package questionbank

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"errors"
)

func readHandoff(ctx context.Context, q queryer, tenant, id string) (*HandoffState, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT to_jsonb(h)-'tenant_id'-'version_id' FROM question_bank_handoff h WHERE tenant_id=$1 AND version_id=$2`, tenant, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h HandoffState
	err = json.Unmarshal(raw, &h)
	return &h, err
}
func (s *PostgresStore) postgresRecipient(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, bankID, recipient, action string) error {
	var allowed bool
	// 选中的人员必须仍启用、同时具备账号职责与题库动作；沿用当前题库锁防止ACL在交接中途撤销。
	err := tx.QueryRowContext(ctx, `SELECT question_bank_actor_has_action(u.tenant_id,$2::uuid,u.id,$4) AND question_bank_actor_has_action(u.tenant_id,$2::uuid,u.id,'read')
 AND NOT EXISTS(SELECT needed FROM unnest(ARRAY['read',$4]) needed WHERE NOT EXISTS(
 SELECT 1 FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
 JOIN role_permission rp ON rp.tenant_id=r.tenant_id AND rp.role_id=r.id AND rp.deleted_at IS NULL
 JOIN permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id AND p.deleted_at IS NULL
 WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL AND p.code='question_bank:'||needed))
 FROM app_user u WHERE u.tenant_id=$1 AND u.id=$3::uuid AND u.status='active' AND u.deleted_at IS NULL FOR SHARE OF u`, scope.TenantID, bankID, recipient, action).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
		return ErrInvalidInput
	}
	return err
}
func saveHandoff(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, v *Version, h *HandoffState, kind, recipient string) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO question_bank_handoff(tenant_id,version_id,revision,reviewer_id,publisher_id,claimed_by,claimed_at,updated_at)
 VALUES($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,$7,$8)
 ON CONFLICT(tenant_id,version_id) DO UPDATE SET revision=EXCLUDED.revision,reviewer_id=EXCLUDED.reviewer_id,publisher_id=EXCLUDED.publisher_id,claimed_by=EXCLUDED.claimed_by,claimed_at=EXCLUDED.claimed_at,updated_at=EXCLUDED.updated_at
 WHERE question_bank_handoff.revision=$9`, scope.TenantID, v.ID, h.Revision, h.ReviewerID, h.PublisherID, h.ClaimedBy, h.ClaimedAt, h.UpdatedAt, h.Revision-1)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO question_bank_handoff_event(tenant_id,version_id,handoff_revision,content_revision,kind,actor_id,recipient_id) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid)`, scope.TenantID, v.ID, h.Revision, v.Revision, kind, scope.ActorID, recipient)
	if err == nil {
		v.Handoff = h
	}
	return err
}
func (s *PostgresStore) AssignHandoff(ctx context.Context, scope auth.AccessScope, id string, in HandoffInput) (Version, error) {
	action := "edit"
	if in.Manage {
		action = "manage"
		// 同时拥有两种全局职责时，缺少本题库管理ACL仍可按作者编辑职责处理；事务内重新检查选定动作。
		if in.AuthorEdit {
			if _, err := s.version(ctx, s.db, scope, id, "manage"); err != nil {
				action = "edit"
			}
		}
	}
	return pgMutation(ctx, s, scope, pgMutationCommand{op: "question_bank.handoff.assign", target: id, input: in, action: action}, func(tx *sql.Tx) (Version, error) {
		v, i, err := s.lockedVersion(ctx, tx, scope, id, action)
		if err != nil {
			return v, err
		}
		if _, err = s.bank(ctx, tx, scope, i.BankID, "read", ""); err != nil {
			return Version{}, err
		}
		if action != "manage" && v.AuthorID != scope.ActorID {
			return v, auth.ErrForbidden
		}
		h, err := handoffAssignment(v, in)
		if err != nil {
			return v, err
		}
		if err = s.postgresRecipient(ctx, tx, scope, i.BankID, h.ReviewerID, "review"); err != nil {
			return v, err
		}
		if err = s.postgresRecipient(ctx, tx, scope, i.BankID, h.PublisherID, "publish"); err != nil {
			return v, err
		}
		candidate := v
		candidate.Handoff = &h
		err = saveHandoff(ctx, tx, scope, &v, &h, "reassigned", handoffRecipient(candidate))
		return v, err
	})
}
func (s *PostgresStore) ClaimHandoff(ctx context.Context, scope auth.AccessScope, id, action string, in HandoffInput) (Version, error) {
	return pgMutation(ctx, s, scope, pgMutationCommand{op: "question_bank.handoff.claim." + action, target: id, input: in, action: action}, func(tx *sql.Tx) (Version, error) {
		v, _, err := s.lockedVersion(ctx, tx, scope, id, action)
		if err != nil {
			return v, err
		}
		h, err := handoffClaim(v, scope.ActorID, action, in)
		if err != nil {
			return v, err
		}
		err = saveHandoff(ctx, tx, scope, &v, &h, "claimed", scope.ActorID)
		return v, err
	})
}
func (s *PostgresStore) ListHandoffEvents(ctx context.Context, scope auth.AccessScope, id string) ([]HandoffEvent, error) {
	if _, err := s.version(ctx, s.db, scope, id, "read"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,version_id::text,handoff_revision,content_revision,kind,actor_id::text,COALESCE(recipient_id::text,''),created_at FROM question_bank_handoff_event WHERE tenant_id=$1 AND version_id=$2 ORDER BY created_at DESC,id DESC LIMIT 100`, scope.TenantID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HandoffEvent{}
	for rows.Next() {
		var e HandoffEvent
		if err = rows.Scan(&e.ID, &e.VersionID, &e.HandoffRevision, &e.ContentRevision, &e.Kind, &e.ActorID, &e.RecipientID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

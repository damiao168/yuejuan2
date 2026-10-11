package questionbank

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type Member struct {
	UserID      string   `json:"user_id"`
	DisplayName string   `json:"display_name"`
	Username    string   `json:"username,omitempty"`
	Actions     []string `json:"actions"`
}

// ListMembers 只提供本题库可授权或可联系的成员，不借题库权限开放全租户通讯录。
// 管理者可选同校启用人员；内容读者只能看到本题库已有审核/发布权限的人。
func (s *PostgresStore) ListMembers(ctx context.Context, scope auth.AccessScope, bankID string) ([]Member, error) {
	bank, err := s.bank(ctx, s.db, scope, bankID, "read", "")
	if err != nil {
		bank, err = s.bank(ctx, s.db, scope, bankID, "manage", "")
	}
	if err != nil {
		return nil, err
	}
	_, manageErr := s.bank(ctx, s.db, scope, bankID, "manage", "")
	// 题库授权与账号职责同时生效；只拥有题库 ACL、但已被撤销审核职责的人不能被提示为可审核人员。
	rows, err := s.db.QueryContext(ctx, `WITH available AS (
 SELECT u.id,u.school_id,u.display_name,u.username,ARRAY(SELECT a.action
 FROM unnest(ARRAY['read','create','edit','review','publish','retire','statistics','manage']) a(action)
 WHERE question_bank_actor_has_action(u.tenant_id,$2::uuid,u.id,a.action)
 AND EXISTS(SELECT 1 FROM user_role ur
 JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
 JOIN role_permission rp ON rp.tenant_id=r.tenant_id AND rp.role_id=r.id AND rp.deleted_at IS NULL
 JOIN permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id AND p.deleted_at IS NULL
 WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL AND p.code='question_bank:'||a.action)
 ORDER BY a.action) AS actions
 FROM app_user u WHERE u.tenant_id=$1::uuid AND u.status='active' AND u.deleted_at IS NULL)
 SELECT id::text,display_name,CASE WHEN $4 THEN username ELSE '' END,to_jsonb(actions) FROM available
 WHERE ($4 AND school_id=$3::uuid) OR ('read'=ANY(actions) AND ('review'=ANY(actions) OR 'publish'=ANY(actions)))
 ORDER BY display_name,id`, scope.TenantID, bankID, bank.SchoolID, manageErr == nil)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// 账号用于管理者区分同名人员；普通内容读者的联系人视图不额外返回登录账号。
	members := []Member{}
	for rows.Next() {
		var member Member
		var raw []byte
		if err = rows.Scan(&member.UserID, &member.DisplayName, &member.Username, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &member.Actions); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (s *MemoryStore) ListMembers(_ context.Context, scope auth.AccessScope, bankID string) ([]Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.bank(scope, bankID, "read")
	if err != nil {
		_, err = s.bank(scope, bankID, "manage")
	}
	if err != nil {
		return nil, err
	}
	_, manageErr := s.bank(scope, bankID, "manage")
	byID := map[string]*Member{}
	prefix := scope.TenantID + ":" + bankID + ":"
	for key, enabled := range s.acl {
		if !enabled || !strings.HasPrefix(key, prefix) {
			continue
		}
		parts := strings.Split(key, ":")
		if len(parts) != 4 {
			continue
		}
		member := byID[parts[2]]
		if member == nil {
			member = &Member{UserID: parts[2], DisplayName: "已授权成员", Actions: []string{}}
			byID[parts[2]] = member
		}
		member.Actions = append(member.Actions, parts[3])
	}
	members := []Member{}
	for _, member := range byID {
		if manageErr == nil || isReviewContact(member.Actions) {
			sort.Strings(member.Actions)
			members = append(members, *member)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].UserID < members[j].UserID })
	return members, nil
}

// 内容读者只能联系同时有读取和审核/发布授权的成员；管理者的成员视图由调用方单独判定。
func isReviewContact(actions []string) bool {
	read, review := false, false
	for _, action := range actions {
		read = read || action == "read"
		review = review || action == "review" || action == "publish"
	}
	return read && review
}

func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	members, err := h.store.ListMembers(r.Context(), scope, r.PathValue("bankId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"members": members})
}

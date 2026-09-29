package questionbank

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func presetActions(preset string) ([]string, bool) {
	switch preset {
	case "Viewer":
		return []string{"read", "statistics"}, true
	case "Author":
		return []string{"read", "create", "edit"}, true
	case "Reviewer":
		return []string{"read", "review"}, true
	case "Publisher":
		return []string{"read", "publish", "retire"}, true
	case "Manager":
		// 管理题库结构和授权不等于能读题；内容读取仍需单独的 read 权限。
		return []string{"manage"}, true
	default:
		return nil, false
	}
}

func presetForActions(actions []string) string {
	sort.Strings(actions)
	for _, preset := range []string{"Viewer", "Author", "Reviewer", "Publisher", "Manager"} {
		want, _ := presetActions(preset)
		sort.Strings(want)
		if strings.Join(actions, ",") == strings.Join(want, ",") {
			return preset
		}
	}
	return "Custom"
}

func (s *PostgresStore) metadataSchema(ctx context.Context, q queryer, scope auth.AccessScope, bankID string, version int, action string) (MetadataSchema, error) {
	b, err := s.bank(ctx, q, scope, bankID, action, "")
	if err != nil {
		return MetadataSchema{}, err
	}
	if version <= 0 {
		version = b.MetadataSchemaVersion
	}
	var out MetadataSchema
	var fields, taxonomies []byte
	err = q.QueryRowContext(ctx, `SELECT bank_id::text,version,fields,taxonomies,created_by::text,created_at FROM question_bank_metadata_schema WHERE tenant_id=$1 AND bank_id=$2 AND version=$3`, scope.TenantID, bankID, version).Scan(&out.BankID, &out.Version, &fields, &taxonomies, &out.CreatedBy, &out.CreatedAt)
	if err != nil {
		return out, pgError(err)
	}
	if err = json.Unmarshal(fields, &out.Fields); err == nil {
		err = json.Unmarshal(taxonomies, &out.Taxonomies)
	}
	return out, err
}

func (s *PostgresStore) GetMetadataSchema(ctx context.Context, scope auth.AccessScope, bankID string, version int) (MetadataSchema, error) {
	out, err := s.metadataSchema(ctx, s.db, scope, bankID, version, "read")
	if errors.Is(err, ErrNotFound) {
		return s.metadataSchema(ctx, s.db, scope, bankID, version, "manage")
	}
	return out, err
}

func (s *PostgresStore) UpdateMetadataSchema(ctx context.Context, scope auth.AccessScope, bankID string, in UpdateMetadataSchemaInput) (MetadataSchema, error) {
	fields, taxonomies, err := normalizeSchema(in.Fields, in.Taxonomies)
	if err != nil || in.ExpectedRevision <= 0 {
		if err != nil {
			return MetadataSchema{}, err
		}
		return MetadataSchema{}, ErrInvalidInput
	}
	in.Fields, in.Taxonomies = fields, taxonomies
	return pgMutation(ctx, s, scope, "question_bank.metadata_schema.update", bankID, in, "manage", func(tx *sql.Tx) (MetadataSchema, error) {
		b, err := s.bank(ctx, tx, scope, bankID, "manage", " FOR UPDATE OF b")
		if err != nil {
			return MetadataSchema{}, err
		}
		if b.Revision != in.ExpectedRevision {
			return MetadataSchema{}, ErrConflict
		}
		if b.Status != "active" {
			return MetadataSchema{}, ErrLocked
		}
		version := b.MetadataSchemaVersion + 1
		// schema 只追加新版本，不改旧规则；历史题目的 SchemaVersion 仍指向原版。
		fieldsRaw, _ := json.Marshal(fields)
		taxonomiesRaw, _ := json.Marshal(taxonomies)
		var out MetadataSchema
		var rawFields, rawTaxonomies []byte
		err = tx.QueryRowContext(ctx, `INSERT INTO question_bank_metadata_schema(tenant_id,bank_id,version,fields,taxonomies,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING bank_id::text,version,fields,taxonomies,created_by::text,created_at`, scope.TenantID, bankID, version, fieldsRaw, taxonomiesRaw, scope.ActorID).Scan(&out.BankID, &out.Version, &rawFields, &rawTaxonomies, &out.CreatedBy, &out.CreatedAt)
		if err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE question_bank SET metadata_schema_version=$3,revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2`, scope.TenantID, bankID, version); err != nil {
			return out, err
		}
		out.Fields, out.Taxonomies = fields, taxonomies
		return out, nil
	})
}

func (s *PostgresStore) ValidateMetadata(ctx context.Context, scope auth.AccessScope, bankID string, in ValidateMetadataInput) (MetadataValidationResult, error) {
	schema, err := s.GetMetadataSchema(ctx, scope, bankID, in.SchemaVersion)
	if err != nil {
		return MetadataValidationResult{}, err
	}
	return validateMetadataValues(schema, copyMetadataValues(in.Values), true), nil
}

func validateACLInput(in UpdateACLInput) error {
	if in.ExpectedRevision <= 0 || len(in.Bindings) > 500 || len(in.Groups) > 100 {
		return ErrInvalidInput
	}
	users, groups := map[string]bool{}, map[string]bool{}
	for _, b := range in.Bindings {
		if !validID(b.UserID) || users[b.UserID] {
			return ErrInvalidInput
		}
		if _, ok := presetActions(b.Preset); !ok {
			return ErrInvalidInput
		}
		users[b.UserID] = true
	}
	for _, g := range in.Groups {
		if g.ID != "" && !validID(g.ID) || strings.TrimSpace(g.Name) == "" || !validText(strings.TrimSpace(g.Name), 160) || groups[g.Name] || len(g.MemberIDs) > 500 {
			return ErrInvalidInput
		}
		if _, ok := presetActions(g.Preset); !ok {
			return ErrInvalidInput
		}
		members := map[string]bool{}
		for _, id := range g.MemberIDs {
			if !validID(id) || members[id] {
				return ErrInvalidInput
			}
			members[id] = true
		}
		groups[g.Name] = true
	}
	return nil
}

func (s *PostgresStore) aclDocument(ctx context.Context, q queryer, scope auth.AccessScope, bankID string) (ACLDocument, error) {
	b, err := s.bank(ctx, q, scope, bankID, "manage", "")
	out := ACLDocument{BankID: bankID, Bindings: []ACLBinding{}, Groups: []ACLGroup{}}
	if err != nil {
		return out, err
	}
	out.Revision = b.Revision
	rows, err := q.QueryContext(ctx, `SELECT user_id::text,to_jsonb(array_agg(action ORDER BY action)) FROM question_bank_acl WHERE tenant_id=$1 AND bank_id=$2 GROUP BY user_id ORDER BY user_id`, scope.TenantID, bankID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var binding ACLBinding
		var actions []byte
		if err = rows.Scan(&binding.UserID, &actions); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal(actions, &binding.Actions); err != nil {
			rows.Close()
			return out, err
		}
		binding.Preset = presetForActions(append([]string{}, binding.Actions...))
		out.Bindings = append(out.Bindings, binding)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = q.QueryContext(ctx, `SELECT g.id::text,g.name,to_jsonb(array_agg(DISTINCT a.action ORDER BY a.action)),COALESCE(to_jsonb(array_agg(DISTINCT m.user_id::text ORDER BY m.user_id::text) FILTER(WHERE m.user_id IS NOT NULL)),'[]'::jsonb) FROM question_bank_group g JOIN question_bank_group_acl a ON a.tenant_id=g.tenant_id AND a.group_id=g.id LEFT JOIN question_bank_group_member m ON m.tenant_id=g.tenant_id AND m.group_id=g.id WHERE g.tenant_id=$1 AND g.bank_id=$2 GROUP BY g.id,g.name ORDER BY g.name,g.id`, scope.TenantID, bankID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var group ACLGroup
		var actions, members []byte
		if err = rows.Scan(&group.ID, &group.Name, &actions, &members); err != nil {
			return out, err
		}
		if err = json.Unmarshal(actions, &group.Actions); err == nil {
			err = json.Unmarshal(members, &group.MemberIDs)
		}
		if err != nil {
			return out, err
		}
		group.Preset = presetForActions(append([]string{}, group.Actions...))
		out.Groups = append(out.Groups, group)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetACL(ctx context.Context, scope auth.AccessScope, bankID string) (ACLDocument, error) {
	return s.aclDocument(ctx, s.db, scope, bankID)
}

func (s *PostgresStore) UpdateACL(ctx context.Context, scope auth.AccessScope, bankID string, in UpdateACLInput) (ACLDocument, error) {
	if err := validateACLInput(in); err != nil {
		return ACLDocument{}, err
	}
	return pgMutation(ctx, s, scope, "question_bank.acl.update", bankID, in, "manage", func(tx *sql.Tx) (ACLDocument, error) {
		b, err := s.bank(ctx, tx, scope, bankID, "manage", " FOR UPDATE OF b")
		if err != nil {
			return ACLDocument{}, err
		}
		if b.Revision != in.ExpectedRevision {
			return ACLDocument{}, ErrConflict
		}
		if b.Status != "active" {
			return ACLDocument{}, ErrLocked
		}
		ids := []string{}
		for _, binding := range in.Bindings {
			ids = append(ids, binding.UserID)
		}
		for _, group := range in.Groups {
			ids = append(ids, group.MemberIDs...)
		}
		if len(ids) > 0 {
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT count(DISTINCT id) FROM app_user WHERE tenant_id=$1 AND id::text=ANY($2::text[]) AND status='active' AND deleted_at IS NULL`, scope.TenantID, ids).Scan(&count); err != nil || count != len(uniqueStrings(ids)) {
				if err != nil {
					return ACLDocument{}, err
				}
				return ACLDocument{}, ErrInvalidInput
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO question_bank_acl(tenant_id,bank_id,user_id,action) VALUES($1,$2,$3,'manage') ON CONFLICT DO NOTHING`, scope.TenantID, bankID, scope.ActorID); err != nil {
			return ACLDocument{}, err
		}
		// 保留创建者和操作者的直接授权，其他直接授权及分组按本次请求整体重建。
		if _, err = tx.ExecContext(ctx, `DELETE FROM question_bank_acl WHERE tenant_id=$1 AND bank_id=$2 AND user_id<>$3 AND user_id<>$4`, scope.TenantID, bankID, b.CreatedBy, scope.ActorID); err != nil {
			return ACLDocument{}, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM question_bank_group WHERE tenant_id=$1 AND bank_id=$2`, scope.TenantID, bankID); err != nil {
			return ACLDocument{}, err
		}
		for _, binding := range in.Bindings {
			actions, _ := presetActions(binding.Preset)
			for _, action := range actions {
				if _, err = tx.ExecContext(ctx, `INSERT INTO question_bank_acl(tenant_id,bank_id,user_id,action) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, scope.TenantID, bankID, binding.UserID, action); err != nil {
					return ACLDocument{}, err
				}
			}
		}
		for _, group := range in.Groups {
			id := group.ID
			if id == "" {
				id = uuid.NewString()
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO question_bank_group(id,tenant_id,bank_id,name,created_by) VALUES($1,$2,$3,$4,$5)`, id, scope.TenantID, bankID, strings.TrimSpace(group.Name), scope.ActorID); err != nil {
				return ACLDocument{}, err
			}
			actions, _ := presetActions(group.Preset)
			for _, action := range actions {
				if _, err = tx.ExecContext(ctx, `INSERT INTO question_bank_group_acl(tenant_id,bank_id,group_id,action) VALUES($1,$2,$3,$4)`, scope.TenantID, bankID, id, action); err != nil {
					return ACLDocument{}, err
				}
			}
			for _, memberID := range group.MemberIDs {
				if _, err = tx.ExecContext(ctx, `INSERT INTO question_bank_group_member(tenant_id,group_id,user_id) VALUES($1,$2,$3)`, scope.TenantID, id, memberID); err != nil {
					return ACLDocument{}, err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE question_bank SET revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2`, scope.TenantID, bankID); err != nil {
			return ACLDocument{}, err
		}
		return s.aclDocument(ctx, tx, scope, bankID)
	})
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func cleanSearchFilter(f SearchFilter) (SearchFilter, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	if f.Offset < 0 || f.Offset > 1000000 || !validText(f.Query, 128) {
		return f, ErrInvalidInput
	}
	if f.Mode == "" {
		f.Mode = "default"
	}
	if f.Sort == "" {
		f.Sort = "updated_desc"
	}
	if !oneOf(f.Mode, "default", "published", "my_drafts", "all") || !oneOf(f.Sort, "updated_desc", "created_desc", "item_code_asc") {
		return f, ErrInvalidInput
	}
	if f.BankID != "" && !validID(f.BankID) || f.MetadataKey != "" && !metadataKeyPattern.MatchString(f.MetadataKey) {
		return f, ErrInvalidInput
	}
	return f, nil
}

func scanSearch(row scanner) (SearchItem, error) {
	var out SearchItem
	var itemRaw, contentRaw, scoringRaw []byte
	err := row.Scan(&itemRaw, &out.Version.ID, &out.Version.TenantID, &out.Version.ItemID, &out.Version.VersionNo, &out.Version.SchemaVersion, &out.Version.Revision, &out.Version.WorkflowStatus, &out.Version.SourceVersionID, &out.Version.AuthorID, &out.Version.ContentHash, &contentRaw, &out.Version.CreatedAt, &out.Version.UpdatedAt, &scoringRaw, &out.Version.BundleHash)
	if err != nil {
		return out, pgError(err)
	}
	if err = json.Unmarshal(itemRaw, &out.Item); err == nil {
		err = json.Unmarshal(contentRaw, &out.Version.Content)
	}
	if err == nil {
		err = json.Unmarshal(scoringRaw, &out.Version.Scoring)
	}
	out.Version.HashScope, out.Version.BundleSchemaVersion = "draft_content_v1", 2
	out.StatisticsAvailable = false
	return out, err
}

const searchVersionCols = `v.id::text,v.tenant_id::text,v.item_id::text,v.version_no,v.schema_version,v.revision,v.workflow_status,v.source_version_id::text,v.author_id::text,v.content_hash,v.content,v.created_at,v.updated_at,v.scoring,v.bundle_hash`

func (s *PostgresStore) SearchItems(ctx context.Context, scope auth.AccessScope, filter SearchFilter) (page SearchPage, err error) {
	f, err := cleanSearchFilter(filter)
	page = SearchPage{Items: []SearchItem{}, Limit: f.Limit, Offset: f.Offset}
	if err != nil {
		return page, err
	}
	if !validScope(scope) {
		return page, auth.ErrForbidden
	}
	stats := -1
	// 统计功能目前没有数据来源；显式要求 statistics_available=true 时返回空集。
	if f.StatisticsAvailable != nil {
		if *f.StatisticsAvailable {
			stats = 1
		} else {
			stats = 0
		}
	}
	// 总数与明细共用范围条件：先过滤学校及读权限，默认展示当前发布版和本人草稿。
	where := ` FROM question_bank_item_version v JOIN question_bank_item i ON i.tenant_id=v.tenant_id AND i.id=v.item_id JOIN question_bank b ON b.tenant_id=i.tenant_id AND b.id=i.bank_id
WHERE v.tenant_id=$1::uuid AND ($2 OR b.school_id::text=ANY($3::text[])) AND question_bank_actor_has_action(b.tenant_id,b.id,$4::uuid,'read') AND b.status='active' AND i.status='active' AND i.kind='question'
AND (($5='default' AND (v.id=i.current_published_version_id OR (v.workflow_status='draft' AND v.author_id=$4::uuid))) OR ($5='published' AND v.id=i.current_published_version_id) OR ($5='my_drafts' AND v.workflow_status='draft' AND v.author_id=$4::uuid) OR $5='all')
AND ($6='' OR i.item_code ILIKE '%'||$6||'%' OR v.content->>'stem' ILIKE '%'||$6||'%') AND ($7='' OR i.bank_id::text=$7) AND ($8='' OR i.subject_code=$8)
AND ($9='' OR v.content->'knowledge_points' ? $9) AND ($10='' OR v.content->>'question_type'=$10) AND ($11='' OR v.content->>'assessment_archetype'=$11) AND ($12='' OR v.workflow_status=$12)
AND ($13='' OR v.content->'metadata'->>'difficulty_band'=$13) AND ($14='' OR v.content->'metadata'->>'cognitive_level'=$14) AND ($15='' OR v.content->'metadata'->>'copyright'=$15)
AND ($16='' OR v.content->'metadata'->>'intended_use'=$16) AND ($17='' OR v.scoring->>'use_policy'=$17) AND ($18='' OR v.content->'custom_metadata'->>$18=$19) AND $20<>1`
	args := []any{scope.TenantID, scope.TenantWide, scope.SchoolIDs, scope.ActorID, f.Mode, f.Query, f.BankID, f.SubjectCode, f.KnowledgePoint, f.QuestionType, f.Archetype, f.WorkflowStatus, f.DifficultyBand, f.CognitiveLevel, f.Copyright, f.IntendedUse, f.UsePolicy, f.MetadataKey, f.MetadataValue, stats}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	order := `v.updated_at DESC,v.id`
	if f.Sort == "created_desc" {
		order = `v.created_at DESC,v.id`
	}
	if f.Sort == "item_code_asc" {
		order = `i.item_code,i.id,v.version_no DESC,v.id`
	}
	rows, err := tx.QueryContext(ctx, `SELECT to_jsonb(i),`+searchVersionCols+where+` ORDER BY `+order+` LIMIT $21 OFFSET $22`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		item, scanErr := scanSearch(rows)
		if scanErr != nil {
			rows.Close()
			return page, scanErr
		}
		page.Items = append(page.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	return page, tx.Commit()
}

func (s *PostgresStore) RetireItem(ctx context.Context, scope auth.AccessScope, itemID string, in RetireItemInput) (Item, error) {
	if in.ExpectedRevision <= 0 {
		return Item{}, ErrInvalidInput
	}
	return pgMutation(ctx, s, scope, "question_bank.item.retire", itemID, in, "retire", func(tx *sql.Tx) (Item, error) {
		i, err := s.item(ctx, tx, scope, itemID, "retire", " FOR UPDATE")
		if err != nil {
			return i, err
		}
		if i.Revision != in.ExpectedRevision {
			return i, ErrConflict
		}
		if i.Status != "active" {
			return i, ErrLocked
		}
		return scanItem(tx.QueryRowContext(ctx, `UPDATE question_bank_item SET status='retired',revision=revision+1 WHERE tenant_id=$1 AND id=$2 RETURNING `+itemCols, scope.TenantID, itemID))
	})
}

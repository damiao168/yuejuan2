package questionbank

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func (s *PostgresStore) scoringIDs(ctx context.Context, q queryer, tenant string, v *Version) error {
	return q.QueryRowContext(ctx, `SELECT (SELECT id::text FROM question_bank_answer_version WHERE tenant_id=$1 AND version_id=$2 AND content_revision=$3),(SELECT id::text FROM question_bank_rubric_version WHERE tenant_id=$1 AND version_id=$2 AND content_revision=$3)`, tenant, v.ID, v.Revision).Scan(&v.AnswerVersionID, &v.RubricVersionID)
}

func (s *PostgresStore) lockedVersion(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, id, action string) (Version, Item, error) {
	// 按题库、题目、版本的顺序取锁，再重读版本，防止编辑与审核基于不同状态写入。
	v, err := s.version(ctx, tx, scope, id, action)
	if err != nil {
		return v, Item{}, err
	}
	i, err := s.item(ctx, tx, scope, v.ItemID, action, "")
	if err != nil {
		return v, i, err
	}
	b, err := s.bank(ctx, tx, scope, i.BankID, action, bankShareLockClause)
	if err != nil {
		return v, i, err
	}
	i, err = s.item(ctx, tx, scope, v.ItemID, action, " FOR UPDATE")
	if err != nil {
		return v, i, err
	}
	v, err = scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionCols+` FROM question_bank_item_version WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.TenantID, id))
	if err == nil && (b.Status != "active" || i.Status != "active") {
		err = ErrLocked
	}
	return v, i, err
}
func (s *PostgresStore) UpdateScoring(ctx context.Context, scope auth.AccessScope, id string, in UpdateScoringInput) (Version, error) {
	scoring, err := normalizeScoring(in.Scoring)
	if err != nil || in.ExpectedRevision <= 0 {
		return Version{}, ErrInvalidInput
	}
	return pgMutation(ctx, s, scope, pgMutationCommand{op: "question_bank.scoring.update", target: id, input: in, action: "edit"}, func(tx *sql.Tx) (Version, error) {
		v, i, err := s.lockedVersion(ctx, tx, scope, id, "edit")
		if err != nil {
			return v, err
		}
		if v.WorkflowStatus != "draft" {
			return v, ErrLocked
		}
		if v.Revision != in.ExpectedRevision {
			return v, ErrConflict
		}
		scoring, err = s.resolveScoringTemplate(ctx, tx, scope, i, scoring)
		if err != nil {
			return v, err
		}
		for _, a := range scoring.Assets {
			// 资产和读取 ACL 使用当前变更事务；文件共享锁保留到评分事实一起提交。
			f, err := files.NewPostgresStore(s.db).GetScopedTx(ctx, tx, scope, a.FileAssetID)
			if err != nil {
				if errors.Is(err, files.ErrNotFound) || errors.Is(err, files.ErrForbidden) {
					return v, ErrInvalidInput
				}
				return v, err
			}
			if f.HashSHA256 != a.SHA256 || f.OriginalName != a.Name || f.ContentType != a.ContentType {
				return v, ErrInvalidInput
			}
		}
		raw, _ := json.Marshal(scoring)
		return scanVersion(tx.QueryRowContext(ctx, `UPDATE question_bank_item_version SET scoring=$3,revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2 RETURNING `+versionCols, scope.TenantID, id, raw))
	})
}

func (s *PostgresStore) resolveScoringTemplate(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, item Item, scoring Scoring) (Scoring, error) {
	if scoring.TemplateVersionID == nil {
		return scoring, nil
	}
	source, err := s.version(ctx, tx, scope, *scoring.TemplateVersionID, "read")
	if err != nil {
		return scoring, err
	}
	si, err := s.item(ctx, tx, scope, source.ItemID, "read", "")
	if err != nil {
		return scoring, err
	}
	b, err := s.bank(ctx, tx, scope, si.BankID, "read", bankShareLockClause)
	if err != nil {
		return scoring, err
	}
	if item.Kind == "rubric_template" || si.Kind != "rubric_template" || source.WorkflowStatus != "published" || source.Scoring.Rubric == nil || si.Status != "active" || b.Status != "active" {
		return scoring, ErrInvalidInput
	}
	// 只复制已发布模板的 Rubric；模板来源引用不会替换当前版本的答案或解析。
	scoring.Rubric = copyScoring(source.Scoring).Rubric
	return scoring, nil
}

func (s *PostgresStore) Transition(ctx context.Context, scope auth.AccessScope, id, decision string, in ReviewInput) (Version, error) {
	v, err := s.version(ctx, s.db, scope, id, "read")
	if err != nil {
		return v, err
	}
	action := transitionAction(decision, v.AuthorID == scope.ActorID)
	return pgMutation(ctx, s, scope, pgMutationCommand{op: "question_bank." + decision, target: id, input: in, action: action}, func(tx *sql.Tx) (Version, error) {
		v, i, err := s.lockedVersion(ctx, tx, scope, id, action)
		if err != nil {
			return v, err
		}
		if decision == "submit-review" || decision == "publish" {
			schema, schemaErr := s.metadataSchema(ctx, tx, scope, i.BankID, v.SchemaVersion, action)
			if schemaErr != nil {
				return v, schemaErr
			}
			if schemaErr = validationErr(validateContentMetadata(schema, v.Content, true)); schemaErr != nil {
				return v, schemaErr
			}
		}
		status, err := nextStatus(v, i.Kind, scope.ActorID, decision, in)
		if err != nil {
			return v, err
		}
		var reviewID string
		err = tx.QueryRowContext(ctx, `INSERT INTO question_bank_review(tenant_id,version_id,reviewer_id,decision,comment,content_revision,bundle_hash) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, scope.TenantID, id, scope.ActorID, decision, in.Comment, v.Revision, v.BundleHash).Scan(&reviewID)
		if err != nil {
			return v, err
		}
		bump := 0
		if status == "draft" {
			bump = 1
		}
		v, err = scanVersion(tx.QueryRowContext(ctx, `UPDATE question_bank_item_version SET workflow_status=$3,last_review_id=$4,revision=revision+$5,updated_at=now() WHERE tenant_id=$1 AND id=$2 RETURNING `+versionCols, scope.TenantID, id, status, reviewID, bump))
		if err != nil {
			return v, err
		}
		if status == "published" {
			// 发布状态、审核记录和当前版本指针在同一事务提交，读取者不会看到只完成一半的发布。
			_, err = tx.ExecContext(ctx, `UPDATE question_bank_item SET current_published_version_id=$3 WHERE tenant_id=$1 AND id=$2`, scope.TenantID, i.ID, id)
		}
		return v, err
	})
}
func (s *PostgresStore) ListReviews(ctx context.Context, scope auth.AccessScope, id string) ([]Review, error) {
	if _, err := s.version(ctx, s.db, scope, id, "read"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,version_id::text,reviewer_id::text,decision,comment,content_revision,bundle_hash,created_at FROM question_bank_review WHERE tenant_id=$1 AND version_id=$2 ORDER BY created_at,id`, scope.TenantID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Review{}
	for rows.Next() {
		var r Review
		if err = rows.Scan(&r.ID, &r.VersionID, &r.ReviewerID, &r.Decision, &r.Comment, &r.ContentRevision, &r.BundleHash, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *PostgresStore) BindReviewers(ctx context.Context, scope auth.AccessScope, id string, in ReviewerBinding) (Bank, error) {
	if !validID(in.UserID) || in.ExpectedRevision <= 0 || ((in.Review || in.Publish) && !in.Read) {
		return Bank{}, ErrInvalidInput
	}
	return pgMutation(ctx, s, scope, pgMutationCommand{op: "question_bank.reviewers.bind", target: id, input: in, action: "manage"}, func(tx *sql.Tx) (Bank, error) {
		b, err := s.bank(ctx, tx, scope, id, "manage", " FOR UPDATE OF b")
		if err != nil {
			return b, err
		}
		if b.Revision != in.ExpectedRevision {
			return b, ErrConflict
		}
		if b.Status != "active" {
			return b, ErrLocked
		}
		var active bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM app_user WHERE tenant_id=$1 AND id=$2 AND status='active' AND deleted_at IS NULL)`, scope.TenantID, in.UserID).Scan(&active)
		if err != nil {
			return b, err
		}
		if !active {
			return b, ErrInvalidInput
		}
		if err = bindReviewerActions(ctx, tx, scope.TenantID, id, in); err != nil {
			return b, err
		}
		return scanBank(tx.QueryRowContext(ctx, `UPDATE question_bank b SET revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2 RETURNING `+bankCols, scope.TenantID, id))
	})
}

func bindReviewerActions(ctx context.Context, tx *sql.Tx, tenant, id string, in ReviewerBinding) error {
	// 调用方持有题库写锁；按 read、review、publish 更新，任一失败交由同一事务回滚。
	for _, binding := range []struct {
		action  string
		enabled bool
	}{{"read", in.Read}, {"review", in.Review}, {"publish", in.Publish}} {
		var err error
		if binding.enabled {
			_, err = tx.ExecContext(ctx, `INSERT INTO question_bank_acl(tenant_id,bank_id,user_id,action) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, id, in.UserID, binding.action)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM question_bank_acl WHERE tenant_id=$1 AND bank_id=$2 AND user_id=$3 AND action=$4`, tenant, id, in.UserID, binding.action)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) checkExam(ctx context.Context, q queryer, scope auth.AccessScope, id string) error {
	if !validScope(scope) || !validID(id) {
		return ErrNotFound
	}
	var b auth.ResourceBoundary
	b.ResourceType = "exam"
	b.ResourceID = id
	b.ExamID = id
	b.TenantID = scope.TenantID
	var classes []byte
	err := q.QueryRowContext(ctx, `SELECT school_id::text,to_jsonb(ARRAY(SELECT class_id::text FROM exam_class WHERE tenant_id=$1 AND exam_id=$2 AND deleted_at IS NULL)) FROM exam WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL`, scope.TenantID, id).Scan(&b.SchoolID, &classes)
	if err != nil {
		return pgError(err)
	}
	if err = json.Unmarshal(classes, &b.ClassIDs); err != nil {
		return err
	}
	if !auth.AllowsResourceBoundary(scope, b) {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) Materialize(ctx context.Context, scope auth.AccessScope, id string, in MaterializeInput) (MaterializeResult, error) {
	// 将指定已发布版本复制进尚可配置的考试；后续题库变化不会自动替换考试里的内容。
	if err := validateMaterializeInput(id, in); err != nil {
		return MaterializeResult{}, err
	}
	return pgMutation(ctx, s, scope, pgMutationCommand{op: "question_bank.materialize", target: id, input: in, action: "read"}, func(tx *sql.Tx) (MaterializeResult, error) {
		out := MaterializeResult{ExamID: id, Questions: []paper.Question{}, SourceVersionIDs: []string{}}
		if err := s.lockMaterializeExam(ctx, tx, scope, id, in.ExpectedRevision); err != nil {
			return out, err
		}
		sources, err := s.materializeSources(ctx, tx, scope, in.Selections)
		if err != nil {
			return out, err
		}
		if err = s.lockMaterializeItems(ctx, tx, scope, sources); err != nil {
			return out, err
		}
		if err = materializeQuestions(ctx, tx, scope, in.Selections, sources, &out); err != nil {
			return out, err
		}
		err = tx.QueryRowContext(ctx, `UPDATE exam SET revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2 RETURNING revision`, scope.TenantID, id).Scan(&out.Revision)
		return out, err
	})
}

func validateMaterializeInput(id string, in MaterializeInput) error {
	if !validID(id) || in.ExpectedRevision <= 0 || len(in.Selections) == 0 || len(in.Selections) > 100 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, x := range in.Selections {
		if !validID(x.VersionID) || stringsInvalidQuestionNo(x.QuestionNo) || x.SortOrder <= 0 || seen[x.QuestionNo] {
			return ErrInvalidInput
		}
		seen[x.QuestionNo] = true
	}
	return nil
}

func (s *PostgresStore) lockMaterializeExam(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, id string, expectedRevision int64) error {
	if err := s.checkExam(ctx, tx, scope, id); err != nil {
		return err
	}
	var status, subject string
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT status,revision,subject FROM exam WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE`, scope.TenantID, id).Scan(&status, &revision, &subject); err != nil {
		return err
	}
	if revision != expectedRevision {
		return ErrConflict
	}
	if !oneOf(status, "draft", "configured") {
		return ErrLocked
	}
	return nil
}

func (s *PostgresStore) materializeSources(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, selections []MaterializeSelection) (map[string]Version, error) {
	sources := map[string]Version{}
	banks := map[string]bool{}
	for _, x := range selections {
		v, err := s.version(ctx, tx, scope, x.VersionID, "read")
		if err != nil {
			return sources, err
		}
		i, err := s.item(ctx, tx, scope, v.ItemID, "read", "")
		if err != nil {
			return sources, err
		}
		if i.Kind != "question" || i.Status != "active" || v.WorkflowStatus != "published" || publishable(v, i.Kind) != nil {
			return sources, ErrInvalidInput
		}
		sources[x.VersionID] = v
		banks[i.BankID] = true
	}
	return sources, s.lockMaterializeBanks(ctx, tx, scope, banks)
}

func (s *PostgresStore) lockMaterializeBanks(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, banks map[string]bool) error {
	// 题库共享锁先于条目锁；对题库排序后取锁，防止并行复制按不同顺序互相等待。
	ids := []string{}
	for b := range banks {
		ids = append(ids, b)
	}
	sort.Strings(ids)
	for _, b := range ids {
		bank, err := s.bank(ctx, tx, scope, b, "read", bankShareLockClause)
		if err != nil {
			return err
		}
		if bank.Status != "active" {
			return ErrLocked
		}
	}
	return nil
}

func (s *PostgresStore) lockMaterializeItems(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, sources map[string]Version) error {
	itemIDs := []string{}
	seenItems := map[string]bool{}
	for _, v := range sources {
		if !seenItems[v.ItemID] {
			itemIDs = append(itemIDs, v.ItemID)
			seenItems[v.ItemID] = true
		}
	}
	sort.Strings(itemIDs)
	for _, itemID := range itemIDs {
		i, err := s.item(ctx, tx, scope, itemID, "read", " FOR SHARE")
		if err != nil {
			return err
		}
		if i.Status != "active" {
			return ErrLocked
		}
	}
	return nil
}

func materializeQuestions(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, selections []MaterializeSelection, sources map[string]Version, out *MaterializeResult) error {
	// 按请求顺序追加；后续复制失败时保留先前返回值，事务仍由 pgMutation 整体回滚。
	for _, x := range selections {
		v := sources[x.VersionID]
		if err := checkMaterializeAssets(ctx, tx, scope, v.Scoring.Assets); err != nil {
			return err
		}
		q, err := paper.MaterializeBankQuestionTx(ctx, tx, scope.TenantID, out.ExamID, scope.ActorID, paper.BankQuestionFacts{ItemID: v.ItemID, VersionID: v.ID, BundleHash: v.BundleHash, Content: v.Content, QuestionNo: x.QuestionNo, SortOrder: x.SortOrder, QuestionType: v.QuestionType, Score: v.DefaultScore, Stem: v.Stem, KnowledgePoints: v.KnowledgePoints, Archetype: v.AssessmentArchetype, Answer: v.Scoring.Answer, Solution: v.Scoring.Solution, Rubric: v.Scoring.Rubric, Assets: v.Scoring.Assets, TemplateVersionID: v.Scoring.TemplateVersionID})
		if err != nil {
			return err
		}
		out.Questions = append(out.Questions, q)
		out.SourceVersionIDs = append(out.SourceVersionIDs, v.ID)
	}
	return nil
}

func checkMaterializeAssets(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, assets []Asset) error {
	for _, a := range assets {
		var active bool
		err := tx.QueryRowContext(ctx, `SELECT lifecycle_status='active' AND deleted_at IS NULL AND hash_sha256=$3 FROM file_asset WHERE tenant_id=$1 AND id=$2 FOR SHARE`, scope.TenantID, a.FileAssetID, a.SHA256).Scan(&active)
		if err != nil {
			return pgError(err)
		}
		if !active {
			return ErrInvalidInput
		}
	}
	return nil
}
func stringsInvalidQuestionNo(s string) bool { return len(s) == 0 || len(s) > 32 || !validText(s, 32) }

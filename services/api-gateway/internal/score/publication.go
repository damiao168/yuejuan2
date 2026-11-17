package score

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

var ErrPublicationUnavailable = errors.New("canonical score publication is not configured")

type publicationAdapter struct {
	releases  *scorerelease.Service
	publisher scorerelease.PublicationPublisher
	db        *sql.DB
	receipts  *commandreceipt.Memory
	exams     exam.Store
}

// WithPublication 将旧命令接入与新接口相同的策略协调器；未组装时不能退回修改工作表的旧发布逻辑。
func (s *PostgresStore) WithPublication(releases *scorerelease.Service, publisher scorerelease.PublicationPublisher) *PostgresStore {
	s.publications = &publicationAdapter{releases: releases, publisher: publisher, db: s.db}
	return s
}

func (s *MemoryStore) WithPublication(releases *scorerelease.Service, publisher scorerelease.PublicationPublisher, exams exam.Store) *MemoryStore {
	s.publications = &publicationAdapter{releases: releases, publisher: publisher, receipts: &s.receipts, exams: exams}
	return s
}

func (a *publicationAdapter) publish(ctx context.Context, tenantID, examID, actorID string, input PublishInput) (PublishResult, error) {
	if a == nil || a.releases == nil || a.publisher == nil || a.db == nil && a.receipts == nil {
		return PublishResult{}, ErrPublicationUnavailable
	}
	if tenantID == "" || examID == "" || actorID == "" || utf8.RuneCountInString(input.Reason) > 1000 || len(commandreceipt.ID(ctx)) > 128 {
		return PublishResult{}, ErrInvalidInput
	}
	receipt := &publicationCommand{adapter: a, identity: commandreceipt.Identity{TenantID: tenantID, ActorID: actorID, Operation: "score.publish", TargetID: examID}, input: input}
	// 先只读恢复已成功的命令，避免后来已有复评版本时重建草稿。锁在离开此读取事务前释放。
	if found, err := receipt.recover(ctx); err != nil || found {
		return receipt.result, err
	}
	current, err := a.releases.CurrentPublished(ctx, tenantID, examID)
	var release scorerelease.Release
	if err == nil {
		// 旧请求没有选择发布版本的参数；已有正式版本时只确认当前事实，不能从工作表再建一个覆盖版本。
		release = current.Release
	} else if !errors.Is(err, scorerelease.ErrNotFound) {
		return PublishResult{}, publicationError(err)
	} else {
		appealEnabled, err := a.examAppealEnabled(ctx, tenantID, examID)
		if err != nil {
			return PublishResult{}, err
		}
		reason := strings.TrimSpace(input.Reason)
		if reason == "" {
			reason = "兼容旧客户端发布成绩"
		}
		// 草稿键绑定操作者与原命令，不能借用另一个人的草稿。完整请求冲突仍由事务回执校验。
		commandKey := "command:" + commandreceipt.ID(ctx)
		if commandreceipt.ID(ctx) == "" {
			// 未提供命令 ID 的历史客户端按本次内容复用草稿；修改理由属于新的尝试。
			commandKey = "unkeyed:" + reason
		}
		digest := sha256.Sum256([]byte(tenantID + "\x00" + actorID + "\x00" + examID + "\x00" + commandKey))
		release, err = a.releases.Create(ctx, tenantID, examID, actorID, scorerelease.CreateInput{
			Source: scorerelease.SourceInitial, Reason: reason, IdempotencyKey: "legacy-publish:" + hex.EncodeToString(digest[:]),
			VisibilityPolicy: scorerelease.VisibilityPolicy{ShowQuestionScores: true},
			AppealWindow:     scorerelease.AppealWindow{Enabled: appealEnabled},
		})
		if err == nil && (release.CreatedBy != actorID || release.Reason != reason) {
			err = commandreceipt.ErrConflict
		}
		if err != nil {
			return a.blockedResult(ctx, tenantID, examID, err)
		}
	}
	ctx = scorerelease.WithInitialPublicationOnly(scorerelease.WithPublicationReceipt(ctx, receipt))
	_, err = a.publisher.PublishPublication(ctx, tenantID, examID, release.ID, actorID)
	if err != nil {
		return a.blockedResult(ctx, tenantID, examID, err)
	}
	return receipt.result, nil
}

// 旧请求只有理由字段。与新版页面一样，首发时把考试是否开放申诉固化到发布版本。
func (a *publicationAdapter) examAppealEnabled(ctx context.Context, tenantID, examID string) (bool, error) {
	if a.db != nil {
		var enabled bool
		err := a.db.QueryRowContext(ctx, `SELECT appeal_enabled FROM exam WHERE tenant_id=$1::uuid AND id::text=$2 AND deleted_at IS NULL`, tenantID, examID).Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
		return enabled, err
	}
	if a.exams == nil {
		return false, nil
	}
	item, err := a.exams.GetExam(ctx, auth.AccessScope{TenantID: tenantID, ExamIDs: []string{examID}}, examID)
	if errors.Is(err, exam.ErrNotFound) {
		return false, ErrNotFound
	}
	return item.AppealEnabled, err
}

func (a *publicationAdapter) blockedResult(ctx context.Context, tenantID, examID string, err error) (PublishResult, error) {
	if errors.Is(err, scorerelease.ErrGateBlocked) {
		quality := QualityReport{Passed: false, Issues: []QualityIssue{}}
		if gate, readErr := a.releases.Gate(ctx, tenantID, examID); readErr == nil {
			for _, issue := range gate.Blocking {
				quality.Issues = append(quality.Issues, QualityIssue{Code: issue.Code, Message: issue.Message, Blocking: issue.Blocking, Count: issue.Count})
			}
		}
		if len(quality.Issues) == 0 {
			quality.Issues = append(quality.Issues, QualityIssue{Code: "score_release_gate_blocked", Message: "正式发布策略或复评状态尚未通过，请到成绩发布页面核对阻断记录。", Blocking: true, Count: 1})
		}
		return PublishResult{Status: "blocked", Quality: quality}, ErrQualityGateFailed
	}
	return PublishResult{}, publicationError(err)
}

func publicationError(err error) error {
	switch {
	case errors.Is(err, scorerelease.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, scorerelease.ErrInvalidInput):
		return ErrInvalidInput
	case errors.Is(err, scorerelease.ErrInvalidTransition), errors.Is(err, scorerelease.ErrStudentReleaseChanged):
		return ErrInvalidTransition
	case errors.Is(err, scorerelease.ErrGateBlocked):
		return ErrQualityGateFailed
	default:
		return err
	}
}

type publicationCommand struct {
	adapter  *publicationAdapter
	identity commandreceipt.Identity
	input    PublishInput
	result   PublishResult
}

func (c *publicationCommand) recover(ctx context.Context) (bool, error) {
	if commandreceipt.ID(ctx) == "" {
		return false, nil
	}
	if c.adapter.db == nil {
		_, found, err := c.Load(ctx, nil)
		return found, err
	}
	tx, err := c.adapter.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	_, found, err := c.Load(ctx, tx)
	return found, err
}

func (c *publicationCommand) Load(ctx context.Context, tx *sql.Tx) (scorerelease.Release, bool, error) {
	var found bool
	var err error
	if c.adapter.db != nil {
		found, err = commandreceipt.Load(ctx, tx, c.identity, c.input, &c.result)
	} else {
		found, err = c.adapter.receipts.Load(ctx, c.identity.TenantID, c.identity.ActorID, c.identity.Operation, c.identity.TargetID, c.input, &c.result)
	}
	if c.result.ScoreRelease != nil {
		return *c.result.ScoreRelease, found, err
	}
	return scorerelease.Release{}, found, err
}

func (c *publicationCommand) Save(ctx context.Context, tx *sql.Tx, release scorerelease.Release, facts []scorerelease.SubmissionFact, newlyPublished bool) error {
	grades := make([]SubmissionGrade, 0, len(facts))
	for _, fact := range facts {
		grades = append(grades, publishedSubmissionGrade(c.identity.TenantID, release, fact))
	}
	result := PublishResult{Status: "published", SubmissionGrades: grades, Quality: QualityReport{Passed: true, Issues: []QualityIssue{}}, ScoreRelease: &release}
	if release.PublishedAt != nil {
		result.PublishedAt = *release.PublishedAt
	}
	// 这里由正式发布事务调用；回执失败必须让公开版本、current 指针和回执一起回滚。
	var err error
	if c.adapter.db != nil {
		err = commandreceipt.Save(ctx, tx, c.identity, c.input, result)
	} else {
		err = c.adapter.receipts.Save(ctx, c.identity.TenantID, c.identity.ActorID, c.identity.Operation, c.identity.TargetID, c.input, result)
	}
	if err == nil {
		result.newlyPublished = newlyPublished
		c.result = result
	}
	return err
}

func (a *publicationAdapter) studentGrade(ctx context.Context, tenantID, studentID, examID string) (SubmissionGrade, error) {
	if a == nil || a.releases == nil {
		return SubmissionGrade{}, ErrNotFound
	}
	result, err := a.releases.StudentResult(ctx, tenantID, examID, studentID)
	if err != nil {
		return SubmissionGrade{}, publicationError(err)
	}
	var submissionID string
	if a.db != nil {
		// 总分、题分和答卷归属都按刚读取的同一发布 ID 定位，期间 current 切换也不拼接工作表数据。
		err = a.db.QueryRowContext(ctx, `SELECT submission_id::text FROM score_release_item WHERE tenant_id=$1 AND release_id=$2::uuid AND student_id=$3::uuid`, tenantID, result.ReleaseID, studentID).Scan(&submissionID)
	} else {
		var detail scorerelease.Detail
		detail, err = a.releases.Get(ctx, tenantID, result.ReleaseID)
		for _, item := range detail.Items {
			if item.StudentID == studentID {
				submissionID = item.SubmissionID
				break
			}
		}
	}
	if errors.Is(err, sql.ErrNoRows) || err == nil && submissionID == "" {
		return SubmissionGrade{}, ErrNotFound
	}
	if err != nil {
		return SubmissionGrade{}, publicationError(err)
	}
	return studentPublishedGrade(tenantID, studentID, submissionID, result), nil
}

package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *PostgresStore) applyPaperImportAssessmentArchetypes(ctx context.Context, tenantID, importID string, drafts []PaperImportDraftQuestion) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT q.question_no,q.question_type,COALESCE(config.archetype_code,'')
FROM paper_import_job job
JOIN question q ON q.tenant_id=job.tenant_id AND q.exam_id=job.exam_id AND q.deleted_at IS NULL AND q.status<>'deleted'
LEFT JOIN question_assessment_config config ON config.tenant_id=q.tenant_id AND config.exam_id=q.exam_id AND config.question_id=q.id
WHERE job.tenant_id=$1 AND job.id=$2::uuid AND job.deleted_at IS NULL
`, tenantID, importID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type assessmentMatch struct{ kind, archetype string }
	byNumber := map[string]assessmentMatch{}
	for rows.Next() {
		var number, kind, archetype string
		if err := rows.Scan(&number, &kind, &archetype); err != nil {
			return err
		}
		if archetype != "" {
			byNumber[normalizePaperImportQuestionNumber(number)] = assessmentMatch{kind, archetype}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for index := range drafts {
		if matched := byNumber[normalizePaperImportQuestionNumber(drafts[index].QuestionNo)]; matched.archetype != "" && matched.kind == drafts[index].QuestionType {
			drafts[index].AssessmentArchetype = matched.archetype
		} else if drafts[index].AssessmentArchetype == "" {
			drafts[index].AssessmentArchetype = defaultPaperImportArchetype(drafts[index].QuestionType)
		}
	}
	return nil
}

func (s *PostgresStore) SavePaperImportReview(ctx context.Context, tenantID, id, _ string, input ReviewPaperImportInput) (PaperImportJob, error) {
	if input.ExpectedGeneration <= 0 {
		return PaperImportJob{}, ErrConflict
	}
	expectedUpdatedAt, err := time.Parse(time.RFC3339Nano, input.ExpectedUpdatedAt)
	if err != nil || expectedUpdatedAt.IsZero() {
		return PaperImportJob{}, ErrInvalidInput
	}
	if err := s.applyPaperImportAssessmentArchetypes(ctx, tenantID, id, input.Questions); err != nil {
		return PaperImportJob{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	if err = lockPaperImportReviewVersion(ctx, tx, tenantID, id, input.ExpectedGeneration, expectedUpdatedAt); err != nil {
		return PaperImportJob{}, err
	}
	job, err := getPaperImportDetailsInTx(ctx, tx, tenantID, id)
	if err != nil {
		return PaperImportJob{}, err
	}
	var reconciliationIssues []string
	input.Questions, reconciliationIssues, err = reconcilePaperImportQuestions(ctx, tx, tenantID, job.ExamID, input.Questions, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	for i := range input.Questions {
		normalizeAnswerKeyOnlyPaperImportDraft(&input.Questions[i])
		input.Questions[i].HumanConfirmedFields = normalizeHumanConfirmedFields(input.Questions[i].HumanConfirmedFields)
		refreshDraftCompleteness(&input.Questions[i], len(job.QuestionCandidates) > 0)
	}
	structured := appendReviewedDraftIssues(issuesAfterHumanReview(job.StructuredIssues, input.Questions, true), input.Questions)
	structured = s.appendPaperImportBlueprintIssues(ctx, tenantID, id, questionCandidatesFromDrafts(input.Questions), withoutBlueprintIssues(structured))
	structured = withPaperImportReconciliationIssues(structured, reconciliationIssues)
	q, _ := json.Marshal(input.Questions)
	si, _ := json.Marshal(structured)
	messages, _ := json.Marshal(issueMessages(structured))
	// now()是事务开始时间；等待行锁后也必须生成更大的内容时间，避免旧CAS令牌再次有效。
	result, err := tx.ExecContext(ctx, `UPDATE paper_import_job SET draft_questions=$3,structured_issues=$4,issues=$5,updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE tenant_id=$1 AND id=$2::uuid AND current_generation=$6 AND updated_at=$7 AND status='review_required' AND deleted_at IS NULL`, tenantID, id, q, si, messages, input.ExpectedGeneration, expectedUpdatedAt)
	if err != nil {
		return PaperImportJob{}, err
	}
	n, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return PaperImportJob{}, rowsErr
	}
	if n != 1 {
		return PaperImportJob{}, ErrConflict
	}
	// 在仍持行锁时读取本次写入回执；提交后重读可能把其他人的下一次保存冒充成本次结果。
	saved, err := getPaperImportDetailsInTx(ctx, tx, tenantID, id)
	if err != nil {
		return PaperImportJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return saved, nil
}

// generation 只标记资料代次，核对内容时间也必须精确匹配；调用方事务持有行锁直到提交或回滚。
func lockPaperImportReviewVersion(ctx context.Context, tx *sql.Tx, tenantID, id string, expectedGeneration int64, expectedUpdatedAt time.Time) error {
	var currentGeneration int64
	var status string
	var updatedAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT current_generation,status,updated_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&currentGeneration, &status, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status != "review_required" || currentGeneration != expectedGeneration || !updatedAt.Equal(expectedUpdatedAt) {
		return ErrConflict
	}
	return nil
}

type paperImportBlueprintSection struct {
	title, kind string
	count       int
	score       float64
}

func (s *PostgresStore) paperImportBlueprintSections(ctx context.Context, tenantID, examID string) []paperImportBlueprintSection {
	sections := []paperImportBlueprintSection{}
	rows, err := s.db.QueryContext(ctx, `SELECT title,question_type,question_count,score_per_question::float8 FROM exam_blueprint_section WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL ORDER BY sort_order`, tenantID, examID)
	// Blueprint 是补充核对信息；沿用查询或单行读取失败时忽略该信息的行为。
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var item paperImportBlueprintSection
			if rows.Scan(&item.title, &item.kind, &item.count, &item.score) == nil {
				sections = append(sections, item)
			}
		}
	}
	return sections
}

func (s *PostgresStore) appendPaperImportBlueprintIssues(ctx context.Context, tenantID, importID string, questions []QuestionCandidate, issues []PaperImportIssue) []PaperImportIssue {
	var examID string
	var total float64
	if err := s.db.QueryRowContext(ctx, `SELECT exam_id::text,total_score::float8 FROM exam JOIN paper_import_job j ON j.exam_id=exam.id AND j.tenant_id=exam.tenant_id WHERE j.tenant_id=$1 AND j.id=$2::uuid AND exam.deleted_at IS NULL`, tenantID, importID).Scan(&examID, &total); err != nil {
		return issues
	}
	sections := s.paperImportBlueprintSections(ctx, tenantID, examID)
	knownTotal := 0.0
	byType := map[string]int{}
	for _, q := range questions {
		byType[q.QuestionType]++
		if q.Score != nil {
			knownTotal += *q.Score
		}
	}
	if len(sections) > 0 {
		expected := 0
		expectedByType := map[string]int{}
		titlesByType := map[string][]string{}
		for _, section := range sections {
			expected += section.count
			expectedByType[section.kind] += section.count
			titlesByType[section.kind] = append(titlesByType[section.kind], section.title)
		}
		for _, kind := range sortedPaperImportKeys(expectedByType) {
			expectedCount := expectedByType[kind]
			if byType[kind] == expectedCount {
				continue
			}
			label := strings.Join(titlesByType[kind], " / ")
			issues = append(issues, candidateIssue("SECTION_COUNT_MISMATCH", "error", "confirmed", "", fmt.Sprintf("%s预计%d道，当前识别%d道", label, expectedCount, byType[kind]), "请核对该部分是否漏传或题型识别错误", nil))
		}
		if len(questions) != expected {
			issues = append(issues, candidateIssue("QUESTION_COUNT_MISMATCH", "error", "confirmed", "", fmt.Sprintf("考试配置共%d道，当前识别%d道", expected, len(questions)), "请核对缺失资料或识别结果", nil))
		}
	}
	if len(questions) > 0 && !scoreEqual(knownTotal, total) {
		issues = append(issues, candidateIssue("SCORE_TOTAL_MISMATCH", "error", "confirmed", "", fmt.Sprintf("当前识别题目合计%.2f分，与考试配置%.2f分不一致", knownTotal, total), "可能存在漏题、分值识别错误或考试配置差异", nil))
	}
	return dedupePaperImportIssues(issues)
}

func (s *PostgresStore) CompletePaperImport(ctx context.Context, tenantID, id string, questions []PaperImportDraftQuestion, issues []string) (PaperImportJob, error) {
	// 这个 v2 之前的完成钩子缺少运行、输入、任务和租约身份。
	// 生产 PostgreSQL 调用方必须改用 CompletePaperImportParseTask。
	return PaperImportJob{}, ErrConflict
}

type paperImportQuestionReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func reconcilePaperImportQuestions(ctx context.Context, reader paperImportQuestionReader, tenantID, examID string, drafts []PaperImportDraftQuestion, baseIssues []string) ([]PaperImportDraftQuestion, []string, error) {
	totalRows, err := reader.QueryContext(ctx, `SELECT total_score::float8 FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, examID)
	if err != nil {
		return nil, nil, err
	}
	var expectedTotal float64
	if !totalRows.Next() {
		err := totalRows.Err()
		totalRows.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, ErrNotFound
	}
	if err := totalRows.Scan(&expectedTotal); err != nil {
		totalRows.Close()
		return nil, nil, err
	}
	if err := totalRows.Close(); err != nil {
		return nil, nil, err
	}

	rows, err := reader.QueryContext(ctx, `SELECT id::text,question_no,question_type,score::float8,sort_order FROM question WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status<>'deleted' ORDER BY sort_order,question_no FOR UPDATE`, tenantID, examID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	existing := []paperImportExistingQuestion{}
	for rows.Next() {
		var item paperImportExistingQuestion
		if err := rows.Scan(&item.id, &item.number, &item.kind, &item.score, &item.sortOrder); err != nil {
			return nil, nil, err
		}
		existing = append(existing, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	drafts, issues := reconcilePaperImportDrafts(drafts, existing, &expectedTotal, baseIssues)
	return drafts, issues, nil
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

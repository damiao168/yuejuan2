package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// 预览和采用共用读集与锁顺序：导入、当前识别回执、考试、题目。
// 预览哈希覆盖人工编辑内容，确认期间的并发保存会使旧预览失效。
func (s *PostgresStore) loadStructureStateTx(ctx context.Context, tx *sql.Tx, tenantID, id string) (PaperImportJob, []Question, float64, error) {
	job, err := scanPaperImport(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id))
	if err != nil {
		return job, nil, 0, err
	}
	if err = loadStructureReceiptTx(ctx, tx, tenantID, id, &job); err != nil {
		return job, nil, 0, err
	}
	if err = ensureExamPaperMutableTx(ctx, tx, tenantID, job.ExamID); err != nil {
		return job, nil, 0, err
	}
	var total float64
	if err = tx.QueryRowContext(ctx, `SELECT total_score::float8 FROM exam WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, job.ExamID).Scan(&total); err != nil {
		return job, nil, 0, err
	}
	var candidates, issues []byte
	if err = tx.QueryRowContext(ctx, `SELECT question_candidates,structured_issues FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id).Scan(&candidates, &issues); err != nil {
		return job, nil, 0, err
	}
	if err = json.Unmarshal(candidates, &job.QuestionCandidates); err != nil {
		return job, nil, 0, err
	}
	if err = json.Unmarshal(issues, &job.StructuredIssues); err != nil {
		return job, nil, 0, err
	}
	if err = appendStructureSourcesTx(ctx, tx, tenantID, id, &job); err != nil {
		return job, nil, 0, err
	}
	if err = lockStructureQuestionsTx(ctx, tx, tenantID, job.ExamID); err != nil {
		return job, nil, 0, err
	}
	qs, err := listQuestions(ctx, tx, tenantID, job.ExamID)
	if err != nil {
		return job, nil, 0, err
	}
	if err = annotateStructureQuestionsTx(ctx, tx, tenantID, job.ExamID, qs); err != nil {
		return job, nil, 0, err
	}
	return job, qs, total, nil
}

// 当前代回执必须同时匹配 task 和 hash；仅无 task 的 legacy-unverified 存量资料走兼容路径。
func loadStructureReceiptTx(ctx context.Context, tx *sql.Tx, tenantID, id string, job *PaperImportJob) error {
	var resultGeneration sql.NullInt64
	var jobTask, runTask, jobHash, runHash, runStatus, revision string
	err := tx.QueryRowContext(ctx, `SELECT j.current_generation,j.result_generation,COALESCE(j.result_task_id::text,''),COALESCE(j.result_payload_hash,''),r.status,r.source_revision,COALESCE(r.result_task_id::text,''),COALESCE(r.result_payload_hash,''),COALESCE(j.structure_confirmed_generation,0) FROM paper_import_job j JOIN paper_import_run r ON r.tenant_id=j.tenant_id AND r.paper_import_id=j.id AND r.generation=j.current_generation WHERE j.tenant_id=$1 AND j.id=$2::uuid FOR UPDATE OF r`, tenantID, id).Scan(&job.Generation, &resultGeneration, &jobTask, &jobHash, &runStatus, &revision, &runTask, &runHash, &job.StructureConfirmedGeneration)
	if err != nil {
		return err
	}
	verified := resultGeneration.Valid && resultGeneration.Int64 == job.Generation && jobTask != "" && jobTask == runTask && jobHash != "" && jobHash == runHash
	legacy := revision == "legacy-unverified" && jobTask == "" && runTask == ""
	if job.Status != "review_required" || runStatus != "review_required" || (!verified && !legacy) {
		return ErrConflict
	}
	return nil
}

func appendStructureSourcesTx(ctx context.Context, tx *sql.Tx, tenantID, id string, job *PaperImportJob) error {
	rows, err := tx.QueryContext(ctx, `SELECT id::text,detected_role FROM paper_import_source WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL ORDER BY document_index`, tenantID, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var source PaperImportSource
		if err = rows.Scan(&source.ID, &source.DetectedRole); err != nil {
			rows.Close()
			return err
		}
		job.Sources = append(job.Sources, source)
	}
	err = rows.Err()
	rows.Close()
	return err
}

// 先读完并关闭加锁结果集，再用同一事务连接读取题目详情。
func lockStructureQuestionsTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM question WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status<>'deleted' ORDER BY id FOR UPDATE`, tenantID, examID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var qid string
		if err = rows.Scan(&qid); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	return err
}

func annotateStructureQuestionsTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, qs []Question) error {
	for i := range qs {
		if err := normalizePresetStructureScoreTx(ctx, tx, tenantID, examID, &qs[i]); err != nil {
			return err
		}
		var published bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scoring_rule WHERE tenant_id=$1 AND question_id=$2::uuid AND status='published' AND deleted_at IS NULL)`, tenantID, qs[i].ID).Scan(&published); err != nil {
			return err
		}
		if published {
			qs[i].Rubric = &Rubric{Status: "locked"}
		}
	}
	return nil
}

func normalizePresetStructureScoreTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, q *Question) error {
	if q.ScoreSource == "preset" && q.Score > 0 {
		// 存量来源标记只能找到与考试同时创建的题目；教师后来改过满分时不能清零。
		// 与原分区不同的现存正分值按人工内容保留，无法证明一致分值曾被单独确认。
		var presetScore float64
		presetErr := tx.QueryRowContext(ctx, `WITH sections AS (
SELECT score_per_question,COALESCE(SUM(question_count) OVER(ORDER BY sort_order,id ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) AS before_count, SUM(question_count) OVER(ORDER BY sort_order,id) AS through_count
FROM exam_blueprint_section WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL)
SELECT score_per_question::float8 FROM sections WHERE $3>before_count AND $3<=through_count`, tenantID, examID, q.SortOrder).Scan(&presetScore)
		if presetErr != nil && !errors.Is(presetErr, sql.ErrNoRows) {
			return presetErr
		}
		if presetErr == nil && !scoreEqual(presetScore, q.Score) {
			q.ScoreSource = "human"
		}
	}
	return nil
}
func (s *PostgresStore) PreviewPaperStructure(ctx context.Context, tenantID, id string) (PaperStructurePreview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperStructurePreview{}, err
	}
	defer tx.Rollback()
	job, qs, total, err := s.loadStructureStateTx(ctx, tx, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return PaperStructurePreview{}, err
	}
	out, _ := planPaperStructure(job, qs, total)
	return out, nil
}

// 只软删除没有内容的占位题；已有答案、解析、细则或区域的多余题目必须单独核对。
// 调整分值后旧客观规则退役，不能让按原满分建立的规则继续生效。
func (s *PostgresStore) AdoptPaperStructure(ctx context.Context, tenantID, id, userID string, input AdoptPaperStructureInput) (PaperStructurePreview, error) {
	if !validStructureAdoptionInput(input) {
		return PaperStructurePreview{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperStructurePreview{}, err
	}
	defer tx.Rollback()
	job, qs, out, drafts, err := s.prepareStructureAdoptionTx(ctx, tx, tenantID, id, input)
	if err != nil {
		return out, err
	}
	if out.Confirmed {
		return out, nil
	}
	if err = removeStructureQuestionsTx(ctx, tx, tenantID, out.Changes); err != nil {
		return out, err
	}
	for index, draft := range drafts {
		old := findStructureQuestion(qs, draft.QuestionNo)
		// 结构未变的题库快照和生效评分题目保持原内容；变化已在预览阶段阻断。
		if structureQuestionProtected(old) {
			continue
		}
		row := prepareStructureQuestionWrite(tenantID, id, job, old, draft, index)
		if row.ID, err = upsertStructureQuestionTx(ctx, tx, row); err != nil {
			return out, err
		}
		if err = syncStructureQuestionScoringTx(ctx, tx, old, row); err != nil {
			return out, err
		}
		draft = importableStructureDraft(draft, old)
		if err = s.applyImportedAnswerSolutionAndRubric(ctx, tx, tenantID, id, row.ID, userID, draft); err != nil {
			return out, err
		}
	}
	if err = finalizeStructureAdoptionTx(ctx, tx, tenantID, id, userID, job.ExamID); err != nil {
		return out, err
	}
	out.Confirmed = true
	return out, nil
}

func validStructureAdoptionInput(input AdoptPaperStructureInput) bool {
	return input.ExpectedGeneration > 0 && input.PreviewHash != "" && input.CompletePaperConfirmed
}

func (s *PostgresStore) prepareStructureAdoptionTx(ctx context.Context, tx *sql.Tx, tenantID, id string, input AdoptPaperStructureInput) (PaperImportJob, []Question, PaperStructurePreview, []PaperImportDraftQuestion, error) {
	job, qs, total, err := s.loadStructureStateTx(ctx, tx, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return job, qs, PaperStructurePreview{}, nil, err
	}
	out, drafts := planPaperStructure(job, qs, total)
	if job.Generation != input.ExpectedGeneration || out.PreviewHash != input.PreviewHash {
		return job, qs, out, drafts, ErrConflict
	}
	if len(out.BlockedReasons) > 0 {
		return job, qs, out, drafts, ErrInvalidInput
	}
	return job, qs, out, drafts, nil
}

func removeStructureQuestionsTx(ctx context.Context, tx *sql.Tx, tenantID string, changes []PaperStructureChange) error {
	for _, change := range changes {
		if change.Action == "remove" {
			if _, err := tx.ExecContext(ctx, `UPDATE question SET status='deleted',deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, change.QuestionID); err != nil {
				return err
			}
		}
	}
	return nil
}

// 每个草稿就在原写入位置准备题目行；租户和导入 ID 沿用调用方，而不是识别回执字段。
func prepareStructureQuestionWrite(tenantID, id string, job PaperImportJob, old Question, draft PaperImportDraftQuestion, index int) Question {
	row := Question{ID: old.ID, TenantID: tenantID, ExamID: job.ExamID, ExamPaperID: job.ExamPaperID, QuestionNo: draft.QuestionNo, QuestionType: draft.QuestionType, Score: draft.Score, ScoreSource: draft.ScoreSource, Stem: draft.Stem, Options: draft.Options, KnowledgePoints: draft.KnowledgePoints, SortOrder: index + 1, PaperImportID: id, PaperImportCandidateID: draft.CandidateID, PaperImportSourceRefs: draft.SourceRefs, ParentQuestionNo: draft.ParentQuestionNo, SubquestionNo: draft.SubquestionNo, AssessmentArchetype: draftAssessmentArchetype(draft)}
	if old.Stem != "" {
		row.Stem = old.Stem
	}
	if len(old.Options) > 0 {
		row.Options = old.Options
	}
	return row
}

func upsertStructureQuestionTx(ctx context.Context, tx *sql.Tx, row Question) (string, error) {
	optionsJSON, _ := json.Marshal(wireList(row.Options))
	refs, _ := json.Marshal(wireList(row.PaperImportSourceRefs))
	kp, _ := json.Marshal(wireList(row.KnowledgePoints))
	qid := row.ID
	var err error
	// 空白占位题软删除后可能在替换试卷时重新出现；复用其题号记录。
	// 有历史内容或评分配置的删除题目不复活，避免覆盖教师主动删除的内容。
	if qid == "" {
		err = tx.QueryRowContext(ctx, `INSERT INTO question(tenant_id,exam_id,exam_paper_id,question_no,question_type,score,stem,options,knowledge_points,answer_area,sort_order,status,paper_import_id,paper_import_candidate_id,paper_import_source_refs,parent_question_no,subquestion_no)
VALUES($1,$2::uuid,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,'{}',$10,'draft',$11::uuid,NULLIF($12,''),$13,$14,$15)
ON CONFLICT(tenant_id,exam_id,question_no) DO UPDATE SET exam_paper_id=excluded.exam_paper_id,question_type=excluded.question_type,score=excluded.score,stem=excluded.stem,options=excluded.options,knowledge_points=excluded.knowledge_points,sort_order=excluded.sort_order,status='draft',deleted_at=NULL,updated_at=now(),paper_import_id=excluded.paper_import_id,paper_import_candidate_id=excluded.paper_import_candidate_id,paper_import_source_refs=excluded.paper_import_source_refs,parent_question_no=excluded.parent_question_no,subquestion_no=excluded.subquestion_no
WHERE question.deleted_at IS NOT NULL AND question.status='deleted' AND COALESCE(question.stem,'')='' AND COALESCE(question.options,'[]')='[]'::jsonb AND COALESCE(question.answer_area,'{}')='{}'::jsonb AND question.source_bank_item_id IS NULL
AND NOT EXISTS(SELECT 1 FROM question_answer_key a WHERE a.tenant_id=question.tenant_id AND a.question_id=question.id)
AND NOT EXISTS(SELECT 1 FROM question_solution a WHERE a.tenant_id=question.tenant_id AND a.question_id=question.id)
AND NOT EXISTS(SELECT 1 FROM question_rubric a WHERE a.tenant_id=question.tenant_id AND a.question_id=question.id)
AND NOT EXISTS(SELECT 1 FROM scoring_rule a WHERE a.tenant_id=question.tenant_id AND a.question_id=question.id)
RETURNING id::text`, row.TenantID, row.ExamID, row.ExamPaperID, row.QuestionNo, row.QuestionType, row.Score, row.Stem, optionsJSON, kp, row.SortOrder, row.PaperImportID, row.PaperImportCandidateID, refs, row.ParentQuestionNo, row.SubquestionNo).Scan(&qid)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE question SET question_type=$3,score=$4,stem=$5,options=$6,sort_order=$7,paper_import_id=$8::uuid,paper_import_candidate_id=NULLIF($9,''),paper_import_source_refs=$10,parent_question_no=$11,subquestion_no=$12,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, row.TenantID, qid, row.QuestionType, row.Score, row.Stem, optionsJSON, row.SortOrder, row.PaperImportID, row.PaperImportCandidateID, refs, row.ParentQuestionNo, row.SubquestionNo)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return qid, ErrConflict
	}
	return qid, err
}

func syncStructureQuestionScoringTx(ctx context.Context, tx *sql.Tx, old, row Question) error {
	if _, err := tx.ExecContext(ctx, `UPDATE question SET score_source=$3 WHERE tenant_id=$1 AND id=$2::uuid`, row.TenantID, row.ID, row.ScoreSource); err != nil {
		return err
	}
	if old.ID != "" && (!scoreEqual(old.Score, row.Score) || old.QuestionType != row.QuestionType) {
		if _, err := tx.ExecContext(ctx, `UPDATE scoring_rule SET status='retired',updated_at=now() WHERE tenant_id=$1 AND question_id=$2::uuid AND status IN ('draft','published') AND deleted_at IS NULL`, row.TenantID, row.ID); err != nil {
			return err
		}
	}
	if old.ID == "" || old.QuestionType != row.QuestionType {
		archetype := row.AssessmentArchetype
		if _, err := tx.ExecContext(ctx, `UPDATE question SET assessment_archetype=$3 WHERE tenant_id=$1 AND id=$2::uuid`, row.TenantID, row.ID, archetype); err != nil {
			return err
		}
		if err := syncQuestionAssessmentArchetypeTx(ctx, tx, row.TenantID, row.ExamID, row.ID, archetype); err != nil {
			return err
		}
	}
	return nil
}

func importableStructureDraft(draft PaperImportDraftQuestion, old Question) PaperImportDraftQuestion {
	draft.Rubric = nil
	draft.HumanConfirmedFields = nil
	if old.AnswerKey != nil {
		draft.AnswerKey = nil
	}
	if old.Solution != nil {
		draft.Solution = nil
	}
	return draft
}

// 回执和旧分区随题目写入一起提交；Commit 返回失败时调用方不能标记 Confirmed。
func finalizeStructureAdoptionTx(ctx context.Context, tx *sql.Tx, tenantID, id, userID, examID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE paper_import_job SET structure_confirmed_generation=current_generation,structure_confirmed_at=now(),structure_confirmed_by=$3::uuid,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE exam_blueprint_section SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL`, tenantID, examID); err != nil {
		return err
	}
	return tx.Commit()
}

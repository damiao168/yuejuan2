package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func (s *PostgresStore) ApplyPaperImport(ctx context.Context, tenantID, id, userID string) (PaperImportJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	job, err := scanPaperImport(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrNotFound
	}
	if err != nil {
		return PaperImportJob{}, err
	}
	if job.Status != "review_required" {
		return PaperImportJob{}, ErrConflict
	}
	var generation int64
	var resultGeneration sql.NullInt64
	var runID, runStatus, runSourceRevision, jobResultTaskID, runResultTaskID, jobResultHash, runResultHash string
	if err := tx.QueryRowContext(ctx, `SELECT job.current_generation,job.result_generation,COALESCE(job.result_task_id::text,''),COALESCE(job.result_payload_hash,''),
run.id::text,run.status,run.source_revision,COALESCE(run.result_task_id::text,''),COALESCE(run.result_payload_hash,'')
FROM paper_import_job job
JOIN paper_import_run run ON run.tenant_id=job.tenant_id AND run.paper_import_id=job.id AND run.generation=job.current_generation
WHERE job.tenant_id=$1 AND job.id=$2::uuid
FOR UPDATE OF run`, tenantID, id).Scan(&generation, &resultGeneration, &jobResultTaskID, &jobResultHash, &runID, &runStatus, &runSourceRevision, &runResultTaskID, &runResultHash); err != nil {
		return PaperImportJob{}, err
	}
	verifiedResult := resultGeneration.Valid && resultGeneration.Int64 == generation && jobResultTaskID != "" && jobResultTaskID == runResultTaskID && jobResultHash != "" && jobResultHash == runResultHash
	// 迁移前的人工待核对数据没有任务回执，只对明确标记为 legacy-unverified 的记录保留兼容入口。
	legacyHumanReview := runSourceRevision == "legacy-unverified" && jobResultTaskID == "" && runResultTaskID == ""
	if runStatus != "review_required" || (!verifiedResult && !legacyHumanReview) {
		return PaperImportJob{}, ErrConflict
	}
	var structureConfirmed bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(structure_confirmed_generation=current_generation,false) FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id).Scan(&structureConfirmed); err != nil {
		return PaperImportJob{}, err
	}
	// 已采用结构后只在正式题目中核对，旧候选整批导入会覆盖后来保存的答案和分值。
	if structureConfirmed {
		return PaperImportJob{}, ErrConflict
	}
	var structured, candidateQuestions []byte
	if err := tx.QueryRowContext(ctx, `SELECT structured_issues,question_candidates FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id).Scan(&structured, &candidateQuestions); err != nil {
		return PaperImportJob{}, err
	}
	_ = json.Unmarshal(structured, &job.StructuredIssues)
	_ = json.Unmarshal(candidateQuestions, &job.QuestionCandidates)
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, job.ExamID); err != nil {
		return PaperImportJob{}, err
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM question WHERE tenant_id=$1 AND exam_id=$2 AND deleted_at IS NULL AND status<>'deleted'`, tenantID, job.ExamID).Scan(&existing); err != nil {
		return PaperImportJob{}, err
	}
	job.Questions, job.Issues, err = reconcilePaperImportQuestions(ctx, tx, tenantID, job.ExamID, job.Questions, job.Issues)
	if err != nil {
		return PaperImportJob{}, err
	}
	if paperImportHasBlockingIssues(job) {
		return PaperImportJob{}, ErrInvalidInput
	}
	if err = s.applyReviewedPaperImportQuestions(ctx, tx, tenantID, userID, job, existing); err != nil {
		return PaperImportJob{}, err
	}
	questionsJSON, _ := json.Marshal(job.Questions)
	issuesJSON, _ := json.Marshal(job.Issues)
	job, err = scanPaperImport(tx.QueryRowContext(ctx, `UPDATE paper_import_job SET status='applied',draft_questions=$3,issues=$4,applied_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at`, tenantID, id, questionsJSON, issuesJSON))
	if err != nil {
		return PaperImportJob{}, err
	}
	runResult, err := tx.ExecContext(ctx, `UPDATE paper_import_run SET status='applied',completed_at=COALESCE(completed_at,now()),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND generation=$3 AND status='review_required'`, tenantID, runID, generation)
	if err != nil {
		return PaperImportJob{}, err
	}
	runRows, rowsErr := runResult.RowsAffected()
	if rowsErr != nil {
		return PaperImportJob{}, rowsErr
	}
	if runRows != 1 {
		return PaperImportJob{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return job, nil
}

func (s *PostgresStore) applyReviewedPaperImportQuestions(ctx context.Context, tx *sql.Tx, tenantID, userID string, job PaperImportJob, existing int) error {
	// 新建题目的身份写回共享草稿切片；与已有题目的更新都在调用方事务中按原顺序完成。
	for index := range job.Questions {
		draft := &job.Questions[index]
		if err := validateQuestionInput(draft.QuestionNo, draft.QuestionType, draft.Score); err != nil {
			return ErrInvalidInput
		}
		if existing > 0 {
			if draft.MatchedQuestionID == "" || draft.MatchStatus == "extra" || draft.MatchStatus == "ambiguous" {
				continue
			}
			if err := s.updateReviewedPaperImportQuestion(ctx, tx, tenantID, userID, job, *draft); err != nil {
				return err
			}
			continue
		}
		if err := s.createReviewedPaperImportQuestion(ctx, tx, tenantID, userID, job, index); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) updateReviewedPaperImportQuestion(ctx context.Context, tx *sql.Tx, tenantID, userID string, job PaperImportJob, draft PaperImportDraftQuestion) error {
	kp, _ := json.Marshal(draft.KnowledgePoints)
	refs, _ := json.Marshal(draft.SourceRefs)
	options, _ := json.Marshal(wireList(draft.Options))
	result, updateErr := tx.ExecContext(ctx, `UPDATE question SET exam_paper_id=NULLIF($3,'')::uuid,stem=$4,knowledge_points=$5,paper_import_id=$7::uuid,paper_import_candidate_id=NULLIF($8,''),paper_import_source_refs=$9,question_type=$10,score=$11,assessment_archetype=$12,parent_question_no=$13,subquestion_no=$14,options=$15,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND exam_id=$6::uuid AND deleted_at IS NULL AND status<>'deleted'`, tenantID, draft.MatchedQuestionID, job.ExamPaperID, draft.Stem, kp, job.ExamID, job.ID, draft.CandidateID, refs, draft.QuestionType, draft.Score, draft.AssessmentArchetype, draft.ParentQuestionNo, draft.SubquestionNo, options)
	if updateErr != nil {
		return updateErr
	}
	affected, affectedErr := result.RowsAffected()
	if affectedErr != nil {
		return affectedErr
	}
	if affected != 1 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE question SET score_source=$3 WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, draft.MatchedQuestionID, importedScoreSource(draft)); err != nil {
		return err
	}
	// 应用已复核材料会替换题目的评分上下文。
	// 历史规则保留用于审计，但为之前题目版本发布的规则绝不能用于评分新导入内容。
	if _, err := tx.ExecContext(ctx, `UPDATE scoring_rule SET status='retired',updated_at=now() WHERE tenant_id=$1::uuid AND question_id=$2::uuid AND status='published' AND deleted_at IS NULL`, tenantID, draft.MatchedQuestionID); err != nil {
		return err
	}
	if err := s.applyImportedAnswerSolutionAndRubric(ctx, tx, tenantID, job.ID, draft.MatchedQuestionID, userID, draft); err != nil {
		return err
	}
	if err := insertImportedObjectiveRuleDraft(ctx, tx, tenantID, job.ExamID, draft.MatchedQuestionID, userID, draft); err != nil {
		return err
	}
	return syncQuestionAssessmentArchetypeTx(ctx, tx, tenantID, job.ExamID, draft.MatchedQuestionID, draftAssessmentArchetype(draft))
}

func (s *PostgresStore) createReviewedPaperImportQuestion(ctx context.Context, tx *sql.Tx, tenantID, userID string, job PaperImportJob, index int) error {
	// job 的副本仍共享 Questions 切片；新题绑定按原阶段写回待持久化的草稿。
	draft := &job.Questions[index]
	kp, _ := json.Marshal(draft.KnowledgePoints)
	refs, _ := json.Marshal(draft.SourceRefs)
	options, _ := json.Marshal(wireList(draft.Options))
	area := []byte(`{}`)
	var questionID string
	if err := tx.QueryRowContext(ctx, `INSERT INTO question (tenant_id,exam_id,exam_paper_id,question_no,question_type,score,stem,knowledge_points,answer_area,sort_order,status,paper_import_id,paper_import_candidate_id,paper_import_source_refs,assessment_archetype,parent_question_no,subquestion_no,options) VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10,'active',$11::uuid,NULLIF($12,''),$13,$14,$15,$16,$17) RETURNING id::text`, tenantID, job.ExamID, job.ExamPaperID, draft.QuestionNo, draft.QuestionType, draft.Score, draft.Stem, kp, area, index+1, job.ID, draft.CandidateID, refs, draft.AssessmentArchetype, draft.ParentQuestionNo, draft.SubquestionNo, options).Scan(&questionID); err != nil {
		return err
	}
	draft.MatchedQuestionID = questionID
	if _, err := tx.ExecContext(ctx, `UPDATE question SET score_source=$3 WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, questionID, importedScoreSource(*draft)); err != nil {
		return err
	}
	draft.MatchStatus = "matched"
	if err := s.applyImportedAnswerSolutionAndRubric(ctx, tx, tenantID, job.ID, questionID, userID, *draft); err != nil {
		return err
	}
	if err := insertImportedObjectiveRuleDraft(ctx, tx, tenantID, job.ExamID, questionID, userID, *draft); err != nil {
		return err
	}
	return syncQuestionAssessmentArchetypeTx(ctx, tx, tenantID, job.ExamID, questionID, draftAssessmentArchetype(*draft))
}

func (s *PostgresStore) applyImportedAnswerSolutionAndRubric(ctx context.Context, tx *sql.Tx, tenantID, importID, questionID, userID string, draft PaperImportDraftQuestion) error {
	if draft.AnswerKey != nil {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM question_answer_key WHERE tenant_id=$1 AND question_id=$2::uuid AND deleted_at IS NULL`, tenantID, questionID).Scan(&count); err != nil {
			return err
		}
		key, err := s.insertAnswerKey(ctx, tx, tenantID, questionID, userID, fmt.Sprintf("v%d", count+1), *draft.AnswerKey)
		if err != nil {
			return err
		}
		refs, _ := json.Marshal(draft.SourceRefs)
		if _, err = tx.ExecContext(ctx, `UPDATE question_answer_key SET paper_import_id=$3::uuid,paper_import_candidate_id=NULLIF($4,''),paper_import_source_refs=$5 WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, key.ID, importID, draft.AnswerCandidateID, refs); err != nil {
			return err
		}
	}
	if draft.Solution != nil {
		steps, _ := json.Marshal(draft.Solution.Steps)
		refs, _ := json.Marshal(draft.Solution.SourceRefs)
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM question_solution WHERE tenant_id=$1 AND question_id=$2::uuid AND deleted_at IS NULL`, tenantID, questionID).Scan(&count); err != nil {
			return err
		}
		verification := "machine"
		if stringSet(draft.HumanConfirmedFields)["solution"] {
			verification = "human_confirmed"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO question_solution(tenant_id,question_id,paper_import_id,solution_version,raw_text,steps,source_refs,verification_status,created_by) VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9::uuid)`, tenantID, questionID, importID, fmt.Sprintf("v%d", count+1), draft.Solution.RawText, steps, refs, verification, userID); err != nil {
			return err
		}
	}
	if draft.Rubric == nil {
		return nil
	}
	// 导入确认与直接保存执行相同的 ID 边界；模型候选不能绕过人工编辑入口的校验。
	if !ValidRubricPointIdentities(draft.Rubric.Points) {
		return ErrInvalidInput
	}
	status := draft.Rubric.Status
	if status == "" {
		status = "draft"
	}
	if !IsValidRubricStatus(status) {
		return ErrInvalidInput
	}
	// 导入可以生成评分细则草稿；锁定必须有人工确认，不能把模型候选直接当成最终规则。
	if status == "locked" && !stringSet(draft.HumanConfirmedFields)["rubric"] {
		return ErrInvalidInput
	}
	if !ValidRubricEvidenceRequirements(draft.Rubric.Points) {
		return ErrInvalidInput
	}
	if !scoreEqual(SumRubricPoints(draft.Rubric.Points), draft.Score) || !scoreEqual(draft.Rubric.MaxScore, draft.Score) {
		return ErrRubricMismatch
	}
	var count int
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(BOOL_OR(status='locked'),false) FROM rubric_version WHERE tenant_id=$1 AND question_id=$2::uuid AND deleted_at IS NULL`, tenantID, questionID).Scan(&count, &locked); err != nil {
		return err
	}
	if locked {
		return ErrRubricLocked
	}
	points, _ := json.Marshal(draft.Rubric.Points)
	deductions, _ := json.Marshal(draft.Rubric.Deductions)
	examples, _ := json.Marshal(draft.Rubric.Examples)
	hash := contentHash(points, deductions, examples)
	var versionID string
	if err := tx.QueryRowContext(ctx, `INSERT INTO rubric_version (tenant_id,question_id,version,status,content_hash,created_by) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`, tenantID, questionID, fmt.Sprintf("v%d", count+1), status, hash, userID).Scan(&versionID); err != nil {
		return err
	}
	refs, _ := json.Marshal(draft.SourceRefs)
	_, err := tx.ExecContext(ctx, `INSERT INTO question_rubric (tenant_id,question_id,rubric_version_id,status,max_score,points,deductions,examples,created_by,paper_import_id,paper_import_candidate_id,paper_import_source_refs) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::uuid,$11,$12)`, tenantID, questionID, versionID, status, draft.Rubric.MaxScore, points, deductions, examples, userID, importID, draft.RubricCandidateID, refs)
	return err
}

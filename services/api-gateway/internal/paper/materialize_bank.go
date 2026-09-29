package paper

import (
	"context"
	"database/sql"
	"encoding/json"
)

// BankQuestionFacts is a frozen input, with no question-bank Store dependency.
// The caller owns the transaction, target revision and authorization checks.
type BankQuestionFacts struct {
	ItemID, VersionID, BundleHash, QuestionNo, QuestionType, Stem, Archetype string
	SortOrder                                                                int
	Score                                                                    float64
	KnowledgePoints                                                          []string
	Content                                                                  any
	Assets                                                                   any
	TemplateVersionID                                                        *string
	Answer                                                                   *AnswerKeyInput
	Solution                                                                 *SolutionInput
	Rubric                                                                   *RubricInput
}

func MaterializeBankQuestionTx(ctx context.Context, tx *sql.Tx, tenant, exam, actor string, f BankQuestionFacts) (Question, error) {
	if err := ensureExamPaperMutableTx(ctx, tx, tenant, exam); err != nil {
		return Question{}, err
	}
	kp, _ := json.Marshal(f.KnowledgePoints)
	frozen, _ := json.Marshal(map[string]any{"schema_version": 2, "content": f.Content, "assets": f.Assets, "template_version_id": f.TemplateVersionID})
	var q Question
	err := scanQuestion(tx.QueryRowContext(ctx, `INSERT INTO question(tenant_id,exam_id,question_no,question_type,score,stem,knowledge_points,answer_area,sort_order,status,assessment_archetype,source_type,source_bank_item_id,source_bank_item_version_id,source_content_hash,bank_content) VALUES($1,$2,$3,$4,$5,$6,$7,NULL,$8,'active',$9,'question_bank',$10,$11,$12,$13) RETURNING id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),question_no,question_type,score::float8,COALESCE(stem,''),knowledge_points,answer_area,sort_order,status`, tenant, exam, f.QuestionNo, f.QuestionType, f.Score, f.Stem, kp, f.SortOrder, f.Archetype, f.ItemID, f.VersionID, f.BundleHash, frozen), &q)
	if err != nil {
		return q, err
	}
	q.AssessmentArchetype = f.Archetype
	q.SourceType = "question_bank"
	q.SourceBankItemID = f.ItemID
	q.SourceBankItemVersionID = f.VersionID
	q.SourceContentHash = f.BundleHash
	_ = json.Unmarshal(frozen, &q.BankContent)
	store := PostgresStore{}
	if f.Answer != nil {
		a, err := store.insertAnswerKey(ctx, tx, tenant, q.ID, actor, "v1", *f.Answer)
		if err != nil {
			return q, err
		}
		q.AnswerKey = &a
	}
	if f.Solution != nil {
		steps, _ := json.Marshal(f.Solution.Steps)
		var id string
		err = tx.QueryRowContext(ctx, `INSERT INTO question_solution(tenant_id,question_id,solution_version,raw_text,steps,source_refs,verification_status,created_by) VALUES($1,$2,'v1',$3,$4,'[]','human_confirmed',$5) RETURNING id::text`, tenant, q.ID, f.Solution.RawText, steps, actor).Scan(&id)
		if err != nil {
			return q, err
		}
		q.Solution = &QuestionSolution{ID: id, QuestionID: q.ID, SolutionVersion: "v1", RawText: f.Solution.RawText, Steps: f.Solution.Steps, SourceRefs: []PaperImportSourceRef{}, VerificationStatus: "human_confirmed"}
	}
	if f.Rubric != nil {
		r := f.Rubric
		points, _ := json.Marshal(r.Points)
		deductions, _ := json.Marshal(r.Deductions)
		examples, _ := json.Marshal(r.Examples)
		var versionID, id string
		err = tx.QueryRowContext(ctx, `INSERT INTO rubric_version(tenant_id,question_id,version,status,content_hash,created_by) VALUES($1,$2,'v1','approved',$3,$4) RETURNING id::text`, tenant, q.ID, contentHash(points, deductions, examples), actor).Scan(&versionID)
		if err != nil {
			return q, err
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO question_rubric(tenant_id,question_id,rubric_version_id,status,max_score,points,deductions,examples,created_by) VALUES($1,$2,$3,'approved',$4,$5,$6,$7,$8) RETURNING id::text`, tenant, q.ID, versionID, r.MaxScore, points, deductions, examples, actor).Scan(&id)
		if err != nil {
			return q, err
		}
		q.Rubric = &Rubric{ID: id, QuestionID: q.ID, Version: "v1", Status: "approved", MaxScore: r.MaxScore, Points: r.Points, Deductions: r.Deductions, Examples: r.Examples}
	}
	err = syncQuestionAssessmentArchetypeTx(ctx, tx, tenant, exam, q.ID, f.Archetype)
	return q, err
}

// Preserve existing risk and AI gates when authored content changes the
// question archetype. A missing subject profile remains missing.
func syncQuestionAssessmentArchetypeTx(ctx context.Context, tx *sql.Tx, tenant, exam, question, archetype string) error {
	if _, err := tx.ExecContext(ctx, `SELECT assessment_apply_default_question_config($1,$2,$3)`, tenant, exam, question); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE question_assessment_config c SET archetype_code=$4,
 allowed_evidence_types=COALESCE((SELECT jsonb_agg(e.value ORDER BY e.value) FROM question_archetype a,subject_profile p,jsonb_array_elements_text(a.evidence_types_json) e(value) WHERE a.code=$4 AND p.tenant_id=c.tenant_id AND p.id=c.subject_profile_id AND p.evidence_policy_json->'allowed_types' ? e.value),'[]'),
 scoring_policy_json=jsonb_set(c.scoring_policy_json,'{mode}',to_jsonb(CASE WHEN c.scoring_policy_json->>'mode'='DUAL_HUMAN' THEN 'DUAL_HUMAN' ELSE (SELECT default_scoring_mode FROM question_archetype WHERE code=$4) END)),revision=revision+1
 WHERE tenant_id=$1 AND exam_id=$2 AND question_id=$3 AND archetype_code<>$4`, tenant, exam, question, archetype)
	return err
}

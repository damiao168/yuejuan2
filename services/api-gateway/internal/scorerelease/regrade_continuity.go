package scorerelease

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
)

func questionFactKey(submissionID, questionID string) string {
	return submissionID + "\x00" + questionID
}

func indexedQuestions(facts []SubmissionFact) map[string]QuestionFact {
	out := map[string]QuestionFact{}
	for _, fact := range facts {
		for _, question := range fact.Questions {
			out[questionFactKey(fact.SubmissionID, question.QuestionID)] = question
		}
	}
	return out
}

// 工作表尚未同步已发布更正时，普通版本继承当前正式事实；仍允许已有的普通发布流程采用其它工作表变更。
// 调用方持有考试锁，发布时还会记录实际继承的来源，避免旧工作表覆盖已经公布的更正。
func (s *PostgresStore) ordinaryFactsTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) ([]SubmissionFact, string, error) {
	facts, err := s.currentFactsTx(ctx, tx, tenantID, examID)
	if err != nil {
		return nil, "", err
	}
	if preserved, err := s.preservesRegradeScoresTx(ctx, tx, tenantID, examID, facts); err != nil {
		return nil, "", err
	} else if preserved {
		return facts, "", nil
	}
	var currentID string
	err = tx.QueryRowContext(ctx, `SELECT release_id::text FROM score_release_current WHERE tenant_id=$1 AND exam_id=$2::uuid`, tenantID, examID).Scan(&currentID)
	if errors.Is(err, sql.ErrNoRows) {
		facts, err := s.currentFactsTx(ctx, tx, tenantID, examID)
		return facts, "", err
	}
	if err != nil {
		return nil, "", err
	}
	facts, err = s.releaseFactsTx(ctx, tx, tenantID, currentID)
	return facts, currentID, err
}

// 调用方持有考试锁；只保护当前正式快照仍沿用的重评事实，历史已被显式回滚的更正不强行恢复。
func (s *PostgresStore) preservesRegradeScoresTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, facts []SubmissionFact) (bool, error) {
	var currentID string
	if err := tx.QueryRowContext(ctx, `SELECT release_id::text FROM score_release_current WHERE tenant_id=$1 AND exam_id=$2::uuid`, tenantID, examID).Scan(&currentID); err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}
	current, err := s.releaseFactsTx(ctx, tx, tenantID, currentID)
	if err != nil {
		return false, err
	}
	// 普通版本追平分数后来源可能切回工作表；保护沿当前版本链继承，不能依赖来源字段仍相同。
	// 显式回滚改为沿其目标版本继续追溯，不能把已撤回分支的更正重新强加到当前成绩。
	rows, err := tx.QueryContext(ctx, `
WITH RECURSIVE active_lineage AS (
 SELECT id,tenant_id,exam_id,source,source_release_id,supersedes_release_id,ARRAY[id] AS visited
 FROM score_release WHERE tenant_id=$1 AND id=$2::uuid AND exam_id=$3::uuid AND status='published'
 UNION ALL
 SELECT parent.id,parent.tenant_id,parent.exam_id,parent.source,parent.source_release_id,parent.supersedes_release_id,lineage.visited || parent.id
 FROM active_lineage lineage JOIN score_release parent
 ON parent.tenant_id=lineage.tenant_id AND parent.exam_id=lineage.exam_id
 AND parent.id=CASE WHEN lineage.source='rollback' THEN lineage.source_release_id ELSE lineage.supersedes_release_id END
 WHERE parent.status='published' AND NOT parent.id=ANY(lineage.visited)
)
SELECT DISTINCT cq.submission_id::text, cq.question_id::text
FROM score_release_question cq
JOIN score_release_question corrected ON corrected.tenant_id=cq.tenant_id
 AND corrected.submission_id=cq.submission_id AND corrected.question_id=cq.question_id
JOIN active_lineage r ON r.tenant_id=corrected.tenant_id AND r.id=corrected.release_id
JOIN score_release_question original ON original.tenant_id=r.tenant_id AND original.release_id=r.source_release_id
 AND original.submission_id=corrected.submission_id AND original.question_id=corrected.question_id
WHERE cq.tenant_id=$1 AND cq.release_id=$2::uuid AND r.exam_id=$3::uuid
 AND r.source='regrade'
 AND ROW(cq.score,cq.max_score) IS NOT DISTINCT FROM ROW(corrected.score,corrected.max_score)
 AND ROW(original.score,original.max_score) IS DISTINCT FROM ROW(corrected.score,corrected.max_score)
`, tenantID, currentID, examID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	protected := map[string]bool{}
	for rows.Next() {
		var submissionID, questionID string
		if err := rows.Scan(&submissionID, &questionID); err != nil {
			return false, err
		}
		protected[questionFactKey(submissionID, questionID)] = true
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return preservesRegradeScores(facts, current, protected), nil
}

func sameQuestionFact(first, second QuestionFact) bool {
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	return string(a) == string(b)
}

func sameQuestionScore(first, second QuestionFact) bool {
	return math.Abs(first.Score-second.Score) <= 0.000001 && math.Abs(first.MaxScore-second.MaxScore) <= 0.000001
}

// rebaseRegradeFacts 只搬运本次重评相对原发布的变化。其他任务先发布的题分沿用当前版本；
// 同一答卷同题已有不同更正、题目或名册结构变化时拒绝合并，不能猜测哪次复核应覆盖另一次。
func rebaseRegradeFacts(base, draft, current []SubmissionFact) ([]SubmissionFact, bool) {
	before, proposed, present := indexedQuestions(base), indexedQuestions(draft), indexedQuestions(current)
	if len(before) != len(proposed) || len(before) != len(present) || len(base) != len(draft) || len(base) != len(current) {
		return nil, false
	}
	changes := map[string]QuestionFact{}
	for key, old := range before {
		next, hasNext := proposed[key]
		now, hasNow := present[key]
		if !hasNext || !hasNow || old.MaxScore != next.MaxScore || old.MaxScore != now.MaxScore {
			return nil, false
		}
		if !sameQuestionFact(old, next) {
			if !sameQuestionFact(old, now) && !sameQuestionFact(next, now) {
				return nil, false
			}
			changes[key] = next
		}
	}
	out := cloneFacts(current)
	for i := range out {
		var totalHundredths int64
		for j := range out[i].Questions {
			if change, ok := changes[questionFactKey(out[i].SubmissionID, out[i].Questions[j].QuestionID)]; ok {
				out[i].Questions[j] = change
			}
			// 题分来自 NUMERIC(8,2) 的已发布快照。用整数分位求和，使预览及落库总分都与题分一致。
			totalHundredths += int64(math.Round(out[i].Questions[j].Score * 100))
		}
		out[i].TotalScore = float64(totalHundredths) / 100
		if out[i].TotalScore > out[i].MaxScore+0.000001 {
			return nil, false
		}
	}
	return out, true
}

// 普通草稿不能把已发布的重评分数恢复成未同步的工作表旧值；需要再次更正时继续走有来源的重评流程。
func preservesRegradeScores(facts, current []SubmissionFact, protected map[string]bool) bool {
	proposed := indexedQuestions(facts)
	for key, question := range indexedQuestions(current) {
		if !protected[key] {
			continue
		}
		next, ok := proposed[key]
		if !ok || math.Abs(next.Score-question.Score) > 0.000001 || math.Abs(next.MaxScore-question.MaxScore) > 0.000001 {
			return false
		}
	}
	return true
}

func (s *MemoryStore) preservesRegradeScoresLocked(tenantID, examID string, facts []SubmissionFact) bool {
	currentID := s.current[tenantID+":"+examID]
	current := s.snapshotFactsLocked(currentID)
	protected := map[string]bool{}
	present := indexedQuestions(current)
	// 与 PostgreSQL 使用同一条有效版本链；普通版本换来源不消除保护，回滚转向它选定的目标。
	visited := map[string]bool{}
	for id := currentID; id != "" && !visited[id]; {
		visited[id] = true
		release, ok := s.releases[id]
		if !ok || release.TenantID != tenantID || release.ExamID != examID || release.Status != StatusPublished {
			break
		}
		if release.Source == SourceRegrade && release.SourceReleaseID != "" {
			old := indexedQuestions(s.snapshotFactsLocked(release.SourceReleaseID))
			for key, corrected := range indexedQuestions(s.snapshotFactsLocked(release.ID)) {
				if previous, ok := old[key]; ok && !sameQuestionScore(previous, corrected) && sameQuestionScore(present[key], corrected) {
					protected[key] = true
				}
			}
		}
		id = release.SupersedesReleaseID
		if release.Source == SourceRollback {
			id = release.SourceReleaseID
		}
	}
	return preservesRegradeScores(facts, current, protected)
}

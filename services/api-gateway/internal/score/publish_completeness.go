package score

import (
	"context"
	"database/sql"

	"edugrade-enterprise/services/api-gateway/internal/score/autoprep"
)

// 确认和发布都要从收到的答卷反查成绩，不能只检查已经生成的题分和总分。
// 无答卷的冻结名册学生自动缺考；收到的答卷仍必须完成身份、页数和评分检查。
func appendGradeCompletenessIssues(issues []QualityIssue, missingTotals, missingQuestions int) []QualityIssue {
	if missingTotals > 0 {
		issues = append(issues, QualityIssue{Code: "missing_submission_grades", Message: "matched submissions have no submission grade", Blocking: true, Count: missingTotals})
	}
	if missingQuestions > 0 {
		issues = append(issues, QualityIssue{Code: "question_coverage_incomplete", Message: "matched submissions do not cover all official questions with final grades", Blocking: true, Count: missingQuestions})
	}
	return issues
}

func (s *PostgresStore) gradeCompletenessTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, issues []QualityIssue, prepared bool) ([]QualityIssue, error) {
	// 历史直接导入可能保留 unassigned，但仍须冻结名册内唯一学生关系；unknown/conflict 不享受此兼容。
	const matchedBody = `matched AS (
  SELECT sub.* FROM submission sub
  JOIN exam_candidate_snapshot candidate ON candidate.tenant_id=sub.tenant_id AND candidate.exam_id=sub.exam_id AND candidate.student_id=sub.student_id
  WHERE sub.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.deleted_at IS NULL
    AND sub.identity_status NOT IN ('unknown','conflict')
    AND sub.actual_page_count>=sub.expected_page_count AND sub.quality_status<>'failed' AND sub.status<>'rejected'
    AND (SELECT COUNT(*) FROM submission other WHERE other.tenant_id=sub.tenant_id AND other.exam_id=sub.exam_id AND other.student_id=sub.student_id AND other.deleted_at IS NULL)=1
)
`
	matched := `WITH ` + matchedBody
	// 准备后版本：自动终评算作题目已覆盖，准备会新建总分的答卷不算缺总分；其余条件与严格版相同。
	missingTotalExtra, missingQuestionExtra := "", ""
	if prepared {
		matched = `WITH ` + autoprep.FinalSourcesCTE + `, ` + matchedBody
		missingTotalExtra = ` AND NOT ` + autoprep.NewTotalPredicate("sub")
		missingQuestionExtra = `
    AND NOT EXISTS (
      SELECT 1 FROM answer_segment segment
      JOIN automatic_final af ON af.answer_segment_id=segment.id
      WHERE segment.tenant_id=sub.tenant_id AND segment.submission_id=sub.id AND segment.question_id=question.question_id AND segment.deleted_at IS NULL
    )`
	}
	missingTotals, err := scalarCountTx(ctx, tx, matched+`SELECT COUNT(*) FROM matched sub WHERE NOT EXISTS (
  SELECT 1 FROM submission_grade grade WHERE grade.tenant_id=sub.tenant_id AND grade.exam_id=sub.exam_id AND grade.submission_id=sub.id AND grade.deleted_at IS NULL
)`+missingTotalExtra, tenantID, examID)
	if err != nil {
		return nil, err
	}
	// 准备考试时冻结的题集才是完整性检查的覆盖范围；不把准备后新增的题目混入历史考试。
	missingQuestions, err := scalarCountTx(ctx, tx, matched+`SELECT COUNT(*) FROM matched sub WHERE EXISTS (
  SELECT 1 FROM exam_question_snapshot question WHERE question.tenant_id=sub.tenant_id AND question.exam_id=sub.exam_id
    AND NOT EXISTS (
      SELECT 1 FROM answer_segment segment
      JOIN final_grade grade ON grade.tenant_id=segment.tenant_id AND grade.answer_segment_id=segment.id AND grade.deleted_at IS NULL
      WHERE segment.tenant_id=sub.tenant_id AND segment.submission_id=sub.id AND segment.question_id=question.question_id AND segment.deleted_at IS NULL
    )`+missingQuestionExtra+`
)`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	return appendGradeCompletenessIssues(issues, missingTotals, missingQuestions), nil
}

func (s *MemoryStore) gradeCompletenessLocked(examID string, issues []QualityIssue) []QualityIssue {
	missingTotals, missingQuestions := 0, 0
	// 内存夹具没有独立题目仓库，题集取本考试已注入的题目标识；生产使用冻结快照。
	questions := map[string]bool{}
	for _, segment := range s.segments {
		if segment.ExamID == examID {
			questions[segment.QuestionID] = true
		}
	}
	for _, entry := range s.rosterReportLocked(examID).Entries {
		if entry.ResolutionCode != "graded" && entry.ResolutionCode != "grading_incomplete" {
			continue
		}
		if _, ok := s.submissions[key(examID, entry.SubmissionID)]; !ok {
			missingTotals++
		}
		covered := map[string]bool{}
		for _, segment := range s.segments {
			if segment.ExamID == examID && segment.SubmissionID == entry.SubmissionID {
				if _, ok := s.finalBySeg[segment.AnswerSegmentID]; ok {
					covered[segment.QuestionID] = true
				}
			}
		}
		if len(covered) != len(questions) {
			missingQuestions++
		}
	}
	return appendGradeCompletenessIssues(issues, missingTotals, missingQuestions)
}

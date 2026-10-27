package score

import (
	"context"
	"database/sql"
)

// 发布要从收到的答卷反查成绩，不能只检查已经生成的题分和总分。
// 缺交、身份冲突和拒收仍由名册门禁说明；这里补上有效答卷尚未开始阅卷的空洞。
func appendPublishCompletenessIssues(issues []QualityIssue, missingTotals, missingQuestions int) []QualityIssue {
	if missingTotals > 0 {
		issues = append(issues, QualityIssue{Code: "missing_submission_grades", Message: "matched submissions have no submission grade", Blocking: true, Count: missingTotals})
	}
	if missingQuestions > 0 {
		issues = append(issues, QualityIssue{Code: "question_coverage_incomplete", Message: "matched submissions do not cover all official questions with final grades", Blocking: true, Count: missingQuestions})
	}
	return issues
}

func (s *PostgresStore) publishCompletenessTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, issues []QualityIssue) ([]QualityIssue, error) {
	const matched = `WITH matched AS (
  SELECT sub.* FROM submission sub
  JOIN exam_candidate_snapshot candidate ON candidate.tenant_id=sub.tenant_id AND candidate.exam_id=sub.exam_id AND candidate.student_id=sub.student_id
  LEFT JOIN exam_student_attendance attendance ON attendance.tenant_id=sub.tenant_id AND attendance.exam_id=sub.exam_id AND attendance.student_id=sub.student_id AND attendance.deleted_at IS NULL
  WHERE sub.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.deleted_at IS NULL
    AND COALESCE(attendance.status,'expected')<>'absent'
    AND sub.actual_page_count>=sub.expected_page_count AND sub.quality_status<>'failed' AND sub.status<>'rejected'
    AND (SELECT COUNT(*) FROM submission other WHERE other.tenant_id=sub.tenant_id AND other.exam_id=sub.exam_id AND other.student_id=sub.student_id AND other.deleted_at IS NULL)=1
)
`
	missingTotals, err := scalarCountTx(ctx, tx, matched+`SELECT COUNT(*) FROM matched sub WHERE NOT EXISTS (
  SELECT 1 FROM submission_grade grade WHERE grade.tenant_id=sub.tenant_id AND grade.exam_id=sub.exam_id AND grade.submission_id=sub.id AND grade.deleted_at IS NULL
)`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	// 准备考试时冻结的题集才是发布覆盖范围；不把准备后新增的题目混入历史考试。
	missingQuestions, err := scalarCountTx(ctx, tx, matched+`SELECT COUNT(*) FROM matched sub WHERE EXISTS (
  SELECT 1 FROM exam_question_snapshot question WHERE question.tenant_id=sub.tenant_id AND question.exam_id=sub.exam_id
    AND NOT EXISTS (
      SELECT 1 FROM answer_segment segment
      JOIN final_grade grade ON grade.tenant_id=segment.tenant_id AND grade.answer_segment_id=segment.id AND grade.deleted_at IS NULL
      WHERE segment.tenant_id=sub.tenant_id AND segment.submission_id=sub.id AND segment.question_id=question.question_id AND segment.deleted_at IS NULL
    )
)`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	return appendPublishCompletenessIssues(issues, missingTotals, missingQuestions), nil
}

func (s *MemoryStore) publishCompletenessLocked(examID string, issues []QualityIssue) []QualityIssue {
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
	return appendPublishCompletenessIssues(issues, missingTotals, missingQuestions)
}

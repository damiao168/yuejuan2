package scorerelease

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
)

func (s *PostgresStore) studentRankings(ctx context.Context, tenantID, examID, releaseID, studentID string, score float64) (*StudentRankings, error) {
	var out StudentRankings
	err := s.db.QueryRowContext(ctx, `
WITH target AS (
  SELECT class_id_snapshot,grade_id_snapshot FROM exam_candidate_snapshot
  WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND student_id=$3::uuid
), population AS (
  SELECT item.total_score::float8,c.class_id_snapshot,c.grade_id_snapshot
  FROM score_release_item item
  JOIN exam_candidate_snapshot c ON c.tenant_id=item.tenant_id AND c.exam_id=$2::uuid AND c.student_id=item.student_id
  WHERE item.tenant_id=$1::uuid AND item.release_id=$4::uuid
)
SELECT 1+count(*) FILTER(WHERE p.class_id_snapshot=t.class_id_snapshot AND p.total_score>$5),
       count(*) FILTER(WHERE p.class_id_snapshot=t.class_id_snapshot),
       1+count(*) FILTER(WHERE p.grade_id_snapshot=t.grade_id_snapshot AND p.total_score>$5),
       count(*) FILTER(WHERE p.grade_id_snapshot=t.grade_id_snapshot)
FROM population p CROSS JOIN target t
`, tenantID, examID, studentID, releaseID, score).Scan(&out.ClassRank, &out.ClassSize, &out.GradeRank, &out.GradeSize)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if out.ClassSize < privacyMinCohortSize {
		out.ClassUnavailableReason = "small_cohort"
		out.ClassRank, out.ClassSize = 0, 0
	}
	if out.GradeSize < privacyMinCohortSize {
		out.GradeUnavailableReason = "small_cohort"
		out.GradeRank, out.GradeSize = 0, 0
	}
	return &out, err
}

func (s *PostgresStore) studentSubjectBalance(ctx context.Context, tenantID, examID, studentID string) ([]StudentSubjectBalance, error) {
	// 同一考次中每科只取最新且允许展示统计的发布版本；人数不足的科目直接省略。
	rows, err := s.db.QueryContext(ctx, `
WITH target AS (
  SELECT id,exam_session_id FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
), latest_subject_release AS (
  SELECT DISTINCT ON(lower(e.subject)) e.subject,current_release.release_id
  FROM target
  JOIN exam e ON e.tenant_id=$1::uuid AND e.deleted_at IS NULL
   AND ((target.exam_session_id IS NOT NULL AND e.exam_session_id=target.exam_session_id) OR e.id=target.id)
  JOIN score_release_current current_release ON current_release.tenant_id=e.tenant_id AND current_release.exam_id=e.id
  JOIN score_release release ON release.tenant_id=current_release.tenant_id AND release.id=current_release.release_id AND release.status='published'
  JOIN score_release_item mine ON mine.tenant_id=release.tenant_id AND mine.release_id=release.id AND mine.student_id=$3::uuid
  WHERE current_release.tenant_id=$1::uuid AND COALESCE((release.visibility_policy->>'show_cohort_statistics')::boolean,false)
  ORDER BY lower(e.subject),release.published_at DESC,release.id DESC
)
SELECT latest.subject,
       CASE WHEN mine.max_score>0 THEN mine.total_score/mine.max_score ELSE 0 END::float8,
       avg(CASE WHEN cohort.max_score>0 THEN cohort.total_score/cohort.max_score ELSE 0 END)::float8,
       count(cohort.student_id)::int
FROM latest_subject_release latest
JOIN score_release_item mine ON mine.tenant_id=$1::uuid AND mine.release_id=latest.release_id AND mine.student_id=$3::uuid
JOIN score_release_item cohort ON cohort.tenant_id=mine.tenant_id AND cohort.release_id=mine.release_id AND cohort.student_id IS NOT NULL
GROUP BY latest.subject,mine.total_score,mine.max_score
HAVING count(cohort.student_id)>=$4
ORDER BY latest.subject
`, tenantID, examID, studentID, privacyMinCohortSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StudentSubjectBalance{}
	for rows.Next() {
		var item StudentSubjectBalance
		if err := rows.Scan(&item.Subject, &item.StudentScoreRate, &item.SchoolMeanScoreRate, &item.SampleSize); err != nil {
			return nil, err
		}
		item.Subject = strings.TrimSpace(item.Subject)
		item.StudentScoreRate = math.Round(item.StudentScoreRate*1000) / 1000
		item.SchoolMeanScoreRate = math.Round(item.SchoolMeanScoreRate*1000) / 1000
		if item.Subject != "" {
			out = append(out, item)
		}
	}
	return out, rows.Err()
}

type studentQuestionAggregates struct {
	Comparison *StudentQuestionScoreComparison
	Cohort     *StudentQuestionReference
}

func (s *PostgresStore) studentQuestionStatistics(ctx context.Context, tenantID, examID, releaseID, studentID string) (map[string]studentQuestionAggregates, error) {
	// 分数固定读取传入的发布版本；班校范围由考试冻结班级确定，不能因学生转班、转校或班级软删除而漂移。
	// 同一发布可含多校，先限本校再聚合；四项比较允许单份公开成绩，旧 Cohort 分析仍保留班校各自的人数门槛。
	rows, err := s.db.QueryContext(ctx, `
WITH target AS (
  SELECT c.class_id_snapshot,frozen_class.school_id
  FROM exam_candidate_snapshot c
  JOIN school_class frozen_class ON frozen_class.tenant_id=c.tenant_id AND frozen_class.id=c.class_id_snapshot
  WHERE c.tenant_id=$1::uuid AND c.exam_id=$2::uuid AND c.student_id=$4::uuid
), facts AS (
  SELECT q.question_id,q.score::float8,q.max_score::float8,c.class_id_snapshot
  FROM score_release_question q
  JOIN score_release release ON release.tenant_id=q.tenant_id AND release.id=q.release_id AND release.exam_id=$2::uuid
  JOIN score_release_item item ON item.tenant_id=q.tenant_id AND item.release_id=q.release_id AND item.submission_id=q.submission_id
  JOIN exam_candidate_snapshot c ON c.tenant_id=item.tenant_id AND c.exam_id=$2::uuid AND c.student_id=item.student_id
  JOIN school_class frozen_class ON frozen_class.tenant_id=c.tenant_id AND frozen_class.id=c.class_id_snapshot
  JOIN target t ON frozen_class.school_id=t.school_id
  WHERE q.tenant_id=$1::uuid AND q.release_id=$3::uuid
)
SELECT f.question_id::text,
       count(*)::int,
       count(*) FILTER(WHERE f.class_id_snapshot=t.class_id_snapshot)::int,
       avg(f.score) FILTER(WHERE f.class_id_snapshot=t.class_id_snapshot)::float8,
       max(f.score) FILTER(WHERE f.class_id_snapshot=t.class_id_snapshot)::float8,
       avg(f.score)::float8,
       max(f.score)::float8,
       percentile_cont(.5) WITHIN GROUP(ORDER BY f.score)::float8,
       avg(CASE WHEN f.max_score>0 THEN f.score/f.max_score ELSE 0 END)::float8,
       avg(CASE WHEN f.score=f.max_score THEN 1 ELSE 0 END)::float8,
       avg(CASE WHEN f.score=0 THEN 1 ELSE 0 END)::float8
FROM facts f CROSS JOIN target t GROUP BY f.question_id
`, tenantID, examID, releaseID, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]studentQuestionAggregates{}
	for rows.Next() {
		var id string
		var ref StudentQuestionReference
		var classSize int
		var classMean, classMax sql.NullFloat64
		var schoolMean, schoolMax, median float64
		if err := rows.Scan(&id, &ref.SampleSize, &classSize, &classMean, &classMax, &schoolMean, &schoolMax, &median, &ref.MeanScoreRate, &ref.FullScoreRate, &ref.ZeroScoreRate); err != nil {
			return nil, err
		}
		comparison := &StudentQuestionScoreComparison{SchoolMeanScore: &schoolMean, SchoolMaxScore: &schoolMax}
		if classSize > 0 && classMean.Valid && classMax.Valid {
			comparison.ClassMeanScore, comparison.ClassMaxScore = &classMean.Float64, &classMax.Float64
		}
		aggregates := studentQuestionAggregates{Comparison: comparison}
		if ref.SampleSize >= privacyMinCohortSize {
			ref.SchoolMeanScore, ref.MedianScore = &schoolMean, &median
			if classSize >= privacyMinCohortSize && classMean.Valid && classMax.Valid {
				ref.ClassMeanScore = &classMean.Float64
			} else {
				ref.ClassUnavailableReason = "small_cohort"
			}
			aggregates.Cohort = &ref
		}
		out[id] = aggregates
	}
	return out, rows.Err()
}

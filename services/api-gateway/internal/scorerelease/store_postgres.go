package scorerelease

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
)

// DashboardReader is intentionally aggregate-only. The release service never
// receives Gold answers, grader observations, candidate text, or other
// sensitive quality data from the dashboard.
type DashboardReader interface {
	Get(context.Context, string, string) (qualitydashboard.Dashboard, error)
}

type PostgresStore struct {
	db        *sql.DB
	dashboard DashboardReader
	now       func() time.Time
}

func NewPostgresStore(db *sql.DB, dashboard DashboardReader) *PostgresStore {
	return &PostgresStore{db: db, dashboard: dashboard, now: func() time.Time { return time.Now().UTC() }}
}

func (s *PostgresStore) Create(ctx context.Context, tenantID, examID, actorID string, input CreateInput) (Release, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExam(ctx, tx, tenantID, examID); err != nil {
		return Release{}, err
	}
	if release, found, err := s.findIdempotentTx(ctx, tx, tenantID, examID, input.IdempotencyKey); err != nil {
		return Release{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return Release{}, err
		}
		return release, nil
	}
	facts, err := s.currentFactsTx(ctx, tx, tenantID, examID)
	if err != nil {
		return Release{}, err
	}
	if len(facts) == 0 {
		return Release{}, ErrNotFound
	}
	version, err := s.nextVersionTx(ctx, tx, tenantID, examID)
	if err != nil {
		return Release{}, err
	}
	release, err := s.insertReleaseTx(ctx, tx, tenantID, examID, actorID, version, input.Source, input.Reason, input.IdempotencyKey, input.VisibilityPolicy, input.AppealWindow, "")
	if err != nil {
		return Release{}, err
	}
	if err := s.insertFactsTx(ctx, tx, tenantID, release.ID, facts); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return Release{}, err
	}
	return release, nil
}

func (s *PostgresStore) CreateRollback(ctx context.Context, tenantID, examID, actorID string, input RollbackInput) (Release, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExam(ctx, tx, tenantID, examID); err != nil {
		return Release{}, err
	}
	if release, found, err := s.findIdempotentTx(ctx, tx, tenantID, examID, input.IdempotencyKey); err != nil {
		return Release{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return Release{}, err
		}
		return release, nil
	}
	source, err := s.releaseTx(ctx, tx, tenantID, input.SourceReleaseID, false)
	if err != nil {
		return Release{}, err
	}
	if source.ExamID != examID || source.Status != StatusPublished {
		return Release{}, ErrInvalidTransition
	}
	// Historical releases may carry a high-score sharing policy. Do not
	// propagate it into a new release while anonymous assets are unavailable.
	source.VisibilityPolicy.ShowHighScorePaper = false
	version, err := s.nextVersionTx(ctx, tx, tenantID, examID)
	if err != nil {
		return Release{}, err
	}
	release, err := s.insertReleaseTx(ctx, tx, tenantID, examID, actorID, version, SourceRollback, input.Reason, input.IdempotencyKey, source.VisibilityPolicy, source.AppealWindow, source.ID)
	if err != nil {
		return Release{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO score_release_item (tenant_id, release_id, student_id, submission_id, total_score, max_score, status, snapshot_hash)
SELECT tenant_id, $3::uuid, student_id, submission_id, total_score, max_score, status, snapshot_hash
FROM score_release_item WHERE tenant_id = $1 AND release_id = $2::uuid
`, tenantID, source.ID, release.ID); err != nil {
		return Release{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO score_release_question (tenant_id, release_id, submission_id, question_id, question_no, final_grade_id, score, max_score, source_type, source_id, student_explanation)
SELECT tenant_id, $3::uuid, submission_id, question_id, question_no, final_grade_id, score, max_score, source_type, source_id, student_explanation
FROM score_release_question WHERE tenant_id = $1 AND release_id = $2::uuid
`, tenantID, source.ID, release.ID); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return Release{}, err
	}
	return release, nil
}

// CreateFromRegrade copies a published source snapshot under the exam lock,
// applies only the reviewed plan's question replacements in memory, then
// writes a new draft. It intentionally never joins current final_grade or
// submission_grade, so later mutable activity cannot leak into the release.
func (s *PostgresStore) CreateFromRegrade(ctx context.Context, tenantID, examID, actorID string, input CreateRegradeInput) (Release, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExam(ctx, tx, tenantID, examID); err != nil {
		return Release{}, err
	}
	if release, found, err := s.findIdempotentTx(ctx, tx, tenantID, examID, input.IdempotencyKey); err != nil {
		return Release{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return Release{}, err
		}
		return release, nil
	}
	source, err := s.releaseTx(ctx, tx, tenantID, input.SourceReleaseID, false)
	if err != nil {
		return Release{}, err
	}
	if source.ExamID != examID || source.Status != StatusPublished {
		return Release{}, ErrInvalidTransition
	}
	source.VisibilityPolicy.ShowHighScorePaper = false
	facts, err := s.releaseFactsTx(ctx, tx, tenantID, source.ID)
	if err != nil {
		return Release{}, err
	}
	if len(facts) == 0 || !applyRegradeChanges(facts, input) {
		return Release{}, ErrInvalidInput
	}
	version, err := s.nextVersionTx(ctx, tx, tenantID, examID)
	if err != nil {
		return Release{}, err
	}
	release, err := s.insertReleaseTx(ctx, tx, tenantID, examID, actorID, version, SourceRegrade, input.Reason, input.IdempotencyKey, source.VisibilityPolicy, source.AppealWindow, source.ID)
	if err != nil {
		return Release{}, err
	}
	if err := s.insertFactsTx(ctx, tx, tenantID, release.ID, facts); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return Release{}, err
	}
	return release, nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID, examID string) ([]Release, error) {
	rows, err := s.db.QueryContext(ctx, releaseColumns+` FROM score_release WHERE tenant_id = $1 AND exam_id = $2::uuid ORDER BY version DESC`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		release, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, release)
	}
	return out, rows.Err()
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, id string) (Detail, error) {
	release, err := s.release(ctx, tenantID, id)
	if err != nil {
		return Detail{}, err
	}
	items, err := s.items(ctx, tenantID, id)
	if err != nil {
		return Detail{}, err
	}
	questions, err := s.questions(ctx, tenantID, id, "")
	if err != nil {
		return Detail{}, err
	}
	return Detail{Release: release, Items: items, Questions: questions}, nil
}

func (s *PostgresStore) Diff(ctx context.Context, tenantID, id, baseID string) (Diff, error) {
	current, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return Diff{}, err
	}
	base, err := s.Get(ctx, tenantID, baseID)
	if err != nil {
		return Diff{}, err
	}
	if current.Release.ExamID != base.Release.ExamID {
		return Diff{}, ErrInvalidInput
	}
	return releaseDiff(current, base), nil
}

func (s *PostgresStore) Gate(ctx context.Context, tenantID, examID string) (Gate, error) {
	dashboardIssues, err := s.dashboardIssues(ctx, tenantID, examID)
	if err != nil {
		return Gate{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Gate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	gate, err := s.gateTx(ctx, tx, tenantID, examID, "", dashboardIssues)
	if err != nil {
		return Gate{}, err
	}
	if err := tx.Commit(); err != nil {
		return Gate{}, err
	}
	return gate, nil
}

func (s *PostgresStore) Publish(ctx context.Context, tenantID, id, actorID string) (Release, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = tx.Rollback() }()
	release, err := s.releaseTx(ctx, tx, tenantID, id, true)
	if err != nil {
		return Release{}, err
	}
	if err := lockExam(ctx, tx, tenantID, release.ExamID); err != nil {
		return Release{}, err
	}
	if release.Status == StatusPublished {
		if err := tx.Commit(); err != nil {
			return Release{}, err
		}
		return release, nil
	}
	if release.Status != StatusDraft {
		return Release{}, ErrInvalidTransition
	}
	if release.VisibilityPolicy.ShowHighScorePaper {
		ready, err := s.anonymousPaperReadyTx(ctx, tx, tenantID, release.ID)
		if err != nil {
			return Release{}, err
		}
		if !ready {
			return Release{}, ErrAnonymousPaperUnavailable
		}
	}
	// Refresh the cross-service aggregate only after the exam advisory lock is
	// held. Score writers use the same lock, so the gate and publication cannot
	// race a normal score-finalisation transition.
	dashboardIssues, err := s.dashboardIssues(ctx, tenantID, release.ExamID)
	if err != nil {
		return Release{}, err
	}
	gate, err := s.gateTx(ctx, tx, tenantID, release.ExamID, release.ID, dashboardIssues)
	if err != nil {
		return Release{}, err
	}
	if !gate.Passed {
		return Release{}, ErrGateBlocked
	}
	gateJSON, err := json.Marshal(gate)
	if err != nil {
		return Release{}, err
	}
	var supersedes sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT release_id::text FROM score_release_current WHERE tenant_id = $1 AND exam_id = $2::uuid FOR UPDATE`, tenantID, release.ExamID).Scan(&supersedes)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Release{}, err
	}
	if release.Source == SourceRegrade && release.SourceReleaseID != "" && release.SourceReleaseID != supersedes.String {
		// 其他版本已先发布时，旧基础上的重评草稿必须重建，不能覆盖期间发布的更正。
		return Release{}, ErrStaleSource
	}
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE score_release
SET status = 'published', published_by = $3::uuid, published_at = $4, gate_snapshot = $5::jsonb, supersedes_release_id = NULLIF($6, '')::uuid
WHERE tenant_id = $1 AND id = $2::uuid AND status = 'draft'
`, tenantID, release.ID, actorID, now, string(gateJSON), supersedes.String); err != nil {
		return Release{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO score_release_current (tenant_id, exam_id, release_id, updated_at)
VALUES ($1, $2::uuid, $3::uuid, $4)
ON CONFLICT (tenant_id, exam_id) DO UPDATE SET release_id = EXCLUDED.release_id, updated_at = EXCLUDED.updated_at
`, tenantID, release.ExamID, release.ID, now); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return Release{}, err
	}
	release.Status, release.PublishedBy, release.PublishedAt, release.GateSnapshot, release.SupersedesReleaseID = StatusPublished, actorID, &now, gate, supersedes.String
	return release, nil
}

func (s *PostgresStore) CurrentPublished(ctx context.Context, tenantID, examID string) (Detail, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT release_id::text FROM score_release_current WHERE tenant_id = $1 AND exam_id = $2::uuid`, tenantID, examID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Detail{}, ErrNotFound
		}
		return Detail{}, err
	}
	return s.Get(ctx, tenantID, id)
}

func (s *PostgresStore) StudentResult(ctx context.Context, tenantID, examID, studentID string) (StudentResult, error) {
	detail, err := s.studentDetail(ctx, tenantID, examID, studentID)
	if err != nil {
		return StudentResult{}, err
	}
	for _, item := range detail.Items {
		if item.StudentID != studentID {
			continue
		}
		result := StudentResult{ExamID: examID, ReleaseID: detail.Release.ID, ReleaseVersion: detail.Release.Version, TotalScore: item.TotalScore, MaxScore: item.MaxScore, OverallTotalScore: item.TotalScore, OverallMaxScore: item.MaxScore, ScoreRate: scoreRate(item.TotalScore, item.MaxScore),
			AppealWindow: appealView(detail.Release.AppealWindow, s.now().UTC())}
		if overallScore, overallMax, overallErr := s.studentSessionTotal(ctx, tenantID, examID, studentID); overallErr != nil {
			return StudentResult{}, overallErr
		} else if overallMax > 0 {
			result.OverallTotalScore, result.OverallMaxScore = overallScore, overallMax
		}
		var exam StudentExam
		if err := s.db.QueryRowContext(ctx, `SELECT name, subject, exam_type FROM exam WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, examID).Scan(&exam.Name, &exam.Subject, &exam.ExamType); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return StudentResult{}, err
		} else if err == nil {
			exam.PublishedAt = *detail.Release.PublishedAt
			result.Exam = &exam
		}
		result.Reference = buildStudentReference(detail.Items, item.TotalScore, detail.Release.VisibilityPolicy)
		if detail.Release.VisibilityPolicy.ShowCohortStatistics {
			result.SubjectBalance, err = s.studentSubjectBalance(ctx, tenantID, examID, studentID)
			if err != nil {
				return StudentResult{}, err
			}
		}
		if detail.Release.VisibilityPolicy.ShowExactRank {
			result.Rankings, err = s.studentRankings(ctx, tenantID, examID, detail.Release.ID, studentID, item.TotalScore)
			if err != nil {
				return StudentResult{}, err
			}
		}
		if !detail.Release.VisibilityPolicy.ShowQuestionScores {
			return result, nil
		}
		detail.Questions, err = s.questions(ctx, tenantID, detail.Release.ID, item.SubmissionID)
		if err != nil {
			return StudentResult{}, err
		}
		presentation, pages, err := s.studentPaperPresentation(ctx, tenantID, item.SubmissionID)
		if err != nil {
			return StudentResult{}, err
		}
		result.PaperPages = pages
		questionStats := map[string]*StudentQuestionReference{}
		if detail.Release.VisibilityPolicy.ShowQuestionStatistics {
			questionStats, err = s.studentQuestionStatistics(ctx, tenantID, examID, detail.Release.ID, studentID)
			if err != nil {
				return StudentResult{}, err
			}
		}
		// Peer paper images are withheld until a verified anonymous asset exists.
		result.Questions = []StudentQuestion{}
		for _, question := range detail.Questions {
			if question.SubmissionID != item.SubmissionID {
				continue
			}
			viewPolicy := detail.Release.VisibilityPolicy
			viewPolicy.ShowQuestionStatistics = false // PostgreSQL already computed scoped statistics above.
			view := studentQuestionView(question, nil, viewPolicy)
			if result.Exam != nil {
				view.Subject = result.Exam.Subject
			}
			if item, ok := presentation[question.QuestionID]; ok {
				view.PageNo, view.SubmissionPageID, view.AnswerGeometry = item.PageNo, item.SubmissionPageID, item.Geometry
			}
			if stat, ok := questionStats[question.QuestionID]; ok {
				view.Cohort = stat
			}
			if detail.Release.VisibilityPolicy.ShowFeedback {
				view.Feedback = question.Explanation.Feedback
			}
			if detail.Release.VisibilityPolicy.ShowRubricSummary {
				view.RubricSummary = append([]string(nil), question.Explanation.RubricSummary...)
			}
			result.Questions = append(result.Questions, view)
		}
		sort.Slice(result.Questions, func(i, j int) bool { return result.Questions[i].QuestionNo < result.Questions[j].QuestionNo })
		if detail.Release.VisibilityPolicy.ShowHighScorePaper {
			result.HighScorePaper, err = s.studentHighScorePaper(ctx, tenantID, detail.Release.ID)
			if err != nil {
				return StudentResult{}, err
			}
		}
		return result, nil
	}
	return StudentResult{}, ErrNotFound
}

type studentQuestionPresentation struct {
	PageNo           int
	SubmissionPageID string
	Geometry         *StudentImageGeometry
}

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
		out.ClassRank, out.ClassSize = 0, 0
	}
	if out.GradeSize < privacyMinCohortSize {
		out.GradeRank, out.GradeSize = 0, 0
	}
	return &out, err
}

func (s *PostgresStore) studentSessionTotal(ctx context.Context, tenantID, examID, studentID string) (float64, float64, error) {
	var score, maxScore float64
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(sum(mine.total_score),0)::float8,COALESCE(sum(mine.max_score),0)::float8
FROM exam target
JOIN exam sibling ON sibling.tenant_id=target.tenant_id AND sibling.deleted_at IS NULL
 AND ((target.exam_session_id IS NOT NULL AND sibling.exam_session_id=target.exam_session_id) OR sibling.id=target.id)
JOIN score_release_current current_release ON current_release.tenant_id=sibling.tenant_id AND current_release.exam_id=sibling.id
JOIN score_release release ON release.tenant_id=current_release.tenant_id AND release.id=current_release.release_id AND release.status='published'
JOIN score_release_item mine ON mine.tenant_id=release.tenant_id AND mine.release_id=release.id AND mine.student_id=$3::uuid
WHERE target.tenant_id=$1::uuid AND target.id=$2::uuid AND target.deleted_at IS NULL
`, tenantID, examID, studentID).Scan(&score, &maxScore)
	return score, maxScore, err
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

func (s *PostgresStore) studentQuestionStatistics(ctx context.Context, tenantID, examID, releaseID, studentID string) (map[string]*StudentQuestionReference, error) {
	rows, err := s.db.QueryContext(ctx, `
WITH target AS (
  SELECT class_id_snapshot FROM exam_candidate_snapshot
  WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND student_id=$4::uuid
), facts AS (
  SELECT q.question_id,q.score::float8,q.max_score::float8,c.class_id_snapshot
  FROM score_release_question q
  JOIN score_release_item item ON item.tenant_id=q.tenant_id AND item.release_id=q.release_id AND item.submission_id=q.submission_id
  JOIN exam_candidate_snapshot c ON c.tenant_id=item.tenant_id AND c.exam_id=$2::uuid AND c.student_id=item.student_id
  WHERE q.tenant_id=$1::uuid AND q.release_id=$3::uuid
)
SELECT f.question_id::text,
       count(*)::int,
       count(*) FILTER(WHERE f.class_id_snapshot=t.class_id_snapshot)::int,
       avg(f.score) FILTER(WHERE f.class_id_snapshot=t.class_id_snapshot)::float8,
       avg(f.score)::float8,
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
	out := map[string]*StudentQuestionReference{}
	for rows.Next() {
		var id string
		var ref StudentQuestionReference
		var classSize int
		var classMean, schoolMean, median float64
		if err := rows.Scan(&id, &ref.SampleSize, &classSize, &classMean, &schoolMean, &median, &ref.MeanScoreRate, &ref.FullScoreRate, &ref.ZeroScoreRate); err != nil {
			return nil, err
		}
		if ref.SampleSize >= privacyMinCohortSize {
			ref.SchoolMeanScore, ref.MedianScore = &schoolMean, &median
			if classSize >= privacyMinCohortSize {
				ref.ClassMeanScore = &classMean
			}
			out[id] = &ref
		}
	}
	return out, rows.Err()
}

func (s *PostgresStore) studentPaperPresentation(ctx context.Context, tenantID, submissionID string) (map[string]studentQuestionPresentation, []StudentPaperPage, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT ON(seg.question_id) seg.question_id::text,sp.page_no,sp.id::text,COALESCE(seg.normalized_bbox,'{}'::jsonb)
FROM answer_segment seg JOIN submission_page sp ON sp.tenant_id=seg.tenant_id AND sp.id=seg.submission_page_id AND sp.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND seg.submission_id=$2::uuid AND seg.deleted_at IS NULL
ORDER BY seg.question_id,seg.updated_at DESC,seg.id DESC
`, tenantID, submissionID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := map[string]studentQuestionPresentation{}
	pageByNo := map[int]StudentPaperPage{}
	for rows.Next() {
		var id, pageID string
		var pageNo int
		var raw []byte
		if err := rows.Scan(&id, &pageNo, &pageID, &raw); err != nil {
			return nil, nil, err
		}
		var values map[string]float64
		_ = json.Unmarshal(raw, &values)
		var geometry *StudentImageGeometry
		if values["width"] > 0 && values["height"] > 0 {
			geometry = &StudentImageGeometry{X: values["x"], Y: values["y"], Width: values["width"], Height: values["height"]}
		}
		out[id] = studentQuestionPresentation{PageNo: pageNo, SubmissionPageID: pageID, Geometry: geometry}
		if _, exists := pageByNo[pageNo]; !exists {
			pageByNo[pageNo] = StudentPaperPage{PageNo: pageNo, QuestionID: id, SubmissionPageID: pageID}
		}
	}
	pages := make([]StudentPaperPage, 0, len(pageByNo))
	for _, page := range pageByNo {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].PageNo < pages[j].PageNo })
	return out, pages, rows.Err()
}

func (s *PostgresStore) StudentQuestion(ctx context.Context, tenantID, examID, studentID, questionID string) (StudentQuestion, error) {
	result, err := s.StudentResult(ctx, tenantID, examID, studentID)
	if err != nil {
		return StudentQuestion{}, err
	}
	if !result.QuestionsIsVisible() {
		return StudentQuestion{}, ErrForbidden
	}
	for _, question := range result.Questions {
		if question.QuestionID == questionID {
			return question, nil
		}
	}
	return StudentQuestion{}, ErrNotFound
}

func (s *PostgresStore) StudentQuestionImage(ctx context.Context, tenantID, examID, studentID, questionID string) (StudentQuestionImageSource, error) {
	var source StudentQuestionImageSource
	err := s.db.QueryRowContext(ctx, `
SELECT answer_segment.id::text
FROM score_release_current current_release
JOIN score_release release
  ON release.tenant_id = current_release.tenant_id
 AND release.id = current_release.release_id
 AND release.status = 'published'
JOIN score_release_item item
  ON item.tenant_id = release.tenant_id
 AND item.release_id = release.id
 AND item.student_id = $3::uuid
JOIN score_release_question release_question
  ON release_question.tenant_id = item.tenant_id
 AND release_question.release_id = item.release_id
 AND release_question.submission_id = item.submission_id
 AND release_question.question_id = $4::uuid
JOIN answer_segment
  ON answer_segment.tenant_id = release_question.tenant_id
 AND answer_segment.submission_id = release_question.submission_id
 AND answer_segment.question_id = release_question.question_id
 AND answer_segment.deleted_at IS NULL
WHERE current_release.tenant_id = $1
  AND current_release.exam_id = $2::uuid
  AND COALESCE((release.visibility_policy ->> 'show_question_scores')::boolean, false)
ORDER BY answer_segment.updated_at DESC, answer_segment.id
LIMIT 1
`, tenantID, examID, studentID, questionID).Scan(&source.AnswerSegmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return StudentQuestionImageSource{}, ErrNotFound
	}
	if err != nil {
		return StudentQuestionImageSource{}, err
	}
	return source, nil
}

func (s *PostgresStore) StudentPaperPageImage(ctx context.Context, tenantID, examID, studentID, questionID string, highScore bool) (StudentQuestionImageSource, error) {
	if highScore {
		var source StudentQuestionImageSource
		err := s.db.QueryRowContext(ctx, `
SELECT current_release.release_id::text,ap.id::text,ap.file_asset_id::text
FROM score_release_current current_release
JOIN score_release release ON release.tenant_id=current_release.tenant_id AND release.id=current_release.release_id AND release.status='published'
JOIN score_release_item mine ON mine.tenant_id=release.tenant_id AND mine.release_id=release.id AND mine.student_id=$3::uuid
JOIN LATERAL (
  SELECT submission_id FROM score_release_item
  WHERE tenant_id=release.tenant_id AND release_id=release.id AND student_id IS NOT NULL
  ORDER BY total_score DESC,submission_id LIMIT 1
) highest ON true
JOIN score_release_question q ON q.tenant_id=release.tenant_id AND q.release_id=release.id
  AND q.submission_id=highest.submission_id AND q.question_id=$4::uuid
JOIN answer_segment seg ON seg.tenant_id=q.tenant_id AND seg.submission_id=q.submission_id
  AND seg.question_id=q.question_id AND seg.deleted_at IS NULL
JOIN score_release_anonymous_page ap ON ap.tenant_id=release.tenant_id AND ap.release_id=release.id
  AND ap.source_submission_page_id=seg.submission_page_id AND ap.revoked_at IS NULL
WHERE current_release.tenant_id=$1::uuid AND current_release.exam_id=$2::uuid
  AND COALESCE((release.visibility_policy->>'show_question_scores')::boolean,false)
  AND COALESCE((release.visibility_policy->>'show_high_score_paper')::boolean,false)
ORDER BY seg.updated_at DESC,seg.id DESC LIMIT 1
`, tenantID, examID, studentID, questionID).Scan(&source.ReleaseID, &source.AnonymousPageID, &source.AnonymousFileAssetID)
		if errors.Is(err, sql.ErrNoRows) {
			return StudentQuestionImageSource{}, ErrNotFound
		}
		return source, err
	}
	// 整页图片同样以发布题目清单为范围；仅有答题分段不能证明题目已发布。
	var source StudentQuestionImageSource
	err := s.db.QueryRowContext(ctx, `
SELECT seg.id::text FROM score_release_current current_release
  JOIN score_release release ON release.tenant_id=current_release.tenant_id AND release.id=current_release.release_id AND release.status='published'
JOIN score_release_item item ON item.tenant_id=release.tenant_id AND item.release_id=release.id AND item.student_id=$3::uuid
JOIN score_release_question release_question
  ON release_question.tenant_id=item.tenant_id
 AND release_question.release_id=item.release_id
 AND release_question.submission_id=item.submission_id
 AND release_question.question_id=$4::uuid
JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.submission_id=item.submission_id AND seg.question_id=$4::uuid AND seg.deleted_at IS NULL
WHERE current_release.tenant_id=$1::uuid AND current_release.exam_id=$2::uuid
  AND COALESCE((release.visibility_policy->>'show_question_scores')::boolean,false)
ORDER BY seg.updated_at DESC,seg.id DESC LIMIT 1
`, tenantID, examID, studentID, questionID).Scan(&source.AnswerSegmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return StudentQuestionImageSource{}, ErrNotFound
	}
	return source, err
}

func (s *PostgresStore) currentFactsTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) ([]SubmissionFact, error) {
	// 单评反馈只采用终评分数、满分一致且时间来源唯一的记录；无法对应时留空，避免错贴评语。
	rows, err := tx.QueryContext(ctx, `
SELECT sg.student_id::text, sg.submission_id::text, sg.total_score::float8, sg.max_score::float8, sg.status,
  fg.question_id::text, fg.question_no, fg.id::text, fg.score::float8, fg.max_score::float8, fg.source,
  CASE fg.source
    WHEN 'arbitration' THEN COALESCE(fg.arbitration_task_id::text, '')
    WHEN 'single_review' THEN COALESCE(human_source.id::text, '')
    WHEN 'double_mark_auto' THEN COALESCE(fg.double_mark_session_id::text, '')
    ELSE fg.id::text END,
  CASE fg.source
    WHEN 'arbitration' THEN COALESCE(arbitration_source.student_feedback, '')
    WHEN 'single_review' THEN COALESCE(human_source.student_feedback, '')
    ELSE '' END,
  COALESCE(q.question_type, ''), COALESCE(q.stem, ''), COALESCE(q.knowledge_points, '[]'::jsonb),
  COALESCE((SELECT ak.standard_answer::text FROM question_answer_key ak
    WHERE ak.tenant_id=q.tenant_id AND ak.question_id=q.id AND ak.deleted_at IS NULL
    ORDER BY ak.created_at DESC,ak.id DESC LIMIT 1), ''),
  COALESCE((SELECT NULLIF(asa.answer_text, '') FROM answer_segment_answer asa
    WHERE asa.tenant_id=fg.tenant_id AND asa.answer_segment_id=fg.answer_segment_id AND asa.deleted_at IS NULL
    ORDER BY asa.created_at DESC,asa.id DESC LIMIT 1), '')
FROM submission_grade sg
JOIN final_grade fg ON fg.tenant_id = sg.tenant_id AND fg.exam_id = sg.exam_id AND fg.submission_id = sg.submission_id AND fg.deleted_at IS NULL
JOIN question q ON q.tenant_id = fg.tenant_id AND q.id = fg.question_id AND q.deleted_at IS NULL
LEFT JOIN LATERAL (
  SELECT at.id, at.student_feedback FROM arbitration_task at
  WHERE fg.source = 'arbitration' AND at.tenant_id = fg.tenant_id AND at.id = fg.arbitration_task_id
    AND at.status = 'submitted' AND at.final_score = fg.score AND at.deleted_at IS NULL
) arbitration_source ON true
LEFT JOIN LATERAL (
  SELECT hg.id, hg.student_feedback, hg.score, hg.max_score,
    COUNT(*) OVER (PARTITION BY hg.created_at) AS timestamp_peers
  FROM human_grade hg
  JOIN review_task rt ON rt.tenant_id=hg.tenant_id AND rt.id=hg.review_task_id
  WHERE fg.source = 'single_review' AND hg.tenant_id = fg.tenant_id AND hg.answer_segment_id = fg.answer_segment_id
    AND hg.grade_round = 'single' AND rt.grade_round = 'single' AND rt.status IN ('submitted','completed')
    AND hg.created_at <= fg.created_at AND hg.deleted_at IS NULL
    AND rt.deleted_at IS NULL
  ORDER BY hg.created_at DESC, hg.id DESC LIMIT 1
) human_source ON human_source.score = fg.score AND human_source.max_score = fg.max_score AND human_source.timestamp_peers = 1
WHERE sg.tenant_id = $1 AND sg.exam_id = $2::uuid AND sg.deleted_at IS NULL
ORDER BY sg.submission_id, fg.question_no, fg.id
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bySubmission := map[string]SubmissionFact{}
	order := []string{}
	for rows.Next() {
		var studentID sql.NullString
		var fact SubmissionFact
		var question QuestionFact
		var feedback, questionType, stem, correctAnswer, actualAnswer string
		var knowledgePoints []byte
		if err := rows.Scan(&studentID, &fact.SubmissionID, &fact.TotalScore, &fact.MaxScore, &fact.Status, &question.QuestionID, &question.QuestionNo, &question.FinalGradeID, &question.Score, &question.MaxScore, &question.SourceType, &question.SourceID, &feedback, &questionType, &stem, &knowledgePoints, &correctAnswer, &actualAnswer); err != nil {
			return nil, err
		}
		fact.StudentID, question.Explanation.Feedback = studentID.String, strings.TrimSpace(feedback)
		question.Explanation.QuestionType, question.Explanation.Stem = strings.TrimSpace(questionType), strings.TrimSpace(stem)
		question.Explanation.CorrectAnswer = displayAnswer(correctAnswer)
		question.Explanation.ActualAnswer = strings.TrimSpace(actualAnswer)
		if err := json.Unmarshal(knowledgePoints, &question.Explanation.KnowledgePoints); err != nil {
			return nil, err
		}
		if existing, ok := bySubmission[fact.SubmissionID]; ok {
			existing.Questions = append(existing.Questions, question)
			bySubmission[fact.SubmissionID] = existing
		} else {
			fact.Questions = []QuestionFact{question}
			bySubmission[fact.SubmissionID] = fact
			order = append(order, fact.SubmissionID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SubmissionFact, 0, len(order))
	for _, id := range order {
		out = append(out, bySubmission[id])
	}
	return out, nil
}

func displayAnswer(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "null" {
		return ""
	}
	var scalar string
	if json.Unmarshal([]byte(value), &scalar) == nil {
		return strings.TrimSpace(scalar)
	}
	return value
}

func (s *PostgresStore) insertReleaseTx(ctx context.Context, tx *sql.Tx, tenantID, examID, actorID string, version int, source, reason, idempotencyKey string, visibility VisibilityPolicy, window AppealWindow, sourceReleaseID string) (Release, error) {
	visibilityJSON, err := json.Marshal(visibility)
	if err != nil {
		return Release{}, err
	}
	windowJSON, err := json.Marshal(window)
	if err != nil {
		return Release{}, err
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO score_release (tenant_id, exam_id, version, source, reason, idempotency_key, visibility_policy, appeal_window, source_release_id, created_by)
VALUES ($1, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8::jsonb, NULLIF($9, '')::uuid, $10::uuid)
RETURNING `+releaseReturning, tenantID, examID, version, source, reason, idempotencyKey, string(visibilityJSON), string(windowJSON), sourceReleaseID, actorID)
	return scanRelease(row)
}

func (s *PostgresStore) insertFactsTx(ctx context.Context, tx *sql.Tx, tenantID, releaseID string, facts []SubmissionFact) error {
	for _, fact := range facts {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO score_release_item (tenant_id, release_id, student_id, submission_id, total_score, max_score, status, snapshot_hash)
VALUES ($1, $2::uuid, NULLIF($3, '')::uuid, $4::uuid, $5, $6, $7, $8)
`, tenantID, releaseID, fact.StudentID, fact.SubmissionID, fact.TotalScore, fact.MaxScore, fact.Status, snapshotHash(fact)); err != nil {
			return err
		}
		for _, question := range fact.Questions {
			explanation, err := json.Marshal(question.Explanation)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO score_release_question (tenant_id, release_id, submission_id, question_id, question_no, final_grade_id, score, max_score, source_type, source_id, student_explanation)
VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, $7, $8, $9, NULLIF($10, '')::uuid, $11::jsonb)
`, tenantID, releaseID, fact.SubmissionID, question.QuestionID, question.QuestionNo, question.FinalGradeID, question.Score, question.MaxScore, question.SourceType, question.SourceID, string(explanation)); err != nil {
				return err
			}
		}
	}
	return nil
}

// nextVersionTx 依赖调用方持有考试事务锁；单独计算 MAX+1 不能防止并发分配同一版本号。
func (s *PostgresStore) nextVersionTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) (int, error) {
	var version int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM score_release WHERE tenant_id = $1 AND exam_id = $2::uuid`, tenantID, examID).Scan(&version)
	return version, err
}

func (s *PostgresStore) findIdempotentTx(ctx context.Context, tx *sql.Tx, tenantID, examID, key string) (Release, bool, error) {
	release, err := scanRelease(tx.QueryRowContext(ctx, releaseColumns+` FROM score_release WHERE tenant_id = $1 AND exam_id = $2::uuid AND idempotency_key = $3`, tenantID, examID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, false, nil
	}
	return release, err == nil, err
}

func (s *PostgresStore) release(ctx context.Context, tenantID, id string) (Release, error) {
	release, err := scanRelease(s.db.QueryRowContext(ctx, releaseColumns+` FROM score_release WHERE tenant_id = $1 AND id = $2::uuid`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, ErrNotFound
	}
	return release, err
}

func (s *PostgresStore) releaseTx(ctx context.Context, tx *sql.Tx, tenantID, id string, forUpdate bool) (Release, error) {
	query := releaseColumns + ` FROM score_release WHERE tenant_id = $1 AND id = $2::uuid`
	if forUpdate {
		query += " FOR UPDATE"
	}
	release, err := scanRelease(tx.QueryRowContext(ctx, query, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, ErrNotFound
	}
	return release, err
}

func (s *PostgresStore) items(ctx context.Context, tenantID, releaseID string) ([]ReleaseItem, error) {
	return s.filteredItems(ctx, tenantID, releaseID, "")
}

func (s *PostgresStore) filteredItems(ctx context.Context, tenantID, releaseID, studentID string) ([]ReleaseItem, error) {
	query := `SELECT release_id::text, COALESCE(student_id::text, ''), submission_id::text, total_score::float8, max_score::float8, status, snapshot_hash FROM score_release_item WHERE tenant_id = $1 AND release_id = $2::uuid`
	args := []any{tenantID, releaseID}
	if studentID != "" {
		query += " AND student_id=$3::uuid"
		args = append(args, studentID)
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY submission_id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReleaseItem{}
	for rows.Next() {
		var item ReleaseItem
		if err := rows.Scan(&item.ReleaseID, &item.StudentID, &item.SubmissionID, &item.TotalScore, &item.MaxScore, &item.Status, &item.SnapshotHash); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) questions(ctx context.Context, tenantID, releaseID, submissionID string) ([]ReleaseQuestion, error) {
	query := `SELECT release_id::text, submission_id::text, question_id::text, question_no, final_grade_id::text, score::float8, max_score::float8, source_type, COALESCE(source_id::text, ''), student_explanation FROM score_release_question WHERE tenant_id = $1 AND release_id = $2::uuid`
	args := []any{tenantID, releaseID}
	if submissionID != "" {
		query += " AND submission_id = $3::uuid"
		args = append(args, submissionID)
	}
	query += " ORDER BY submission_id, question_no, question_id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReleaseQuestion{}
	for rows.Next() {
		var item ReleaseQuestion
		var raw []byte
		if err := rows.Scan(&item.ReleaseID, &item.SubmissionID, &item.QuestionID, &item.QuestionNo, &item.FinalGradeID, &item.Score, &item.MaxScore, &item.SourceType, &item.SourceID, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Explanation); err != nil {
			return nil, err
		}
		item.Explanation.RubricSummary = append([]string(nil), item.Explanation.RubricSummary...)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) releaseFactsTx(ctx context.Context, tx *sql.Tx, tenantID, releaseID string) ([]SubmissionFact, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT item.student_id::text, item.submission_id::text, item.total_score::float8, item.max_score::float8, item.status,
       question.question_id::text, question.question_no, question.final_grade_id::text,
       question.score::float8, question.max_score::float8, question.source_type,
       COALESCE(question.source_id::text, ''), question.student_explanation
FROM score_release_item item
JOIN score_release_question question
  ON question.tenant_id = item.tenant_id AND question.release_id = item.release_id AND question.submission_id = item.submission_id
WHERE item.tenant_id = $1 AND item.release_id = $2::uuid
ORDER BY item.submission_id, question.question_no, question.question_id
`, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bySubmission := map[string]SubmissionFact{}
	order := []string{}
	for rows.Next() {
		var studentID sql.NullString
		var fact SubmissionFact
		var question QuestionFact
		var explanation []byte
		if err := rows.Scan(&studentID, &fact.SubmissionID, &fact.TotalScore, &fact.MaxScore, &fact.Status,
			&question.QuestionID, &question.QuestionNo, &question.FinalGradeID, &question.Score, &question.MaxScore,
			&question.SourceType, &question.SourceID, &explanation); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(explanation, &question.Explanation); err != nil {
			return nil, err
		}
		fact.StudentID = studentID.String
		if existing, exists := bySubmission[fact.SubmissionID]; exists {
			existing.Questions = append(existing.Questions, question)
			bySubmission[fact.SubmissionID] = existing
		} else {
			fact.Questions = []QuestionFact{question}
			bySubmission[fact.SubmissionID] = fact
			order = append(order, fact.SubmissionID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	facts := make([]SubmissionFact, 0, len(order))
	for _, submissionID := range order {
		facts = append(facts, bySubmission[submissionID])
	}
	return facts, nil
}

func applyRegradeChanges(facts []SubmissionFact, input CreateRegradeInput) bool {
	changes := make(map[string]RegradeChange, len(input.Changes))
	for _, change := range input.Changes {
		changes[change.SubmissionID+"\x00"+change.QuestionID] = change
	}
	matched := 0
	for factIndex := range facts {
		fact := &facts[factIndex]
		for questionIndex := range fact.Questions {
			question := &fact.Questions[questionIndex]
			change, found := changes[fact.SubmissionID+"\x00"+question.QuestionID]
			if !found {
				continue
			}
			if question.QuestionID != input.QuestionID || math.Abs(question.MaxScore-change.MaxScore) > 0.000001 {
				return false
			}
			question.Score, question.SourceType, question.SourceID = change.Score, "single_review", change.ReviewedGradeID
			// The reviewed regrade plan has no approved student feedback. The
			// source release's explanation belongs to the old score.
			question.Explanation.Feedback = ""
			question.Explanation.RubricSummary = nil
			matched++
		}
		fact.TotalScore = 0
		for _, question := range fact.Questions {
			fact.TotalScore += question.Score
		}
		if fact.TotalScore > fact.MaxScore+0.000001 {
			return false
		}
	}
	return matched == len(changes)
}

func (s *PostgresStore) gateTx(ctx context.Context, tx *sql.Tx, tenantID, examID, releaseID string, dashboard []GateIssue) (Gate, error) {
	gate := emptyGate(s.now().UTC())
	checks := []struct{ code, message, route, query string }{
		{"unfinished_review_tasks", "there are unfinished review tasks", "review", `SELECT COUNT(*) FROM review_task WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL AND status NOT IN ('submitted', 'completed')`},
		{"unfinished_arbitration_tasks", "there are unresolved arbitration tasks", "arbitration", `SELECT COUNT(*) FROM arbitration_task WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL AND status <> 'submitted'`},
		{"ocr_failed_unhandled", "there are failed OCR tasks", "processing", `SELECT COUNT(*) FROM ocr_task ot JOIN submission sub ON sub.tenant_id = ot.tenant_id AND sub.id = ot.submission_id AND sub.deleted_at IS NULL WHERE ot.tenant_id = $1 AND sub.exam_id = $2::uuid AND ot.deleted_at IS NULL AND ot.status = 'failed'`},
		{"missing_final_grades", "there are answer segments without final grades", "review", `SELECT COUNT(*) FROM answer_segment seg WHERE seg.tenant_id = $1 AND seg.deleted_at IS NULL AND EXISTS (SELECT 1 FROM submission sub WHERE sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL) AND NOT EXISTS (SELECT 1 FROM final_grade fg WHERE fg.tenant_id = seg.tenant_id AND fg.answer_segment_id = seg.id AND fg.deleted_at IS NULL)`},
		{"missing_submission_unresolved", "expected students have no matched submission", "capture", `WITH roster AS (SELECT candidate.student_id AS id FROM exam_candidate_snapshot candidate LEFT JOIN exam_student_attendance ea ON ea.tenant_id=candidate.tenant_id AND ea.exam_id=candidate.exam_id AND ea.student_id=candidate.student_id AND ea.deleted_at IS NULL WHERE candidate.tenant_id=$1::uuid AND candidate.exam_id=$2::uuid AND COALESCE(ea.status,'expected')<>'absent') SELECT COUNT(*) FROM roster r WHERE NOT EXISTS (SELECT 1 FROM submission sub WHERE sub.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.student_id=r.id AND sub.deleted_at IS NULL)`},
		{"unidentified_submission", "submissions are not uniquely matched to an expected student", "capture", `WITH roster AS (SELECT student_id AS id FROM exam_candidate_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid), counts AS (SELECT student_id,COUNT(*) AS count FROM submission WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL GROUP BY student_id) SELECT COUNT(*) FROM submission sub LEFT JOIN counts c ON c.student_id=sub.student_id LEFT JOIN exam_student_attendance ea ON ea.tenant_id=sub.tenant_id AND ea.exam_id=sub.exam_id AND ea.student_id=sub.student_id AND ea.deleted_at IS NULL WHERE sub.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.deleted_at IS NULL AND (sub.student_id IS NULL OR NOT EXISTS (SELECT 1 FROM roster r WHERE r.id=sub.student_id) OR COALESCE(ea.status,'expected')='absent' OR COALESCE(c.count,0)>1)`},
		{"missing_pages_unresolved", "matched submissions have unresolved missing or rejected pages", "capture", `SELECT COUNT(*) FROM submission sub WHERE sub.tenant_id = $1 AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL AND (sub.actual_page_count < sub.expected_page_count OR sub.quality_status = 'failed' OR sub.status = 'rejected')`},
		{"no_submission_grades", "there are no submission grades to release", "grades", `SELECT CASE WHEN EXISTS (SELECT 1 FROM submission_grade WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL) THEN 0 ELSE 1 END`},
		{"grades_not_confirmed", "there are grades not confirmed for release", "grades", `SELECT COUNT(*) FROM submission_grade WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL AND status NOT IN ('confirmed', 'published', 'locked')`},
		{"question_coverage_incomplete", "a submission does not cover the exact official question set or maximum scores", "grades", `WITH official AS (
  SELECT q.id,q.score::float8 FROM question q WHERE q.tenant_id=$1::uuid AND q.exam_id=$2::uuid AND q.deleted_at IS NULL AND COALESCE(q.status,'active')<>'deleted'
), official_summary AS (
  SELECT COUNT(*)::int AS question_count,COALESCE(SUM(score),0)::float8 AS total FROM official
)
SELECT COUNT(*) FROM submission sub CROSS JOIN official_summary summary JOIN exam e ON e.tenant_id=sub.tenant_id AND e.id=sub.exam_id
WHERE sub.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.deleted_at IS NULL AND (
  summary.question_count<>(SELECT COUNT(DISTINCT seg.question_id) FROM answer_segment seg WHERE seg.tenant_id=sub.tenant_id AND seg.submission_id=sub.id AND seg.deleted_at IS NULL)
  OR EXISTS (SELECT 1 FROM official q WHERE NOT EXISTS (SELECT 1 FROM answer_segment seg WHERE seg.tenant_id=sub.tenant_id AND seg.submission_id=sub.id AND seg.question_id=q.id AND seg.deleted_at IS NULL))
  OR EXISTS (SELECT 1 FROM answer_segment seg WHERE seg.tenant_id=sub.tenant_id AND seg.submission_id=sub.id AND seg.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM official q WHERE q.id=seg.question_id))
  OR ABS(summary.total-e.total_score::float8)>0.000001
  OR summary.question_count<>(SELECT COUNT(DISTINCT fg.question_id) FROM final_grade fg WHERE fg.tenant_id=sub.tenant_id AND fg.submission_id=sub.id AND fg.deleted_at IS NULL)
  OR ABS(summary.total-(SELECT COALESCE(SUM(fg.max_score),0)::float8 FROM final_grade fg WHERE fg.tenant_id=sub.tenant_id AND fg.submission_id=sub.id AND fg.deleted_at IS NULL))>0.000001
  OR EXISTS (SELECT 1 FROM final_grade fg JOIN official q ON q.id=fg.question_id WHERE fg.tenant_id=sub.tenant_id AND fg.submission_id=sub.id AND fg.deleted_at IS NULL AND ABS(fg.max_score::float8-q.score)>0.000001)
)`},
		{"score_integrity_mismatch", "submission total does not match final-grade total", "grades", `SELECT COUNT(*) FROM submission_grade sg LEFT JOIN LATERAL (SELECT COALESCE(SUM(fg.score), 0)::float8 AS total FROM final_grade fg WHERE fg.tenant_id = sg.tenant_id AND fg.exam_id = sg.exam_id AND fg.submission_id = sg.submission_id AND fg.deleted_at IS NULL) fg ON true WHERE sg.tenant_id = $1 AND sg.exam_id = $2::uuid AND sg.deleted_at IS NULL AND abs(sg.total_score::float8 - fg.total) > 0.000001`},
	}
	for _, check := range checks {
		count, err := scalar(ctx, tx, check.query, tenantID, examID)
		if err != nil {
			return Gate{}, err
		}
		if count > 0 {
			addIssue(&gate, GateIssue{Code: check.code, Message: check.message, Blocking: true, Count: count, ActionRoute: check.route})
		}
	}
	if releaseID != "" {
		checks := []struct{ code, message, query string }{
			{"release_snapshot_incomplete", "release contains an item without question facts", `SELECT COUNT(*) FROM score_release_item item WHERE item.tenant_id = $1 AND item.release_id = $2::uuid AND NOT EXISTS (SELECT 1 FROM score_release_question question WHERE question.tenant_id = item.tenant_id AND question.release_id = item.release_id AND question.submission_id = item.submission_id)`},
			{"release_snapshot_incomplete", "release snapshot does not reconcile to its question scores", `SELECT COUNT(*) FROM score_release_item item LEFT JOIN LATERAL (SELECT COALESCE(SUM(question.score), 0)::float8 AS total FROM score_release_question question WHERE question.tenant_id = item.tenant_id AND question.release_id = item.release_id AND question.submission_id = item.submission_id) questions ON true WHERE item.tenant_id = $1 AND item.release_id = $2::uuid AND abs(item.total_score::float8 - questions.total) > 0.000001`},
		}
		for _, check := range checks {
			count, err := scalar(ctx, tx, check.query, tenantID, releaseID)
			if err != nil {
				return Gate{}, err
			}
			if count > 0 {
				addIssue(&gate, GateIssue{Code: check.code, Message: check.message, Blocking: true, Count: count, ActionRoute: "grades"})
			}
		}
	}
	for _, issue := range dashboard {
		addIssue(&gate, issue)
	}
	gate.Passed = len(gate.Blocking) == 0
	return gate, nil
}

func (s *PostgresStore) dashboardIssues(ctx context.Context, tenantID, examID string) ([]GateIssue, error) {
	if s.dashboard == nil {
		return nil, nil
	}
	dashboard, err := s.dashboard.Get(ctx, tenantID, examID)
	if err != nil {
		return nil, fmt.Errorf("release quality dashboard: %w", err)
	}
	issues := make([]GateIssue, 0, len(dashboard.Blocking)+len(dashboard.Warnings))
	riskByQuestion := make(map[string]string, len(dashboard.Questions))
	for _, question := range dashboard.Questions {
		riskByQuestion[question.Question.ID] = question.Question.RiskTier
	}
	for _, finding := range dashboard.Blocking {
		issues = append(issues, GateIssue{Code: finding.Code, Message: finding.Reason, Blocking: true, Count: 1, ActionRoute: "quality"})
	}
	for _, finding := range dashboard.Warnings {
		// Incomplete backmark is explicitly a release blocker. Other warnings
		// remain visible for routine low-risk exams. R3 is intentionally
		// fail-closed for the Gold/calibration/Seed evidence chain; a warning
		// there is not a releaseable quality state.
		blocking := finding.Code == "backmark_pending" || (riskByQuestion[finding.QuestionID] == "R3" && r3EvidenceRequired(finding.Code))
		issues = append(issues, GateIssue{Code: finding.Code, Message: finding.Reason, Blocking: blocking, Count: 1, ActionRoute: "quality"})
	}
	return issues, nil
}

func r3EvidenceRequired(code string) bool {
	return strings.HasPrefix(code, "gold_") || strings.HasPrefix(code, "calibration_") || strings.HasPrefix(code, "seed_") || code == "answer_group_sampling_pending"
}

func releaseDiff(current, base Detail) Diff {
	result := Diff{BaseReleaseID: base.Release.ID, ReleaseID: current.Release.ID, Items: []DiffItem{}, Questions: []DiffQuestion{}}
	baseItems, currentItems := indexItems(base.Items), indexItems(current.Items)
	for submissionID, item := range currentItems {
		previous, exists := baseItems[submissionID]
		if !exists || math.Abs(item.TotalScore-previous.TotalScore) > 0.000001 {
			result.Items = append(result.Items, DiffItem{SubmissionID: submissionID, OldTotal: previous.TotalScore, NewTotal: item.TotalScore})
		}
	}
	baseQuestions, currentQuestions := indexQuestions(base.Questions), indexQuestions(current.Questions)
	for key, question := range currentQuestions {
		previous, exists := baseQuestions[key]
		if !exists || math.Abs(question.Score-previous.Score) > 0.000001 {
			result.Questions = append(result.Questions, DiffQuestion{SubmissionID: question.SubmissionID, QuestionID: question.QuestionID, OldScore: previous.Score, NewScore: question.Score})
		}
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].SubmissionID < result.Items[j].SubmissionID })
	sort.Slice(result.Questions, func(i, j int) bool {
		if result.Questions[i].SubmissionID == result.Questions[j].SubmissionID {
			return result.Questions[i].QuestionID < result.Questions[j].QuestionID
		}
		return result.Questions[i].SubmissionID < result.Questions[j].SubmissionID
	})
	result.AffectedCount = len(result.Items)
	return result
}

const releaseColumns = `SELECT id::text, tenant_id::text, exam_id::text, version, source, reason, status, idempotency_key, visibility_policy, appeal_window, gate_snapshot, COALESCE(source_release_id::text, ''), COALESCE(supersedes_release_id::text, ''), created_by::text, created_at, COALESCE(published_by::text, ''), published_at`
const releaseReturning = `id::text, tenant_id::text, exam_id::text, version, source, reason, status, idempotency_key, visibility_policy, appeal_window, gate_snapshot, COALESCE(source_release_id::text, ''), COALESCE(supersedes_release_id::text, ''), created_by::text, created_at, COALESCE(published_by::text, ''), published_at`

type releaseScanner interface{ Scan(...any) error }

func scanRelease(row releaseScanner) (Release, error) {
	var release Release
	var visibility, window, gate []byte
	var publishedBy string
	if err := row.Scan(&release.ID, &release.TenantID, &release.ExamID, &release.Version, &release.Source, &release.Reason, &release.Status, &release.IdempotencyKey, &visibility, &window, &gate, &release.SourceReleaseID, &release.SupersedesReleaseID, &release.CreatedBy, &release.CreatedAt, &publishedBy, &release.PublishedAt); err != nil {
		return Release{}, err
	}
	if err := json.Unmarshal(visibility, &release.VisibilityPolicy); err != nil {
		return Release{}, err
	}
	if err := json.Unmarshal(window, &release.AppealWindow); err != nil {
		return Release{}, err
	}
	if err := json.Unmarshal(gate, &release.GateSnapshot); err != nil {
		return Release{}, err
	}
	release.PublishedBy = publishedBy
	if release.GateSnapshot.Counts == nil {
		release.GateSnapshot = emptyGate(release.CreatedAt)
	}
	return release, nil
}

func lockExam(ctx context.Context, tx *sql.Tx, tenantID, examID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, tenantID, examID)
	return err
}
func scalar(ctx context.Context, tx *sql.Tx, query, tenantID, id string) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, query, tenantID, id).Scan(&count)
	return count, err
}

func (result StudentResult) QuestionsIsVisible() bool { return result.Questions != nil }

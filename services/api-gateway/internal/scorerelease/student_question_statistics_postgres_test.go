package scorerelease

import (
	"context"
	"database/sql"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 仅在新建随机库验证真实聚合 SQL；最小表不验证迁移、RLS 或 HTTP 发布流程。
func studentQuestionStatisticsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for question statistics PostgreSQL test")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "postgres"
	admin := stdlib.OpenDB(*config)
	name := pgx.Identifier{"student_question_stats_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.Database = strings.Trim(name, `"`)
	database := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = database.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	for _, statement := range []string{
		`CREATE TABLE school_class(id uuid,tenant_id uuid,school_id uuid,deleted_at timestamptz)`,
		`CREATE TABLE student(id uuid,tenant_id uuid,school_id uuid,class_id uuid)`,
		`CREATE TABLE exam_candidate_snapshot(tenant_id uuid,exam_id uuid,student_id uuid,class_id_snapshot uuid)`,
		`CREATE TABLE score_release(id uuid,tenant_id uuid,exam_id uuid)`,
		`CREATE TABLE score_release_current(tenant_id uuid,exam_id uuid,release_id uuid)`,
		`CREATE TABLE score_release_item(tenant_id uuid,release_id uuid,student_id uuid,submission_id uuid)`,
		`CREATE TABLE score_release_question(tenant_id uuid,release_id uuid,submission_id uuid,question_id uuid,score numeric,max_score numeric)`,
		`CREATE TABLE final_grade(tenant_id uuid,exam_id uuid,submission_id uuid,question_id uuid,score numeric)`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestStudentQuestionStatisticsWithPostgres(t *testing.T) {
	database := studentQuestionStatisticsTestDB(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	tenant, examID, releaseID, latestID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	school, foreignSchool := uuid.NewString(), uuid.NewString()
	class, otherClass, foreignClass := uuid.NewString(), uuid.NewString(), uuid.NewString()
	question, zeroQuestion, smallSchoolQuestion, smallClassQuestion := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, entry := range [][2]string{{class, school}, {otherClass, school}, {foreignClass, foreignSchool}} {
		exec(`INSERT INTO school_class VALUES($1,$2,$3,NULL)`, entry[0], tenant, entry[1])
	}
	for _, id := range []string{releaseID, latestID} {
		exec(`INSERT INTO score_release VALUES($1,$2,$3)`, id, tenant, examID)
	}
	exec(`INSERT INTO score_release_current VALUES($1,$2,$3)`, tenant, examID, latestID)
	students := make([]string, 30)
	submissions := make([]string, 30)
	for i := range students {
		students[i], submissions[i] = uuid.NewString(), uuid.NewString()
		classID, schoolID, score := class, school, float64(i)
		if i >= 20 {
			classID, schoolID, score = foreignClass, foreignSchool, 100
		} else if i >= 10 {
			classID = otherClass
		}
		exec(`INSERT INTO student VALUES($1,$2,$3,$4)`, students[i], tenant, schoolID, classID)
		exec(`INSERT INTO exam_candidate_snapshot VALUES($1,$2,$3,$4)`, tenant, examID, students[i], classID)
		for _, id := range []string{releaseID, latestID} {
			exec(`INSERT INTO score_release_item VALUES($1,$2,$3,$4)`, tenant, id, students[i], submissions[i])
		}
		exec(`INSERT INTO score_release_question VALUES($1,$2,$3,$4,$5,100)`, tenant, releaseID, submissions[i], question, score)
		exec(`INSERT INTO score_release_question VALUES($1,$2,$3,$4,80,100)`, tenant, latestID, submissions[i], question)
		// 工作成绩与当前发布均已更正；旧版本统计必须继续使用自己的冻结分数。
		exec(`INSERT INTO final_grade VALUES($1,$2,$3,$4,99)`, tenant, examID, submissions[i], question)
		if i < 10 {
			exec(`INSERT INTO score_release_question VALUES($1,$2,$3,$4,0,0)`, tenant, releaseID, submissions[i], zeroQuestion)
		}
		if i < 9 || i >= 20 {
			exec(`INSERT INTO score_release_question VALUES($1,$2,$3,$4,100,100)`, tenant, releaseID, submissions[i], smallSchoolQuestion)
		}
		if i < 9 || i == 10 {
			exec(`INSERT INTO score_release_question VALUES($1,$2,$3,$4,$5,100)`, tenant, releaseID, submissions[i], smallClassQuestion, i)
		}
	}
	// 无冻结名册、冻结班级无法解析、其他租户的数据都不能进入本校群体。
	for _, variant := range []string{"no_roster", "missing_class", "foreign_tenant"} {
		student, submission, owner := uuid.NewString(), uuid.NewString(), tenant
		if variant == "foreign_tenant" {
			owner = uuid.NewString()
			exec(`INSERT INTO school_class VALUES($1,$2,$3,NULL)`, class, owner, school)
		}
		exec(`INSERT INTO score_release_item VALUES($1,$2,$3,$4)`, owner, releaseID, student, submission)
		exec(`INSERT INTO score_release_question VALUES($1,$2,$3,$4,100,100)`, owner, releaseID, submission, question)
		if variant != "no_roster" {
			classID := class
			if variant == "missing_class" {
				classID = uuid.NewString()
			}
			exec(`INSERT INTO exam_candidate_snapshot VALUES($1,$2,$3,$4)`, owner, examID, student, classID)
		}
	}
	store := NewPostgresStore(database, nil)
	read := func(studentID, selectedReleaseID string) map[string]studentQuestionAggregates {
		t.Helper()
		stats, err := store.studentQuestionStatistics(t.Context(), tenant, examID, selectedReleaseID, studentID)
		if err != nil {
			t.Fatal(err)
		}
		return stats
	}
	stats := read(students[0], releaseID)
	assertQuestionStatistics(t, stats[question], 20, 4.5, 9, 9.5, 19)
	if ref := stats[question].Cohort; math.Abs(ref.MeanScoreRate-.095) > .000001 || ref.FullScoreRate != 0 || math.Abs(ref.ZeroScoreRate-.05) > .000001 {
		t.Fatalf("question rates included another school: %+v", ref)
	}
	assertQuestionStatistics(t, stats[zeroQuestion], 10, 0, 0, 0, 0)
	if small := stats[smallSchoolQuestion]; small.Cohort != nil || small.Comparison == nil || small.Comparison.SchoolMaxScore == nil || *small.Comparison.SchoolMaxScore != 100 {
		t.Fatalf("score comparison and local analytics threshold were mixed: %+v", small)
	}
	small := stats[smallClassQuestion].Cohort
	comparison := stats[smallClassQuestion].Comparison
	if small == nil || small.ClassMeanScore != nil || small.ClassUnavailableReason != "small_cohort" || small.SchoolMeanScore == nil || *small.SchoolMeanScore != 4.6 || comparison == nil || comparison.ClassMeanScore == nil || *comparison.ClassMeanScore != 4 || comparison.ClassMaxScore == nil || *comparison.ClassMaxScore != 8 || comparison.SchoolMaxScore == nil || *comparison.SchoolMaxScore != 10 {
		t.Fatalf("class and school thresholds were not applied separately: %+v", small)
	}
	assertQuestionStatistics(t, read(students[10], releaseID)[question], 20, 14.5, 19, 9.5, 19)
	assertQuestionStatistics(t, read(students[20], releaseID)[question], 10, 100, 100, 100, 100)
	assertQuestionStatistics(t, read(students[0], latestID)[question], 20, 80, 80, 80, 80)
	// 旧发布只有一名学生、统计开关关闭时，四项比较仍随可见题分返回，零分及零满分都有效。
	singleRelease := uuid.NewString()
	exec(`INSERT INTO score_release VALUES($1,$2,$3)`, singleRelease, tenant, examID)
	exec(`INSERT INTO score_release_item VALUES($1,$2,$3,$4)`, tenant, singleRelease, students[0], submissions[0])
	exec(`INSERT INTO score_release_question VALUES($1,$2,$3,$4,0,0)`, tenant, singleRelease, submissions[0], question)
	singleStats := read(students[0], singleRelease)
	assertQuestionStatistics(t, singleStats[question], 1, 0, 0, 0, 0)
	view := postgresStudentQuestion(ReleaseQuestion{QuestionFact: QuestionFact{QuestionID: question}}, VisibilityPolicy{ShowQuestionScores: true}, nil, nil, singleStats)
	if view.ScoreComparison == nil || view.Cohort != nil || view.CohortUnavailableReason != "not_public" {
		t.Fatalf("legacy hidden analytics suppressed published score comparison: %+v", view)
	}
	withAnalytics := postgresStudentQuestion(ReleaseQuestion{QuestionFact: QuestionFact{QuestionID: question}}, VisibilityPolicy{ShowQuestionScores: true, ShowQuestionStatistics: true}, nil, nil, singleStats)
	if withAnalytics.ScoreComparison == nil || withAnalytics.Cohort != nil || withAnalytics.CohortUnavailableReason != "small_cohort" {
		t.Fatalf("single score bypassed existing analytics privacy: %+v", withAnalytics)
	}
	// 学生现已转校，原班级也已软删除；统计仍按考试时的班级和其学校归属计算。
	exec(`UPDATE student SET school_id=$2,class_id=$3 WHERE id=$1`, students[0], foreignSchool, foreignClass)
	exec(`UPDATE school_class SET deleted_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, class)
	assertQuestionStatistics(t, read(students[0], releaseID)[question], 20, 4.5, 9, 9.5, 19)
	if missing := read(uuid.NewString(), releaseID); len(missing) != 0 {
		t.Fatalf("missing frozen target produced school scores: %+v", missing)
	}
	otherExamRelease := uuid.NewString()
	exec(`INSERT INTO score_release VALUES($1,$2,$3)`, otherExamRelease, tenant, uuid.NewString())
	exec(`INSERT INTO score_release_item VALUES($1,$2,$3,$4)`, tenant, otherExamRelease, students[0], submissions[0])
	exec(`INSERT INTO score_release_question VALUES($1,$2,$3,$4,100,100)`, tenant, otherExamRelease, submissions[0], question)
	if wrongExam := read(students[0], otherExamRelease); len(wrongExam) != 0 {
		t.Fatalf("another exam release entered the requested exam: %+v", wrongExam)
	}
}

func assertQuestionStatistics(t *testing.T, aggregates studentQuestionAggregates, sample int, classMean, classMax, schoolMean, schoolMax float64) {
	t.Helper()
	ref := aggregates.Comparison
	if ref == nil {
		t.Fatalf("missing question comparison: %+v", aggregates)
	}
	if sample < privacyMinCohortSize {
		if aggregates.Cohort != nil {
			t.Fatalf("small sample exposed legacy analytics: %+v", aggregates.Cohort)
		}
	} else if aggregates.Cohort == nil || aggregates.Cohort.SampleSize != sample || aggregates.Cohort.ClassUnavailableReason != "" {
		t.Fatalf("unexpected question reference: %+v", aggregates.Cohort)
	}
	for _, expected := range []struct {
		name  string
		score *float64
		want  float64
	}{
		{"class mean", ref.ClassMeanScore, classMean}, {"class max", ref.ClassMaxScore, classMax},
		{"school mean", ref.SchoolMeanScore, schoolMean}, {"school max", ref.SchoolMaxScore, schoolMax},
	} {
		if expected.score == nil || math.Abs(*expected.score-expected.want) > .000001 {
			t.Fatalf("%s = %v, want %v; reference=%+v", expected.name, expected.score, expected.want, ref)
		}
	}
}

package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 克隆正式表结构和默认值，在随机 schema 里执行真正的写入和回滚；不复制业务数据或触发器。
func TestPostgresSubjectQualifiedReviewAssignments(t *testing.T) {
	f := newSubjectAssignmentFixture(t)
	ctx := t.Context()
	store := NewPostgresStore(f.db)
	mathTeacher := f.actor("math-teacher", f.school, []string{"mathematics"}, "grader")
	secondMathTeacher := f.actor("second-math-teacher", f.school, []string{"数学"}, "grader")
	englishTeacher := f.actor("english-teacher", f.school, []string{"english"}, "grader")
	foreignTeacher := f.actor("foreign-teacher", uuid.NewString(), []string{"mathematics"}, "grader")
	admin := f.actor("school-admin-grader", f.school, []string{}, "grader", "school_admin")
	mathArbitrator := f.actor("math-arbitrator", f.school, []string{"mathematics"}, "arbitrator")
	englishArbitrator := f.actor("english-arbitrator", f.school, []string{"english"}, "arbitrator")
	mathExam, mathSegment := f.segment("math")
	_, englishSegment := f.segment("english")
	create := func(segmentID, assignedTo string) (ReviewTask, error) {
		return store.CreateTask(ctx, f.tenant, f.manager, CreateTaskInput{AnswerSegmentID: segmentID, Source: "manual_sample", AssignedTo: assignedTo})
	}

	t.Run("create guards direct store calls", func(t *testing.T) {
		for _, assignee := range []string{englishTeacher, foreignTeacher} {
			if _, err := create(mathSegment, assignee); !errors.Is(err, ErrForbidden) {
				t.Fatalf("invalid create error=%v", err)
			}
		}
		if task, err := create(mathSegment, mathTeacher); err != nil || task.AssignedTo != mathTeacher {
			t.Fatalf("qualified create=%#v %v", task, err)
		}
		if _, err := create(mathSegment, admin); err != nil {
			t.Fatalf("administrator subject exemption=%v", err)
		}
	})

	t.Run("single and batch assignments are authoritative and atomic", func(t *testing.T) {
		mathTask, err := create(mathSegment, "")
		if err != nil {
			t.Fatal(err)
		}
		englishTask, err := create(englishSegment, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AssignTask(ctx, f.tenant, mathTask.ID, f.manager, AssignTaskInput{AssignedTo: englishTeacher, ExpectedRevision: mathTask.Revision}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("wrong-subject single assignment=%v", err)
		}
		_, err = store.BatchAssignTasks(ctx, f.tenant, f.manager, BatchAssignInput{TaskIDs: []string{mathTask.ID, englishTask.ID}, AssignedTo: mathTeacher, ExpectedRevisions: map[string]int64{mathTask.ID: mathTask.Revision, englishTask.ID: englishTask.Revision}})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("mixed-subject batch=%v", err)
		}
		for _, task := range []ReviewTask{mathTask, englishTask} {
			actual, err := store.GetTask(ctx, f.tenant, task.ID)
			if err != nil || actual.AssignedTo != "" || actual.Revision != task.Revision {
				t.Fatalf("rejected batch left a partial write: %#v %v", actual, err)
			}
		}
		if _, err := store.AssignTask(ctx, f.tenant, mathTask.ID, f.manager, AssignTaskInput{AssignedTo: mathTeacher, ExpectedRevision: mathTask.Revision}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.BatchAssignTasks(ctx, f.tenant, f.manager, BatchAssignInput{TaskIDs: []string{englishTask.ID}, AssignedTo: englishTeacher, ExpectedRevisions: map[string]int64{englishTask.ID: englishTask.Revision}}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("double mark rejects either reviewer without leaving one task", func(t *testing.T) {
		examID, segmentID := f.segment("math")
		if _, err := store.SetExamDoubleMarkPolicy(ctx, f.tenant, examID, f.manager, SetDoubleMarkPolicyInput{Enabled: true, Threshold: 1, ResolutionStrategy: "average"}); err != nil {
			t.Fatal(err)
		}
		input := CreateDoubleMarkSessionInput{AnswerSegmentID: segmentID, FirstReviewerID: mathTeacher, SecondReviewerID: englishTeacher}
		if _, err := store.CreateDoubleMarkSession(ctx, f.tenant, f.manager, input); !errors.Is(err, ErrForbidden) {
			t.Fatalf("unqualified second reviewer=%v", err)
		}
		var count int
		if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM review_task WHERE answer_segment_id=$1`, segmentID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("double mark rollback left %d tasks, err=%v", count, err)
		}
		input.SecondReviewerID = secondMathTeacher
		if _, err := store.CreateDoubleMarkSession(ctx, f.tenant, f.manager, input); err != nil {
			t.Fatalf("qualified double mark=%v", err)
		}
	})

	t.Run("arbitration creation and assignment share subject guard", func(t *testing.T) {
		base, err := create(mathSegment, mathTeacher)
		if err != nil {
			t.Fatal(err)
		}
		second, err := create(mathSegment, secondMathTeacher)
		if err != nil {
			t.Fatal(err)
		}
		var questionID, submissionID string
		if err := f.db.QueryRowContext(ctx, `SELECT question_id::text,submission_id::text FROM answer_segment WHERE id=$1`, mathSegment).Scan(&questionID, &submissionID); err != nil {
			t.Fatal(err)
		}
		session := DoubleMarkSession{ID: uuid.NewString(), ExamID: mathExam, QuestionID: questionID, QuestionNo: "Q1", AnswerSegmentID: mathSegment, SubmissionID: submissionID, AnonymousCode: "synthetic", FirstReviewTaskID: base.ID, SecondReviewTaskID: second.ID, FirstReviewerID: mathTeacher, SecondReviewerID: secondMathTeacher, Threshold: 1}
		f.insert("double_mark_session", map[string]any{"id": session.ID, "tenant_id": f.tenant, "exam_id": mathExam, "first_review_task_id": base.ID, "second_review_task_id": second.ID})
		insertArbitration := func(assignee string) (ArbitrationTask, error) {
			tx, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			task, err := store.createArbitrationTaskTx(ctx, tx, f.tenant, f.manager, session, ArbitrationTask{AssignedTo: assignee, FirstScore: 3, SecondScore: 1})
			if err != nil {
				return ArbitrationTask{}, err
			}
			return task, tx.Commit()
		}
		if _, err := insertArbitration(englishArbitrator); !errors.Is(err, ErrForbidden) {
			t.Fatalf("wrong-subject arbitration create=%v", err)
		}
		task, err := insertArbitration("")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AssignArbitrationTask(ctx, f.tenant, task.ID, f.manager, AssignArbitrationTaskInput{AssignedTo: englishArbitrator, ExpectedRevision: task.Revision}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("wrong-subject arbitration assignment=%v", err)
		}
		if _, err := store.AssignArbitrationTask(ctx, f.tenant, task.ID, f.manager, AssignArbitrationTaskInput{AssignedTo: mathArbitrator, ExpectedRevision: task.Revision}); err != nil {
			t.Fatalf("qualified arbitration assignment=%v", err)
		}
	})

	t.Run("internal unassigned claims also validate the exam subject", func(t *testing.T) {
		examID, segmentID := f.segment("math")
		task, err := create(segmentID, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextTask(ctx, f.tenant, englishTeacher, NextTaskInput{ExamID: examID}, ClaimTaskOptions{AllowUnassigned: true}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("unqualified new claim=%v", err)
		}
		actual, err := store.GetTask(ctx, f.tenant, task.ID)
		if err != nil || actual.AssignedTo != "" || actual.Revision != task.Revision {
			t.Fatalf("failed claim changed task=%#v %v", actual, err)
		}
		if claimed, err := store.ClaimNextTask(ctx, f.tenant, mathTeacher, NextTaskInput{ExamID: examID}, ClaimTaskOptions{AllowUnassigned: true}); err != nil || claimed.AssignedTo != mathTeacher {
			t.Fatalf("qualified internal claim=%#v %v", claimed, err)
		}
	})

	t.Run("existing owner can continue after teaching subject changes", func(t *testing.T) {
		task, err := create(mathSegment, mathTeacher)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(`UPDATE app_user SET teaching_subjects=ARRAY['english'] WHERE id=$1`, mathTeacher)
		claimed, err := store.ClaimNextTask(ctx, f.tenant, mathTeacher, NextTaskInput{ExamID: mathExam}, ClaimTaskOptions{})
		if err != nil || claimed.AssignedTo != mathTeacher {
			t.Fatalf("historical owner cannot continue: %#v %v (created %s)", claimed, err, task.ID)
		}
		if _, err := create(mathSegment, mathTeacher); !errors.Is(err, ErrForbidden) {
			t.Fatalf("subject changes must block new assignments, error=%v", err)
		}
	})
}

type subjectAssignmentFixture struct {
	t                       *testing.T
	db                      *sql.DB
	tenant, school, manager string
	roles                   map[string]string
}

func newSubjectAssignmentFixture(t *testing.T) *subjectAssignmentFixture {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	schema := "review_subject_assignment_" + uuid.NewString()[:8]
	if _, err := admin.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	f := &subjectAssignmentFixture{t: t, db: db, tenant: uuid.NewString(), school: uuid.NewString(), manager: uuid.NewString(), roles: map[string]string{}}
	for _, table := range []string{"app_user", "exam", "role", "user_role", "role_permission", "permission", "review_task", "double_mark_policy", "double_mark_session", "arbitration_task", "human_grade", "submission", "question", "answer_segment", "exam_question_snapshot", "question_answer_key", "question_solution", "question_rubric", "rubric_version", "answer_segment_answer", "scoring_run_item", "scoring_run", "answer_input_version", "subjective_grading_run", "ai_grade", "answer_candidate", "scoring_rule", "question_grade"} {
		f.exec(`CREATE TABLE ` + table + ` AS TABLE public.` + table + ` WITH NO DATA`)
		rows, err := db.QueryContext(t.Context(), `SELECT a.attname,pg_get_expr(d.adbin,d.adrelid) FROM pg_attribute a JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=$1::regclass`, "public."+table)
		if err != nil {
			t.Fatal(err)
		}
		defaults := map[string]string{}
		for rows.Next() {
			var column, expression string
			if err := rows.Scan(&column, &expression); err != nil {
				t.Fatal(err)
			}
			defaults[column] = expression
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		for column, expression := range defaults {
			f.exec(`ALTER TABLE ` + table + ` ALTER COLUMN ` + column + ` SET DEFAULT ` + expression)
		}
	}
	for _, code := range []string{"grader", "arbitrator", "school_admin"} {
		roleID := uuid.NewString()
		f.roles[code] = roleID
		f.insert("role", map[string]any{"id": roleID, "tenant_id": f.tenant, "code": code})
		if code != "school_admin" {
			permissionID := uuid.NewString()
			permission := "review:work"
			if code == "arbitrator" {
				permission = "arbitration:work"
			}
			f.insert("permission", map[string]any{"id": permissionID, "tenant_id": f.tenant, "code": permission})
			f.insert("role_permission", map[string]any{"tenant_id": f.tenant, "role_id": roleID, "permission_id": permissionID})
		}
	}
	return f
}

func (f *subjectAssignmentFixture) exec(statement string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), statement, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *subjectAssignmentFixture) insert(table string, fields map[string]any) {
	f.t.Helper()
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	raw, err := json.Marshal(fields)
	if err != nil {
		f.t.Fatal(err)
	}
	f.exec(`INSERT INTO `+table+` (`+strings.Join(keys, ",")+`) SELECT `+strings.Join(keys, ",")+` FROM jsonb_populate_record(NULL::`+table+`,$1::jsonb)`, string(raw))
}

func (f *subjectAssignmentFixture) actor(username, school string, subjects []string, roles ...string) string {
	f.t.Helper()
	id := uuid.NewString()
	f.insert("app_user", map[string]any{"id": id, "tenant_id": f.tenant, "school_id": school, "username": username, "status": "active", "teaching_subjects": subjects})
	for _, code := range roles {
		f.insert("user_role", map[string]any{"tenant_id": f.tenant, "user_id": id, "role_id": f.roles[code]})
	}
	return id
}

func (f *subjectAssignmentFixture) segment(subject string) (string, string) {
	f.t.Helper()
	examID, questionID, submissionID, segmentID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.insert("exam", map[string]any{"id": examID, "tenant_id": f.tenant, "school_id": f.school, "subject": subject, "name": "synthetic " + subject, "grading_mode": "single_mark"})
	f.insert("question", map[string]any{"id": questionID, "tenant_id": f.tenant, "exam_id": examID, "question_no": "Q1", "question_type": "short_answer", "score": 5, "knowledge_points": []any{}, "sort_order": 1, "status": "active"})
	f.insert("submission", map[string]any{"id": submissionID, "tenant_id": f.tenant, "exam_id": examID, "candidate_no": "synthetic"})
	f.insert("answer_segment", map[string]any{"id": segmentID, "tenant_id": f.tenant, "question_id": questionID, "submission_id": submissionID})
	f.insert("exam_question_snapshot", map[string]any{"id": uuid.NewString(), "tenant_id": f.tenant, "exam_id": examID, "question_id": questionID, "snapshot_version": 1, "rubric_snapshot_json": map[string]any{}})
	return examID, segmentID
}

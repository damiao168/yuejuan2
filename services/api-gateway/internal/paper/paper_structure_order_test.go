package paper

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
)

func TestBatch37PostgresStructureInsertKeepsCallerBindingsAndFailureOrder(t *testing.T) {
	// 回执字段故意与调用参数不同，保护 SQL 继续绑定原 tenant/import，而不是借用回执身份。
	job, _ := structureBatch37Fixture()
	job.ID, job.TenantID = "database-import", "database-tenant"
	qs := []Question{}
	read := structureBatch37ReadSteps(t, job, qs)
	writes := append(structureBatch37NewQuestionSteps(t, job), structureBatch37FinalSteps(job)...)
	input := structureBatch37Input(job, qs)
	t.Run("success", func(t *testing.T) {
		steps := append(append([]structureBatch37Step{}, read...), writes...)
		store, script := openStructureBatch37Store(t, steps)
		out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
		if err != nil || !out.Confirmed || out.ImportID != job.ID {
			t.Fatalf("insert result=%#v err=%v", out, err)
		}
		assertStructureBatch37Script(t, script, "commit", out)
	})
	for index := range writes {
		t.Run(fmt.Sprintf("write_failure_%02d", index), func(t *testing.T) {
			failure := errors.New("write failure")
			steps := append(append([]structureBatch37Step{}, read...), writes[:index+1]...)
			steps[len(steps)-1].err = failure
			store, script := openStructureBatch37Store(t, steps)
			out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
			if !errors.Is(err, failure) || out.Confirmed {
				t.Fatalf("write failure=%v preview=%#v", err, out)
			}
			assertStructureBatch37Script(t, script, "rollback", map[string]any{"preview": out, "error": err.Error()})
		})
	}
	t.Run("upsert_no_rows_is_conflict", func(t *testing.T) {
		steps := append(append([]structureBatch37Step{}, read...), writes[0])
		steps[len(steps)-1].rows = nil
		store, script := openStructureBatch37Store(t, steps)
		out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
		if !errors.Is(err, ErrConflict) || out.Confirmed {
			t.Fatalf("upsert conflict=%v preview=%#v", err, out)
		}
		assertStructureBatch37Script(t, script, "rollback", err.Error())
	})
	t.Run("commit_failure_keeps_unconfirmed_result", func(t *testing.T) {
		// database/sql 在 Commit 调用后已结束事务；这里只观察返回错误，不宣称 driver 必然收到回滚。
		steps := append(append([]structureBatch37Step{}, read...), writes...)
		store, script := openStructureBatch37Store(t, steps)
		failure := errors.New("commit failure")
		script.commitErr = failure
		out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
		if !errors.Is(err, failure) || out.Confirmed {
			t.Fatalf("commit error=%v preview=%#v", err, out)
		}
		assertStructureBatch37Script(t, script, "commit", map[string]any{"preview": out, "error": err.Error()})
	})
}

func TestBatch37PostgresStructureRemoveUpdateRetireAndContentOrder(t *testing.T) {
	job, qs := structureBatch37Fixture()
	job.Questions[0].Score = 7
	job.Questions[0].AnswerKey = &AnswerKeyInput{StandardAnswer: "new answer"}
	job.Questions[0].Solution = &SolutionInput{RawText: "new solution"}
	qs[0].Options = []string{"teacher option"}
	qs[0].AnswerKey = &AnswerKey{ID: "key", QuestionID: "question", AnswerVersion: "v1", StandardAnswer: "teacher answer"}
	qs[0].Solution = &QuestionSolution{ID: "solution", QuestionID: "question", SolutionVersion: "v1", RawText: "teacher solution", VerificationStatus: "human_confirmed"}
	qs = append(qs, Question{ID: "extra", TenantID: "tenant", ExamID: "exam", QuestionNo: "2", QuestionType: "calculation", SortOrder: 2, Status: "draft"})
	read := structureBatch37ReadSteps(t, job, qs)
	writes := []structureBatch37Step{
		{kind: "exec", fragment: "UPDATE question SET status='deleted'", args: []driver.Value{"tenant", "extra"}},
		{kind: "exec", fragment: "UPDATE question SET question_type", args: []driver.Value{"tenant", "question", "calculation", float64(7), "teacher stem", []byte(`["teacher option"]`), int64(1), "import", "candidate", []byte(`[]`), "", ""}},
		{kind: "exec", fragment: "UPDATE question SET score_source", args: []driver.Value{"tenant", "question", "material"}},
		{kind: "exec", fragment: "UPDATE scoring_rule SET status='retired'", args: []driver.Value{"tenant", "question"}},
	}
	writes = append(writes, structureBatch37FinalSteps(job)...)
	input := structureBatch37Input(job, qs)
	for failed := -1; failed < len(writes); failed++ {
		t.Run(fmt.Sprintf("failure_index_%d", failed), func(t *testing.T) {
			end := "commit"
			var want error
			steps := append(append([]structureBatch37Step{}, read...), writes...)
			if failed >= 0 {
				end, want = "rollback", errors.New("update failure")
				steps = steps[:len(read)+failed+1]
				steps[len(steps)-1].err = want
			}
			store, script := openStructureBatch37Store(t, steps)
			out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
			if !errors.Is(err, want) || out.Confirmed != (failed < 0) {
				t.Fatalf("update error=%v preview=%#v", err, out)
			}
			assertStructureBatch37Script(t, script, end, map[string]any{"preview": out, "error": fmt.Sprint(err)})
		})
	}
}

func TestBatch37PostgresStructureImportedAnswerAndSolutionStayMachine(t *testing.T) {
	job, _ := structureBatch37Fixture()
	job.Questions[0].AnswerKey = &AnswerKeyInput{StandardAnswer: "A"}
	job.Questions[0].AnswerCandidateID = "answer-candidate"
	job.Questions[0].Solution = &SolutionInput{RawText: "material solution"}
	job.Questions[0].HumanConfirmedFields = []string{"solution"}
	job.Questions[0].Rubric = &RubricInput{Status: "locked"}
	qs := []Question{}
	read := structureBatch37ReadSteps(t, job, qs)
	writes := structureBatch37NewQuestionSteps(t, job)
	writes = append(writes,
		structureBatch37Step{kind: "query", fragment: "SELECT COUNT(*) FROM question_answer_key", args: []driver.Value{"tenant", "new-question"}, columns: 1, rows: [][]driver.Value{{int64(0)}}},
		structureBatch37Step{kind: "query", fragment: "INSERT INTO question_answer_key", args: []driver.Value{"tenant", "new-question", "v1", []byte(`"A"`), []byte(`null`), []byte(`null`), "user"}, columns: 1, rows: [][]driver.Value{{"answer-key"}}},
		structureBatch37Step{kind: "exec", fragment: "UPDATE question_answer_key SET paper_import_id", args: []driver.Value{"tenant", "answer-key", "import", "answer-candidate", []byte(`null`)}},
		structureBatch37Step{kind: "query", fragment: "SELECT COUNT(*) FROM question_solution", args: []driver.Value{"tenant", "new-question"}, columns: 1, rows: [][]driver.Value{{int64(0)}}},
		structureBatch37Step{kind: "exec", fragment: "INSERT INTO question_solution", args: []driver.Value{"tenant", "new-question", "import", "v1", "material solution", []byte(`null`), []byte(`null`), "machine", "user"}},
	)
	writes = append(writes, structureBatch37FinalSteps(job)...)
	input := structureBatch37Input(job, qs)
	for failed := -1; failed < 5; failed++ {
		t.Run(fmt.Sprintf("content_failure_%d", failed), func(t *testing.T) {
			end := "commit"
			var want error
			steps := append(append([]structureBatch37Step{}, read...), writes...)
			if failed >= 0 {
				end, want = "rollback", errors.New("content failure")
				steps = steps[:len(read)+5+failed+1]
				steps[len(steps)-1].err = want
			}
			store, script := openStructureBatch37Store(t, steps)
			out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
			if !errors.Is(err, want) || out.Confirmed != (failed < 0) {
				t.Fatalf("content error=%v preview=%#v", err, out)
			}
			assertStructureBatch37Script(t, script, end, map[string]any{"preview": out, "error": fmt.Sprint(err)})
		})
	}
}

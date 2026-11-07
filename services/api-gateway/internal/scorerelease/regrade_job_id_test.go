package scorerelease

import (
	"encoding/json"
	"strings"
	"testing"
)

// 复评任务发布后任务状态仍是 ready_for_release，前端只能靠版本上的 regrade_job_id 判断更正是否已发布；
// 创建回执、列表、详情、发布结果和当前版本都要带上它，普通版本不输出，幂等键本身仍不外露。
func TestRegradeJobIDFollowsEveryReleaseRead(t *testing.T) {
	ctx := t.Context()
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(2)})
	base, err := service.Create(ctx, tenant, exam, actor, CreateInput{Reason: "initial", IdempotencyKey: "initial-release"})
	if err != nil {
		t.Fatal(err)
	}
	if base.RegradeJobID != "" {
		t.Fatalf("ordinary release exposed a regrade job: %#v", base)
	}
	if _, err := service.Publish(ctx, tenant, base.ID, actor); err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFromRegrade(ctx, tenant, exam, actor, CreateRegradeInput{SourceReleaseID: base.ID, QuestionID: "question-1", Reason: "reviewed regrade", IdempotencyKey: "regrade-job:job-42",
		Changes: []RegradeChange{{SubmissionID: "submission-1", QuestionID: "question-1", Score: 4, MaxScore: 5, ReviewedGradeID: "grade-42"}}})
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]Release{"create": created}
	if detail, err := service.Get(ctx, tenant, created.ID); err == nil {
		reads["detail"] = detail.Release
	} else {
		t.Fatal(err)
	}
	if published, err := service.Publish(ctx, tenant, created.ID, actor); err == nil {
		reads["publish"] = published
	} else {
		t.Fatal(err)
	}
	if current, err := service.CurrentPublished(ctx, tenant, exam); err == nil {
		reads["current"] = current.Release
	} else {
		t.Fatal(err)
	}
	listed, err := service.List(ctx, tenant, exam)
	if err != nil {
		t.Fatal(err)
	}
	for _, release := range listed {
		if release.ID == created.ID {
			reads["list"] = release
		} else if release.RegradeJobID != "" {
			t.Fatalf("listed ordinary release exposed a regrade job: %#v", release)
		}
	}
	for name, release := range reads {
		if release.RegradeJobID != "job-42" {
			t.Fatalf("%s lost regrade job id: %#v", name, release)
		}
	}
	encoded, err := json.Marshal(map[string]Release{"regrade": created, "ordinary": base})
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"regrade_job_id":"job-42"`) || strings.Count(text, "regrade_job_id") != 1 || strings.Contains(text, "regrade-job:") || strings.Contains(text, "idempotency") {
		t.Fatalf("release JSON must expose only the derived job id: %s", text)
	}
}

func TestRegradeJobIDRequiresTheRegradeKeyPrefix(t *testing.T) {
	for key, want := range map[string]string{"regrade-job:abc": "abc", "regrade-job-abc": "", "manual-regrade-job:abc": "", "": ""} {
		if got := regradeJobID(key); got != want {
			t.Fatalf("regradeJobID(%q)=%q want %q", key, got, want)
		}
	}
}

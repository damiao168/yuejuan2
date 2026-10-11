package questionbank

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

func fixtureContent() Content {
	return Content{QuestionType: "single_choice", AssessmentArchetype: "selected_response", Stem: "Which is even?", Options: []string{"2", "3"}, DefaultScore: 2.5, KnowledgePoints: []string{"integers"}, Metadata: Metadata{SubjectCode: "mathematics", EducationStage: "junior", GradeScope: "grade_8"}}
}

func TestContentValidationAndHash(t *testing.T) {
	original := fixtureContent()
	normalized, err := normalizeContent(original)
	if err != nil {
		t.Fatal(err)
	}
	normalized.Options[0] = "changed"
	if original.Options[0] != "2" {
		t.Fatal("normalization aliases caller options")
	}
	normalized, _ = normalizeContent(original)
	same, _ := normalizeContent(fixtureContent())
	if contentHash(normalized) != contentHash(same) {
		t.Fatal("hash must be stable")
	}
	same.Options[0], same.Options[1] = same.Options[1], same.Options[0]
	if contentHash(normalized) == contentHash(same) {
		t.Fatal("option order is semantic")
	}
	mutations := []func(*Content){
		func(c *Content) { c.DefaultScore = 0 }, func(c *Content) { c.DefaultScore = 0.001 }, func(c *Content) { c.DefaultScore = math.NaN() },
		func(c *Content) { c.QuestionType = "unknown" }, func(c *Content) { c.AssessmentArchetype = "unknown" }, func(c *Content) { c.Metadata.SubjectCode = "unknown" },
		func(c *Content) { c.Metadata.EducationStage = "primary" }, func(c *Content) { c.Metadata.Copyright = "public" }, func(c *Content) { c.Stem = "  " },
		func(c *Content) { c.KnowledgePoints = []string{"x", " x "} }, func(c *Content) { c.Options = []string{" "} },
	}
	for i, mutate := range mutations {
		c := fixtureContent()
		mutate(&c)
		if _, err := normalizeContent(c); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("case %d accepted: %v", i, err)
		}
	}
}

func TestMemoryConcurrencyReceiptsAndACL(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	scope := auth.AccessScope{TenantID: uuid.NewString(), ActorID: uuid.NewString(), TenantWide: true}
	bank, err := s.CreateBank(commandreceipt.WithID(ctx, "bank"), scope, CreateBankInput{SchoolID: uuid.NewString(), Name: "Bank"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(commandreceipt.WithID(ctx, "item"), scope, bank.ID, CreateItemInput{ItemCode: "M1", Content: fixtureContent()})
	if err != nil {
		t.Fatal(err)
	}
	cloneCtx := commandreceipt.WithID(ctx, "clone")
	id := cloneQuestionBankVersionConcurrently(t, s, cloneCtx, scope, item)
	edited := fixtureContent()
	edited.Stem = "Edited"
	editCtx := commandreceipt.WithID(ctx, "edit")
	v, err := s.UpdateVersion(editCtx, scope, id, UpdateVersionInput{ExpectedRevision: 1, Content: edited})
	if err != nil || v.Revision != 2 {
		t.Fatalf("update: %+v %v", v, err)
	}
	if _, err = s.UpdateVersion(commandreceipt.WithID(ctx, "stale"), scope, id, UpdateVersionInput{ExpectedRevision: 1, Content: edited}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	other := scope
	other.ActorID = uuid.NewString()
	page, err := s.ListBanks(ctx, other, Filter{})
	if err != nil || page.Total != 0 {
		t.Fatalf("bank count leaks: %+v %v", page, err)
	}
	if _, err = s.GetVersion(ctx, other, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("same tenant private version exposed")
	}
	delete(s.acl, aclKey(scope.TenantID, bank.ID, scope.ActorID, "read"))
	if _, err = s.UpdateVersion(editCtx, scope, id, UpdateVersionInput{ExpectedRevision: 1, Content: edited}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receipt bypassed revoked read: %v", err)
	}
	if _, err = s.CreateVersion(cloneCtx, scope, item.Item.ID, CreateVersionInput{SourceVersionID: item.Version.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clone replay bypassed ACL: %v", err)
	}
}

// 四次调用共用同一命令标识；先等所有调用结束，再分别检查每个错误和版本身份。
func cloneQuestionBankVersionConcurrently(t *testing.T, s *MemoryStore, cloneCtx context.Context, scope auth.AccessScope, item ItemResult) string {
	t.Helper()
	var wg sync.WaitGroup
	results := make(chan Version, 4)
	failures := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.CreateVersion(cloneCtx, scope, item.Item.ID, CreateVersionInput{SourceVersionID: item.Version.ID})
			results <- v
			failures <- e
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	var id string
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	for v := range results {
		if id == "" {
			id = v.ID
		}
		if v.ID != id || v.VersionNo != 2 {
			t.Fatal("one command created multiple versions")
		}
	}
	return id
}

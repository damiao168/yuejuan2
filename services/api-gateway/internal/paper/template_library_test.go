package paper

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
)

func templateLibraryFixture(t *testing.T) (*MemoryStore, auth.AccessScope, AnswerSheetTemplate) {
	t.Helper()
	s := NewMemoryStore()
	s.libraryExams = questionDraftExamReader(func(_ context.Context, scope auth.AccessScope, id string) (exam.Exam, error) {
		if scope.TenantID != "tenant" || id == "missing" {
			return exam.Exam{}, ErrNotFound
		}
		school := "school"
		if id == "other-school" {
			school = "other"
		}
		return exam.Exam{ID: id, TenantID: scope.TenantID, SchoolID: school}, nil
	})
	scope := auth.AccessScope{TenantID: "tenant", ActorID: "teacher", TenantWide: true}
	s.papers["source-paper"] = Paper{ID: "source-paper", TenantID: "tenant", ExamID: "source-exam", Purpose: PaperPurposeAnswerSheet, FileAssetID: "source-file", File: FileAssetInput{OriginalName: "blank.png", ContentType: "image/png", SizeBytes: 100, HashSHA256: strings.Repeat("a", 64), StorageBucket: "test", StorageKey: "test/blank.png"}}
	for _, e := range []string{"source-exam", "next-exam", "other-school", "frozen"} {
		s.questions[e+"-q"] = Question{ID: e + "-q", TenantID: "tenant", ExamID: e, QuestionNo: "17", QuestionType: "calculation", Status: "active"}
	}
	source := AnswerSheetTemplate{ID: "source-template", TenantID: "tenant", ExamID: "source-exam", ExamPaperID: "source-paper", Name: "source", Status: "locked", Revision: 3, PageCount: 1, Layout: TemplateLayout{Pages: []TemplatePage{{PageNo: 1, Width: 1000, Height: 1500, QuestionRegions: []LayoutRegion{{ID: "region", QuestionID: "source-exam-q", X: .1, Y: .2, Width: .8, Height: .4, OptionRegions: []OptionRegion{{ID: "option", Label: "A", X: .1, Y: .2, Width: .1, Height: .1}}}}}}}}
	source.Layout = NormalizeTemplateLayout(source.Layout)
	source.ContentHash = stableContentHash(source.Layout)
	s.templates[source.ID] = source
	return s, scope, source
}

func publishLibraryFixture(t *testing.T, s *MemoryStore, scope auth.AccessScope, source AnswerSheetTemplate) AnswerSheetTemplateLibraryVersion {
	t.Helper()
	out, err := s.PublishTemplateToLibrary(t.Context(), scope, source.ID, PublishTemplateToLibraryInput{Name: "学校通用卡", ExpectedRevision: source.Revision, ConfirmedBlankReference: true, ConfirmedReusableLayout: true})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTemplateLibraryRejectsOverlappingQuestionAndIdentityAreas(t *testing.T) {
	for _, scenario := range []string{"overlapping questions", "identity overlap", "touching edges"} {
		t.Run(scenario, func(t *testing.T) {
			store, scope, source := templateLibraryFixture(t)
			region := source.Layout.Pages[0].QuestionRegions[0]
			if scenario == "identity overlap" {
				source.Layout.Pages[0].IdentityRegions = []LayoutRegion{{ID: "identity", X: region.X, Y: region.Y, Width: .1, Height: .1}}
			} else {
				second := store.questions["source-exam-q"]
				second.ID, second.QuestionNo = "source-exam-q18", "18"
				store.questions[second.ID] = second
				region.ID, region.QuestionID = "r18", second.ID
				if scenario == "touching edges" {
					region.Y += region.Height
				}
				source.Layout.Pages[0].QuestionRegions = append(source.Layout.Pages[0].QuestionRegions, region)
			}
			store.templates[source.ID] = source
			_, err := store.PublishTemplateToLibrary(t.Context(), scope, source.ID, PublishTemplateToLibraryInput{Name: "card", ExpectedRevision: source.Revision, ConfirmedBlankReference: true, ConfirmedReusableLayout: true})
			if scenario == "touching edges" && err != nil {
				t.Fatalf("touching edges rejected: %v", err)
			}
			if scenario != "touching edges" && (err != ErrInvalidInput || len(store.templateLibrary) != 0) {
				t.Fatalf("overlap published: %v", err)
			}
		})
	}
}

func TestTemplateLibraryCrossExamReuseRemapsQuestionsWithoutAnswersCalibrationOrBinding(t *testing.T) {
	s, scope, source := templateLibraryFixture(t)
	item := publishLibraryFixture(t, s, scope, source)
	raw, _ := json.Marshal(item)
	if strings.Contains(string(raw), "question_id") || strings.Contains(string(raw), "source-exam-q") {
		t.Fatalf("library leaked old question id: %s", raw)
	}
	if item.Layout.Pages[0].QuestionRegions[0].QuestionNo != "17" {
		t.Fatal("printed slot missing")
	}
	instance, err := s.InstantiateTemplateLibrary(t.Context(), scope, "next-exam", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Template.Status != "draft" || instance.Template.Layout.Pages[0].QuestionRegions[0].QuestionID != "next-exam-q" || instance.Paper.ExamID != "next-exam" || instance.Paper.FileAssetID == "source-file" || instance.Template.Layout.OMRProfile.Mode != OMRProfileModeManualOnly {
		t.Fatalf("unsafe reuse: %#v", instance)
	}
	if len(s.bindings) != 0 || s.questions["next-exam-q"].AnswerKey != nil || s.questions["next-exam-q"].AnswerArea != nil {
		t.Fatal("reuse wrote binding, answer or formal area")
	}
	// 返回值的切片不能改写不可变快照或另一请求读到的模板。
	instance.Template.Layout.Pages[0].QuestionRegions[0].X = .9
	item.Layout.Pages[0].QuestionRegions[0].X = .9
	again, err := s.InstantiateTemplateLibrary(t.Context(), scope, "next-exam", item.ID)
	if err != nil || !again.Reused || again.Template.ID != instance.Template.ID || again.Template.Layout.Pages[0].QuestionRegions[0].X != .1 {
		t.Fatalf("idempotence or copy broken: %#v %v", again, err)
	}
	publishedAgain := publishLibraryFixture(t, s, scope, source)
	if publishedAgain.ID != item.ID || publishedAgain.Layout.Pages[0].QuestionRegions[0].X != .1 || len(s.templateLibrary) != 1 {
		t.Fatal("publication retry mutated/duplicated version")
	}
}

func templateLibraryReviewInstanceFixture(t *testing.T) (*MemoryStore, auth.AccessScope, TemplateLibraryInstance) {
	t.Helper()
	store, scope, source := templateLibraryFixture(t)
	source.Layout.Pages[0].RegistrationMarks = []LayoutRegion{{ID: "registration", X: .02, Y: .02, Width: .02, Height: .02}}
	source.Layout.Pages[0].IdentityRegions = []LayoutRegion{{ID: "name", Label: "姓名", X: .1, Y: .05, Width: .2, Height: .05}}
	store.templates[source.ID] = source
	item := publishLibraryFixture(t, store, scope, source)
	instance, err := store.InstantiateTemplateLibrary(t.Context(), scope, "next-exam", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, scope, instance
}

func TestTemplateLibraryInstanceCanLockBindAndPublishWithoutPerRegionConfirmation(t *testing.T) {
	store, scope, instance := templateLibraryReviewInstanceFixture(t)
	page := instance.Template.Layout.Pages[0]
	for _, regions := range [][]LayoutRegion{page.RegistrationMarks, page.IdentityRegions, page.QuestionRegions} {
		for _, region := range regions {
			if region.Origin != "library" || region.ReviewState != "suggested" || region.SemanticCheck != "not_evaluated" {
				t.Fatalf("library reuse invented a new review or model fact: %+v", region)
			}
		}
	}
	locked, err := store.LockTemplateAtRevision(t.Context(), scope.TenantID, instance.Template.ID, scope.ActorID, instance.Template.Revision)
	if err != nil {
		t.Fatalf("usable library geometry required per-region confirmation: %v", err)
	}
	if _, err := store.BindExamTemplate(t.Context(), scope.TenantID, locked.ExamID, scope.ActorID, BindExamTemplateInput{TemplateID: locked.ID, ExpectedRevision: 0}); err != nil {
		t.Fatalf("usable library instance could not be enabled: %v", err)
	}
	if _, err := store.PublishTemplateToLibrary(t.Context(), scope, locked.ID, PublishTemplateToLibraryInput{Name: "reused", ExpectedRevision: locked.Revision, ConfirmedBlankReference: true, ConfirmedReusableLayout: true}); err != nil {
		t.Fatalf("usable library instance could not be republished: %v", err)
	}
}

func TestTemplateLibraryInstanceConflictStillBlocksLockBindAndPublish(t *testing.T) {
	for _, kind := range []string{"registration", "identity", "question"} {
		t.Run(kind, func(t *testing.T) {
			store, scope, instance := templateLibraryReviewInstanceFixture(t)
			source := instance.Template
			page := &source.Layout.Pages[0]
			region := &page.QuestionRegions[0]
			if kind == "registration" {
				region = &page.RegistrationMarks[0]
			} else if kind == "identity" {
				region = &page.IdentityRegions[0]
			}
			region.SemanticCheck = "needs_review"
			store.templates[source.ID] = source
			if _, err := store.LockTemplateAtRevision(t.Context(), scope.TenantID, source.ID, scope.ActorID, source.Revision); !errors.Is(err, ErrTemplateReviewRequired) {
				t.Fatalf("conflicting library region locked: %v", err)
			}
			// 模拟历史已锁定实例；启用和入库仍须单独拒绝显式冲突，不能只相信库来源或 locked 状态。
			source.Status = "locked"
			store.templates[source.ID] = source
			if _, err := store.BindExamTemplate(t.Context(), scope.TenantID, source.ExamID, scope.ActorID, BindExamTemplateInput{TemplateID: source.ID, ExpectedRevision: 0}); !errors.Is(err, ErrTemplateReviewRequired) {
				t.Fatalf("conflicting library region enabled: %v", err)
			}
			if _, err := store.PublishTemplateToLibrary(t.Context(), scope, source.ID, PublishTemplateToLibraryInput{Name: "conflict", ExpectedRevision: source.Revision, ConfirmedBlankReference: true, ConfirmedReusableLayout: true}); !errors.Is(err, ErrTemplateReviewRequired) {
				t.Fatalf("conflicting library region published: %v", err)
			}
		})
	}
}

func TestTemplateLibraryRejectsUnreviewedDraftStaleAndIncompletePublication(t *testing.T) {
	for _, caseName := range []string{"blank unconfirmed", "layout unconfirmed", "stale revision", "draft", "wrong purpose", "missing question", "invalid hash"} {
		t.Run(caseName, func(t *testing.T) {
			s, scope, source := templateLibraryFixture(t)
			input := PublishTemplateToLibraryInput{Name: "card", ExpectedRevision: 3, ConfirmedBlankReference: true, ConfirmedReusableLayout: true}
			switch caseName {
			case "blank unconfirmed":
				input.ConfirmedBlankReference = false
			case "layout unconfirmed":
				input.ConfirmedReusableLayout = false
			case "stale revision":
				input.ExpectedRevision = 2
			case "draft":
				source.Status = "draft"
				s.templates[source.ID] = source
			case "wrong purpose":
				p := s.papers[source.ExamPaperID]
				p.Purpose = PaperPurposeQuestion
				s.papers[p.ID] = p
			case "missing question":
				s.questions["extra"] = Question{ID: "extra", TenantID: scope.TenantID, ExamID: source.ExamID, QuestionNo: "18", QuestionType: "calculation"}
			case "invalid hash":
				p := s.papers[source.ExamPaperID]
				p.File.HashSHA256 = strings.Repeat("z", 64)
				s.papers[p.ID] = p
			}
			if _, err := s.PublishTemplateToLibrary(t.Context(), scope, source.ID, input); err == nil || len(s.templateLibrary) != 0 {
				t.Fatalf("invalid publication accepted: %v", err)
			}
		})
	}
}

func TestTemplateLibraryIsolationFrozenAndQuestionMismatchLeaveNoPartialInstance(t *testing.T) {
	for _, caseName := range []string{"school", "tenant", "frozen", "missing question", "duplicate question", "different type"} {
		t.Run(caseName, func(t *testing.T) {
			s, scope, source := templateLibraryFixture(t)
			item := publishLibraryFixture(t, s, scope, source)
			target := "next-exam"
			switch caseName {
			case "school":
				target = "other-school"
			case "tenant":
				scope.TenantID = "other-tenant"
			case "frozen":
				s.SetReadinessContext(target, 10, 1, 1, "collecting")
			case "missing question":
				delete(s.questions, "next-exam-q")
			case "duplicate question":
				s.questions["duplicate"] = Question{ID: "duplicate", TenantID: scope.TenantID, ExamID: target, QuestionNo: "17", QuestionType: "calculation"}
			case "different type":
				q := s.questions["next-exam-q"]
				q.QuestionType = "essay"
				s.questions[q.ID] = q
			}
			_, err := s.InstantiateTemplateLibrary(t.Context(), scope, target, item.ID)
			if err == nil || len(s.papers) != 1 || len(s.templates) != 1 || len(s.libraryInstances) != 0 {
				t.Fatalf("partial or invalid instance: %v", err)
			}
			if caseName == "frozen" && !errors.Is(err, ErrExamFrozen) {
				t.Fatal(err)
			}
		})
	}
}

func TestTemplateLibraryResolveSeparatesUnknownAmbiguousAndMatched(t *testing.T) {
	s, scope, source := templateLibraryFixture(t)
	item := publishLibraryFixture(t, s, scope, source)
	p := s.papers[source.ExamPaperID]
	p.ID = "uploaded"
	p.ExamID = "next-exam"
	p.FileAssetID = "uploaded-file"
	s.papers[p.ID] = p
	out, err := s.ResolveTemplateLibrary(t.Context(), scope, "next-exam", ResolveTemplateLibraryInput{ExamPaperID: p.ID})
	if err != nil || out.Status != "matched" || out.Instance == nil || out.Instance.Paper.ID != p.ID {
		t.Fatalf("exact reference did not reuse upload: %#v %v", out, err)
	}
	duplicate := item
	duplicate.ID = "different-reviewed-layout"
	s.templateLibrary[duplicate.ID] = duplicate
	out, err = s.ResolveTemplateLibrary(t.Context(), scope, "next-exam", ResolveTemplateLibraryInput{ExamPaperID: p.ID})
	if err != nil || out.Status != "ambiguous" || out.Instance != nil || len(s.libraryInstances) != 1 {
		t.Fatalf("ambiguity silently chose a version: %#v %v", out, err)
	}
	p.File.HashSHA256 = strings.Repeat("b", 64)
	s.papers[p.ID] = p
	out, err = s.ResolveTemplateLibrary(t.Context(), scope, "next-exam", ResolveTemplateLibraryInput{ExamPaperID: p.ID})
	if err != nil || out.Status != "unknown" || out.Instance != nil || len(out.Candidates) != 0 {
		t.Fatalf("unknown silently created instance: %#v %v", out, err)
	}
}

func TestTemplateLibraryConcurrentInstantiateCreatesOneExamDraft(t *testing.T) {
	s, scope, source := templateLibraryFixture(t)
	item := publishLibraryFixture(t, s, scope, source)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.InstantiateTemplateLibrary(t.Context(), scope, "next-exam", item.ID)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(s.papers) != 2 || len(s.templates) != 2 || len(s.libraryInstances) != 1 {
		t.Fatal("parallel requests duplicated the draft")
	}
}

func TestTemplateLibraryDuplicateReferenceAndGeometryReuseExistingVersion(t *testing.T) {
	s, scope, source := templateLibraryFixture(t)
	first := publishLibraryFixture(t, s, scope, source)
	other := copyLibraryValue(source)
	other.ID = "separately-confirmed-source"
	other.Layout.Pages[0].QuestionRegions[0].ID = "another-editor-region-id"
	other.Layout.Pages[0].QuestionRegions[0].OptionRegions[0].ID = "another-option-id"
	s.templates[other.ID] = other
	second := publishLibraryFixture(t, s, scope, other)
	if first.ID != second.ID || len(s.templateLibrary) != 1 {
		t.Fatal("same reference and geometry produced a duplicate version")
	}
}

func TestTemplateLibraryFrozenResolveDoesNotCreateDraftOrReplaceBinding(t *testing.T) {
	s, scope, source := templateLibraryFixture(t)
	item := publishLibraryFixture(t, s, scope, source)
	p := s.papers[source.ExamPaperID]
	p.ID = "uploaded"
	p.ExamID = "next-exam"
	p.FileAssetID = "uploaded-file"
	s.papers[p.ID] = p
	s.SetReadinessContext("next-exam", 10, 1, 1, "collecting")
	s.bindings["next-exam"] = ExamTemplateBinding{TemplateID: "existing-bound-template", Revision: 4}
	_, err := s.ResolveTemplateLibrary(t.Context(), scope, "next-exam", ResolveTemplateLibraryInput{ExamPaperID: p.ID})
	if !errors.Is(err, ErrExamFrozen) || len(s.templates) != 1 || len(s.libraryInstances) != 0 || s.bindings["next-exam"].TemplateID != "existing-bound-template" {
		t.Fatalf("frozen resolve changed exam: %v", err)
	}
	if s.templateLibrary[item.ID].ID == "" {
		t.Fatal("frozen exam removed reusable school version")
	}
}

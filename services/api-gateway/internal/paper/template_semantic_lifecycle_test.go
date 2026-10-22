package paper

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// 故障响应保留框和安全题号用于人工核对；工作节点缺字段、类型矛盾或重复分块都不能直接绑定正式题目。
func TestTemplateSemanticDefenseDowngradesUnverifiedAndConflictingRegions(t *testing.T) {
	input := testPageAnalysisInput(t)
	for _, test := range []struct {
		name       string
		edit       func(*TemplatePageAnalysis)
		objective  []string
		wantReason string
	}{
		{"legacy worker", func(out *TemplatePageAnalysis) { out.Candidates[0].SemanticCheck = "" }, nil, "SEMANTIC_EVIDENCE_INSUFFICIENT"},
		{"failed local check", func(out *TemplatePageAnalysis) { out.Candidates[0].SemanticCheck = "needs_review" }, nil, "SEMANTIC_EVIDENCE_INSUFFICIENT"},
		{"subjective as objective", func(out *TemplatePageAnalysis) { out.Candidates[0].Role = "objective_group" }, []string{"1"}, "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT"},
		{"objective without configured objective questions", func(out *TemplatePageAnalysis) {
			out.Candidates[0].Role, out.Candidates[0].PrintedQuestionLabel, out.Candidates[0].AssociationStatus = "objective_group", nil, "unassociated"
		}, nil, "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT"},
		{"objective as identity", func(out *TemplatePageAnalysis) {
			out.Candidates[0].Role, out.Candidates[0].IdentityLabel = "identity", new("姓名")
		}, []string{"17"}, "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT"},
		{"barcode negative evidence", func(out *TemplatePageAnalysis) {
			out.Candidates[0].Role, out.Candidates[0].IdentityLabel, out.Candidates[0].PrintedQuestionLabel, out.Candidates[0].AssociationStatus, out.Candidates[0].SemanticCheck = "identity", new("条码"), nil, "unassociated", "needs_review"
			out.Candidates[0].ReasonCodes = []string{"CONTENT_FORBIDDEN_WRITING"}
		}, nil, "SEMANTIC_FORBIDDEN_AREA_DETECTED"},
		{"multipart question", func(out *TemplatePageAnalysis) {
			extra := out.Candidates[0]
			extra.CandidateID = "part-two"
			out.Candidates = append(out.Candidates, extra)
		}, nil, "SEMANTIC_MULTIPART_UNSUPPORTED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := testPageAnalysisResult(input)
			test.edit(&out)
			originalBox := out.Candidates[0].ProposedBBox
			defendTemplatePageSemantics(&out, templateAnalysisQuestionLabels{All: []string{"1", "17"}, Objective: test.objective})
			for _, row := range out.Candidates {
				if row.Role != "uncertain" || row.AssociationStatus != "unassociated" || row.SemanticCheck != "needs_review" || row.IdentityLabel != nil || !slices.Contains(row.ReasonCodes, test.wantReason) {
					t.Fatalf("unsafe candidate survived: %+v", row)
				}
			}
			if out.Candidates[0].ProposedBBox != originalBox {
				t.Fatal("semantic downgrade changed geometry")
			}
			if err := validateTemplatePageAnalysisResult(out, input, []string{"1", "17"}); err != nil {
				t.Fatalf("reviewable result rejected: %v", err)
			}
		})
	}
}

func TestTemplateSemanticDefenseRejectsExplicitNegativeEvidenceEvenWhenSupported(t *testing.T) {
	for _, role := range []string{"answer", "identity", "objective_group"} {
		for _, code := range []string{"CONTENT_FORBIDDEN_WRITING", "SEMANTIC_FORBIDDEN_AREA_DETECTED", "LOCAL_FORBIDDEN_LABEL_VERIFIED", "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED", "SEMANTIC_CONTENT_CONFLICT", "MODEL_OBS_WARNING_CATEGORY_CONFLICT", "REGION_CONFLICT"} {
			t.Run(role+"/"+code, func(t *testing.T) {
				input := testPageAnalysisInput(t)
				out := testPageAnalysisResult(input)
				row := &out.Candidates[0]
				row.Role, row.ReasonCodes = role, []string{code}
				if role != "answer" {
					row.PrintedQuestionLabel, row.AssociationStatus = nil, "unassociated"
				}
				if role == "identity" {
					row.IdentityLabel = new("条码")
				}
				box := row.ProposedBBox
				defendTemplatePageSemantics(&out, templateAnalysisQuestionLabels{All: []string{"1", "17"}, Objective: []string{"1"}})
				if row.Role != "uncertain" || row.SemanticCheck != "needs_review" || row.AssociationStatus != "unassociated" || row.IdentityLabel != nil || row.ProposedBBox != box || !slices.Contains(row.ReasonCodes, code) {
					t.Fatalf("explicit negative evidence became usable: %+v", row)
				}
				if err := validateTemplatePageAnalysisResult(out, input, []string{"1", "17"}); err != nil {
					t.Fatalf("reviewable geometry rejected: %v", err)
				}
			})
		}
	}
	// 独立警示牌已经正确归为说明区时，排除原因码是正常结果，不能把它变回待纠错框。
	for _, code := range []string{"LOCAL_FORBIDDEN_LABEL_VERIFIED", "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED"} {
		input := testPageAnalysisInput(t)
		out := testPageAnalysisResult(input)
		row := &out.Candidates[0]
		row.Role, row.PrintedQuestionLabel, row.AssociationStatus, row.ReasonCodes = "instruction", nil, "unassociated", []string{code}
		defendTemplatePageSemantics(&out, templateAnalysisQuestionLabels{All: []string{"17"}})
		if row.Role != "instruction" || row.SemanticCheck != "supported" {
			t.Fatalf("excluded warning became a visible review candidate: %+v", row)
		}
	}
}

func TestTemplateReviewLifecycleRejectsAutomaticConflictButAcceptsHumanCorrection(t *testing.T) {
	for _, state := range []string{"suggested", "edited", "confirmed"} {
		for _, code := range []string{"SEMANTIC_FORBIDDEN_AREA_DETECTED", "LOCAL_FORBIDDEN_LABEL_VERIFIED", "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED", "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT", "MODEL_OBS_WARNING_CATEGORY_CONFLICT", "MODEL_OBS_MULTIPLE_QUESTION_LABELS", "SEMANTIC_MULTIPART_UNSUPPORTED", "OBJECTIVE_QUESTION_REQUIRES_GROUP"} {
			t.Run(state+"/"+code, func(t *testing.T) {
				store, scope, source := templateLibraryFixture(t)
				source.Status = "draft"
				region := &source.Layout.Pages[0].QuestionRegions[0]
				region.Origin, region.ReviewState, region.SemanticCheck, region.ReasonCodes = "model", state, "supported", []string{code}
				store.templates[source.ID] = source
				saved, err := store.UpdateTemplate(t.Context(), source.TenantID, source.ID, UpdateTemplateInput{Name: source.Name, PageCount: source.PageCount, Layout: source.Layout, ExpectedRevision: source.Revision})
				if err != nil {
					t.Fatalf("conflicting draft must remain saveable: %v", err)
				}
				check := func(action string, err error) {
					t.Helper()
					if state == "suggested" && !errors.Is(err, ErrTemplateReviewRequired) {
						t.Fatalf("%s accepted automatic negative evidence: %v", action, err)
					}
					if state != "suggested" && err != nil {
						t.Fatalf("%s rejected human correction: %v", action, err)
					}
				}
				_, err = store.LockTemplateAtRevision(t.Context(), source.TenantID, source.ID, "teacher", saved.Revision)
				check("lock", err)
				// 历史模板可能已锁定；绑定和入库也必须独立拒绝未解决的自动反证。
				saved.Status = "locked"
				store.templates[source.ID] = saved
				_, err = store.BindExamTemplate(t.Context(), source.TenantID, source.ExamID, "teacher", BindExamTemplateInput{TemplateID: source.ID, ExpectedRevision: 0})
				check("bind", err)
				_, err = store.PublishTemplateToLibrary(t.Context(), scope, source.ID, PublishTemplateToLibraryInput{Name: "library", ExpectedRevision: saved.Revision, ConfirmedBlankReference: true, ConfirmedReusableLayout: true})
				check("publish", err)
			})
		}
	}
}

func TestTemplateReviewLifecycleAcceptsSupportedRecognitionAndHumanEditsWithoutExtraConfirmation(t *testing.T) {
	for _, test := range []struct {
		name     string
		state    string
		semantic string
		wantLock bool
	}{
		{"supported automatic result", "suggested", "supported", true},
		{"automatic result with unresolved conflict", "suggested", "needs_review", false},
		{"automatic result without semantic check", "suggested", "not_evaluated", false},
		{"human corrected conflict", "edited", "needs_review", true},
		{"human edited result", "edited", "supported", true},
		{"explicitly confirmed result", "confirmed", "supported", true},
		{"discarded formal region", "discarded", "supported", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, source := templateLibraryFixture(t)
			source.Status = "draft"
			region := &source.Layout.Pages[0].QuestionRegions[0]
			region.Origin, region.ReviewState, region.SemanticCheck = "model", test.state, test.semantic
			// 正向说明和辅助检查缺失也可写入原因码，不能把非空列表一律当作识别失败。
			region.ReasonCodes = []string{"MODEL_OBSERVATION_SUPPORTED", "CONTENT_OCR_UNAVAILABLE", "SEMANTIC_OCR_UNAVAILABLE", "LOCAL_FORBIDDEN_OCR_UNAVAILABLE", "SEMANTIC_CHECK_BUDGET_EXCEEDED"}
			store.templates[source.ID] = source
			saved, err := store.UpdateTemplate(t.Context(), source.TenantID, source.ID, UpdateTemplateInput{Name: source.Name, PageCount: source.PageCount, Layout: source.Layout, ExpectedRevision: source.Revision})
			if err != nil {
				t.Fatalf("draft could not save %s region: %v", test.state, err)
			}
			_, err = store.LockTemplateAtRevision(t.Context(), source.TenantID, source.ID, "teacher", saved.Revision)
			if test.wantLock {
				if err != nil {
					t.Fatalf("usable region required an extra confirmation: %v", err)
				}
			} else if !errors.Is(err, ErrTemplateReviewRequired) {
				t.Fatalf("unresolved region locked: %v", err)
			}
		})
	}
}

func TestTemplateSupportedRecognitionCanBindAndPublishWithoutPerRegionConfirmation(t *testing.T) {
	store, scope, source := templateLibraryFixture(t)
	region := &source.Layout.Pages[0].QuestionRegions[0]
	region.Origin, region.ReviewState, region.SemanticCheck = "model", "suggested", "supported"
	store.templates[source.ID] = source
	if _, err := store.BindExamTemplate(t.Context(), source.TenantID, source.ExamID, "teacher", BindExamTemplateInput{TemplateID: source.ID, ExpectedRevision: 0}); err != nil {
		t.Fatalf("supported recognition could not be enabled: %v", err)
	}
	if _, err := store.PublishTemplateToLibrary(t.Context(), scope, source.ID, PublishTemplateToLibraryInput{Name: "library", ExpectedRevision: source.Revision, ConfirmedBlankReference: true, ConfirmedReusableLayout: true}); err != nil {
		t.Fatalf("supported recognition could not be published: %v", err)
	}
}

func TestTemplatePendingDraftSurvivesReloadButCannotLockBindOrPublish(t *testing.T) {
	store, scope, source := templateLibraryFixture(t)
	source.Status = "draft"
	source.Layout.EditorState = &TemplateEditorState{Version: 1, SourceSHA256: strings.Repeat("a", 64), Candidates: []TemplateEditorCandidate{{ID: "pending", PageNo: 1, Role: "uncertain", Region: LayoutRegion{ID: "pending", X: .1, Y: .7, Width: .2, Height: .1, Origin: "model", ReviewState: "suggested", SemanticCheck: "needs_review", ReasonCodes: []string{"SEMANTIC_EVIDENCE_INSUFFICIENT"}}}}}
	store.templates[source.ID] = source
	saved, err := store.UpdateTemplate(t.Context(), source.TenantID, source.ID, UpdateTemplateInput{Name: source.Name, PageCount: source.PageCount, Layout: source.Layout, ExpectedRevision: source.Revision})
	if err != nil {
		t.Fatal(err)
	}
	read, err := store.ReadTemplateForAnalysis(t.Context(), source.TenantID, source.ID)
	if err != nil || len(read.Layout.EditorState.Candidates) != 1 {
		t.Fatalf("pending draft lost: %+v %v", read, err)
	}
	if _, err := store.LockTemplateAtRevision(t.Context(), source.TenantID, source.ID, "teacher", saved.Revision); !errors.Is(err, ErrTemplateReviewRequired) {
		t.Fatalf("pending draft locked: %v", err)
	}
	// 模拟历史状态已锁定但候选未解决；启用和入库各自复核，不能只相信 status。
	saved.Status = "locked"
	store.templates[source.ID] = saved
	if _, err := store.BindExamTemplate(t.Context(), source.TenantID, source.ExamID, "teacher", BindExamTemplateInput{TemplateID: source.ID, ExpectedRevision: 0}); !errors.Is(err, ErrTemplateReviewRequired) {
		t.Fatalf("pending draft bound: %v", err)
	}
	if _, err := store.PublishTemplateToLibrary(t.Context(), scope, source.ID, PublishTemplateToLibraryInput{Name: "library", ExpectedRevision: saved.Revision, ConfirmedBlankReference: true, ConfirmedReusableLayout: true}); !errors.Is(err, ErrTemplateReviewRequired) {
		t.Fatalf("pending draft published: %v", err)
	}
	read.Layout.EditorState.Candidates[0].Region.ReviewState = "discarded"
	if err := validateTemplateReviewResolved(read.Layout); err != nil {
		t.Fatalf("discarded tombstone blocked resolution: %v", err)
	}
}

func TestTemplateMemorySnapshotsDoNotShareEditorMetadata(t *testing.T) {
	store, _, source := templateLibraryFixture(t)
	source.Status = "draft"
	source.Layout.Pages[0].QuestionRegions[0].ReasonCodes = []string{"CONTENT_VERIFIED"}
	source.Layout.EditorState = &TemplateEditorState{Version: 1, SourceSHA256: strings.Repeat("a", 64), Candidates: []TemplateEditorCandidate{{ID: "discarded", PageNo: 1, Role: "uncertain", Region: LayoutRegion{ID: "discarded", X: .1, Y: .7, Width: .2, Height: .1, ReviewState: "discarded", ReasonCodes: []string{"CONTENT_UNCERTAIN"}}}}}
	store.templates[source.ID] = cloneTemplateSnapshot(source)
	read, err := store.ReadTemplateForAnalysis(t.Context(), source.TenantID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	read.Layout.EditorState.Candidates[0].Region.ReasonCodes[0] = "CHANGED"
	read.Layout.Pages[0].QuestionRegions[0].ReasonCodes[0] = "CHANGED"
	read.Layout.Pages[0].QuestionRegions[0].OptionRegions[0].X = .9
	again, _ := store.ReadTemplateForAnalysis(t.Context(), source.TenantID, source.ID)
	if again.Layout.EditorState.Candidates[0].Region.ReasonCodes[0] != "CONTENT_UNCERTAIN" || again.Layout.Pages[0].QuestionRegions[0].ReasonCodes[0] != "CONTENT_VERIFIED" || again.Layout.Pages[0].QuestionRegions[0].OptionRegions[0].X != .1 {
		t.Fatal("caller rewrote repository snapshot")
	}
	_, err = store.UpdateTemplate(t.Context(), source.TenantID, source.ID, UpdateTemplateInput{Name: source.Name, PageCount: source.PageCount, Layout: source.Layout, ExpectedRevision: source.Revision})
	if err != nil {
		t.Fatal(err)
	}
	source.Layout.EditorState.Candidates[0].Region.ReasonCodes[0] = "INPUT_CHANGED"
	again, _ = store.ReadTemplateForAnalysis(t.Context(), source.TenantID, source.ID)
	if again.Layout.EditorState.Candidates[0].Region.ReasonCodes[0] != "CONTENT_UNCERTAIN" {
		t.Fatal("saved layout retained input alias")
	}
}

func TestTemplateEditorStateBindsSourceAndRejectsMalformedDraftMetadata(t *testing.T) {
	store, _, source := templateLibraryFixture(t)
	source.Status = "draft"
	source.Layout.EditorState = &TemplateEditorState{Version: 1, SourceSHA256: strings.Repeat("a", 64), Candidates: []TemplateEditorCandidate{}}
	store.templates[source.ID] = cloneTemplateSnapshot(source)
	for _, test := range []struct {
		name string
		edit func(*TemplateLayout)
		want error
	}{
		{"different source", func(layout *TemplateLayout) { layout.EditorState.SourceSHA256 = strings.Repeat("b", 64) }, ErrConflict},
		{"missing candidate array", func(layout *TemplateLayout) { layout.EditorState.Candidates = nil }, ErrInvalidInput},
		{"free text reason", func(layout *TemplateLayout) {
			layout.Pages[0].QuestionRegions[0].ReasonCodes = []string{"student value"}
		}, ErrInvalidInput},
		{"invalid source enum", func(layout *TemplateLayout) { layout.Pages[0].QuestionRegions[0].Origin = "model_guaranteed" }, ErrInvalidInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			layout := cloneTemplateLayout(source.Layout)
			test.edit(&layout)
			_, err := store.UpdateTemplate(t.Context(), source.TenantID, source.ID, UpdateTemplateInput{Name: source.Name, PageCount: source.PageCount, Layout: layout, ExpectedRevision: source.Revision})
			if !errors.Is(err, test.want) {
				t.Fatalf("malformed draft accepted: %v", err)
			}
			if store.templates[source.ID].Revision != source.Revision {
				t.Fatal("failed draft update changed revision")
			}
		})
	}
}

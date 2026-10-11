package capture

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type scanBatchFixture struct {
	Store
	batch Batch
}

func (s scanBatchFixture) GetBatch(_ context.Context, tenantID, id string) (Batch, error) {
	if tenantID != s.batch.TenantID || id != s.batch.ID {
		return Batch{}, ErrNotFound
	}
	return s.batch, nil
}

type scanTemplateFixture struct {
	binding             paper.ExamTemplateBinding
	templates           []paper.AnswerSheetTemplate
	bindingErr, listErr error
	changed             bool
	reads               int
}

func (s *scanTemplateFixture) GetExamTemplateBinding(_ context.Context, _, _ string) (paper.ExamTemplateBinding, error) {
	s.reads++
	if s.bindingErr != nil {
		return paper.ExamTemplateBinding{}, s.bindingErr
	}
	binding := s.binding
	if s.changed && s.reads > 1 {
		binding.Revision++
	}
	return binding, nil
}
func (s *scanTemplateFixture) ListTemplates(_ context.Context, _, _ string) ([]paper.AnswerSheetTemplate, error) {
	return s.templates, s.listErr
}

func newScanContextFixture(t *testing.T) (*Handler, *scanTemplateFixture, auth.AccessScope) {
	t.Helper()
	exams := exam.NewMemoryStore()
	current, err := exams.CreateExam(context.Background(), auth.AccessScope{TenantID: "tenant-a", ActorID: "scanner", TenantWide: true}, "admin", exam.CreateInput{Name: "高一英语月考", Subject: "english"})
	if err != nil {
		t.Fatal(err)
	}
	reader := &scanTemplateFixture{
		binding: paper.ExamTemplateBinding{TenantID: "tenant-a", ExamID: current.ID, TemplateID: "template-locked", TemplateContentHash: "hash-current", Revision: 2, Mode: "locked_with_guard"},
		templates: []paper.AnswerSheetTemplate{
			{ID: "draft-hidden", TenantID: "tenant-a", ExamID: current.ID, Name: "不要泄露的草稿", Status: "draft"},
			{ID: "template-locked", TenantID: "tenant-a", ExamID: current.ID, Name: "英语答题卡", Status: "locked", ContentHash: "hash-current", VersionNo: 3, PageCount: 1, Layout: paper.TemplateLayout{Pages: []paper.TemplatePage{{PageNo: 1, Width: 1000, Height: 1400, QuestionRegions: []paper.LayoutRegion{{ID: "region-1", QuestionID: "private-question", Label: "作文区", X: 30, Y: 90, Width: 400, Height: 300}}}}}},
		},
	}
	batch := Batch{ID: "batch-a", TenantID: "tenant-a", ExamID: current.ID, Name: "教务处上午扫描", Status: "uploading"}
	return NewHandler(scanBatchFixture{batch: batch}, nil, exams, nil, nil).WithScanTemplates(reader), reader, auth.AccessScope{TenantID: "tenant-a", ActorID: "scanner", ExamIDs: []string{current.ID}}
}
func requestScanContext(h *Handler, scope auth.AccessScope, permissions []string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/capture-batches/batch-a/scan-context", nil)
	r.SetPathValue("id", "batch-a")
	r = r.WithContext(auth.WithAccessScope(auth.WithUser(r.Context(), auth.User{ID: "scanner", TenantID: scope.TenantID, Permissions: permissions}), scope))
	w := httptest.NewRecorder()
	auth.RequirePermission("capture:manage")(http.HandlerFunc(h.GetScanContext)).ServeHTTP(w, r)
	return w
}

func TestScanContextUsesOnlyAuthorizedLockedVersionWithoutExamManage(t *testing.T) {
	h, _, scope := newScanContextFixture(t)
	w := requestScanContext(h, scope, []string{"capture:manage"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var out ScanContext
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Exam.Name != "高一英语月考" || out.Batch.Name != "教务处上午扫描" || out.TemplateStatus != "ready" || out.Template == nil || out.Template.VersionNo != 3 || out.Template.BindingRevision != 2 {
		t.Fatalf("wrong current context: %#v", out)
	}
	for _, forbidden := range []string{"draft-hidden", "不要泄露的草稿", "private-question", "created_by", "omr_profile"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("unexpected projection %s: %s", forbidden, w.Body.String())
		}
	}
	if len(out.Template.Layout.Pages[0].IdentityRegions) != 0 || out.Template.Layout.Pages[0].QuestionRegions[0].Label != "作文区" {
		t.Fatalf("wrong preview: %#v", out.Template.Layout)
	}
}

func TestScanContextKeepsReadFailureSeparateFromMissingBinding(t *testing.T) {
	for _, tc := range []struct {
		name                string
		bindingErr, listErr error
		status              int
		expected            string
	}{
		{"unbound", paper.ErrNotFound, nil, http.StatusOK, `"template_status":"missing"`},
		{"binding read failed", errors.New("db offline"), nil, http.StatusServiceUnavailable, "capture_scan_context_unavailable"},
		{"template read failed", nil, errors.New("db offline"), http.StatusServiceUnavailable, "capture_scan_context_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, reader, scope := newScanContextFixture(t)
			reader.bindingErr, reader.listErr = tc.bindingErr, tc.listErr
			w := requestScanContext(h, scope, []string{"capture:manage"})
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.expected) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestScanContextRejectsScopeAndTemplateVersionMismatch(t *testing.T) {
	for _, tc := range []struct {
		name           string
		change         func(*scanTemplateFixture, *auth.AccessScope)
		expectedStatus int
		expected       string
	}{
		{"outside exam", func(_ *scanTemplateFixture, scope *auth.AccessScope) { scope.ExamIDs = []string{"other-exam"} }, http.StatusNotFound, "exam_not_found"},
		{"other tenant", func(_ *scanTemplateFixture, scope *auth.AccessScope) { scope.TenantID = "tenant-b" }, http.StatusNotFound, "capture_not_found"},
		{"hash changed", func(reader *scanTemplateFixture, _ *auth.AccessScope) {
			reader.templates[1].ContentHash = "another-hash"
		}, http.StatusOK, "template_version_mismatch"},
		{"template not locked", func(reader *scanTemplateFixture, _ *auth.AccessScope) { reader.templates[1].Status = "draft" }, http.StatusOK, "template_version_mismatch"},
		{"binding changed during read", func(reader *scanTemplateFixture, _ *auth.AccessScope) { reader.changed = true }, http.StatusOK, "binding_changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, reader, scope := newScanContextFixture(t)
			tc.change(reader, &scope)
			w := requestScanContext(h, scope, []string{"capture:manage"})
			if w.Code != tc.expectedStatus || !strings.Contains(w.Body.String(), tc.expected) || strings.Contains(w.Body.String(), `"template":`) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	h, _, scope := newScanContextFixture(t)
	w := requestScanContext(h, scope, []string{"exam:manage"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("configuration role without capture permission: %d %s", w.Code, w.Body.String())
	}
}

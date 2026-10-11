package capture

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type scanContextBatch49Reader struct {
	*scanTemplateFixture
	secondErr error
	change    func(*paper.ExamTemplateBinding)
}

func (s scanContextBatch49Reader) GetExamTemplateBinding(ctx context.Context, tenantID, examID string) (paper.ExamTemplateBinding, error) {
	binding, err := s.scanTemplateFixture.GetExamTemplateBinding(ctx, tenantID, examID)
	if err != nil || s.reads != 2 {
		return binding, err
	}
	if s.secondErr != nil {
		return paper.ExamTemplateBinding{}, s.secondErr
	}
	if s.change != nil {
		s.change(&binding)
	}
	return binding, nil
}

func TestBatch49ScanContextRejectsSecondBindingReadChanges(t *testing.T) {
	for _, tc := range []struct {
		name      string
		secondErr error
		change    func(*paper.ExamTemplateBinding)
		status    int
	}{
		{"binding disappeared", paper.ErrNotFound, nil, http.StatusOK},
		{"binding read failed", errors.New("second read failed"), nil, http.StatusServiceUnavailable},
		{"tenant changed", nil, func(b *paper.ExamTemplateBinding) { b.TenantID = "other" }, http.StatusOK},
		{"exam changed", nil, func(b *paper.ExamTemplateBinding) { b.ExamID = "other" }, http.StatusOK},
		{"mode changed", nil, func(b *paper.ExamTemplateBinding) { b.Mode = "other" }, http.StatusOK},
		{"template changed", nil, func(b *paper.ExamTemplateBinding) { b.TemplateID = "other" }, http.StatusOK},
		{"hash changed", nil, func(b *paper.ExamTemplateBinding) { b.TemplateContentHash = "other" }, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, reader, scope := newScanContextFixture(t)
			h.WithScanTemplates(scanContextBatch49Reader{reader, tc.secondErr, tc.change})
			w := requestScanContext(h, scope, []string{"capture:manage"})
			if w.Code != tc.status || reader.reads != 2 {
				t.Fatalf("status=%d reads=%d body=%s", w.Code, reader.reads, w.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			var out ScanContext
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.TemplateStatus != "invalid" || out.TemplateIssue != "binding_changed" || out.Template != nil {
				t.Fatalf("stale template projected: %+v", out)
			}
		})
	}
}

func TestBatch49ScanContextNeverReturnsPartialInvalidLayout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*paper.AnswerSheetTemplate)
	}{
		{"zero page count", func(p *paper.AnswerSheetTemplate) { p.PageCount = 0 }},
		{"missing page", func(p *paper.AnswerSheetTemplate) { p.PageCount = 2 }},
		{"later invalid width", func(p *paper.AnswerSheetTemplate) {
			p.PageCount = 2
			p.Layout.Pages = append(p.Layout.Pages, paper.TemplatePage{PageNo: 2, Width: 0, Height: 1400})
		}},
		{"invalid height", func(p *paper.AnswerSheetTemplate) { p.Layout.Pages[0].Height = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, reader, scope := newScanContextFixture(t)
			tc.change(&reader.templates[1])
			w := requestScanContext(h, scope, []string{"capture:manage"})
			var out ScanContext
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || out.TemplateStatus != "invalid" || out.TemplateIssue != "template_layout_invalid" || out.Template != nil {
				t.Fatalf("partial layout projected: status=%d %+v", w.Code, out)
			}
		})
	}
}

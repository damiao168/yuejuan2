package paper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type templateLibraryMatcherFunc func(context.Context, TemplateLibraryMatchSources) (TemplateLibraryVisualMatch, error)

func (f templateLibraryMatcherFunc) MatchTemplateLibrary(ctx context.Context, sources TemplateLibraryMatchSources) (TemplateLibraryVisualMatch, error) {
	return f(ctx, sources)
}

func TestTemplateLibraryVisualResolveValidatesCandidatesAndNeverCreatesOnFailure(t *testing.T) {
	for _, caseName := range []string{"matched", "weak candidate", "unknown", "foreign id", "duplicate ids", "invalid state", "failure", "truncated candidates", "not configured", "source changed during match"} {
		t.Run(caseName, func(t *testing.T) {
			s, scope, source := templateLibraryFixture(t)
			item := publishLibraryFixture(t, s, scope, source)
			p := s.papers[source.ExamPaperID]
			p.ID = "uploaded"
			p.ExamID = "next-exam"
			p.FileAssetID = "uploaded-file"
			p.File.HashSHA256 = strings.Repeat("b", 64)
			s.papers[p.ID] = p
			if caseName == "truncated candidates" {
				for i := 1; i <= TemplateLibraryMatchCandidateLimit; i++ {
					copy := copyLibraryValue(item)
					copy.ID = fmt.Sprintf("candidate-%d", i)
					s.templateLibrary[copy.ID] = copy
					s.librarySources[copy.ID] = s.librarySources[item.ID]
				}
			}
			calls := 0
			h := NewHandler(s, nil).WithTemplateLibraryVisualMatcher(templateLibraryMatcherFunc(func(_ context.Context, sources TemplateLibraryMatchSources) (TemplateLibraryVisualMatch, error) {
				calls++
				if !sources.Complete || len(sources.Candidates) != 1 || sources.Source.SourceSHA256 == sources.Candidates[0].SourceSHA256 {
					t.Fatal("matcher received incomplete or wrong sources")
				}
				data, _ := json.Marshal(sources)
				if strings.Contains(string(data), "storage_key") || strings.Contains(string(data), "blank.png") {
					t.Fatal("internal asset metadata can serialize into HTTP")
				}
				switch caseName {
				case "source changed during match":
					s.mu.Lock()
					changed := s.papers[p.ID]
					changed.File.HashSHA256 = strings.Repeat("c", 64)
					s.papers[p.ID] = changed
					s.mu.Unlock()
				case "weak candidate":
					return TemplateLibraryVisualMatch{Status: "ambiguous", LibraryIDs: []string{item.ID}}, nil
				case "unknown":
					return TemplateLibraryVisualMatch{Status: "unknown"}, nil
				case "foreign id":
					return TemplateLibraryVisualMatch{Status: "matched", LibraryIDs: []string{"another-school-id"}}, nil
				case "duplicate ids":
					return TemplateLibraryVisualMatch{Status: "ambiguous", LibraryIDs: []string{item.ID, item.ID}}, nil
				case "invalid state":
					return TemplateLibraryVisualMatch{Status: "known", LibraryIDs: []string{item.ID}}, nil
				case "failure":
					return TemplateLibraryVisualMatch{}, errors.New("worker unavailable")
				}
				return TemplateLibraryVisualMatch{Status: "matched", LibraryIDs: []string{item.ID}}, nil
			}))
			if caseName == "not configured" {
				h.WithTemplateLibraryVisualMatcher(nil)
			}
			body, _ := json.Marshal(ResolveTemplateLibraryInput{ExamPaperID: p.ID})
			request := httptest.NewRequest(http.MethodPost, "/resolve", bytes.NewReader(body))
			request.SetPathValue("examId", "next-exam")
			request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), auth.User{TenantID: scope.TenantID, ID: scope.ActorID}), scope))
			response := httptest.NewRecorder()
			h.ResolveTemplateLibrary(response, request)
			expected := http.StatusOK
			switch caseName {
			case "foreign id", "duplicate ids", "invalid state":
				expected = http.StatusBadGateway
			case "failure", "not configured":
				expected = http.StatusServiceUnavailable
			case "truncated candidates", "source changed during match":
				expected = http.StatusConflict
			}
			if response.Code != expected {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if caseName == "matched" {
				var resolution TemplateLibraryResolution
				if err := json.Unmarshal(response.Body.Bytes(), &resolution); err != nil || resolution.Status != "matched" || resolution.Instance == nil || resolution.Instance.Template.Status != "draft" || resolution.Instance.Paper.ID == p.ID {
					t.Fatalf("visual reuse failed: %s %v", response.Body.String(), err)
				}
				if len(s.templates) != 2 || len(s.libraryInstances) != 1 || len(s.bindings) != 0 {
					t.Fatal("visual match did not create one unbound draft")
				}
			} else if len(s.templates) != 1 || len(s.libraryInstances) != 0 || len(s.papers) != 2 {
				t.Fatalf("non-match created exam resources: %s", response.Body.String())
			}
			if caseName == "weak candidate" && !strings.Contains(response.Body.String(), `"status":"ambiguous"`) {
				t.Fatal("weak match became unknown")
			}
			if caseName == "truncated candidates" && calls != 0 {
				t.Fatal("matcher ran on incomplete search")
			}
		})
	}
}

func TestTemplateLibraryMatchSourcesFiltersIncompatibleQuestionTypes(t *testing.T) {
	s, scope, source := templateLibraryFixture(t)
	publishLibraryFixture(t, s, scope, source)
	q := s.questions["next-exam-q"]
	q.QuestionType = "essay"
	s.questions[q.ID] = q
	p := s.papers[source.ExamPaperID]
	p.ID = "uploaded"
	p.ExamID = "next-exam"
	s.papers[p.ID] = p
	sources, err := s.TemplateLibraryMatchSources(t.Context(), scope, "next-exam", ResolveTemplateLibraryInput{ExamPaperID: p.ID})
	if err != nil || !sources.Complete || len(sources.Candidates) != 0 {
		t.Fatalf("incompatible candidate leaked: %#v %v", sources, err)
	}
}

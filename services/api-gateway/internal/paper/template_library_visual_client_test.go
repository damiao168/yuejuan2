package paper

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

// 显式启动测试 AI 服务后运行，跨 Go/Python HTTP 验证实际 OpenCV 协议；不调用外部模型。
func TestTemplateLibraryVisualClientAgainstRunningCV(t *testing.T) {
	baseURL := os.Getenv("YUEJUAN_TEMPLATE_AI_TEST_URL")
	if baseURL == "" {
		t.Skip("YUEJUAN_TEMPLATE_AI_TEST_URL is not configured")
	}
	path := os.Getenv("YUEJUAN_TEMPLATE_AI_TEST_IMAGE")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	objects := files.NewMemoryObjectStorage()
	if err := objects.Put(t.Context(), "test", "blank", bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		t.Fatal(err)
	}
	document := TemplateLibraryMatchDocument{LibraryID: "reference", PageCount: 1, SourceSHA256: digest, File: FileAssetInput{ContentType: "image/png", SizeBytes: int64(len(data)), HashSHA256: digest, StorageBucket: "test", StorageKey: "blank"}}
	service := NewDocumentImportService(NewMemoryStore(), nil, nil, baseURL, os.Getenv("YUEJUAN_TEMPLATE_AI_TEST_TOKEN"), 10*time.Second)
	matcher := NewTemplateLibraryVisualMatcher(service, objects)
	if matcher == nil {
		t.Fatal("matcher not configured")
	}
	source := document
	source.LibraryID = ""
	source.PageCount = 0
	result, err := matcher.MatchTemplateLibrary(t.Context(), TemplateLibraryMatchSources{Source: source, Candidates: []TemplateLibraryMatchDocument{document}, Complete: true})
	if err != nil || result.Status != "matched" || len(result.LibraryIDs) != 1 || result.LibraryIDs[0] != "reference" {
		t.Fatalf("real CV transport failed: %#v %v", result, err)
	}
	input := testPageAnalysisInput(t)
	input.DataBase64 = base64.StdEncoding.EncodeToString(data)
	input.SHA256 = digest
	input.Width = 800
	input.Height = 1100
	analysis, err := service.requestTemplatePageAnalysis(t.Context(), input, []string{"17"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTemplatePageAnalysisResult(analysis, input, []string{"17"}); err != nil || analysis.SemanticStatus != "not_configured" || len(analysis.Candidates) == 0 {
		t.Fatalf("real geometry suggestions failed: %#v %v", analysis, err)
	}
}

func TestTemplateLibraryVisualClientUsesVerifiedObjectsAndCurrentCandidateIDs(t *testing.T) {
	for _, scenario := range []string{"matched", "unknown", "ambiguous", "unsupported", "forged selection", "forged candidate", "wrong schema", "source checksum", "source missing", "search incomplete"} {
		t.Run(scenario, func(t *testing.T) {
			objects := files.NewMemoryObjectStorage()
			data := []byte("synthetic image data; transport fixture only")
			if err := objects.Put(t.Context(), "templates", "reference", bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
				t.Fatal(err)
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(data))
			metadata := FileAssetInput{ContentType: "image/png", SizeBytes: int64(len(data)), HashSHA256: digest, StorageBucket: "templates", StorageKey: "reference"}
			sources := TemplateLibraryMatchSources{Complete: true,
				Source:     TemplateLibraryMatchDocument{SourceSHA256: digest, File: metadata},
				Candidates: []TemplateLibraryMatchDocument{{LibraryID: "library", PageCount: 1, SourceSHA256: digest, File: metadata}},
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/answer-sheet/match" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 32) {
					t.Error("incorrect internal operation or auth")
				}
				var request struct {
					RequestID  string                     `json:"request_id"`
					Source     libraryMatchWireDocument   `json:"source"`
					Candidates []libraryMatchWireDocument `json:"candidates"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Candidates) != 1 || request.Candidates[0].ExpectedPageCount != 1 || request.Source.SHA256 != digest {
					t.Error("reference identity or page count missing")
				}
				status, selected, id := scenario, any(nil), "library"
				if scenario == "matched" {
					selected = "library"
				}
				if scenario == "forged selection" {
					status, selected = "matched", "outside-library"
				}
				if scenario == "forged candidate" {
					status, id = "ambiguous", "outside-library"
				}
				if scenario == "wrong schema" {
					status = "matched"
					selected = "library"
				}
				out := map[string]any{"request_id": request.RequestID, "schema_version": "answer-sheet-match-v1", "status": status, "selected_id": selected,
					"candidates": []any{map[string]any{"id": id, "score": .9, "reason_codes": []string{}}}}
				if scenario == "wrong schema" {
					out["schema_version"] = "other-v1"
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer server.Close()
			service := NewDocumentImportService(NewMemoryStore(), nil, nil, server.URL, strings.Repeat("k", 32), time.Second)
			matcher := NewTemplateLibraryVisualMatcher(service, objects)
			if scenario == "source checksum" {
				sources.Source.SourceSHA256 = strings.Repeat("f", 64)
			}
			if scenario == "source missing" {
				sources.Source.File.StorageKey = "missing"
			}
			if scenario == "search incomplete" {
				sources.Complete = false
			}
			out, err := matcher.MatchTemplateLibrary(t.Context(), sources)
			valid := scenario == "matched" || scenario == "unknown" || scenario == "ambiguous"
			if valid && (err != nil || out.Status != scenario) {
				t.Fatalf("match failed: %#v %v", out, err)
			}
			if !valid && err == nil {
				t.Fatalf("invalid result accepted: %#v", out)
			}
			if (scenario == "source checksum" || scenario == "source missing" || scenario == "search incomplete") && calls.Load() != 0 {
				t.Fatal("unverified input reached matcher")
			}
		})
	}
}

func TestLockTemplateAtReviewedRevisionDoesNotFreezeConcurrentEdits(t *testing.T) {
	store, scope, template := templateLibraryFixture(t)
	template.Status = "draft"
	store.templates[template.ID] = template
	if _, err := store.LockTemplateAtRevision(t.Context(), scope.TenantID, template.ID, scope.ActorID, template.Revision-1); err != ErrConflict {
		t.Fatalf("stale revision must conflict: %v", err)
	}
	if store.templates[template.ID].Status != "draft" {
		t.Fatal("stale lock mutated the draft")
	}
	locked, err := store.LockTemplateAtRevision(t.Context(), scope.TenantID, template.ID, scope.ActorID, template.Revision)
	if err != nil || locked.Status != "locked" || locked.Revision != template.Revision+1 {
		t.Fatalf("reviewed revision did not lock: %#v %v", locked, err)
	}
}

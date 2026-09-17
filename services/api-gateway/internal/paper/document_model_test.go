package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

func TestPaperParserLoadsOnlyVerifiedTenantPageImages(t *testing.T) {
	raw := []byte("synthetic-page-image")
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	fileStore := files.NewMemoryStore()
	objects := files.NewMemoryObjectStorage()
	if err := objects.Put(context.Background(), "paper-pages", "page-1.png", bytes.NewReader(raw), int64(len(raw)), "image/png"); err != nil {
		t.Fatal(err)
	}
	asset, err := fileStore.Create(context.Background(), files.CreateAssetInput{
		TenantID: "school-a", OwnerType: "paper_import_page", OwnerID: "import-1",
		OriginalName: "page-1.png", ContentType: "image/png", SizeBytes: int64(len(raw)),
		HashSHA256: digest, StorageBucket: "paper-pages", StorageKey: "page-1.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewDocumentImportService(nil, fileStore, objects, "http://unused", strings.Repeat("t", 32), time.Second)
	documents := []normalizedImportDocument{{SourceID: "source-1", FileAssetID: "original-file", DocumentIndex: 0, Content: "OCR"}}
	pages := []PaperImportDecodedPage{{SourceID: "source-1", DocumentIndex: 0, PageNo: 1, FileAssetID: asset.ID, SHA256: digest, Width: 1200, Height: 1800}}

	visual, err := service.loadDocumentVisualPages(context.Background(), "school-a", documents, pages)
	if err != nil {
		t.Fatal(err)
	}
	if len(visual) != 1 || visual[0].MediaType != "image/png" || visual[0].SHA256 != digest || visual[0].DataBase64 == "" {
		t.Fatalf("unexpected visual page: %#v", visual)
	}
	pages[0].SHA256 = strings.Repeat("0", 64)
	if _, err = service.loadDocumentVisualPages(context.Background(), "school-a", documents, pages); err == nil {
		t.Fatal("checksum mismatch must be rejected")
	}
	if _, err = service.loadDocumentVisualPages(context.Background(), "school-b", documents, []PaperImportDecodedPage{{SourceID: "source-1", DocumentIndex: 0, PageNo: 1, FileAssetID: asset.ID}}); err == nil {
		t.Fatal("cross-tenant page asset must be rejected")
	}
}

func TestPaperParserResolvesSchoolModelOnlyForInternalRequest(t *testing.T) {
	var received documentParseRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 32) {
			t.Error("missing internal service authentication")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"documents":[],"question_candidates":[],"answer_candidates":[],"solution_candidates":[],"rubric_candidates":[],"issues":[]}`))
	}))
	defer server.Close()
	service := NewDocumentImportService(nil, nil, nil, server.URL, strings.Repeat("t", 32), time.Second).
		WithModelResolver(func(_ context.Context, tenantID string) (*DocumentModelConfig, error) {
			if tenantID != "school-a" {
				t.Fatalf("wrong tenant: %s", tenantID)
			}
			return &DocumentModelConfig{AdapterType: "openai_compatible", BaseURL: "https://provider.test/v1", APIKey: "synthetic-secret", ModelName: "school-a-model", ModelVersion: "v1"}, nil
		})
	result, err := service.parseForTenant(context.Background(), "school-a", "request", "math", nil)
	if err != nil {
		t.Fatal(err)
	}
	if received.ManagedModel == nil || received.ManagedModel.ModelName != "school-a-model" {
		t.Fatal("school configuration was not passed to the parser")
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "synthetic-secret") {
		t.Fatal("model credential leaked into paper result")
	}
	service.WithModelResolver(func(context.Context, string) (*DocumentModelConfig, error) {
		return nil, errors.New("secret backend down")
	})
	if _, err = service.parseForTenant(context.Background(), "school-a", "request", "math", nil); err == nil {
		t.Fatal("invalid school configuration must not fall back to global model")
	}
}

func TestPaperParserStreamsExactProgressEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "application/x-ndjson") {
			t.Fatal("streaming parser media type was not requested")
		}
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.Write([]byte("{\"type\":\"progress\",\"progress\":{\"phase\":\"model_request\",\"route\":\"compact_model\",\"completed\":0,\"total\":2}}\n"))
		w.Write([]byte("{\"type\":\"progress\",\"progress\":{\"phase\":\"model_request\",\"route\":\"compact_model\",\"completed\":1,\"total\":2}}\n"))
		w.Write([]byte("{\"type\":\"result\",\"result\":{\"documents\":[],\"question_candidates\":[],\"answer_candidates\":[],\"solution_candidates\":[],\"rubric_candidates\":[],\"issues\":[]}}\n"))
	}))
	defer server.Close()
	service := NewDocumentImportService(nil, nil, nil, server.URL, strings.Repeat("t", 32), time.Second)
	events := make([]map[string]any, 0, 2)
	result, err := service.parseForTenantWithProgress(context.Background(), "school-a", "request", "math", nil, func(progress map[string]any) error {
		events = append(events, progress)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1]["completed"] != float64(1) || events[1]["total"] != float64(2) {
		t.Fatalf("stream progress was not preserved: %#v", events)
	}
	if len(result.QuestionCandidates) != 0 {
		t.Fatalf("unexpected parser result: %#v", result)
	}
}

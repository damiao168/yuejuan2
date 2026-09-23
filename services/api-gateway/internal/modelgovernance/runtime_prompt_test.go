package modelgovernance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPRuntimePromptSourceReadsAuthenticatedRuntimeSnapshot(t *testing.T) {
	const token = "synthetic-service-token-with-32-characters"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/grading/prompts/current" || r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"prompt":{"prompt_version":"subjective-v5","bundle_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","components":[{"key":"base","filename":"base.md","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content":"Common rules."},{"key":"structured","filename":"structured.md","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content":"Structured."},{"key":"role.primary","filename":"roles/primary.md","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content":"Primary."},{"key":"role.arbiter","filename":"roles/arbiter.md","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content":"Arbiter."},{"key":"stage.junior","filename":"stages/junior.md","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content":"Junior."},{"key":"stage.senior","filename":"stages/senior.md","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content":"Senior."},{"key":"subject.mathematics.calculation","filename":"subjects/math/calculation.md","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content":"Mathematics calculation."},{"key":"subject.ethics_politics.discussion","filename":"subjects/politics/discussion.md","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content":"Politics discussion."}],"activation_mode":"deployment_manifest","mutable_at_runtime":false}}`))
	}))
	defer server.Close()

	source := NewHTTPRuntimePromptSource(server.URL, token, time.Second)
	prompt, err := source.Current(context.Background())
	if err != nil {
		t.Fatalf("read runtime prompt: %v", err)
	}
	if prompt.PromptVersion != "subjective-v5" || len(prompt.Components) != 8 || prompt.Components[6].Key != "subject.mathematics.calculation" {
		t.Fatalf("unexpected prompt snapshot: %#v", prompt)
	}
}

func TestHTTPRuntimePromptSourceFailsClosedOnInvalidSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"prompt":{"prompt_version":"","bundle_sha256":"bad","components":[],"activation_mode":"deployment_manifest","mutable_at_runtime":false}}`))
	}))
	defer server.Close()

	source := NewHTTPRuntimePromptSource(server.URL, "synthetic-service-token-with-32-characters", time.Second)
	if _, err := source.Current(context.Background()); err == nil {
		t.Fatal("expected invalid runtime prompt to fail closed")
	}
}

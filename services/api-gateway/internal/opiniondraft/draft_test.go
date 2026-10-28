package opiniondraft

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrivateDraftCASIsolationAndTombstone(t *testing.T) {
	store := NewMemoryStore()
	draft := &Draft{Source: "release", SourceRevision: 3, Values: map[string]any{"reason": "敏感意见"}, ExpiresAt: time.Now().Add(time.Hour)}
	saved, err := store.Put(t.Context(), "tenant", "actor", "arbitration", "task", 0, draft)
	if err != nil || saved.Revision != 1 {
		t.Fatal(saved, err)
	}
	for _, identity := range [][2]string{{"other", "actor"}, {"tenant", "other"}} {
		got, _ := store.Get(t.Context(), identity[0], identity[1], "arbitration", "task")
		if got.Draft != nil {
			t.Fatal("cross identity draft leaked")
		}
	}
	if _, err = store.Put(t.Context(), "tenant", "actor", "arbitration", "task", 0, draft); err != ErrConflict {
		t.Fatal("stale page overwrite accepted")
	}
	if err = store.Commit(t.Context(), "tenant", "actor", "arbitration", "task", 3); err != nil {
		t.Fatal(err)
	}
	cleared, _ := store.Get(t.Context(), "tenant", "actor", "arbitration", "task")
	if cleared.Draft != nil || cleared.Revision != 2 {
		t.Fatal(cleared)
	}
	if _, err = store.Put(t.Context(), "tenant", "actor", "arbitration", "task", 1, draft); err != ErrConflict {
		t.Fatal("deleted draft resurrected")
	}
}

func TestPrivateDraftEndpointValidatesSourceFieldsAndTerminalAccess(t *testing.T) {
	store := NewMemoryStore()
	request := func(method, body, source string, revision int64, editable bool) (int, Record, string) {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1/arbitration-tasks/task/private-draft", strings.NewReader(body))
		req.SetPathValue("id", "task")
		req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "actor", TenantID: "tenant"}))
		rec := httptest.NewRecorder()
		Endpoint(rec, req, store, "arbitration", source, revision, editable)
		var result Record
		_ = json.Unmarshal(rec.Body.Bytes(), &result)
		return rec.Code, result, rec.Body.String()
	}
	valid := `{"source":"exam:question","source_revision":3,"expected_revision":0,"values":{"finalScore":0,"reason":"已核对原卷证据","studentFeedback":""}}`
	if status, _, body := request(http.MethodPut, valid, "other", 3, true); status != 409 {
		t.Fatalf("forged source=%d %s", status, body)
	}
	if status, _, body := request(http.MethodPut, strings.Replace(valid, `"reason":`, `"answer_text":`, 1), "exam:question", 3, true); status != 400 {
		t.Fatalf("extra field=%d %s", status, body)
	}
	if status, saved, body := request(http.MethodPut, valid, "exam:question", 3, true); status != 200 || saved.Source != "exam:question" || saved.SourceRevision != 3 || saved.Revision != 1 {
		t.Fatalf("save=%d %s", status, body)
	}
	if status, loaded, _ := request(http.MethodGet, "", "exam:question", 4, true); status != 200 || loaded.Draft == nil || loaded.Draft.SourceRevision != 3 || loaded.SourceRevision != 4 {
		t.Fatal(status, loaded)
	}
	if status, _, body := request(http.MethodGet, "", "exam:question", 4, false); status != 403 || strings.Contains(body, "已核对") {
		t.Fatalf("terminal=%d %s", status, body)
	}
	if status, loaded, _ := request(http.MethodGet, "", "changed-source", 4, true); status != 200 || loaded.Draft != nil {
		t.Fatal("changed source returned old secret")
	}
	if status, deleted, _ := request(http.MethodDelete, `{"expected_revision":1}`, "exam:question", 4, true); status != 200 || deleted.Revision != 2 || deleted.Draft != nil {
		t.Fatal(status, deleted)
	}
	if status, _, _ := request(http.MethodPut, valid, "exam:question", 3, true); status != 409 {
		t.Fatal("old expected0 resurrected deleted draft")
	}
}

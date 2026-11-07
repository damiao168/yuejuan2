package scorerelease

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func formalTestScore(value float64) *float64 { return &value }

func formalTestRows(t *testing.T, content []byte) []map[string]string {
	t.Helper()
	if !strings.HasPrefix(string(content), "\ufeff") {
		t.Fatal("CSV must use UTF-8 BOM")
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(content), "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	out := []map[string]string{}
	for _, row := range rows[1:] {
		cells := map[string]string{}
		for i, key := range rows[0] {
			cells[key] = row[i]
		}
		out = append(out, cells)
	}
	return out
}

func TestFormalCSVUsesPublishedFactsAndVisibility(t *testing.T) {
	subject := formalExportSubject{formalExportPin: formalExportPin{ExamID: "math", Subject: "mathematics", ReleaseID: "release-v1", Version: 1}, Policy: VisibilityPolicy{ShowExactRank: true, ShowCohortStatistics: true}}
	for i := 0; i < 10; i++ {
		score := float64(90 - i)
		if i == 1 {
			score = 90
		}
		if i == 9 {
			score = 0
		}
		subject.Entries = append(subject.Entries, formalExportEntry{StudentID: strconv.Itoa(i), StudentNo: fmt.Sprintf("%05d", i+1), Name: "学生", GradeID: "grade", GradeName: "高二", ClassID: "old-class", ClassName: "考试时班级", Score: formalTestScore(score), Max: formalTestScore(100)})
	}
	result, err := buildFormalCSV("考试", "本校", "actor", []formalExportSubject{subject}, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	rows := formalTestRows(t, result.Content)
	if rows[0]["学号"] != "00001" || rows[0]["数学·成绩"] != "90.00" || rows[0]["数学·发布版本ID"] != "release-v1" || rows[0]["考试班级"] != "考试时班级" {
		t.Fatalf("identity or published version lost: %#v", rows[0])
	}
	if rows[0]["数学·班级排名"] != "1" || rows[1]["数学·班级排名"] != "1" || rows[2]["数学·班级排名"] != "3" || rows[9]["数学·成绩"] != "0.00" || rows[0]["数学·班级统计人数"] != "10" {
		t.Fatalf("zero/tie/denominator: %#v", rows)
	}
	subject.Policy = VisibilityPolicy{}
	result, err = buildFormalCSV("考试", "本校", "actor", []formalExportSubject{subject}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rows = formalTestRows(t, result.Content)
	if rows[0]["数学·班级排名"] != "" || rows[0]["数学·班级平均分"] != "" || rows[0]["总分年级排名"] != "" {
		t.Fatal("export bypassed publication visibility")
	}
	subject.Policy = VisibilityPolicy{ShowExactRank: true, ShowCohortStatistics: true}
	subject.Entries = subject.Entries[:9]
	result, err = buildFormalCSV("考试", "本校", "actor", []formalExportSubject{subject}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rows = formalTestRows(t, result.Content)
	if rows[0]["数学·班级排名"] != "" || rows[0]["数学·班级平均分"] != "" || rows[0]["数学·班级统计人数"] != "" {
		t.Fatal("small cohort privacy threshold bypassed")
	}
}

func TestFormalCSVMissingReleaseItemsAreNotAbsentOrZero(t *testing.T) {
	entry := formalExportEntry{StudentID: "student", StudentNo: "00123", Name: "=1+1", GradeID: "g", GradeName: "高一", ClassID: "c", ClassName: "1班", Score: formalTestScore(98), Max: formalTestScore(100)}
	mathSubject := formalExportSubject{formalExportPin: formalExportPin{ExamID: "m", Subject: "mathematics", ReleaseID: "r1", Version: 1}, Entries: []formalExportEntry{entry}}
	entry.Score = nil
	entry.Max = nil
	english := formalExportSubject{formalExportPin: formalExportPin{ExamID: "e", Subject: "english", ReleaseID: "r2", Version: 2}, Entries: []formalExportEntry{entry}}
	result, err := buildFormalCSV("考试", "本校", "actor", []formalExportSubject{mathSubject, english}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	row := formalTestRows(t, result.Content)[0]
	if row["英语·状态"] != "该发布版本无成绩" || row["英语·成绩"] != "" || row["总分"] != "" || row["学号"] != "00123" || strings.HasPrefix(row["姓名"], "=") {
		t.Fatalf("unverified missing score or CSV formula: %#v", row)
	}
	english.Entries[0].ClassID = "different-class"
	if _, err = buildFormalCSV("考试", "本校", "actor", []formalExportSubject{mathSubject, english}, time.Now()); !errors.Is(err, errFormalExportIncomplete) {
		t.Fatalf("cross-subject historical identity conflict accepted: %v", err)
	}
}

type formalHandlerStore struct {
	Store
	result formalExportResult
	called bool
	err    error
}

func (s *formalHandlerStore) ExportCurrentPublishedCSV(_ context.Context, _ auth.AccessScope, _ string, _ time.Time) (formalExportResult, error) {
	s.called = true
	return s.result, s.err
}

type formalAuditSink struct {
	event auth.AuditEvent
	err   error
}

func (s *formalAuditSink) Audit(_ context.Context, event auth.AuditEvent) error {
	s.event = event
	return s.err
}

func TestFormalExportHandlerRequiresManagedScopeAndDurableAudit(t *testing.T) {
	tenant, actor, exam, school := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	user := auth.User{ID: actor, TenantID: tenant, Roles: []string{"school_admin"}, Permissions: []string{"score:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": school}}}
	scope, err := auth.ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	store := &formalHandlerStore{result: formalExportResult{Content: []byte("synthetic formal CSV"), RowCount: 1, Pins: []formalExportPin{{ExamID: exam, ReleaseID: "pinned-release", Version: 2}}}}
	sink := &formalAuditSink{}
	handler := NewHandler(NewService(store), sink)
	request := func() *http.Request {
		req := httptest.NewRequest("GET", "/", nil)
		req.SetPathValue("examId", exam)
		return req.WithContext(auth.WithAccessScope(auth.WithUser(req.Context(), user), scope))
	}
	response := httptest.NewRecorder()
	handler.ExportCurrentPublished(response, request())
	if response.Code != 200 || sink.event.Action != "score_release.formal_exported" || sink.event.TargetType != "exam" || sink.event.AfterValue["sha256"] != fmt.Sprintf("%x", sha256.Sum256(store.result.Content)) || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("missing export provenance: status=%d event=%#v", response.Code, sink.event)
	}
	sink.err = errors.New("synthetic audit outage")
	response = httptest.NewRecorder()
	handler.ExportCurrentPublished(response, request())
	if response.Code != 503 || strings.Contains(response.Body.String(), "synthetic formal CSV") {
		t.Fatal("file sent despite unavailable audit")
	}
	store.called = false
	req := request()
	req = req.WithContext(auth.WithAccessScope(req.Context(), auth.AccessScope{TenantID: tenant, ActorID: "another actor", TenantWide: true}))
	response = httptest.NewRecorder()
	handler.ExportCurrentPublished(response, req)
	if response.Code != 403 || store.called {
		t.Fatal("request actor mismatch passed export scope")
	}
	store.err = errFormalExportIncomplete
	sink.err = nil
	response = httptest.NewRecorder()
	handler.ExportCurrentPublished(response, request())
	if response.Code != 409 {
		t.Fatalf("unpublished sibling not blocked: %d", response.Code)
	}
}

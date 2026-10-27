package qualitydashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/graderdrift"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestOpenIncidentCountAndPaginationMemory(t *testing.T) {
	store := graderdrift.NewMemoryStore()
	checkDriftCountAndPages(t, store, func(tenant string, item graderdrift.Incident) {
		if _, err := store.CreateIncidentsIfMissing(t.Context(), tenant, []graderdrift.Incident{item}); err != nil {
			t.Fatal(err)
		}
	})
}

// 复制正式表类型，只在专用测试连接的随机schema写合成记录；执行真实仓储查询，未连接业务库。
func TestOpenIncidentCountAndPaginationPostgres(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { admin.Close() })
	schema := "drift_remediation_" + uuid.NewString()[:8]
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Error(err)
		}
	})
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { db.Close() })
	for _, table := range []string{"grading_quality_incident", "exam", "exam_class"} {
		if _, err := db.Exec(`CREATE TABLE ` + table + ` AS TABLE public.` + table + ` WITH NO DATA`); err != nil {
			t.Fatal(err)
		}
	}
	checkDriftCountAndPages(t, graderdrift.NewPostgresStore(db), func(tenant string, item graderdrift.Incident) { insertDriftFixture(t, db, tenant, item) })
}

func insertDriftFixture(t *testing.T, db *sql.DB, tenant string, item graderdrift.Incident) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO grading_quality_incident(id,tenant_id,exam_id,question_id,grader_id,source_window_id,incident_type,severity,metric_snapshot_json,affected_range_json,status,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,'{}','{}',$9,$10)`, item.ID, tenant, item.ExamID, item.QuestionID, item.GraderID, item.SourceWindowID, item.Type, item.Severity, item.Status, item.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
}

func checkDriftCountAndPages(t *testing.T, store graderdrift.Store, seed func(string, graderdrift.Incident)) {
	t.Helper()
	tenant, examID, questionID, actor := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	criticalID := ""
	for i := 0; i < 1101; i++ {
		item := graderdrift.Incident{ID: uuid.NewString(), ExamID: examID, QuestionID: questionID, GraderID: actor, SourceWindowID: uuid.NewString(), Type: graderdrift.IncidentSevereSeedFailure, Severity: graderdrift.SeverityWarning, Status: graderdrift.IncidentOpen, CreatedAt: now.Add(time.Duration(i) * time.Second)}
		if i == 0 {
			item.Severity = graderdrift.SeverityCritical
			criticalID = item.ID
		}
		if i > 500 {
			item.Status = graderdrift.IncidentResolved
		}
		seed(tenant, item)
	}
	seed(uuid.NewString(), graderdrift.Incident{ID: uuid.NewString(), ExamID: examID, QuestionID: questionID, GraderID: actor, SourceWindowID: uuid.NewString(), Type: graderdrift.IncidentSevereSeedFailure, Severity: graderdrift.SeverityCritical, Status: graderdrift.IncidentOpen, CreatedAt: now})
	service := graderdrift.NewService(store, nil)
	reader := NewDriftReader(service)
	summary, err := reader.GetDriftSummary(context.Background(), tenant, examID, questionID)
	if err != nil || summary.OpenCritical != 1 || summary.OpenWarnings != 500 || len(summary.Incidents) != 500 {
		t.Fatalf("complete counts=%#v err=%v", summary, err)
	}
	quality := NewService(Sources{Questions: staticQuestions{{ID: questionID, QuestionNo: "1", RiskTier: "R3"}}, Drift: reader})
	dashboard, err := quality.Get(context.Background(), tenant, examID)
	if err != nil || dashboard.Gate != GateBlocked {
		t.Fatalf("old critical escaped gate: %#v err=%v", dashboard, err)
	}
	handler := graderdrift.NewHandler(service, nil)
	page := func(cursor string) struct {
		Incidents []graderdrift.Incident `json:"incidents"`
		More      bool                   `json:"has_more"`
		Next      string                 `json:"next_cursor"`
	} {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/grading-quality-incidents?status=open&limit=500&exam_id="+examID+"&cursor="+url.QueryEscape(cursor), nil)
		r = r.WithContext(auth.WithAccessScope(auth.WithUser(r.Context(), auth.User{ID: actor, TenantID: tenant}), auth.AccessScope{TenantID: tenant, ActorID: actor, TenantWide: true}))
		w := httptest.NewRecorder()
		handler.ListIncidents(w, r)
		var response struct {
			Incidents []graderdrift.Incident `json:"incidents"`
			More      bool                   `json:"has_more"`
			Next      string                 `json:"next_cursor"`
		}
		if w.Code != 200 {
			t.Fatalf("list http=%d %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := page("")
	if len(first.Incidents) != 500 || !first.More || first.Next == "" {
		t.Fatalf("first page incomplete: len=%d more=%v", len(first.Incidents), first.More)
	}
	second := page(first.Next)
	if len(second.Incidents) != 1 || second.More || second.Incidents[0].ID != criticalID {
		t.Fatalf("critical older page missing: %#v", second)
	}
	if _, err := service.ResolveIncident(context.Background(), tenant, criticalID); err != nil {
		t.Fatal(err)
	}
	dashboard, err = quality.Get(context.Background(), tenant, examID)
	if err != nil || dashboard.Gate == GateBlocked {
		t.Fatalf("resolved critical still blocked: %#v err=%v", dashboard, err)
	}
}

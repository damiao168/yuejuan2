package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGradeSuccessorTablesRequireForcedTenantRLS(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000221_audit_grade_tenant_rls.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"question_grade", "submission_grade", "score_release", "score_release_item", "question_appeal", "exam_candidate_snapshot", "review_task", "arbitration_task", "answer_candidate", "ai_grade", "subjective_grading_run"} {
		if !strings.Contains(string(raw), "'"+table+"'") {
			t.Fatal("missing tenant protection", table)
		}
	}
	for _, rule := range []string{"ENABLE ROW LEVEL SECURITY", "FORCE ROW LEVEL SECURITY", "USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id))"} {
		if !strings.Contains(string(raw), rule) {
			t.Fatal("missing tenant rule", rule)
		}
	}
}

var newTenantTable = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)\s*\((.*?)\);`)

// 历史表逐项迁移，新表从本次基线开始必须显式给出隔离策略；登录前表的例外须在此附具体原因。
var tenantRLSExceptions = map[string]string{}

func missingTenantRLS(source string) []string {
	var missing []string
	upper := strings.ToUpper(source)
	for _, match := range newTenantTable.FindAllStringSubmatch(source, -1) {
		if !strings.Contains(strings.ToLower(match[2]), "tenant_id") || tenantRLSExceptions[strings.ToLower(match[1])] != "" {
			continue
		}
		table := strings.ToUpper(match[1])
		if !strings.Contains(upper, "ALTER TABLE "+table+" ENABLE ROW LEVEL SECURITY") || !strings.Contains(upper, "ALTER TABLE "+table+" FORCE ROW LEVEL SECURITY") || !regexp.MustCompile(`(?is)CREATE\s+POLICY\s+\w+\s+ON\s+`+regexp.QuoteMeta(table)+`\b`).MatchString(upper) {
			missing = append(missing, match[1])
		}
	}
	return missing
}

func TestNewTenantTablesDeclareRLSOrDocumentedException(t *testing.T) {
	paths, err := filepath.Glob("../../migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if filepath.Base(path) < "000221_" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if missing := missingTenantRLS(string(raw)); len(missing) > 0 {
			t.Fatalf("new tenant tables need explicit forced RLS or a documented login-before-tenant exception: %s %v", path, missing)
		}
	}
}

func TestTenantTableGateRejectsMissingOrPartialIsolation(t *testing.T) {
	base := `CREATE TABLE synthetic_grade (id UUID, tenant_id UUID);`
	for _, raw := range []string{base, base + ` ALTER TABLE synthetic_grade ENABLE ROW LEVEL SECURITY;`} {
		if len(missingTenantRLS(raw)) != 1 {
			t.Fatal("missing protection was accepted")
		}
	}
	complete := base + ` ALTER TABLE synthetic_grade ENABLE ROW LEVEL SECURITY; ALTER TABLE synthetic_grade FORCE ROW LEVEL SECURITY; CREATE POLICY tenant_scope ON synthetic_grade USING (edugrade_tenant_matches(tenant_id));`
	if missing := missingTenantRLS(complete); len(missing) != 0 {
		t.Fatal(missing)
	}
}

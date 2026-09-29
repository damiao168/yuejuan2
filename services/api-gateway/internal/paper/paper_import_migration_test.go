package paper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 此文件只检查迁移文本中的关键结构；不会执行 SQL，也不替代数据库升级与约束验证。
func TestPaperImportMigrationAddsSourcesCandidatesAndFormalProvenance(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000110_paper_import_sources_candidates.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS paper_import_source",
		"question_candidates jsonb",
		"answer_candidates jsonb",
		"solution_candidates jsonb",
		"structured_issues jsonb",
		"uq_paper_import_source_order_active",
		"WHERE deleted_at IS NULL",
		"CREATE TABLE IF NOT EXISTS question_solution",
		"paper_import_source_refs jsonb",
		"INSERT INTO paper_import_source",
		"answer_file_asset_id IS DISTINCT FROM paper_file_asset_id",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration is missing %q", required)
		}
	}
}

func TestPaperImportRubricCandidateMigrationExtendsRolesAndProvenance(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000116_paper_import_rubric_candidates.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"rubric_candidates jsonb NOT NULL DEFAULT '[]'::jsonb",
		"DROP CONSTRAINT IF EXISTS paper_import_source_role_hint_check",
		"DROP CONSTRAINT IF EXISTS paper_import_source_detected_role_check",
		"('auto','question','answer','solution','rubric','mixed','unknown')",
		"('question','answer','solution','rubric','mixed','unknown')",
		"question_rubric",
		"paper_import_candidate_id text",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("rubric candidate migration is missing %q", required)
		}
	}
}

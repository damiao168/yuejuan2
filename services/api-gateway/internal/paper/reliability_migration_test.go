package paper

import (
	"os"
	"strings"
	"testing"
)

func TestReliabilityMigrationDefinesVersionedRunProtocol(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000123_reliability_command_runs.sql")
	if err != nil {
		t.Fatal(err)
	}
	// 这里只检查迁移文本包含关键结构，不执行 SQL；真实约束和升级路径需由数据库集成验证。
	sql := string(raw)
	for _, required := range []string{"CREATE TABLE paper_import_run", "current_generation", "command_request_hash", "paper_import_run_id", "task_protocol_version", "protocol_superseded", "dispatch_lease_owner"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
}

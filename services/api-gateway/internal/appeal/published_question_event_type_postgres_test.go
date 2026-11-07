package appeal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 其他申诉 PostgreSQL 测试使用伪驱动，不执行数据库约束，000092 漏掉 review_opinion_submitted 因此一直没被发现。
// 本用例从源码收集存储层实际写入的事件类型，逐个写入复制了真实约束的临时表；新增事件类型时必须同步迁移约束。
func TestPublishedQuestionAppealEventTypesFitDatabaseCheck(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN required")
	}
	eventTypes := publishedQuestionAppealWrittenEventTypes(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	// LIKE 只复制列、默认值和 CHECK，不带外键、RLS 与触发器，因此不需要租户、申诉和用户夹具；临时表随连接关闭删除。
	if _, err = conn.Exec(ctx, `CREATE TEMP TABLE appeal_event_type_probe (LIKE question_appeal_event INCLUDING DEFAULTS INCLUDING CONSTRAINTS)`); err != nil {
		t.Fatal(err)
	}
	insert := func(eventType string) error {
		_, err := conn.Exec(ctx, `INSERT INTO appeal_event_type_probe (tenant_id, appeal_id, event_type, actor_id, payload)
VALUES (gen_random_uuid(), gen_random_uuid(), $1, gen_random_uuid(), '{}'::jsonb)`, eventType)
		return err
	}
	for _, eventType := range eventTypes {
		if err := insert(eventType); err != nil {
			t.Errorf("event type %q written by the appeal store is rejected by question_appeal_event: %v", eventType, err)
		}
	}
	// 反向核对约束仍然生效，避免迁移误删约束后本用例空跑通过。
	var pgErr *pgconn.PgError
	if err := insert("not_an_appeal_event"); !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("unknown event type must violate the check constraint, got %v", err)
	}
}

// publishedQuestionAppealWrittenEventTypes 只接受字面量事件类型；改成变量会让本用例无法核对，因此直接失败。
func publishedQuestionAppealWrittenEventTypes(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`[^\w]insertPublishedQuestionAppealEvent\(`)
	literal := regexp.MustCompile(`[^\w]insertPublishedQuestionAppealEvent\([^,]+,[^,]+,[^,]+,[^,]+,[^,]+,\s*"([a-z_]+)"`)
	seen := map[string]bool{}
	calls, literals := 0, 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		source := string(raw)
		calls += len(call.FindAllStringIndex(source, -1)) - strings.Count(source, "func insertPublishedQuestionAppealEvent(")
		for _, match := range literal.FindAllStringSubmatch(source, -1) {
			literals++
			seen[match[1]] = true
		}
	}
	if calls == 0 || calls != literals {
		t.Fatalf("found %d insertPublishedQuestionAppealEvent calls but %d literal event types; pass the event type as a string literal", calls, literals)
	}
	out := make([]string, 0, len(seen))
	for eventType := range seen {
		out = append(out, eventType)
	}
	sort.Strings(out)
	return out
}

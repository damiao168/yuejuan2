package reviewbookmark

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/config"
	database "edugrade-enterprise/services/api-gateway/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 本测试使用完整迁移和真实无 BYPASSRLS 登录；不会把 SQL mock 或跳过数据库当成通过。
func TestPostgresBookmarkConcurrencyAndTenantRLS(t *testing.T) {
	admin, runtimeDB, user, task := bookmarkIntegrationDB(t)
	ctx := auth.WithUser(context.Background(), user)
	store := NewPostgresStore(runtimeDB, NewPostgresSourceProvider(runtimeDB))
	type outcome struct {
		ack Acknowledgement
		err error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ack, err := store.Put(ctx, user, task, PutInput{Reason: "并发合成说明"})
			results <- outcome{ack, err}
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	var first Acknowledgement
	for result := range results {
		var conflict *RevisionConflict
		if result.err == nil {
			successes++
			first = result.ack
		} else if errors.As(result.err, &conflict) && conflict.Revision == 1 {
			conflicts++
		} else {
			t.Fatalf("concurrent create: %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("create successes=%d conflicts=%d", successes, conflicts)
	}
	var count int
	if err := admin.QueryRow(`SELECT count(*) FROM review_bookmark WHERE task_id=$1::uuid`, task).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unique target count=%d err=%v", count, err)
	}

	// 同命令的两个并发重试必须共享一次变更和同一回执，不产生第二次 revision。
	results = make(chan outcome, 2)
	same := commandreceipt.WithID(ctx, "bookmark-parallel-edit")
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ack, err := store.Put(same, user, task, PutInput{Reason: "同命令", ExpectedRevision: 1})
			results <- outcome{ack, err}
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result.err != nil || result.ack.Revision != 2 {
			t.Fatalf("concurrent replay: %+v %v", result.ack, result.err)
		}
	}
	if _, err := store.Put(same, user, task, PutInput{Reason: "改变载荷", ExpectedRevision: 1}); !errors.Is(err, commandreceipt.ErrConflict) {
		t.Fatalf("receipt conflict: %v", err)
	}
	if _, err := store.Delete(ctx, user, first.BookmarkID, DeleteInput{ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(same, user, task, PutInput{Reason: "同命令", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	state, err := store.State(ctx, user, task)
	if err != nil || state.Marked || state.Revision != 3 || state.Reason != "" {
		t.Fatalf("late receipt state: %+v %v", state, err)
	}
	if _, err = store.Put(ctx, user, task, PutInput{ExpectedRevision: 3}); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ctx, user, ListFilter{Limit: 1})
	if err != nil || list.TotalCount != 1 || len(list.Items) != 1 {
		t.Fatalf("SQL authorized list: %+v %v", list, err)
	}

	// 连接池在每次操作前重绑租户。缺少身份和错误租户直接 SQL 都不能看到书签。
	if err = runtimeDB.QueryRowContext(context.Background(), `SELECT count(*) FROM review_bookmark`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unscoped RLS count=%d err=%v", count, err)
	}
	other := user
	other.TenantID = uuid.NewString()
	otherCtx := auth.WithUser(context.Background(), other)
	if err = runtimeDB.QueryRowContext(otherCtx, `SELECT count(*) FROM review_bookmark`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross tenant RLS count=%d err=%v", count, err)
	}
	if err = runtimeDB.QueryRowContext(ctx, `SELECT count(*) FROM review_bookmark`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("tenant reset count=%d err=%v", count, err)
	}
	if _, err = runtimeDB.ExecContext(ctx, `DELETE FROM review_bookmark WHERE id=$1::uuid`, first.BookmarkID); err == nil {
		t.Fatal("runtime role was allowed to hard delete")
	}
	t.Run("keyset pagination and owner visibility", func(t *testing.T) {
		// 使用相同标记时间触发 ID 次排序，翻页不能遗漏或重复；源转派后重新过滤整个集合。
		for range 2 {
			segmentID, taskID, questionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
			if _, err := admin.Exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,stem,sort_order,status)
SELECT $2::uuid,tenant_id,exam_id,$2::text,'short_answer',5,'分页合成题',2,'draft' FROM review_task WHERE id=$1::uuid`, task, questionID); err != nil {
				t.Fatal(err)
			}
			if _, err := admin.Exec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status)
SELECT $2::uuid,a.tenant_id,a.submission_id,a.submission_page_id,$3::uuid,$3::text,a.bbox,a.source,a.status
FROM answer_segment a JOIN review_task t ON t.tenant_id=a.tenant_id AND t.answer_segment_id=a.id WHERE t.id=$1::uuid`, task, segmentID, questionID); err != nil {
				t.Fatal(err)
			}
			if _, err := admin.Exec(`INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,assigned_to,created_by)
SELECT $2::uuid,tenant_id,exam_id,$4::uuid,$4::text,$3::uuid,submission_id,'ANON-PAGE','manual_sample','assigned',0,assigned_to,created_by FROM review_task WHERE id=$1::uuid`, task, taskID, segmentID, questionID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Put(ctx, user, taskID, PutInput{}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := admin.Exec(`UPDATE review_bookmark SET marked_at='2026-10-18T08:00:00Z' WHERE tenant_id=$1::uuid AND owner_id=$2::uuid`, user.TenantID, user.ID); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		cursor := ""
		for page := 0; page < 3; page++ {
			list, err := store.List(ctx, user, ListFilter{Limit: 1, Cursor: cursor})
			if err != nil || list.TotalCount != 3 || len(list.Items) != 1 {
				t.Fatalf("page %d: %+v err=%v", page, list, err)
			}
			id := *list.Items[0].ID
			if seen[id] {
				t.Fatalf("duplicate page record %s", id)
			}
			seen[id] = true
			cursor = list.NextCursor
			if page == 0 {
				if _, err := store.List(ctx, user, ListFilter{Limit: 1, Cursor: cursor, Q: "different filter"}); !errors.Is(err, ErrInvalid) {
					t.Fatalf("cursor accepted changed filter: %v", err)
				}
			}
		}
		if cursor != "" || len(seen) != 3 {
			t.Fatalf("pagination did not terminate: cursor=%q seen=%d", cursor, len(seen))
		}
		otherUser := user
		if err := admin.QueryRow(`SELECT id::text FROM app_user WHERE tenant_id=$1::uuid AND username='teacher'`, user.TenantID).Scan(&otherUser.ID); err != nil {
			t.Fatal(err)
		}
		otherCtx := auth.WithUser(context.Background(), otherUser)
		privateList, err := store.List(otherCtx, otherUser, ListFilter{})
		if err != nil || privateList.TotalCount != 0 || len(privateList.Exams) != 0 || len(privateList.Questions) != 0 {
			t.Fatalf("other owner saw personal list: %+v %v", privateList, err)
		}
		if _, err := store.GetByID(otherCtx, otherUser, first.BookmarkID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("other owner read metadata: %v", err)
		}
		if _, err := store.Delete(otherCtx, otherUser, first.BookmarkID, DeleteInput{ExpectedRevision: 4}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("other owner deleted metadata: %v", err)
		}
		if _, err := admin.Exec(`UPDATE review_task SET assigned_to=$2::uuid WHERE id=$1::uuid`, task, otherUser.ID); err != nil {
			t.Fatal(err)
		}
		visible, err := store.List(ctx, user, ListFilter{})
		if err != nil || visible.TotalCount != 2 || len(visible.Items) != 2 {
			t.Fatalf("reassignment list: %+v %v", visible, err)
		}
	})
}

func bookmarkIntegrationDB(t *testing.T) (*sql.DB, *sql.DB, auth.User, string) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for PostgreSQL bookmark integration")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	admin := stdlib.OpenDB(*adminCfg)
	databaseName := "bookmark_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	roleName := "bookmark_role_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedDB := pgx.Identifier{databaseName}.Sanitize()
	quotedRole := pgx.Identifier{roleName}.Sanitize()
	if _, err = admin.Exec(`CREATE DATABASE ` + quotedDB); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	testCfg := cfg.Copy()
	testCfg.Database = databaseName
	db := stdlib.OpenDB(*testCfg)
	db.SetMaxOpenConns(6)
	var runtimeDB *sql.DB
	var closeRuntime func() error
	t.Cleanup(func() {
		if closeRuntime != nil {
			_ = closeRuntime()
		}
		_ = db.Close()
		_, _ = admin.Exec(`DROP DATABASE ` + quotedDB + ` WITH (FORCE)`)
		_, _ = admin.Exec(`DROP ROLE IF EXISTS ` + quotedRole)
		_ = admin.Close()
	})
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("migrations: %v", err)
	}
	sort.Strings(files)
	migrationCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, path := range files {
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.ExecContext(migrationCtx, string(raw)); e != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), e)
		}
	}
	user := auth.User{Status: "active", Permissions: []string{"review:work"}}
	if err = db.QueryRow(`SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.code='demo' AND u.username='grader'`).Scan(&user.TenantID, &user.ID); err != nil {
		t.Fatal(err)
	}
	school, exam, question, submission, asset, page, segment, task := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := db.Exec(query, args...); e != nil {
			t.Fatalf("fixture: %v", e)
		}
	}
	exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'复查并发合成学校',$1::text,'active')`, school, user.TenantID)
	exec(`UPDATE app_user SET school_id=$1::uuid,status='active' WHERE id=$2::uuid`, school, user.ID)
	exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'复查并发合成考试','physics','midterm',5,'draft','ai_assisted','manual_after_confirmation',$4::uuid)`, exam, user.TenantID, school, user.ID)
	exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,stem,sort_order,status) VALUES($1::uuid,$2::uuid,$3::uuid,'Q1','short_answer',5,'合成题干',1,'draft')`, question, user.TenantID, exam)
	exec(`INSERT INTO submission(id,tenant_id,exam_id,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,'manual_import','created',$4::uuid)`, submission, user.TenantID, exam, user.ID)
	exec(`INSERT INTO file_asset(id,tenant_id,school_id,exam_id,submission_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'submission_page',$5::uuid,'synthetic.png','image/png',1,$6,'bookmark-tests',$1::text,'private',$7::uuid)`, asset, user.TenantID, school, exam, submission, strings.Repeat("a", 64), user.ID)
	exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, page, user.TenantID, submission, asset)
	exec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'Q1','{"x":0.1,"y":0.1,"w":0.5,"h":0.5}','manual','accepted')`, segment, user.TenantID, submission, page, question)
	exec(`INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,assigned_to,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1',$5::uuid,$6::uuid,'ANON-CONCURRENCY','manual_sample','assigned',0,$7::uuid,$7::uuid)`, task, user.TenantID, exam, question, segment, submission, user.ID)
	exec(`CREATE ROLE ` + quotedRole + ` LOGIN NOSUPERUSER NOBYPASSRLS`)
	exec(`GRANT edugrade_tenant_runtime TO ` + quotedRole)
	// ConnString 保留原始 DSN，不反映 User/Database 字段修改。
	// 明确重写连接地址，测试池才能真正使用无 BYPASSRLS 的登录身份。
	runtimeURL, parseErr := url.Parse(dsn)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	runtimeURL.User = url.User(roleName)
	runtimeURL.Path = "/" + databaseName
	runtimeDB, closeRuntime, err = database.OpenPostgres(config.PostgresConfig{DSN: runtimeURL.String(), TenantRLSEnabled: true, MaxOpenConns: 6, MaxIdleConns: 2, StatementTimeout: 20 * time.Second, LockTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return db, runtimeDB, user, task
}

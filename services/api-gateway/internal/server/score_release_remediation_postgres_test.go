package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// 验证迁移真实冻结历史公开成绩，并覆盖已发布明细的新增、移出及并发写入。
func TestScoreReleaseSnapshotMigrationWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrationsThrough(t, db, "000229_multiple_anonymous_papers.sql")
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	f := e2eCreateStory056AcceptanceFixture(t, db, router, token, suffix, 2)
	firstSubmission := seedReleaseSnapshotSubmission(t, db, f, f.StudentIDs[0], "historical")
	secondSubmission := seedReleaseSnapshotSubmission(t, db, f, f.StudentIDs[1], "still-private")
	ctx := t.Context()
	publishedAt := time.Date(2025, 10, 10, 8, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `UPDATE submission_grade SET status='published',locked=true,published_by=$2,published_at=$3 WHERE submission_id=$1`,
		firstSubmission, f.AdminID, publishedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE exam SET archived_at=now(),archived_by=$2,archive_reason='historical fixture' WHERE id=$1`, f.ExamID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	e2eApplyPostgresMigrations(t, db)

	var migratedID, source string
	var migratedAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT release.id::text,release.source,release.published_at FROM score_release_current current
JOIN score_release release ON release.tenant_id=current.tenant_id AND release.id=current.release_id
WHERE current.tenant_id=$1 AND current.exam_id=$2`, f.TenantID, f.ExamID).Scan(&migratedID, &source, &migratedAt); err != nil {
		t.Fatal(err)
	}
	if source != "migration" || !migratedAt.Equal(publishedAt) {
		t.Fatalf("migration changed publication metadata: source=%q at=%v", source, migratedAt)
	}
	releaseStore := scorerelease.NewPostgresStore(db, nil)
	scores := score.NewPostgresStore(db).WithPublication(scorerelease.NewService(releaseStore), nil)
	legacy, err := scores.GetStudentGrade(ctx, f.TenantID, f.StudentIDs[0], f.ExamID)
	if err != nil || legacy.TotalScore != 3 || len(legacy.Items) != 3 || !legacy.PublishedAt.Equal(publishedAt) {
		t.Fatalf("migrated legacy result: %#v %v", legacy, err)
	}
	formal, err := releaseStore.StudentResult(ctx, f.TenantID, f.ExamID, f.StudentIDs[0])
	if err != nil || formal.ReleaseID != migratedID || formal.TotalScore != legacy.TotalScore || len(formal.Questions) != len(legacy.Items) {
		t.Fatalf("migrated formal result: %#v %v", formal, err)
	}
	if _, err := scores.GetStudentGrade(ctx, f.TenantID, f.StudentIDs[1], f.ExamID); !errors.Is(err, score.ErrNotFound) {
		t.Fatalf("migration exposed the second student's unconfirmed publication: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE exam SET archived_at=NULL,archived_by=NULL,archive_reason='' WHERE id=$1`, f.ExamID); err != nil {
		t.Fatal(err)
	}
	assertReleaseDetailsImmutable(t, db, f, migratedID, firstSubmission, secondSubmission)
}

func seedReleaseSnapshotSubmission(t *testing.T, db *sql.DB, f story056AcceptanceFixture, student, code string) string {
	t.Helper()
	var submission, page string
	if err := db.QueryRow(`INSERT INTO submission(tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by,identity_status)
VALUES($1,$2,$3,$4,'pdf_upload','quality_checked',1,1,'passed',$5,'matched') RETURNING id::text`,
		f.TenantID, f.ExamID, student, code, f.AdminID).Scan(&submission); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status,quality_status)
VALUES($1,$2,$3,1,'accepted','passed') RETURNING id::text`, f.TenantID, submission, f.PaperFileAssetID).Scan(&page); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`WITH segments AS (
  INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status)
  SELECT tenant_id,$3::uuid,$4::uuid,id,question_no,'{}','manual','accepted' FROM question WHERE tenant_id=$1 AND exam_id=$2
  RETURNING id,question_id,question_no
)
INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,status,created_by)
SELECT $1,$2,question_id,question_no,id,$3,$5,1,1,'single_review','confirmed',$6 FROM segments`,
		f.TenantID, f.ExamID, submission, page, code, f.AdminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO submission_grade(tenant_id,exam_id,submission_id,student_id,anonymous_code,total_score,max_score,status,confirmed_by,confirmed_at,created_by)
VALUES($1,$2,$3,$4,$5,3,3,'confirmed',$6,now(),$6)`, f.TenantID, f.ExamID, submission, student, code, f.AdminID); err != nil {
		t.Fatal(err)
	}
	return submission
}

func assertReleaseDetailsImmutable(t *testing.T, db *sql.DB, f story056AcceptanceFixture, published, firstSubmission, secondSubmission string) {
	t.Helper()
	var draft string
	if err := db.QueryRow(`INSERT INTO score_release(tenant_id,exam_id,version,source,reason,idempotency_key,created_by)
VALUES($1,$2,2,'initial','synthetic immutability fixture','synthetic-immutability',$3) RETURNING id::text`,
		f.TenantID, f.ExamID, f.AdminID).Scan(&draft); err != nil {
		t.Fatal(err)
	}
	itemInsert := `INSERT INTO score_release_item(tenant_id,release_id,student_id,submission_id,total_score,max_score,status,snapshot_hash)
SELECT tenant_id,$1::uuid,student_id,submission_id,total_score,max_score,'confirmed',repeat('a',64) FROM submission_grade WHERE submission_id=$2`
	questionInsert := `INSERT INTO score_release_question(tenant_id,release_id,submission_id,question_id,question_no,final_grade_id,score,max_score,source_type)
SELECT tenant_id,$1::uuid,submission_id,question_id,question_no,id,score,max_score,source FROM final_grade WHERE submission_id=$2`
	for _, query := range []string{itemInsert, questionInsert} {
		assertReleaseImmutableError(t, db, query, published, secondSubmission)
		if _, err := db.Exec(query, draft, secondSubmission); err != nil {
			t.Fatalf("draft detail insert: %v", err)
		}
	}
	for _, table := range []string{"score_release_item", "score_release_question"} {
		move := "UPDATE " + table + " SET release_id=$1 WHERE release_id=$2"
		assertReleaseImmutableError(t, db, move, draft, published)
		assertReleaseImmutableError(t, db, move, published, draft)
		assertReleaseImmutableError(t, db, "DELETE FROM "+table+" WHERE release_id=$1", published)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	publisher, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Rollback()
	if _, err := publisher.ExecContext(ctx, `UPDATE score_release SET status='published',published_by=$2,published_at=now() WHERE id=$1`, draft, f.AdminID); err != nil {
		t.Fatal(err)
	}
	// 写者在父发布行锁上等待，提交后要重新判断状态；仅检查语句开始时的 draft 会误写。
	result := make(chan error, 1)
	go func() {
		_, err := db.ExecContext(ctx, itemInsert, draft, firstSubmission)
		result <- err
	}()
	for {
		var waiting bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'INSERT INTO score_release_item%')`).Scan(&waiting); err != nil {
			t.Fatalf("detail writer did not wait for the publishing transaction: %v", err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("detail write finished before publication committed: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := publisher.Commit(); err != nil {
		t.Fatal(err)
	}
	assertReleaseSQLState(t, <-result)
}

func assertReleaseImmutableError(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), query, args...)
	assertReleaseSQLState(t, err)
}

func assertReleaseSQLState(t *testing.T, err error) {
	t.Helper()
	var postgresErr *pgconn.PgError
	if !errors.As(err, &postgresErr) || postgresErr.Code != "55000" || !strings.Contains(postgresErr.Message, "snapshot is immutable") {
		t.Fatalf("want published snapshot immutability error, got %v", err)
	}
}

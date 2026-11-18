package capture

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresMixedDuplicateUploadGroupCanExitAndReplace(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "exclude"
		if replace {
			name = "replace"
		}
		t.Run(name, func(t *testing.T) {
			ctx, db := captureRecoveryRemediationDB(t)
			// 全部读写都使用随机 schema 的空表；仅复制正式结构，不能回退查询公共业务表。
			for _, table := range []string{"exam", "capture_collection", "exam_answer_sheet_template_binding", "answer_sheet_template", "exam_candidate_snapshot", "submission_page", "capture_upload_session", "page_template_match_run", "answer_segment", "ai_grade", "human_grade", "question_grade", "final_grade", "subjective_grading_run", "scoring_run_item", "capture_operation", "submission_page_quality_run"} {
				if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
					t.Fatal(err)
				}
			}
			tenant, actor, exam := uuid.NewString(), uuid.NewString(), uuid.NewString()
			if _, err := db.ExecContext(ctx, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'Synthetic duplicate recovery','physics','',10,'collecting','human_review_required','manual_after_confirmation',$4)`, exam, tenant, uuid.NewString(), actor); err != nil {
				t.Fatal(err)
			}
			store := NewPostgresStore(db)
			batch, err := store.CreateBatch(ctx, tenant, exam, actor, CreateBatchInput{Name: "Synthetic duplicate group", SourceType: "web_upload"})
			if err != nil {
				t.Fatal(err)
			}
			register := func(group string, count, index int, hash string) File {
				t.Helper()
				asset := FileAssetSnapshot{ID: uuid.NewString(), SizeBytes: 1, SHA256: hash, OriginalName: "synthetic.pdf", ContentType: "application/pdf"}
				file, err := store.RegisterFile(ctx, tenant, batch.ID, actor, RegisterFileInput{FileAssetID: asset.ID, IdempotencyKey: uuid.NewString(), UploadGroupID: group, UploadGroupFileCount: count, UploadGroupFileIndex: index, PagesPerStudent: 2}, asset)
				if err != nil {
					t.Fatal(err)
				}
				return file
			}
			queue := func() {
				t.Helper()
				if _, err := store.QueueBatch(ctx, tenant, batch.ID, actor); err != nil {
					t.Fatal(err)
				}
			}
			decode := func(file File) {
				t.Helper()
				pages := []DecodedPageInput{}
				for page := 1; page <= 2; page++ {
					pages = append(pages, DecodedPageInput{SourceIndex: page, FileAssetID: uuid.NewString(), SHA256: strings.Repeat("c", 64), Width: 100, Height: 100})
				}
				if _, err := store.ApplyFileResult(ctx, tenant, file.ID, pages); err != nil {
					t.Fatal(err)
				}
			}
			mixedIssue := func(want bool) CollectionIssue {
				t.Helper()
				summary, err := store.GetCollectionSummary(ctx, tenant, exam)
				if err != nil {
					t.Fatal(err)
				}
				issues := []CollectionIssue{}
				for _, issue := range summary.Issues {
					if issue.Code == "upload_group_duplicate" {
						issues = append(issues, issue)
					}
				}
				if len(issues) != 0 && !want || len(issues) != 1 && want {
					t.Fatalf("duplicate group issues = %+v, want one=%v", issues, want)
				}
				if want {
					if issues[0].FileID == "" || issues[0].BatchID != batch.ID || issues[0].Kind != "batch" {
						t.Fatalf("issue cannot target the whole group: %+v", issues[0])
					}
					return issues[0]
				}
				return CollectionIssue{}
			}
			old := register(uuid.NewString(), 1, 1, strings.Repeat("a", 64))
			queue()
			decode(old)
			// 纯重复选择没有新材料，不能被误报成阻塞导入的混合组。
			register(uuid.NewString(), 1, 1, old.SHA256)
			mixedIssue(false)
			group := uuid.NewString()
			duplicate := register(group, 2, 1, old.SHA256)
			fresh := register(group, 2, 2, strings.Repeat("b", 64))
			if duplicate.Status != "duplicate" {
				t.Fatalf("duplicate file was processed again: %+v", duplicate)
			}
			issue := mixedIssue(true)
			queue()
			decode(fresh)
			// 查询也须发现历史已解码的等待组，不依赖本次注册/解码时写入一个新标记。
			mixedIssue(true)
			var waiting int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM capture_page WHERE capture_file_id=$1 AND status='decoded' AND page_identity->>'grouping_status'='waiting_files'`, fresh.ID).Scan(&waiting); err != nil || waiting != 2 {
				t.Fatalf("mixed group crossed file boundary: pages=%d err=%v", waiting, err)
			}
			input := ExcludeFileInput{Reason: "Synthetic whole-group recovery"}
			var replacement File
			if replace {
				// 仅重传未成功的完整两页答卷；登记时仍命中旧组，退出旧组后才释放重复状态。
				replacement = register(uuid.NewString(), 1, 1, fresh.SHA256)
				if replacement.Status != "duplicate" {
					t.Fatalf("replacement should wait for old group exclusion: %+v", replacement)
				}
				input.ReplacementFileID = replacement.ID
			}
			if _, err := store.ExcludeFile(ctx, tenant, issue.FileID, actor, input); err != nil {
				t.Fatal(err)
			}
			mixedIssue(false)
			var excluded, originalPages, cancelled, activeOrphans int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM capture_file WHERE upload_group_id=$1 AND deleted_at IS NOT NULL`, group).Scan(&excluded); err != nil || excluded != 2 {
				t.Fatalf("whole mixed group was not excluded: count=%d err=%v", excluded, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM capture_page WHERE capture_file_id=$1 AND status<>'deleted' AND deleted_at IS NULL`, old.ID).Scan(&originalPages); err != nil || originalPages != 2 {
				t.Fatalf("original successful group changed: pages=%d err=%v", originalPages, err)
			}
			if original, err := store.GetFile(ctx, tenant, old.ID); err != nil || original.Status != "completed" {
				t.Fatalf("original successful file changed: %+v err=%v", original, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM agent_worker_task WHERE source_type='capture_file' AND source_id=$1 AND status='cancelled'`, fresh.ID).Scan(&cancelled); err != nil || cancelled != 1 {
				t.Fatalf("old decode task can still reactivate input: tasks=%d err=%v", cancelled, err)
			}
			if _, err := store.ApplyFileResult(ctx, tenant, fresh.ID, []DecodedPageInput{{SourceIndex: 1, FileAssetID: uuid.NewString(), SHA256: strings.Repeat("d", 64), Width: 100, Height: 100}}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("late decode result reactivated the excluded group: %v", err)
			}
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM submission s WHERE s.deleted_at IS NULL AND EXISTS(SELECT 1 FROM capture_page p WHERE p.submission_id=s.id AND p.capture_file_id=$1)`, fresh.ID).Scan(&activeOrphans); err != nil || activeOrphans != 0 {
				t.Fatalf("excluded group left an active submission: count=%d err=%v", activeOrphans, err)
			}
			if !replace {
				replacement = register(uuid.NewString(), 1, 1, fresh.SHA256)
			}
			if replacement, err = store.GetFile(ctx, tenant, replacement.ID); err != nil || replacement.Status != "uploaded" {
				t.Fatalf("fresh full answer is still duplicate: %+v err=%v", replacement, err)
			}
			queue()
			decode(replacement)
			pages, err := store.ListPages(ctx, tenant, batch.ID)
			if err != nil {
				t.Fatal(err)
			}
			pageNumbers, submissions := map[int]bool{}, map[string]bool{}
			for _, page := range pages {
				if page.CaptureFileID == replacement.ID {
					if page.Status != "quality_checking" || page.PageIdentity["grouping_status"] != "complete" {
						t.Fatalf("replacement did not reach quality processing: %+v", page)
					}
					pageNumbers[page.AssignedPageNo], submissions[page.SubmissionID] = true, true
				}
			}
			if len(submissions) != 1 || len(pageNumbers) != 2 || !pageNumbers[1] || !pageNumbers[2] {
				t.Fatalf("replacement lost its two-page boundary: submissions=%v pages=%v", submissions, pageNumbers)
			}
			mixedIssue(false)
		})
	}
}

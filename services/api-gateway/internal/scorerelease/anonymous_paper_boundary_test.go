package scorerelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

func TestStudentSelectedAnonymousPageUsesOwnCurrentRelease(t *testing.T) {
	readFailure := errors.New("current release read failed")
	const ownRelease = "AND mine.release_id=r.id AND mine.student_id=$3::uuid"
	for _, tc := range []struct {
		name, expected string
		row            []driver.Value
		readErr        error
		want           StudentQuestionImageSource
		wantErr        error
	}{
		{name: "本人当前版本的指定匿名卷", expected: "v2", row: []driver.Value{"v2", "anonymous-page", "anonymous-asset"},
			want: StudentQuestionImageSource{ReleaseID: "v2", AnonymousPageID: "anonymous-page", AnonymousFileAssetID: "anonymous-asset"}},
		{name: "无本人成绩时不暴露发布版本", expected: "v2", wantErr: ErrNotFound},
		{name: "换版时优先返回版本冲突", expected: "v1", row: []driver.Value{"v2", "", ""}, wantErr: ErrStudentReleaseChanged},
		{name: "同一版本中的匿名页已不可用", expected: "v2", row: []driver.Value{"v2", "", ""}, wantErr: ErrNotFound},
		{name: "查询故障不降级为缺页", expected: "v2", readErr: readFailure, wantErr: readFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := studentBatch50Step{
				fragment: ownRelease, args: []any{"school-tenant", "exam", "student", "question", "selected-paper"},
				columns: 3, err: tc.readErr,
			}
			if tc.row != nil {
				step.rows = [][]driver.Value{tc.row}
			}
			store := studentBatch50Store(t, []studentBatch50Step{step})
			got, err := store.StudentSelectedHighScorePaperPageImage(t.Context(), "school-tenant", "exam", "student", "question", "selected-paper", tc.expected)
			if !errors.Is(err, tc.wantErr) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("anonymous image = %+v, %v; want %+v, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestStudentHighScorePapersPreserveFrozenScoresWithoutSourceIdentity(t *testing.T) {
	scope := []any{"tenant", "release"}
	store := studentBatch50Store(t, []studentBatch50Step{
		{fragment: "accepted.total,shared.total", args: scope, columns: 6, rows: [][]driver.Value{
			{"private-submission-b", 19.0, 20.0, "anonymous-b", int64(2), int64(2)},
			{"private-submission-a", 17.0, 20.0, "anonymous-a", int64(1), int64(1)},
		}},
		{fragment: "ap.page_no,q.question_id::text", args: scope, columns: 3, rows: [][]driver.Value{
			{"private-submission-b", int64(1), "q1"}, {"private-submission-b", int64(2), "q2"},
			{"private-submission-a", int64(1), "q1"},
		}},
		{fragment: "SELECT DISTINCT ON(q.submission_id,q.question_id)", args: scope, columns: 7, rows: [][]driver.Value{
			{"private-submission-a", "q1", "01", 17.0, 20.0, int64(1), []byte(`{}`)},
			{"private-submission-b", "q2", "02", 9.0, 10.0, int64(2), []byte(`{"width":0.1,"height":0}`)},
			{"private-submission-b", "q1", "01", 10.0, 10.0, int64(1), []byte(`{"x":0.1,"y":0.2,"width":0.3,"height":0.4}`)},
		}},
	})
	// 批注无法定位时只省略坐标，已冻结的题目分和匿名页顺序仍保留。
	want := []StudentHighScorePaper{
		{PaperID: "anonymous-b", Available: true, TotalScore: 19, MaxScore: 20,
			Pages: []StudentPaperPage{{PageNo: 1, QuestionID: "q1"}, {PageNo: 2, QuestionID: "q2"}},
			ScoreMarks: []StudentPaperScoreMark{
				{QuestionID: "q1", QuestionNo: "01", Score: 10, MaxScore: 10, PageNo: 1, AnswerGeometry: &StudentImageGeometry{X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4}},
				{QuestionID: "q2", QuestionNo: "02", Score: 9, MaxScore: 10, PageNo: 2},
			}},
		{PaperID: "anonymous-a", Available: true, TotalScore: 17, MaxScore: 20,
			Pages:      []StudentPaperPage{{PageNo: 1, QuestionID: "q1"}},
			ScoreMarks: []StudentPaperScoreMark{{QuestionID: "q1", QuestionNo: "01", Score: 17, MaxScore: 20, PageNo: 1}}},
	}
	got, err := store.studentHighScorePapers(t.Context(), "tenant", "release")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("anonymous papers = %+v, %v; want %+v", got, err, want)
	}
}

func TestStudentHighScorePapersNeverReturnPartialCandidateSet(t *testing.T) {
	scope := []any{"tenant", "release"}
	candidate := studentBatch50Step{fragment: "accepted.total,shared.total", args: scope, columns: 6, rows: [][]driver.Value{
		{"private-submission", 19.0, 20.0, "anonymous-paper", int64(1), int64(1)},
	}}
	page := studentBatch50Step{fragment: "ap.page_no,q.question_id::text", args: scope, columns: 3, rows: [][]driver.Value{
		{"private-submission", int64(1), "question"},
	}}
	mark := studentBatch50Step{fragment: "SELECT DISTINCT ON(q.submission_id,q.question_id)", args: scope, columns: 7}
	failure := errors.New("release snapshot query failed")
	incomplete := candidate
	incomplete.rows = append(append([][]driver.Value(nil), candidate.rows...),
		[]driver.Value{"missing-page", 18.0, 20.0, "other-anonymous-paper", int64(2), int64(1)})
	noPages := page
	noPages.rows = nil
	for _, tc := range []struct {
		name    string
		prefix  []studentBatch50Step
		last    studentBatch50Step
		wantErr error
	}{
		{name: "任一候选卷缺少匿名页则隐藏整个候选集合", last: incomplete},
		{name: "匿名页无法对应题目则隐藏整个集合", prefix: []studentBatch50Step{candidate}, last: noPages},
		{name: "候选查询错误", last: candidate, wantErr: failure},
		{name: "页查询错误", prefix: []studentBatch50Step{candidate}, last: page, wantErr: failure},
		{name: "分项查询错误", prefix: []studentBatch50Step{candidate, page}, last: mark, wantErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.last.err = tc.wantErr
			store := studentBatch50Store(t, append(tc.prefix, tc.last))
			got, err := store.studentHighScorePapers(t.Context(), "tenant", "release")
			if got != nil || !errors.Is(err, tc.wantErr) {
				t.Fatalf("anonymous papers = %+v, %v; want no partial candidate set and %v", got, err, tc.wantErr)
			}
		})
	}
}

type anonymousPageAssetReader struct {
	files.Store
	t     *testing.T
	asset files.FileAsset
}

func (r anonymousPageAssetReader) Get(_ context.Context, tenantID, assetID string) (files.FileAsset, error) {
	r.t.Helper()
	if tenantID != "tenant" || assetID != "anonymous-asset" {
		r.t.Fatalf("metadata lookup = %q/%q; want tenant's anonymous asset", tenantID, assetID)
	}
	return r.asset, nil
}

func TestReadAnonymousPageRejectsRevokedMetadataAndObjectChanges(t *testing.T) {
	const contents = "anonymous pixels"
	sum := sha256.Sum256([]byte(contents))
	hash := hex.EncodeToString(sum[:])
	readFailure := errors.New("current publication lookup failed")
	for _, tc := range []struct {
		name, stored          string
		rowMissing, stripHold bool
		objectMissing         bool
		queryErr, wantReadErr error
		wantOpenErr           error
	}{
		{name: "完整匿名对象可下载", stored: contents},
		{name: "撤回后无可读页面", rowMissing: true, wantReadErr: ErrNotFound},
		{name: "发布查询失败保留原错误", queryErr: readFailure, wantReadErr: readFailure},
		{name: "文件失去证据保留标记", stripHold: true, wantReadErr: ErrAnonymousPaperUnavailable},
		{name: "元数据存在但对象已丢失", objectMissing: true, wantOpenErr: files.ErrStorageFailure},
		{name: "拒绝等长但内容变化的对象", stored: "different pixels", wantOpenErr: ErrAnonymousPaperUnavailable},
		{name: "拒绝大于冻结大小的对象", stored: contents + "+", wantOpenErr: ErrAnonymousPaperUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := studentBatch50Step{
				fragment: "AND ap.revoked_at IS NULL AND COALESCE((release.visibility_policy->>'show_high_score_paper')::boolean,false)",
				args:     []any{"tenant", "release", "anonymous-page", "anonymous-asset"}, columns: 1, err: tc.queryErr,
			}
			if !tc.rowMissing && tc.queryErr == nil {
				step.rows = [][]driver.Value{{hash}}
			}
			store := studentBatch50Store(t, []studentBatch50Step{step})
			objects := files.NewMemoryObjectStorage()
			if !tc.objectMissing {
				if err := objects.Put(t.Context(), "paper-bucket", "anonymous-key", bytes.NewBufferString(tc.stored), int64(len(tc.stored)), "image/png"); err != nil {
					t.Fatal(err)
				}
			}
			asset := files.FileAsset{
				OwnerType: "score_release_anonymous_page", OwnerID: "anonymous-page", ContentType: "image/png",
				HashSHA256: hash, Lifecycle: files.LifecycleActive, LegalHold: !tc.stripHold, SizeBytes: int64(len(contents)),
				StorageBucket: "paper-bucket", StorageKey: "anonymous-key",
			}
			manager := NewPostgresHighScorePaperManager(store.db, anonymousPageAssetReader{t: t, asset: asset}, objects, "paper-bucket")
			resource, err := manager.ReadAnonymousPage(t.Context(), "tenant", "release", "anonymous-page", "anonymous-asset")
			if !errors.Is(err, tc.wantReadErr) {
				t.Fatalf("read error = %v; want %v", err, tc.wantReadErr)
			}
			if tc.wantReadErr != nil {
				if resource.Open != nil || resource.AuditTargetID != "" {
					t.Fatalf("unavailable page exposed a resource: %+v", resource)
				}
				return
			}
			if resource.Open == nil || resource.Size != int64(len(contents)) || resource.ContentType != "image/png" ||
				resource.CacheControl != "private, no-store" || resource.AuditAction != "student.anonymous_paper_page_viewed" ||
				resource.AuditTargetID != "anonymous-page" {
				t.Fatalf("unexpected anonymous resource: %+v", resource)
			}
			body, err := resource.Open(t.Context())
			if !errors.Is(err, tc.wantOpenErr) {
				t.Fatalf("object open error = %v; want %v", err, tc.wantOpenErr)
			}
			if tc.wantOpenErr != nil {
				if body != nil {
					body.Close()
					t.Fatal("corrupt object exposed a readable body")
				}
				return
			}
			data, readErr := io.ReadAll(body)
			closeErr := body.Close()
			if readErr != nil || closeErr != nil || string(data) != contents {
				t.Fatalf("anonymous bytes = %q, read error %v, close error %v", data, readErr, closeErr)
			}
		})
	}
}

func TestPrepareAnonymousPaperRequiresEveryCandidatePageAndStableRedactionInputs(t *testing.T) {
	const layout = `{"pages":[{"page_no":1,"width":100,"height":140,"identity_regions":[{"x":0,"y":0,"width":0.5,"height":0.2}]}]}`
	sourceHash := strings.Repeat("a", 64)
	lookup := studentBatch50Step{fragment: "SELECT release.exam_id::text, release.status", args: []any{"tenant", "release"},
		columns: 2, rows: [][]driver.Value{{"exam", StatusDraft}}}
	candidates := studentBatch50Step{fragment: "SELECT submission_id::text FROM selected_papers", args: []any{"tenant", "release"},
		columns: 1, rows: [][]driver.Value{{"candidate-1"}}}
	firstPage := studentBatch50Step{fragment: "t.content_hash=r.template_content_hash AND t.status='locked'", args: []any{"tenant", "candidate-1", "exam"},
		columns: 11, rows: [][]driver.Value{{"source-page", int64(1), "original-asset", "source-bucket", "source-key", sourceHash, int64(100), "image/png", "locked-template", "template-hash", []byte(layout)}}}
	accepted := studentBatch50Step{fragment: "deleted_at IS NULL AND status='accepted'", args: []any{"tenant", "candidate-1"},
		columns: 1, rows: [][]driver.Value{{int64(1)}}}
	existing := studentBatch50Step{fragment: "source_submission_page_id=$3::uuid AND revoked_at IS NULL", args: []any{"tenant", "release", "source-page"},
		columns: 2, rows: [][]driver.Value{{sourceHash, "template-hash"}}}
	published := lookup
	published.rows = [][]driver.Value{{"exam", StatusPublished}}
	missingPage := accepted
	missingPage.rows = [][]driver.Value{{int64(2)}}
	templateMoved := existing
	templateMoved.rows = [][]driver.Value{{sourceHash, "new-template-hash"}}
	secondCandidate := candidates
	secondCandidate.rows = [][]driver.Value{{"candidate-1"}, {"candidate-2"}}
	unregisteredSecondPage := studentBatch50Step{fragment: "t.content_hash=r.template_content_hash AND t.status='locked'",
		args: []any{"tenant", "candidate-2", "exam"}, columns: 11}
	secondAccepted := studentBatch50Step{fragment: "deleted_at IS NULL AND status='accepted'", args: []any{"tenant", "candidate-2"},
		columns: 1, rows: [][]driver.Value{{int64(1)}}}
	for _, tc := range []struct {
		name    string
		steps   []studentBatch50Step
		wantErr error
	}{
		{name: "已发布版本不可重新生成", steps: []studentBatch50Step{published}, wantErr: ErrInvalidTransition},
		{name: "已接收页尚未全部完成配准", steps: []studentBatch50Step{lookup, candidates, firstPage, missingPage}, wantErr: ErrAnonymousPaperUnavailable},
		{name: "另一候选缺页时连完整候选也不开始生成", steps: []studentBatch50Step{lookup, secondCandidate, firstPage, accepted, unregisteredSecondPage, secondAccepted}, wantErr: ErrAnonymousPaperUnavailable},
		{name: "相同来源和模板的重试复用既有页面", steps: []studentBatch50Step{lookup, candidates, firstPage, accepted, existing}},
		{name: "已生成页面的模板发生变化则拒绝复用", steps: []studentBatch50Step{lookup, candidates, firstPage, accepted, templateMoved}, wantErr: ErrAnonymousPaperUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 脚本只接受读取；数据不完整或已生成时，不能再登记派生文件。
			store := studentBatch50Store(t, tc.steps)
			manager := NewPostgresHighScorePaperManager(store.db, files.NewMemoryStore(), files.NewMemoryObjectStorage(), "derived-bucket")
			if err := manager.Prepare(t.Context(), "tenant", "release", "actor"); !errors.Is(err, tc.wantErr) {
				t.Fatalf("prepare error = %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAnonymousPaperPublicationRechecksEveryPageAgainstFrozenInputs(t *testing.T) {
	readFailure := errors.New("could not recheck anonymous paper readiness")
	for _, tc := range []struct {
		name                           string
		accepted, registered, matched  int64
		derived, candidates, withPages int64
		wantReady                      bool
		wantErr                        error
	}{
		{name: "全部来源和派生页一一匹配", accepted: 4, registered: 4, matched: 4, derived: 4, candidates: 2, withPages: 2, wantReady: true},
		{name: "没有符合条件的候选卷"},
		{name: "总体页数相同但另一候选一页也没有", accepted: 4, registered: 4, matched: 4, derived: 4, candidates: 2, withPages: 1},
		{name: "已有页未配准", accepted: 4, registered: 3, matched: 3, derived: 3, candidates: 2, withPages: 2},
		{name: "配准变更使旧派生页失效", accepted: 4, registered: 4, matched: 3, derived: 4, candidates: 2, withPages: 2},
		{name: "残留多余的未撤销派生页", accepted: 4, registered: 4, matched: 4, derived: 5, candidates: 2, withPages: 2},
		{name: "查询失败保持阻断", wantErr: readFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &publicationTxProbe{studentBatch50Script: studentBatch50Script{steps: []studentBatch50Step{{
				fragment: "ap.source_sha256=e.source_hash AND ap.template_id=e.template_id",
				args:     []any{"tenant", "release"}, columns: 6, err: tc.wantErr,
				rows: [][]driver.Value{{tc.accepted, tc.registered, tc.matched, tc.derived, tc.candidates, tc.withPages}},
			}}}}
			db := sql.OpenDB(publicationTxConnector{probe: probe})
			t.Cleanup(func() { _ = db.Close() })
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ready, err := NewPostgresStore(db, nil).anonymousPaperReadyTx(t.Context(), tx, "tenant", "release")
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				t.Fatalf("finish readiness read: %v", rollbackErr)
			}
			if ready != tc.wantReady || !errors.Is(err, tc.wantErr) || probe.err != nil || probe.next != 1 || !probe.aborted || probe.committed {
				t.Fatalf("readiness = %v, %v; want %v, %v; SQL probe = %+v", ready, err, tc.wantReady, tc.wantErr, probe)
			}
		})
	}
}

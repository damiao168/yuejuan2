package score

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type compatibilityPGPublisher struct {
	coordinator *releasegate.PublicationCoordinator
}

type compatibilityPGPublishFunc func(context.Context, string, string, string, string) (scorerelease.Release, error)

func (publish compatibilityPGPublishFunc) PublishPublication(ctx context.Context, tenant, exam, release, actor string) (scorerelease.Release, error) {
	return publish(ctx, tenant, exam, release, actor)
}

func (p compatibilityPGPublisher) PublishPublication(ctx context.Context, tenant, exam, release, actor string) (scorerelease.Release, error) {
	result, _, err := p.coordinator.Publish(ctx, tenant, exam, release, actor)
	return result, err
}

// 兼容入口和新版本共享真实数据库发布器、自动准备器及 A20 协调器，不用成功替身代替门禁。
func newCompatibilityPostgresStore(db *sql.DB) (*PostgresStore, *scorerelease.PostgresStore, *scorerelease.Service) {
	scores := NewPostgresStore(db)
	prepared := scorerelease.NewPostgresStore(db, nil).WithGradePreparer(scores)
	releases := scorerelease.NewService(prepared)
	gates := releasegate.NewService(releasegate.NewPostgresStore(db), releases)
	publisher := compatibilityPGPublisher{coordinator: releasegate.NewPublicationCoordinator(gates, releases)}
	scores.WithPublication(releases, publisher)
	return scores, prepared, releases
}

func TestPostgresLegacyPublicationSharesSnapshotAndSurvivesNewerRelease(t *testing.T) {
	db := confirmationTestDB(t)
	f := newAutomaticReadFixture(t, db)
	for index, score := range []int{4, 3, 2} {
		f.submitSingleReview(t, index, score, "submitted")
	}
	store, prepared, releases := newCompatibilityPostgresStore(db)
	ctx := t.Context()
	commandCtx := commandreceipt.WithID(ctx, "legacy-pg-publish-command")
	input := PublishInput{Reason: "verified compatible publication"}
	published, err := store.PublishGrades(commandCtx, f.tenant, f.exam, f.actor, input)
	if err != nil || published.ScoreRelease == nil || !published.newlyPublished || published.Status != "published" || len(published.SubmissionGrades) != 1 || published.SubmissionGrades[0].TotalScore != 9 {
		t.Fatalf("compatibility entry did not publish canonical facts: %+v err=%v", published, err)
	}
	base := *published.ScoreRelease
	current, err := releases.CurrentPublished(ctx, f.tenant, f.exam)
	if err != nil || current.Release.ID != base.ID || len(current.Items) != 1 || current.Items[0].TotalScore != 9 || len(current.Questions) != 3 {
		t.Fatalf("compatibility result differs from immutable current snapshot: %+v %v", current, err)
	}
	var evidence int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM release_gate_evidence WHERE tenant_id=$1 AND exam_id=$2::uuid AND release_id=$3::uuid AND phase='publish'`, f.tenant, f.exam, base.ID).Scan(&evidence); err != nil || evidence != 1 {
		t.Fatalf("compatibility entry bypassed publication coordinator: evidence=%d err=%v", evidence, err)
	}
	// 重新组装存储模拟进程重启；恢复只依赖事务内持久化回执，不依赖原服务实例。
	restarted, _, _ := newCompatibilityPostgresStore(db)
	receipt, err := restarted.RecoverCommand(ctx, f.tenant, f.actor, commandreceipt.ID(commandCtx))
	var recovered PublishResult
	if err != nil || receipt.Status != "succeeded" || receipt.Operation != "score.publish" || json.Unmarshal(receipt.Result, &recovered) != nil || recovered.ScoreRelease == nil || recovered.ScoreRelease.ID != base.ID {
		t.Fatalf("canonical publication lost legacy recovery protocol: %+v %v", receipt, err)
	}
	if _, err := restarted.PublishGrades(commandCtx, f.tenant, f.exam, f.actor, PublishInput{Reason: "changed request"}); !errors.Is(err, commandreceipt.ErrConflict) {
		t.Fatalf("same command accepted a different payload: %v", err)
	}
	correction, err := releases.CreateFromRegrade(ctx, f.tenant, f.exam, f.actor, scorerelease.CreateRegradeInput{
		SourceReleaseID: base.ID, QuestionID: f.questionIDs[0], Reason: "verified correction", IdempotencyKey: "legacy-pg-correction",
		Changes: []scorerelease.RegradeChange{{SubmissionID: f.submission, QuestionID: f.questionIDs[0], Score: 5, MaxScore: 5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.publications.publisher.PublishPublication(ctx, f.tenant, f.exam, correction.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.PublishGrades(commandCtx, f.tenant, f.exam, f.actor, input)
	if err != nil || replayed.ScoreRelease == nil || replayed.newlyPublished || replayed.ScoreRelease.ID != base.ID || len(replayed.SubmissionGrades) != 1 || replayed.SubmissionGrades[0].TotalScore != 9 {
		t.Fatalf("legacy command did not recover original published result: %+v %v", replayed, err)
	}
	student, err := releases.StudentResult(ctx, f.tenant, f.exam, f.student)
	if err != nil || student.ReleaseID != correction.ID || student.TotalScore != 10 {
		t.Fatalf("legacy retry rewound student result: %+v %v", student, err)
	}
	legacyStudent, err := restarted.GetStudentGrade(ctx, f.tenant, f.student, f.exam)
	if err != nil || legacyStudent.ID != student.ReleaseID || legacyStudent.TotalScore != student.TotalScore || len(legacyStudent.Items) != len(student.Questions) {
		t.Fatalf("legacy student read diverged from current published facts: %+v student=%+v err=%v", legacyStudent, student, err)
	}
	export, err := prepared.ExportCurrentPublishedCSV(ctx, auth.AccessScope{TenantID: f.tenant, ActorID: f.actor, TenantWide: true}, f.exam, time.Now().UTC())
	if err != nil || len(export.Pins) != 1 || export.Pins[0].ReleaseID != correction.ID {
		t.Fatalf("formal export diverged from student published version: %+v err=%v", export.Pins, err)
	}
	var status string
	var locked bool
	var total float64
	if err := db.QueryRowContext(ctx, `SELECT status,locked,total_score::float8 FROM submission_grade WHERE tenant_id=$1 AND exam_id=$2::uuid`, f.tenant, f.exam).Scan(&status, &locked, &total); err != nil || status != "confirmed" || locked || total != 9 {
		t.Fatalf("compatibility flow rewrote working grades as a second publication: status=%s locked=%t total=%v err=%v", status, locked, total, err)
	}
	original, err := releases.Get(ctx, f.tenant, base.ID)
	if err != nil || original.Items[0].TotalScore != 9 {
		t.Fatalf("new publication modified original immutable version: %+v %v", original, err)
	}

	t.Run("new API wins while old API is preparing first release", func(t *testing.T) {
		fixture := newAutomaticReadFixture(t, db)
		for index, score := range []int{4, 3, 2} {
			fixture.submitSingleReview(t, index, score, "submitted")
		}
		store, _, releases := newCompatibilityPostgresStore(db)
		publisher := store.publications.publisher
		ctx := t.Context()
		var winner scorerelease.Release
		var legacyDraftID string
		// 在旧入口完成草稿后暂停，调用真正的新协调器发布另一个版本，再恢复旧请求。
		store.WithPublication(releases, compatibilityPGPublishFunc(func(legacyCtx context.Context, tenant, exam, id, actor string) (scorerelease.Release, error) {
			legacyDraftID = id
			draft, err := releases.Create(ctx, tenant, exam, actor, scorerelease.CreateInput{Reason: "new API won", IdempotencyKey: "new-api-winner"})
			if err != nil {
				return scorerelease.Release{}, err
			}
			winner, err = publisher.PublishPublication(ctx, tenant, exam, draft.ID, actor)
			if err != nil {
				return scorerelease.Release{}, err
			}
			return publisher.PublishPublication(legacyCtx, tenant, exam, id, actor)
		}))
		commandCtx := commandreceipt.WithID(ctx, "legacy-first-release-race")
		result, err := store.PublishGrades(commandCtx, fixture.tenant, fixture.exam, fixture.actor, PublishInput{Reason: "another first release"})
		if err != nil || winner.ID == "" || result.ScoreRelease == nil || result.newlyPublished || result.ScoreRelease.ID != winner.ID || result.ScoreRelease.VisibilityPolicy.ShowQuestionScores {
			t.Fatalf("legacy request did not reuse winning release and its visibility: %+v winner=%+v err=%v", result, winner, err)
		}
		current, err := releases.CurrentPublished(ctx, fixture.tenant, fixture.exam)
		if err != nil || current.Release.ID != winner.ID {
			t.Fatalf("legacy request displaced winning publication: %+v %v", current, err)
		}
		loser, err := releases.Get(ctx, fixture.tenant, legacyDraftID)
		if err != nil || loser.Release.Status != scorerelease.StatusDraft {
			t.Fatalf("legacy draft was published after another release won: %+v %v", loser, err)
		}
		legacyStudent, err := store.GetStudentGrade(ctx, fixture.tenant, fixture.student, fixture.exam)
		if err != nil || legacyStudent.ID != winner.ID || len(legacyStudent.Items) != 0 || legacyStudent.TotalScore != 9 {
			t.Fatalf("legacy student read failed to honor winning publication visibility: %+v %v", legacyStudent, err)
		}
	})
}

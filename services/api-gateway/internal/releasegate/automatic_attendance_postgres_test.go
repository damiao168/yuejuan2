package releasegate

import (
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
)

// 名册状态、基础成绩检查和最终发布门禁共用真实答卷事实，历史人工缺考不能再跳过坏卷或评分完整性。
func TestPostgresAutomaticAbsenceKeepsReceivedSubmissionPublicationGates(t *testing.T) {
	f := newPublicationFixture(t)
	missingStudent := uuid.NewString()
	f.exec(t, `INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot) VALUES($1,$2,$3::uuid,$3::uuid::text,'missing',$4,'synthetic',$4,'synthetic')`, f.tenant, f.exam, missingStudent, f.actor)
	f.exec(t, `INSERT INTO exam_student_attendance(tenant_id,exam_id,student_id,status,reason,marked_by) VALUES($1,$2,$3,'absent','historical absent',$4),($1,$2,$5,'expected','historical expected',$4)`, f.tenant, f.exam, f.student, f.actor, missingStudent)
	legacy := score.NewPostgresStore(f.db)
	releases := scorerelease.NewPostgresStore(f.db, nil)
	gates, releaseService := f.services()
	assertPassing := func() {
		t.Helper()
		quality, err := legacy.CheckQuality(t.Context(), f.tenant, f.exam, true)
		if err != nil || !quality.Passed {
			t.Fatalf("automatic absence blocked legacy grades: %#v %v", quality, err)
		}
		gate, err := releases.Gate(t.Context(), f.tenant, f.exam)
		if err != nil || !gate.Passed {
			t.Fatalf("automatic absence blocked release gate: %#v %v", gate, err)
		}
		evidence, err := gates.RecheckForPublish(t.Context(), f.tenant, f.exam, f.draft, f.actor)
		if err != nil || !evidence.Evaluation.Passed {
			t.Fatalf("automatic absence blocked final evidence: %#v %v", evidence, err)
		}
	}
	assertPassing()
	report, err := legacy.ListRoster(t.Context(), f.tenant, f.exam)
	if err != nil || report.Summary.Absent != 1 || report.Summary.Graded != 1 || report.Summary.Unidentified != 0 {
		t.Fatalf("historical flags changed current roster=%#v %v", report, err)
	}
	for _, entry := range report.Entries {
		if entry.AttendanceReason != "" || entry.MarkedBy != "" || entry.MarkedAt != nil {
			t.Fatal("derived roster exposed historical manual flags")
		}
	}
	if _, err := legacy.SetAttendance(t.Context(), f.tenant, f.exam, missingStudent, f.actor, score.AttendanceInput{Status: "absent"}); !errors.Is(err, score.ErrManualAttendanceRetired) {
		t.Fatalf("manual attendance write was accepted=%v", err)
	}
	assertBlocked := func(code string) {
		t.Helper()
		quality, err := legacy.CheckQuality(t.Context(), f.tenant, f.exam, true)
		found := false
		for _, issue := range quality.Issues {
			found = found || issue.Code == code
		}
		if err != nil || quality.Passed || !found {
			t.Fatalf("legacy gate lost %s: %#v %v", code, quality, err)
		}
		gate, err := releases.Gate(t.Context(), f.tenant, f.exam)
		if err != nil || gate.Passed || gate.Counts[code] == 0 {
			t.Fatalf("release gate lost %s: %#v %v", code, gate, err)
		}
	}
	f.exec(t, `UPDATE submission SET quality_status='failed',actual_page_count=0,expected_page_count=1 WHERE id=$1`, f.submission)
	assertBlocked("missing_pages_unresolved")
	report, err = legacy.ListRoster(t.Context(), f.tenant, f.exam)
	if err != nil || report.Summary.Absent != 1 || report.Summary.MissingPages != 1 {
		t.Fatalf("bad received submission became absence=%#v %v", report, err)
	}
	f.exec(t, `UPDATE submission SET quality_status='passed',expected_page_count=0 WHERE id=$1`, f.submission)
	for _, identity := range []string{"unknown", "conflict"} {
		f.exec(t, `UPDATE submission SET identity_status=$2 WHERE id=$1`, f.submission, identity)
		assertBlocked("unidentified_submission")
		report, err := legacy.ListRoster(t.Context(), f.tenant, f.exam)
		if err != nil || report.Summary.Absent != 1 || report.Summary.Unidentified != 1 {
			t.Fatalf("received %s identity was hidden as absence=%#v %v", identity, report, err)
		}
	}
	// 旧的直接导入虽然身份枚举仍是 unassigned，但其冻结名册内唯一学生关系仍按原流程兼容。
	f.exec(t, `UPDATE submission SET identity_status='unassigned' WHERE id=$1`, f.submission)
	assertPassing()
	unknown := uuid.NewString()
	f.exec(t, `INSERT INTO submission(id,tenant_id,exam_id,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1,$2,$3,'manual_import','quality_checked',0,0,'passed',$4)`, unknown, f.tenant, f.exam, f.actor)
	assertBlocked("unidentified_submission")
	// 仅在隔离夹具移除唯一索引，模拟历史重复到卷；正式库的唯一约束仍保留，发布查询也须防御旧重复事实。
	f.exec(t, `DROP INDEX submission_tenant_id_exam_id_student_id_idx`)
	f.exec(t, `UPDATE submission SET student_id=$2 WHERE id=$1`, unknown, f.student)
	assertBlocked("unidentified_submission")
	f.exec(t, `UPDATE submission SET deleted_at=now() WHERE id=$1`, unknown)
	assertPassing()
	release, evidence, err := NewPublicationCoordinator(gates, releaseService).Publish(t.Context(), f.tenant, f.exam, f.draft, f.actor)
	if err != nil || release.Status != scorerelease.StatusPublished || !evidence.Evaluation.Passed {
		t.Fatalf("automatic absence prevented publication=%#v %#v %v", release, evidence, err)
	}
	var historyCount int
	if err := f.db.QueryRowContext(t.Context(), `SELECT count(*) FROM exam_student_attendance`).Scan(&historyCount); err != nil || historyCount != 2 {
		t.Fatalf("historical manual evidence changed: count=%d err=%v", historyCount, err)
	}
}

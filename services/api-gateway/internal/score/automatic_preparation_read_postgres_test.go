package score

import (
	"database/sql"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
)

// automaticReadFixture 是一场两名学生、三道冻结题目（满分 5+3+2=10）的考试。
// 只有 student 有答卷；absent 没有答卷，按自动缺考处理。
type automaticReadFixture struct {
	db                          *sql.DB
	tenant, actor, exam, school string
	student, absent, submission string
	profile                     string
	questionIDs, segmentIDs     []string
}

func newAutomaticReadFixture(t *testing.T, db *sql.DB) *automaticReadFixture {
	t.Helper()
	f := &automaticReadFixture{db: db, exam: uuid.NewString(), school: uuid.NewString(), student: uuid.NewString(), absent: uuid.NewString(), submission: uuid.NewString(), profile: uuid.NewString()}
	if err := db.QueryRowContext(t.Context(), `SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.code='demo' AND u.username='teacher'`).Scan(&f.tenant, &f.actor); err != nil {
		t.Fatal(err)
	}
	year, cohort, grade, class, file, page := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Synthetic automatic read',$1,'active')`, f.school, f.tenant)
	f.exec(t, `INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at) VALUES($1::uuid,$2::uuid,$3::uuid,'Synthetic year',2026,2027,'2026-09-01','2027-08-31')`, year, f.tenant, f.school)
	f.exec(t, `INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name) VALUES($1::uuid,$2::uuid,$3::uuid,'junior',2026,2029,'Synthetic cohort')`, cohort, f.tenant, f.school)
	f.exec(t, `INSERT INTO grade(id,tenant_id,school_id,name,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,'Synthetic grade','active',$4::uuid,$5::uuid)`, grade, f.tenant, f.school, year, cohort)
	f.exec(t, `INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Synthetic class',$1,'active',$5::uuid,$6::uuid)`, class, f.tenant, f.school, grade, year, cohort)
	f.exec(t, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'Synthetic automatic read','physics','',10,'grading','human_review_required','manual_after_confirmation',$4::uuid)`, f.exam, f.tenant, f.school, f.actor)
	f.exec(t, `INSERT INTO subject_profile(id,tenant_id,code,education_stage,subject_code,version) VALUES($1::uuid,$2::uuid,$1,'junior','physics',1)`, f.profile, f.tenant)
	for _, id := range []string{f.student, f.absent} {
		f.exec(t, `INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$1,'Synthetic student','active')`, id, f.tenant, f.school, class)
		f.exec(t, `INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot) VALUES($1::uuid,$2::uuid,$3::uuid,$3,'Synthetic student',$4::uuid,'Synthetic grade',$5::uuid,'Synthetic class')`, f.tenant, f.exam, id, grade, class)
	}
	f.exec(t, `INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$4,'manual_import','quality_checked',1,1,'passed',$5::uuid)`, f.submission, f.tenant, f.exam, f.student, f.actor)
	f.exec(t, `INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'exam',$3::uuid,'synthetic.png','image/png',1,repeat(replace($1::text,'-',''),2),'test',$1,'tenant',$4::uuid)`, file, f.tenant, f.exam, f.actor)
	f.exec(t, `INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, page, f.tenant, f.submission, file)
	for index, max := range []int{5, 3, 2} {
		question, segment, no := uuid.NewString(), uuid.NewString(), []string{"Q1", "Q2", "Q3"}[index]
		f.exec(t, `INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'calculation',$5,$6,'active')`, question, f.tenant, f.exam, no, max, index+1)
		f.exec(t, `INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'structured_steps','[]','R1','{}','{}','{}','{}',repeat('a',64))`, f.tenant, f.exam, question, f.profile)
		f.exec(t, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'{}','manual','accepted')`, segment, f.tenant, f.submission, page, question, no)
		f.questionIDs, f.segmentIDs = append(f.questionIDs, question), append(f.segmentIDs, segment)
	}
	// 本夹具专测评分准备，收卷已由前置流程结束；未结束收卷的真实阻断/结束命令见 release_preparation_postgres_test。
	f.exec(t, `INSERT INTO capture_collection(tenant_id,exam_id,completed_at,completed_by) VALUES($1,$2,now(),$3) ON CONFLICT(tenant_id,exam_id) DO UPDATE SET completed_at=EXCLUDED.completed_at,completed_by=EXCLUDED.completed_by`, f.tenant, f.exam, f.actor)
	return f
}

func (f *automaticReadFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// reopenCollection / closeCollection 模拟补传：已结束的收卷台账会拒绝新答卷写入，补完后再结束。
func (f *automaticReadFixture) reopenCollection(t *testing.T) {
	t.Helper()
	f.exec(t, `UPDATE capture_collection SET completed_at=NULL,completed_by=NULL WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, f.tenant, f.exam)
}

func (f *automaticReadFixture) closeCollection(t *testing.T) {
	t.Helper()
	f.exec(t, `UPDATE capture_collection SET completed_at=now(),completed_by=$3::uuid WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, f.tenant, f.exam, f.actor)
}

// submitSingleReview 写入一条已提交的单评人工分，即 createFinalsFromHumanTx 会采用的来源。
func (f *automaticReadFixture) submitSingleReview(t *testing.T, index int, score int, status string) string {
	t.Helper()
	task := uuid.NewString()
	f.exec(t, `INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7::uuid,$7,'manual_sample',$8,$9::uuid)`,
		task, f.tenant, f.exam, f.questionIDs[index], []string{"Q1", "Q2", "Q3"}[index], f.segmentIDs[index], f.submission, status, f.actor)
	if status == "submitted" {
		f.exec(t, `INSERT INTO human_grade(tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,'single')`,
			f.tenant, task, f.segmentIDs[index], f.actor, score, []int{5, 3, 2}[index])
	}
	return task
}

// confirmRule 写入当前已确认的规则分，即 createFinalsFromRuleTx 会采用的来源。
func (f *automaticReadFixture) confirmRule(t *testing.T, index int, score int) {
	t.Helper()
	f.exec(t, `INSERT INTO question_grade(tenant_id,exam_id,submission_id,question_id,answer_segment_id,source,status,score,max_score,confirmed_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'rule_confirmed','confirmed',$6,$7,$8::uuid)`,
		f.tenant, f.exam, f.submission, f.questionIDs[index], f.segmentIDs[index], score, []int{5, 3, 2}[index], f.actor)
}

func (f *automaticReadFixture) count(t *testing.T, query string) int {
	t.Helper()
	var count int
	if err := f.db.QueryRowContext(t.Context(), query, f.exam).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func issueCodes[T any](issues []T, code func(T) (string, int)) map[string]int {
	out := map[string]int{}
	for _, issue := range issues {
		name, count := code(issue)
		out[name] += count
	}
	return out
}

func qualityCodes(report QualityReport) map[string]int {
	return issueCodes(report.Issues, func(issue QualityIssue) (string, int) { return issue.Code, issue.Count })
}

func gateCodes(gate scorerelease.Gate) map[string]int {
	return issueCodes(gate.Blocking, func(issue scorerelease.GateIssue) (string, int) { return issue.Code, issue.Count })
}

type automaticReadProbe struct {
	t                    *testing.T
	f                    *automaticReadFixture
	store                *PostgresStore
	prepared, strictGate *scorerelease.PostgresStore
	releases             *scorerelease.Service
}

func newAutomaticReadProbe(t *testing.T, f *automaticReadFixture) *automaticReadProbe {
	store, prepared, releases := newCompatibilityPostgresStore(f.db)
	return &automaticReadProbe{t: t, f: f, store: store, prepared: prepared, strictGate: scorerelease.NewPostgresStore(f.db, nil), releases: releases}
}

func (p *automaticReadProbe) roster(submission string) (RosterEntry, RosterSummary) {
	p.t.Helper()
	report, err := p.store.ListRoster(p.t.Context(), p.f.tenant, p.f.exam)
	if err != nil {
		p.t.Fatal(err)
	}
	for _, entry := range report.Entries {
		if entry.SubmissionID == submission {
			return entry, report.Summary
		}
	}
	p.t.Fatalf("roster entry %s missing: %#v", submission, report)
	return RosterEntry{}, RosterSummary{}
}

func (p *automaticReadProbe) quality(publish bool) map[string]int {
	p.t.Helper()
	quality, err := p.store.CheckQuality(p.t.Context(), p.f.tenant, p.f.exam, publish)
	if err != nil {
		p.t.Fatal(err)
	}
	return qualityCodes(quality)
}

func (p *automaticReadProbe) gate(source *scorerelease.PostgresStore) map[string]int {
	p.t.Helper()
	gate, err := source.Gate(p.t.Context(), p.f.tenant, p.f.exam)
	if err != nil {
		p.t.Fatal(err)
	}
	return gateCodes(gate)
}

func (p *automaticReadProbe) preparedFacts() int {
	return p.f.count(p.t, `SELECT (SELECT count(*) FROM final_grade WHERE exam_id=$1)+(SELECT count(*) FROM submission_grade WHERE exam_id=$1)+(SELECT count(*) FROM score_release WHERE exam_id=$1)`)
}

// addLateSubmission 给缺考学生补一份三题都已单评提交、尚未汇总的答卷。
func (f *automaticReadFixture) addLateSubmission(t *testing.T) string {
	t.Helper()
	late, file, page := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$4,'manual_import','quality_checked',1,1,'passed',$5::uuid)`, late, f.tenant, f.exam, f.absent, f.actor)
	f.exec(t, `INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'exam',$3::uuid,'late.png','image/png',1,repeat(replace($1::text,'-',''),2),'test',$1,'tenant',$4::uuid)`, file, f.tenant, f.exam, f.actor)
	f.exec(t, `INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, page, f.tenant, late, file)
	for index, question := range f.questionIDs {
		segment, task, no := uuid.NewString(), uuid.NewString(), []string{"Q1", "Q2", "Q3"}[index]
		f.exec(t, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'{}','manual','accepted')`, segment, f.tenant, late, page, question, no)
		f.exec(t, `INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7::uuid,$7,'manual_sample','submitted',$8::uuid)`, task, f.tenant, f.exam, question, no, segment, late, f.actor)
		f.exec(t, `INSERT INTO human_grade(tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,$5,'single')`, f.tenant, task, segment, f.actor, []int{5, 3, 2}[index])
	}
	return late
}

// 报告 2.1 的三种情况在同一场考试中依次出现：
//  1. 单评已提交、规则分已确认，但仍有一题没有任何评分（或评分任务未完成）：继续阻断；
//  2. 该题也完成单评、尚无草稿：名册 awaiting_aggregation 且 unresolved=0，质量报告和门禁读取不阻断，
//     确认接口与不带准备器的门禁仍严格；兼容发布随后沿正式流程自动准备并发布；
//  3. 正式发布后再来一份只差汇总的晚到答卷：读取结果与未放宽时完全相同。
func TestPostgresPublicationReadsFollowAutomaticPreparation(t *testing.T) {
	db := confirmationTestDB(t)
	f := newAutomaticReadFixture(t, db)
	p := newAutomaticReadProbe(t, f)
	ctx := t.Context()
	f.submitSingleReview(t, 0, 4, "submitted")
	f.confirmRule(t, 1, 3)
	t.Run("question without any grade keeps blocking", func(t *testing.T) {
		if entry, summary := p.roster(f.submission); entry.ResolutionCode != "grading_incomplete" || summary.Unresolved != 1 {
			t.Fatalf("ungraded question hidden from roster: %#v %#v", entry, summary)
		}
		if quality := p.quality(true); quality["missing_final_grades"] != 1 || quality["question_coverage_incomplete"] != 1 || quality["missing_submission_grades"] != 0 {
			t.Fatalf("quality must report only the truly missing grade: %#v", quality)
		}
		if gate := p.gate(p.prepared); gate["missing_final_grades"] != 1 || gate["question_coverage_incomplete"] != 1 || gate["no_submission_grades"] != 0 || gate["grades_not_confirmed"] != 0 {
			t.Fatalf("gate must report only the truly missing grade: %#v", gate)
		}
		if _, err := p.releases.Create(ctx, f.tenant, f.exam, f.actor, scorerelease.CreateInput{Reason: "synthetic", IdempotencyKey: "missing-question"}); !errors.Is(err, scorerelease.ErrGateBlocked) {
			t.Fatalf("draft with ungraded question: %v", err)
		}
		if _, err := p.store.PublishGrades(ctx, f.tenant, f.exam, f.actor, PublishInput{Reason: "missing question"}); !errors.Is(err, ErrQualityGateFailed) {
			t.Fatalf("compatibility publication bypassed missing question gate: %v", err)
		}
		if p.preparedFacts() != 0 {
			t.Fatal("blocked draft kept prepared facts")
		}
	})
	pending := f.submitSingleReview(t, 2, 2, "pending")
	t.Run("unfinished review keeps blocking", func(t *testing.T) {
		if entry, summary := p.roster(f.submission); entry.ResolutionCode != "grading_incomplete" || summary.Unresolved != 1 {
			t.Fatalf("unfinished review hidden from roster: %#v %#v", entry, summary)
		}
		if quality, gate := p.quality(true), p.gate(p.prepared); quality["unfinished_review_tasks"] != 1 || gate["unfinished_review_tasks"] != 1 {
			t.Fatalf("unfinished review must block reads: %#v %#v", quality, gate)
		}
		if _, err := p.store.PublishGrades(ctx, f.tenant, f.exam, f.actor, PublishInput{Reason: "pending review"}); !errors.Is(err, ErrQualityGateFailed) {
			t.Fatalf("compatibility publication bypassed unfinished review gate: %v", err)
		}
	})
	f.exec(t, `UPDATE review_task SET deleted_at=now() WHERE id=$1::uuid`, pending)
	f.submitSingleReview(t, 2, 2, "submitted")
	t.Run("marking complete before first draft is not blocked", func(t *testing.T) {
		entry, summary := p.roster(f.submission)
		if entry.Status != "submitted" || entry.ResolutionCode != "awaiting_aggregation" || summary.Unresolved != 0 || summary.Absent != 1 || summary.Received != 1 {
			t.Fatalf("awaiting aggregation reported as unfinished: %#v %#v", entry, summary)
		}
		if quality := p.quality(true); len(quality) != 0 {
			t.Fatalf("publish read must pass after automatic preparation: %#v", quality)
		}
		if gate := p.gate(p.prepared); len(gate) != 0 {
			t.Fatalf("gate read must pass after automatic preparation: %#v", gate)
		}
		// 未注入准备器时创建草稿不会自动准备，门禁保持原判断。
		if gate := p.gate(p.strictGate); gate["missing_final_grades"] != 3 || gate["no_submission_grades"] != 1 || gate["question_coverage_incomplete"] != 1 {
			t.Fatalf("gate without preparer must stay strict: %#v", gate)
		}
		if quality := p.quality(false); quality["missing_final_grades"] != 3 || quality["missing_submission_grades"] != 1 || quality["question_coverage_incomplete"] != 1 {
			t.Fatalf("confirmation stage must stay strict: %#v", quality)
		}
		if _, err := p.store.ConfirmGrades(ctx, f.tenant, f.exam, f.actor, ConfirmInput{Reason: "legacy"}); !errors.Is(err, ErrQualityGateFailed) {
			t.Fatalf("legacy confirmation must stay strict: %v", err)
		}
		if p.preparedFacts() != 0 {
			t.Fatal("read-only checks or rejected legacy commands wrote facts")
		}
	})
	t.Run("compatibility publication prepares grades and publishes frozen facts", func(t *testing.T) {
		published, err := p.store.PublishGrades(ctx, f.tenant, f.exam, f.actor, PublishInput{Reason: "complete synthetic publication"})
		if err != nil || published.ScoreRelease == nil || published.Status != "published" || len(published.SubmissionGrades) != 1 || published.SubmissionGrades[0].TotalScore != 9 {
			t.Fatalf("complete compatibility publication did not create canonical result: %+v %v", published, err)
		}
		var status string
		var total float64
		if err := db.QueryRowContext(ctx, `SELECT status,total_score::float8 FROM submission_grade WHERE exam_id=$1`, f.exam).Scan(&status, &total); err != nil || status != "confirmed" || total != 9 {
			t.Fatalf("automatic preparation result: %s %v %v", status, total, err)
		}
		if gate := p.gate(p.strictGate); len(gate) != 0 {
			t.Fatalf("strict gate after preparation: %#v", gate)
		}
		current, err := p.releases.CurrentPublished(ctx, f.tenant, f.exam)
		if err != nil || current.Release.ID != published.ScoreRelease.ID || len(current.Questions) != 3 || len(current.Items) != 1 || current.Items[0].TotalScore != 9 {
			t.Fatalf("compatibility publication diverged from frozen result: %+v %v", current, err)
		}
	})
	t.Run("published exam reads are unchanged", func(t *testing.T) {
		f.reopenCollection(t)
		late := f.addLateSubmission(t)
		f.closeCollection(t)
		// 正式发布后创建草稿不再自动准备；晚到答卷即使只差汇总，也不能被读成“等待自动汇总”。
		if entry, summary := p.roster(late); entry.ResolutionCode != "grading_incomplete" || summary.Unresolved != 1 {
			t.Fatalf("published exam roster changed: %#v %#v", entry, summary)
		}
		if quality := p.quality(true); quality["missing_final_grades"] != 3 || quality["missing_submission_grades"] != 1 || quality["question_coverage_incomplete"] != 1 {
			t.Fatalf("published exam quality changed: %#v", quality)
		}
		strict, injected := p.gate(p.strictGate), p.gate(p.prepared)
		if strict["missing_final_grades"] != 3 || strict["question_coverage_incomplete"] != 1 || strict["score_integrity_mismatch"] != 0 || len(strict) != len(injected) {
			t.Fatalf("published exam gate changed: strict=%#v injected=%#v", strict, injected)
		}
		for code, count := range strict {
			if injected[code] != count {
				t.Fatalf("published exam gate differs on %s: strict=%#v injected=%#v", code, strict, injected)
			}
		}
	})
}

// 首发前存在新待确认终评或自动准备会补齐终评时，可重算旧总分；试卷满分与考试配置不一致仍阻断。
func TestPostgresPublicationReadsKeepConfirmedTotalsStrict(t *testing.T) {
	db := confirmationTestDB(t)
	f := newAutomaticReadFixture(t, db)
	p := newAutomaticReadProbe(t, f)
	ctx := t.Context()
	for index, score := range []int{4, 3, 2} {
		f.submitSingleReview(t, index, score, "submitted")
	}
	if _, err := p.store.FinalizeExam(ctx, f.tenant, f.exam, f.actor); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE submission_grade SET total_score=7 WHERE exam_id=$1::uuid`, f.exam)
	if strict := p.gate(p.strictGate); strict["score_integrity_mismatch"] != 1 {
		t.Fatalf("strict gate must see the stale total: %#v", strict)
	}
	if quality, gate := p.quality(true), p.gate(p.prepared); len(quality) != 0 || len(gate) != 0 {
		t.Fatalf("stale pending total is recalculated by preparation: %#v %#v", quality, gate)
	}
	// 去掉 Q3 终评（单评仍在，准备会重新生成 2 分），并把总分确认为 Q1+Q2=7。
	f.exec(t, `UPDATE final_grade SET deleted_at=now() WHERE answer_segment_id=$1::uuid`, f.segmentIDs[2])
	f.exec(t, `UPDATE submission_grade SET status='confirmed',confirmed_by=$2::uuid,confirmed_at=now() WHERE exam_id=$1::uuid`, f.exam, f.actor)
	quality, gate := p.quality(true), p.gate(p.prepared)
	if quality["score_integrity_mismatch"] != 0 || gate["score_integrity_mismatch"] != 0 || quality["missing_final_grades"] != 0 || gate["missing_final_grades"] != 0 {
		t.Fatalf("confirmed total with new pending final must recover: %#v %#v", quality, gate)
	}
	if _, err := p.releases.Create(ctx, f.tenant, f.exam, f.actor, scorerelease.CreateInput{Reason: "synthetic", IdempotencyKey: "confirmed-mismatch"}); err != nil {
		t.Fatalf("new pending final recovery: %v", err)
	}
	f.exec(t, `UPDATE exam SET total_score=12 WHERE id=$1::uuid`, f.exam)
	if gate := p.gate(p.prepared); gate["question_coverage_incomplete"] != 1 {
		t.Fatalf("exam total mismatch must keep blocking: %#v", gate)
	}
	// 草稿已把终评和总分都确认；此后没有新终评时，被改动的已确认总分不可重算，读取与创建草稿都必须拒绝。
	f.exec(t, `UPDATE exam SET total_score=10 WHERE id=$1::uuid`, f.exam)
	f.exec(t, `UPDATE submission_grade SET total_score=8 WHERE exam_id=$1::uuid`, f.exam)
	quality, gate = p.quality(true), p.gate(p.prepared)
	if quality["score_integrity_mismatch"] != 1 || gate["score_integrity_mismatch"] != 1 {
		t.Fatalf("confirmed total without new finals must keep blocking: %#v %#v", quality, gate)
	}
	if _, err := p.releases.Create(ctx, f.tenant, f.exam, f.actor, scorerelease.CreateInput{Reason: "synthetic", IdempotencyKey: "confirmed-tampered"}); !errors.Is(err, scorerelease.ErrGateBlocked) {
		t.Fatalf("draft with tampered confirmed total: %v", err)
	}
}

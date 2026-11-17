package capture

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	captureTxTenant = "tenant"
	captureTxBatch  = "batch"
	captureTxExam   = "exam"
	captureTxActor  = "manager"
)

func captureTxQuery(columns int, values [][]driver.Value, args []driver.Value, fragments ...string) collectionSQLStep {
	return collectionSQLStep{kind: "query", printLedgerQuery: printLedgerQuery{
		columns: columns, values: values, args: args, fragments: fragments,
	}}
}

func captureTxExec(args []driver.Value, fragments ...string) collectionSQLStep {
	return collectionSQLStep{kind: "exec", printLedgerQuery: printLedgerQuery{args: args, fragments: fragments}}
}

func captureTxBatchRow(status string, revision, files, pages int64) []driver.Value {
	return []driver.Value{captureTxBatch, captureTxTenant, captureTxExam, "答卷一批", "web_upload", status,
		revision, captureTxActor, "", files, pages, int64(0), int64(0), int64(0), int64(0), nil, nil,
		time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC), "initial"}
}

func captureTxWritable(status string) collectionSQLStep {
	return captureTxQuery(1, [][]driver.Value{{status}}, []driver.Value{captureTxTenant, captureTxBatch},
		"SELECT status FROM capture_batch", "tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE")
}

func captureTxAggregate() collectionSQLStep {
	return captureTxExec([]driver.Value{captureTxTenant, captureTxBatch},
		"UPDATE capture_batch b SET", "file_count=", "page_count=", "p.status<>'deleted'",
		"b.tenant_id=$1 AND b.id=$2::uuid AND b.status NOT IN ('completed','cancelled')")
}

const (
	excludeTxFile        = "10000000-0000-0000-0000-000000000001"
	excludeTxOtherFile   = "10000000-0000-0000-0000-000000000002"
	excludeTxGroup       = "10000000-0000-0000-0000-000000000003"
	excludeTxReplacement = "10000000-0000-0000-0000-000000000004"
	excludeTxReason      = "扫描顺序错误"
)

func exclusionTxLookup(excludedAt driver.Value, reason, actor, replacement string) collectionSQLStep {
	return captureTxQuery(6, [][]driver.Value{{captureTxBatch, excludeTxGroup, excludedAt, reason, actor, replacement}},
		[]driver.Value{captureTxTenant, excludeTxFile},
		"SELECT capture_batch_id::text,COALESCE(upload_group_id::text,'')", "exclusion_reason",
		"WHERE tenant_id=$1 AND id=$2::uuid")
}

func exclusionTxReplacement(valid bool) collectionSQLStep {
	return captureTxQuery(1, [][]driver.Value{{valid}},
		[]driver.Value{captureTxTenant, captureTxBatch, excludeTxReplacement, excludeTxFile, excludeTxGroup},
		"old.tenant_id=b.tenant_id AND old.exam_id=b.exam_id", "f.tenant_id=$1 AND f.id=$3::uuid AND f.id<>$4::uuid",
		"f.deleted_at IS NULL", "f.upload_group_id IS DISTINCT FROM")
}

func exclusionTxGroupLock() collectionSQLStep {
	return captureTxQuery(1, [][]driver.Value{{excludeTxFile}, {excludeTxOtherFile}},
		[]driver.Value{captureTxTenant, captureTxBatch, excludeTxFile, excludeTxGroup},
		"WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL",
		"id=$3::uuid OR upload_group_id=", "ORDER BY id FOR UPDATE")
}

func exclusionTxSegmentLock(fileID string) collectionSQLStep {
	return captureTxQuery(1, [][]driver.Value{{"answer-segment"}}, []driver.Value{captureTxTenant, fileID},
		"p.tenant_id=a.tenant_id AND p.submission_page_id=a.submission_page_id",
		"p.tenant_id=$1 AND p.capture_file_id=$2::uuid", "FOR UPDATE OF a NOWAIT")
}

func exclusionTxGrades(fileID string, graded bool) collectionSQLStep {
	return captureTxQuery(1, [][]driver.Value{{graded}}, []driver.Value{captureTxTenant, fileID},
		"p.tenant_id=$1 AND p.capture_file_id=$2::uuid", "FROM ai_grade", "FROM human_grade",
		"FROM question_grade", "FROM final_grade", "FROM subjective_grading_run", "FROM scoring_run_item")
}

func exclusionTxRetireFile(fileID string) []collectionSQLStep {
	args := []driver.Value{captureTxTenant, fileID}
	return []collectionSQLStep{
		captureTxExec(args, "UPDATE agent_worker_task SET status='cancelled',lease_token=NULL,lease_expires_at=NULL",
			"status IN ('queued','leased','running')", "source_type='capture_file'",
			"source_type='page_registration_run'", "source_type='page_template_match_run'"),
		captureTxExec(args, "UPDATE answer_segment SET processing_status='invalidated',deleted_at=now()", "capture_file_id=$2::uuid"),
		captureTxExec(args, "UPDATE submission_page SET deleted_at=now()", "capture_file_id=$2::uuid"),
		captureTxExec(args, "UPDATE capture_page SET status='deleted',revision=revision+1", "capture_file_id=$2::uuid"),
		captureTxExec([]driver.Value{captureTxTenant, fileID, excludeTxReason, captureTxActor, excludeTxReplacement},
			"UPDATE capture_file SET exclusion_reason=$3,excluded_by=$4::uuid,replaced_by_file_id=", "WHERE tenant_id=$1 AND id=$2::uuid"),
	}
}

func TestPostgresFileExclusionReplacesWholeGroupAndRollsBackWithoutAudit(t *testing.T) {
	auditErr := errors.New("audit insert failed")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "both files retired and replacement released"},
		{name: "audit error rolls back both files and replacement", err: auditErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []collectionSQLStep{
				{kind: "begin"}, exclusionTxLookup(nil, "", "", ""), captureTxWritable("needs_review"),
				exclusionTxReplacement(true), exclusionTxGroupLock(),
			}
			for _, fileID := range []string{excludeTxFile, excludeTxOtherFile} {
				steps = append(steps, exclusionTxSegmentLock(fileID), exclusionTxGrades(fileID, false))
				steps = append(steps, exclusionTxRetireFile(fileID)...)
			}
			// 同组旧文件退出后才释放替代文件的重复标记。审计失败时，这些写入必须一起回滚。
			audit := captureTxExec([]driver.Value{captureTxTenant, captureTxBatch, excludeTxFile, captureTxActor, excludeTxReason, excludeTxReplacement, excludeTxGroup},
				"INSERT INTO capture_operation", "'delete','capture_file'", "'replacement_file_id',$6::text,'upload_group_id',$7::text")
			audit.queryErr = tc.err
			steps = append(steps,
				captureTxExec([]driver.Value{captureTxTenant, captureTxBatch},
					"UPDATE submission s SET actual_page_count=", "deleted_at=CASE WHEN NOT EXISTS", "p.capture_batch_id=$2::uuid"),
				captureTxExec([]driver.Value{captureTxTenant, excludeTxReplacement, excludeTxFile, excludeTxGroup},
					"UPDATE capture_file target SET status='uploaded',error_code=NULL", "target.status='duplicate'",
					"target.sha256 IN", "AND NOT EXISTS", "other.deleted_at IS NULL", "other.sha256=target.sha256"),
				captureTxAggregate(), audit,
			)
			if tc.err != nil {
				steps = append(steps, collectionSQLStep{kind: "rollback"})
			} else {
				steps = append(steps, captureTxQuery(19, [][]driver.Value{captureTxBatchRow("processing", 12, 1, 0)},
					[]driver.Value{captureTxTenant, captureTxBatch}, "FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid"),
					collectionSQLStep{kind: "commit"})
			}
			store, _ := collectionProbeStore(t, steps)
			batch, err := store.ExcludeFile(t.Context(), captureTxTenant, excludeTxFile, captureTxActor,
				ExcludeFileInput{Reason: " " + excludeTxReason + " ", ReplacementFileID: excludeTxReplacement})
			if tc.err != nil {
				if !errors.Is(err, tc.err) || batch.ID != "" {
					t.Fatalf("audit failure returned batch=%+v, error=%v", batch, err)
				}
				return
			}
			if err != nil || batch.ID != captureTxBatch || batch.TenantID != captureTxTenant || batch.Revision != 12 ||
				batch.FileCount != 1 || batch.PageCount != 0 || batch.Status != "processing" {
				t.Fatalf("replacement returned batch=%+v, error=%v", batch, err)
			}
		})
	}
}

func TestPostgresFileExclusionStopsBeforeWritingOnScopeOrLockedAnswer(t *testing.T) {
	lockFailure := errors.New("segment is already locked for grading")
	lockedSegment := exclusionTxSegmentLock(excludeTxFile)
	lockedSegment.queryErr = lockFailure
	unreadableGroup := exclusionTxGroupLock()
	readFailure := errors.New("upload group row stream interrupted")
	unreadableGroup.readErr = readFailure
	for _, tc := range []struct {
		name  string
		steps []collectionSQLStep
		want  error
	}{
		{name: "replacement outside exam or inside old group", steps: []collectionSQLStep{exclusionTxReplacement(false)}, want: ErrInvalidInput},
		{name: "file group cannot be fully read", steps: []collectionSQLStep{exclusionTxReplacement(true), unreadableGroup}, want: readFailure},
		{name: "segment lock conflicts with grading", steps: []collectionSQLStep{exclusionTxReplacement(true), exclusionTxGroupLock(), lockedSegment}, want: ErrConflict},
		{name: "segment already graded", steps: []collectionSQLStep{exclusionTxReplacement(true), exclusionTxGroupLock(),
			exclusionTxSegmentLock(excludeTxFile), exclusionTxGrades(excludeTxFile, true)}, want: ErrFileHasGrades},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []collectionSQLStep{{kind: "begin"}, exclusionTxLookup(nil, "", "", ""), captureTxWritable("needs_review")}
			steps = append(steps, tc.steps...)
			steps = append(steps, collectionSQLStep{kind: "rollback"})
			store, _ := collectionProbeStore(t, steps)
			batch, err := store.ExcludeFile(t.Context(), captureTxTenant, excludeTxFile, captureTxActor,
				ExcludeFileInput{Reason: excludeTxReason, ReplacementFileID: excludeTxReplacement})
			if !errors.Is(err, tc.want) || batch.ID != "" {
				t.Fatalf("unsafe exclusion returned batch=%+v, error=%v, want %v", batch, err, tc.want)
			}
		})
	}
}

func TestPostgresFileExclusionReplayRequiresSameActorReasonAndReplacement(t *testing.T) {
	excludedAt := time.Date(2026, 11, 17, 1, 5, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, actor, reason, replacement string
		conflict                         bool
	}{
		{name: "same command reads already completed batch", actor: captureTxActor, reason: " " + excludeTxReason + " ", replacement: excludeTxReplacement},
		{name: "different actor", actor: "other-actor", reason: excludeTxReason, replacement: excludeTxReplacement, conflict: true},
		{name: "different reason", actor: captureTxActor, reason: "其他原因", replacement: excludeTxReplacement, conflict: true},
		{name: "different replacement", actor: captureTxActor, reason: excludeTxReason, replacement: excludeTxOtherFile, conflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []collectionSQLStep{{kind: "begin"}, exclusionTxLookup(excludedAt, excludeTxReason, captureTxActor, excludeTxReplacement)}
			if !tc.conflict {
				steps = append(steps, captureTxQuery(19, [][]driver.Value{captureTxBatchRow("completed", 14, 1, 2)},
					[]driver.Value{captureTxTenant, captureTxBatch}, "FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL"))
			}
			steps = append(steps, collectionSQLStep{kind: "rollback"})
			store, _ := collectionProbeStore(t, steps)
			batch, err := store.ExcludeFile(t.Context(), captureTxTenant, excludeTxFile, tc.actor,
				ExcludeFileInput{Reason: tc.reason, ReplacementFileID: tc.replacement})
			if tc.conflict {
				if !errors.Is(err, ErrConflict) || batch.ID != "" {
					t.Fatalf("changed replay returned batch=%+v, error=%v", batch, err)
				}
				return
			}
			if err != nil || batch.ID != captureTxBatch || batch.Status != "completed" || batch.Revision != 14 {
				t.Fatalf("exact replay returned batch=%+v, error=%v", batch, err)
			}
		})
	}
}

func TestPostgresFileExclusionRejectsInvalidInputBeforeTransaction(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input ExcludeFileInput
	}{
		{name: "blank reason", input: ExcludeFileInput{Reason: "  "}},
		{name: "overlong reason", input: ExcludeFileInput{Reason: strings.Repeat("x", 501)}},
		{name: "invalid replacement", input: ExcludeFileInput{Reason: excludeTxReason, ReplacementFileID: "not-an-id"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := collectionProbeStore(t, nil)
			batch, err := store.ExcludeFile(t.Context(), captureTxTenant, excludeTxFile, captureTxActor, tc.input)
			if !errors.Is(err, ErrInvalidInput) || batch.ID != "" {
				t.Fatalf("invalid exclusion returned batch=%+v, error=%v", batch, err)
			}
		})
	}
}

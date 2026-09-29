package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type finding struct {
	TenantID             string `json:"tenant_id"`
	ImportID             string `json:"import_id"`
	Generation           int64  `json:"generation"`
	JobStatus            string `json:"job_status"`
	RunStatus            string `json:"run_status"`
	DispatchStatus       string `json:"dispatch_status"`
	SourceCount          int    `json:"source_count"`
	RecoverableTaskCount int    `json:"recoverable_task_count"`
	TerminalTaskCount    int    `json:"terminal_task_count"`
	Action               string `json:"action"`
}

type commandFinding struct {
	TenantID     string `json:"tenant_id"`
	ActorID      string `json:"actor_id"`
	CommandID    string `json:"command_id"`
	RequestHash  string `json:"command_request_hash"`
	SessionID    string `json:"exam_session_id"`
	ReplayState  string `json:"replay_state"`
	RecoveryPath string `json:"recovery_path"`
	Action       string `json:"action"`
}

// 修复工具默认只输出诊断；只有显式 --apply 才会执行带版本校验的幂等修复。
func main() {
	var databaseURL, tenantID, importID, examID string
	var apply bool
	flag.StringVar(&databaseURL, "database-url", os.Getenv("EDUGRADE_DATABASE_URL"), "PostgreSQL DSN")
	flag.StringVar(&tenantID, "tenant-id", "", "required tenant UUID")
	flag.StringVar(&importID, "import-id", "", "optional paper import UUID")
	flag.StringVar(&examID, "exam-id", "", "optional exam UUID; also requests a projection rebuild")
	flag.BoolVar(&apply, "apply", false, "apply the printed idempotent actions")
	flag.Parse()
	if databaseURL == "" || tenantID == "" {
		fatal("database-url and tenant-id are required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		fatal(err.Error())
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT job.tenant_id::text,job.id::text,job.current_generation,job.status,
COALESCE(run.status,'missing'),COALESCE(run.dispatch_status,'missing'),
(SELECT count(*) FROM paper_import_source source WHERE source.tenant_id=job.tenant_id AND source.paper_import_id=job.id AND source.deleted_at IS NULL),
(SELECT count(*) FROM agent_worker_task task WHERE task.tenant_id=job.tenant_id AND task.paper_import_run_id=run.id AND task.task_protocol_version=2 AND task.status IN ('queued','leased','running')),
(SELECT count(*) FROM agent_worker_task task WHERE task.tenant_id=job.tenant_id AND task.paper_import_run_id=run.id AND task.status IN ('succeeded','failed','dead_letter','cancelled'))
FROM paper_import_job job LEFT JOIN paper_import_run run ON run.tenant_id=job.tenant_id AND run.paper_import_id=job.id AND run.generation=job.current_generation
WHERE job.tenant_id=$1::uuid AND ($2='' OR job.id=$2::uuid) AND ($3='' OR job.exam_id=$3::uuid)
  AND (job.status='processing' OR job.source_revision='legacy-unverified') ORDER BY job.created_at,job.id`, tenantID, importID, examID)
	if err != nil {
		fatal(err.Error())
	}
	defer rows.Close()
	findings := []finding{}
	for rows.Next() {
		var f finding
		if err = rows.Scan(&f.TenantID, &f.ImportID, &f.Generation, &f.JobStatus, &f.RunStatus, &f.DispatchStatus, &f.SourceCount, &f.RecoverableTaskCount, &f.TerminalTaskCount); err != nil {
			fatal(err.Error())
		}
		switch {
		case f.RunStatus == "missing":
			f.Action = "mark_failed_missing_run"
		case f.JobStatus == "processing" && f.SourceCount == 0:
			f.Action = "mark_failed_no_sources"
		case f.JobStatus == "processing" && f.RecoverableTaskCount == 0 && f.TerminalTaskCount > 0:
			f.Action = "create_recovery_generation"
		case f.DispatchStatus == "pending" || f.DispatchStatus == "failed":
			f.Action = "await_dispatch_reconciler"
		case f.JobStatus == "processing" && f.RecoverableTaskCount == 0:
			f.Action = "reset_durable_dispatch_intent"
		case f.RunStatus == "review_required" && f.DispatchStatus == "not_required":
			f.Action = "mark_candidate_provenance_needs_review"
		default:
			f.Action = "none"
		}
		findings = append(findings, f)
	}
	if err = rows.Err(); err != nil {
		fatal(err.Error())
	}
	if err = rows.Close(); err != nil {
		fatal(err.Error())
	}
	commandRows, err := db.QueryContext(ctx, `SELECT session.tenant_id::text,session.created_by::text,session.command_id,session.command_request_hash,session.id::text,COALESCE(receipt.state,'missing')
FROM exam_session session
LEFT JOIN idempotency_record receipt ON receipt.tenant_id=session.tenant_id AND receipt.actor_id=session.created_by
  AND receipt.method='POST' AND receipt.route='/api/v1/exam-sessions' AND receipt.idempotency_key=session.command_id
WHERE session.tenant_id=$1::uuid AND session.command_id IS NOT NULL
  AND $2='' AND ($3='' OR EXISTS(SELECT 1 FROM exam e WHERE e.tenant_id=session.tenant_id AND e.exam_session_id=session.id AND e.id=$3::uuid))
  AND (receipt.id IS NULL OR receipt.state='processing')
ORDER BY session.command_completed_at,session.id`, tenantID, importID, examID)
	if err != nil {
		fatal(err.Error())
	}
	commandFindings := []commandFinding{}
	for commandRows.Next() {
		var f commandFinding
		if err = commandRows.Scan(&f.TenantID, &f.ActorID, &f.CommandID, &f.RequestHash, &f.SessionID, &f.ReplayState); err != nil {
			fatal(err.Error())
		}
		f.RecoveryPath = "/api/v1/exam-sessions/commands/" + f.CommandID
		if f.ReplayState == "processing" {
			f.Action = "complete_replay_from_business_fact"
		} else {
			// A missing transport receipt has no reconstructable HTTP request
			// hash. The durable business command endpoint is authoritative; do
			// not invent a hash that would reject a legitimate retry.
			f.Action = "use_business_command_recovery"
		}
		commandFindings = append(commandFindings, f)
	}
	if err = commandRows.Close(); err != nil {
		fatal(err.Error())
	}
	if err = commandRows.Err(); err != nil {
		fatal(err.Error())
	}
	before, _ := json.MarshalIndent(map[string]any{"mode": map[bool]string{true: "apply", false: "dry-run"}[apply], "count": len(findings) + len(commandFindings), "paper_import_findings": findings, "exam_command_findings": commandFindings}, "", "  ")
	fmt.Println(string(before))
	// 先让操作者核对发现项；干跑阶段不写数据库。
if !apply {
		return
	}
	appliedMutations := 0
	paperStore := paper.NewPostgresStore(db)
	for _, f := range findings {
		if f.Action != "create_recovery_generation" {
			continue
		}
		job, err := paperStore.GetPaperImport(ctx, f.TenantID, f.ImportID)
		if err != nil {
			fatal(err.Error())
		}
		if job.Generation != f.Generation {
			fatal("import changed after dry-run; rerun inspection")
		}
		sources := make([]paper.ReplacePaperImportSourceInput, 0, len(job.Sources))
		for _, source := range job.Sources {
			sources = append(sources, paper.ReplacePaperImportSourceInput{ID: source.ID, DocumentIndex: source.DocumentIndex, RoleHint: source.RoleHint})
		}
		recovered, err := paperStore.ReplacePaperImportSources(ctx, f.TenantID, f.ImportID, job.CreatedBy, paper.ReplacePaperImportSourcesInput{CommandID: fmt.Sprintf("repair:%s:g:%d", job.ID, job.Generation), ExpectedGeneration: job.Generation, Sources: sources})
		if err != nil {
			fatal(err.Error())
		}
		if recovered.Generation != job.Generation+1 {
			fatal("repair did not establish a new run")
		}
		appliedMutations++
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		fatal(err.Error())
	}
	defer tx.Rollback()
	for _, f := range findings {
		if f.Action == "create_recovery_generation" {
			continue
		}
		var generation int64
		if err = tx.QueryRowContext(ctx, `SELECT current_generation FROM paper_import_job WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, f.TenantID, f.ImportID).Scan(&generation); err != nil {
			fatal(err.Error())
		}
		if generation != f.Generation {
			fatal("import changed after inspection; refusing stale repair")
		}
		var update sql.Result
		switch f.Action {
		case "mark_failed_missing_run", "mark_failed_no_sources":
			update, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='failed',error_code='paper_import_repair_unrecoverable',issues='["历史任务缺少可恢复的版本化输入"]'::jsonb,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='processing'`, f.TenantID, f.ImportID)
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET status='failed',error_code='paper_import_repair_unrecoverable',completed_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND paper_import_id=$2::uuid AND generation=$3 AND status='processing'`, f.TenantID, f.ImportID, f.Generation)
			}
		case "reset_durable_dispatch_intent":
			update, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET dispatch_status='pending',dispatch_lease_owner=NULL,dispatch_lease_expires_at=NULL,dispatch_available_at=now(),error_code=NULL,updated_at=now() WHERE tenant_id=$1::uuid AND paper_import_id=$2::uuid AND generation=$3 AND status='processing' AND NOT EXISTS(SELECT 1 FROM agent_worker_task task WHERE task.tenant_id=$1::uuid AND task.paper_import_run_id=paper_import_run.id)`, f.TenantID, f.ImportID, f.Generation)
		case "mark_candidate_provenance_needs_review":
			update, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET status='needs_review',error_code='legacy_result_provenance_unverified',updated_at=now() WHERE tenant_id=$1::uuid AND paper_import_id=$2::uuid AND generation=$3 AND source_revision='legacy-unverified' AND status='review_required'`, f.TenantID, f.ImportID, f.Generation)
		}
		if err != nil {
			fatal(err.Error())
		}
		if update != nil {
			rows, err := update.RowsAffected()
			if err != nil {
				fatal(err.Error())
			}
			appliedMutations += int(rows)
		}
	}
	examStore := exam.NewPostgresStore(db)
	for _, f := range commandFindings {
		if f.Action != "complete_replay_from_business_fact" {
			continue
		}
		result, recoveryErr := examStore.RecoverExamSessionCommand(ctx, auth.AccessScope{TenantID: f.TenantID, TenantWide: true}, f.ActorID, f.CommandID)
		if recoveryErr != nil || result.Status != "succeeded" || result.Session == nil || result.Session.ID != f.SessionID {
			fatal(fmt.Sprintf("cannot recover exam command %s: status=%s error=%v", f.CommandID, result.Status, recoveryErr))
		}
		body, marshalErr := json.Marshal(map[string]any{"exam_session": result.Session})
		if marshalErr != nil {
			fatal(marshalErr.Error())
		}
		update, updateErr := tx.ExecContext(ctx, `UPDATE idempotency_record
SET state='completed',response_status=201,response_headers='{"Content-Type":"application/json"}'::jsonb,response_body=$6,updated_at=now()
WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND method='POST' AND route='/api/v1/exam-sessions'
  AND idempotency_key=$3 AND state='processing'
	  AND EXISTS(SELECT 1 FROM exam_session session WHERE session.tenant_id=$1::uuid AND session.created_by=$2::uuid AND session.command_id=$3 AND session.id=$4::uuid AND session.command_request_hash=$5)`, f.TenantID, f.ActorID, f.CommandID, f.SessionID, f.RequestHash, body)
		if updateErr != nil {
			fatal(updateErr.Error())
		}
		if changed, _ := update.RowsAffected(); changed != 1 {
			fatal("exam command replay receipt changed concurrently")
		}
		appliedMutations++
	}
	if examID != "" {
		if _, err = tx.ExecContext(ctx, `SELECT request_processing_projection_refresh($1::uuid,$2::uuid)`, tenantID, examID); err != nil {
			fatal(err.Error())
		}
	}
	// Capture post-repair facts in the repair transaction. Operators must pause
	// writers as documented in the runbook when collecting maintenance evidence.
	afterImports := []json.RawMessage{}
	for _, f := range findings {
		var state json.RawMessage
		if err = tx.QueryRowContext(ctx, `SELECT jsonb_build_object(
'tenant_id',job.tenant_id,'import_id',job.id,'generation',job.current_generation,
'job_status',job.status,'run_status',run.status,'dispatch_status',run.dispatch_status)
FROM paper_import_job job LEFT JOIN paper_import_run run
ON run.tenant_id=job.tenant_id AND run.paper_import_id=job.id AND run.generation=job.current_generation
WHERE job.tenant_id=$1::uuid AND job.id=$2::uuid`, f.TenantID, f.ImportID).Scan(&state); err != nil {
			fatal(err.Error())
		}
		afterImports = append(afterImports, state)
	}
	afterCommands := []json.RawMessage{}
	for _, f := range commandFindings {
		var state json.RawMessage
		if err = tx.QueryRowContext(ctx, `SELECT jsonb_build_object('command_id',session.command_id,
'exam_session_id',session.id,'command_status',session.command_status,'replay_state',COALESCE(receipt.state,'missing'))
FROM exam_session session LEFT JOIN idempotency_record receipt
ON receipt.tenant_id=session.tenant_id AND receipt.actor_id=session.created_by
AND receipt.method='POST' AND receipt.route='/api/v1/exam-sessions' AND receipt.idempotency_key=session.command_id
WHERE session.tenant_id=$1::uuid AND session.id=$2::uuid`, f.TenantID, f.SessionID).Scan(&state); err != nil {
			fatal(err.Error())
		}
		afterCommands = append(afterCommands, state)
	}
	if err = tx.Commit(); err != nil {
		fatal(err.Error())
	}
	if err = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"applied": true, "mutations": appliedMutations,
		"paper_import_after": afterImports, "exam_command_after": afterCommands,
	}); err != nil {
		fatal(err.Error())
	}
}

func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }

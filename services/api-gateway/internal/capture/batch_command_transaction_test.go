package capture

import (
	"crypto/sha256"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
)

const commandTxKey = "upload-command"

func commandTxPrefix(priorIDs ...[]driver.Value) []collectionSQLStep {
	return []collectionSQLStep{
		{kind: "begin"},
		captureTxExec([]driver.Value{"capture-batch:tenant:manager:" + commandTxKey}, "pg_advisory_xact_lock(hashtextextended($1,0))"),
		captureTxQuery(1, priorIDs, []driver.Value{captureTxTenant, captureTxActor, commandTxKey},
			"WHERE tenant_id=$1::uuid AND operator_id=$2::uuid AND idempotency_key=$3", "ORDER BY created_at,id LIMIT 2"),
	}
}

func commandTxExamStatus(rows ...[]driver.Value) []collectionSQLStep {
	return []collectionSQLStep{
		collectionExamLockQuery(true),
		captureTxQuery(1, rows, []driver.Value{captureTxTenant, captureTxExam},
			"SELECT status FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE NOWAIT"),
	}
}

func TestPostgresBatchCommandAcceptsCollectionOrExplicitSupplement(t *testing.T) {
	for _, tc := range []struct {
		name, status, purpose, reason string
	}{
		{name: "initial batch while collecting", status: "collecting", purpose: "initial"},
		{name: "supplement during grading", status: "grading", purpose: "supplement", reason: "补扫漏页"},
		{name: "supplement during review", status: "reviewing", purpose: "supplement", reason: "补扫漏页"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const original = `{"ExamID":"exam","Name":"答卷一批","SourceType":"web_upload","ScannerDevice":""}`
			hashPayload := original
			if tc.purpose == "supplement" {
				hashPayload += "\nsupplement\n" + tc.reason
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashPayload)))
			row := captureTxBatchRow("draft", 1, 0, 0)
			row[18] = tc.purpose
			steps := append(commandTxPrefix(), commandTxExamStatus([]driver.Value{tc.status})...)
			steps = append(steps,
				captureTxQuery(19, [][]driver.Value{row},
					[]driver.Value{captureTxTenant, captureTxExam, "答卷一批", "web_upload", captureTxActor, "",
						commandTxKey, hash, tc.purpose, tc.reason},
					"INSERT INTO capture_batch", "idempotency_key,command_request_hash,purpose,supplement_reason"),
				collectionSQLStep{kind: "commit"})
			store, _ := collectionProbeStore(t, steps)
			input := CreateBatchInput{Name: " 答卷一批 ", SourceType: " web_upload ", IdempotencyKey: " " + commandTxKey + " "}
			if tc.purpose == "supplement" {
				input.Purpose = tc.purpose
				input.Reason = " " + tc.reason + " "
			}
			batch, err := store.CreateBatch(t.Context(), captureTxTenant, captureTxExam, captureTxActor, input)
			if err != nil || batch.ID != captureTxBatch || batch.ExamID != captureTxExam || batch.OperatorID != captureTxActor ||
				batch.Name != "答卷一批" || batch.Purpose != tc.purpose || batch.Status != "draft" || batch.Revision != 1 {
				t.Fatalf("create returned batch=%+v, error=%v", batch, err)
			}
		})
	}
}

func TestPostgresBatchCommandReplayReadsPriorBatchBeforeExamState(t *testing.T) {
	const accepted = `{"ExamID":"exam","Name":"答卷一批","SourceType":"web_upload","ScannerDevice":""}`
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(accepted)))
	for _, tc := range []struct {
		name, requestedName, storedHash string
		wantErr                         error
	}{
		{name: "exact replay", requestedName: "答卷一批", storedHash: hash},
		{name: "legacy command without stored hash", requestedName: "答卷一批"},
		{name: "current command reused for different batch", requestedName: "另一批", storedHash: hash, wantErr: ErrConflict},
		{name: "legacy command reused for different batch", requestedName: "另一批", wantErr: ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 即使考试已离开采集阶段，也只能查回原批次。同键换内容必须回滚，不能新建批次。
			steps := append(commandTxPrefix([]driver.Value{captureTxBatch}),
				captureTxQuery(19, [][]driver.Value{captureTxBatchRow("completed", 9, 4, 8)}, []driver.Value{captureTxTenant, captureTxBatch},
					"FROM capture_batch WHERE tenant_id=$1::uuid AND id=$2::uuid"),
				captureTxQuery(1, [][]driver.Value{{tc.storedHash}}, []driver.Value{captureTxTenant, captureTxBatch},
					"SELECT COALESCE(command_request_hash,'') FROM capture_batch"),
			)
			if tc.wantErr != nil {
				steps = append(steps, collectionSQLStep{kind: "rollback"})
			} else {
				steps = append(steps, collectionSQLStep{kind: "commit"})
			}
			store, _ := collectionProbeStore(t, steps)
			batch, err := store.CreateBatch(t.Context(), captureTxTenant, captureTxExam, captureTxActor, CreateBatchInput{
				Name: tc.requestedName, SourceType: "web_upload", IdempotencyKey: commandTxKey,
			})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || batch.ID != "" {
					t.Fatalf("changed replay returned batch=%+v, error=%v", batch, err)
				}
				return
			}
			if err != nil || batch.ID != captureTxBatch || batch.Status != "completed" || batch.Revision != 9 ||
				batch.FileCount != 4 || batch.PageCount != 8 {
				t.Fatalf("exact replay returned batch=%+v, error=%v", batch, err)
			}
		})
	}
}

func TestPostgresBatchCommandRefusesAmbiguousHistoryAndUnavailableExam(t *testing.T) {
	readErr := errors.New("prior command stream failed")
	brokenRead := commandTxPrefix([]driver.Value{captureTxBatch})
	brokenRead[2].readErr = readErr
	for _, tc := range []struct {
		name  string
		steps []collectionSQLStep
		want  error
	}{
		{name: "two historical batches claim command", steps: commandTxPrefix([]driver.Value{"one"}, []driver.Value{"two"}), want: ErrConflict},
		{name: "history interrupted after first match", steps: brokenRead, want: readErr},
		{name: "exam locked by an upload", steps: append(commandTxPrefix(), collectionExamLockQuery(false)), want: ErrConflict},
		{name: "exam outside tenant or deleted", steps: append(commandTxPrefix(), commandTxExamStatus()...), want: ErrNotFound},
		{name: "new initial batch during grading", steps: append(commandTxPrefix(), commandTxExamStatus([]driver.Value{"grading"})...), want: ErrInvalidTransition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append(tc.steps, collectionSQLStep{kind: "rollback"})
			store, _ := collectionProbeStore(t, steps)
			batch, err := store.CreateBatch(t.Context(), captureTxTenant, captureTxExam, captureTxActor, CreateBatchInput{
				Name: "答卷一批", SourceType: "web_upload", IdempotencyKey: commandTxKey,
			})
			if !errors.Is(err, tc.want) || batch.ID != "" {
				t.Fatalf("refused command returned batch=%+v, error=%v, want %v", batch, err, tc.want)
			}
		})
	}
}

func TestPostgresBatchCommandRecoveryScopesExamActorAndTenant(t *testing.T) {
	batchRow := captureTxBatchRow("processing", 6, 2, 4)
	readErr := errors.New("command lookup stream failed")
	for _, tc := range []struct {
		name, status string
		rows         [][]driver.Value
		readErr      error
		want         error
	}{
		{name: "scoped command absent", status: "not_accepted"},
		{name: "exact command recovered", status: "succeeded", rows: [][]driver.Value{batchRow}},
		{name: "ambiguous history refused", rows: [][]driver.Value{batchRow, batchRow}, want: ErrConflict},
		{name: "incomplete lookup is an error", status: "not_accepted", readErr: readErr, want: readErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := printLedgerQuery{columns: 19, values: tc.rows, readErr: tc.readErr,
				args: []driver.Value{captureTxTenant, captureTxExam, captureTxActor, commandTxKey},
				fragments: []string{"WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND operator_id=$3::uuid AND idempotency_key=$4",
					"ORDER BY created_at,id LIMIT 2"}}
			store := printLedgerStore(t, []printLedgerQuery{step})
			out, err := store.RecoverBatchCommand(t.Context(), captureTxTenant, captureTxExam, captureTxActor, commandTxKey)
			if !errors.Is(err, tc.want) || out.Status != tc.status {
				t.Fatalf("recovery returned %+v, error=%v, want status %q and error %v", out, err, tc.status, tc.want)
			}
			if tc.status == "succeeded" {
				if out.Batch == nil || out.Batch.ID != captureTxBatch || out.Batch.Revision != 6 || out.CommandID != commandTxKey {
					t.Fatalf("recovery lost accepted batch metadata: %+v", out)
				}
			} else if out.Batch != nil {
				t.Fatalf("recovery exposed an unconfirmed batch: %+v", out.Batch)
			}
		})
	}
}

package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
)

func e2eBusinessCommandRecoveryStates(t *testing.T, db *sql.DB, tenant, actor string) {
	t.Helper()
	insert := func(command, route, state string, status int, body string) {
		t.Helper()
		var responseStatus any
		if status > 0 {
			responseStatus = status
		}
		if _, err := db.Exec(`INSERT INTO idempotency_record
(tenant_id,actor_id,method,route,idempotency_key,request_hash,request_body,state,response_status,response_body,expires_at)
VALUES($1::uuid,$2::uuid,'POST',$3,$4,$5,$6,$7,$8,$9,now()+interval '1 day')`,
			tenant, actor, route, command, strings.Repeat("a", 64), []byte(`{"reason":"server-frozen"}`), state, responseStatus, []byte(body)); err != nil {
			t.Fatalf("seed transport receipt %s: %v", command, err)
		}
	}

	e2eAssertProcessingCommandRecovery(t, db, tenant, actor, insert)
	e2eAssertTerminalCommandRecovery(t, db, tenant, actor, insert)
	t.Log("business command processing/rejected/unknown/domain-isolation recovery passed")
}

// 有效占位仍在处理中；过期占位带回服务器冻结载荷，别的租户或操作者不能接管。
func e2eAssertProcessingCommandRecovery(t *testing.T, db *sql.DB, tenant, actor string, insert func(string, string, string, int, string)) {
	t.Helper()
	processingID := "processing-" + uuid.NewString()
	insert(processingID, "/api/v1/review-tasks/{id}/submit", "processing", 0, "")
	processing, err := commandreceipt.Recover(context.Background(), db, tenant, actor, processingID, "review.")
	if err != nil || processing.Status != "processing" {
		t.Fatalf("processing recovery=%#v err=%v", processing, err)
	}

	staleID := "stale-" + uuid.NewString()
	insert(staleID, "/api/v1/exams/{examId}/publish", "processing", 0, "")
	if _, err := db.Exec(`UPDATE idempotency_record SET updated_at=now()-interval '3 minutes',expires_at=now()-interval '1 minute' WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND idempotency_key=$3`, tenant, actor, staleID); err != nil {
		t.Fatal(err)
	}
	stale, err := commandreceipt.Recover(context.Background(), db, tenant, actor, staleID, "score.")
	if err != nil || stale.Status != "takeover_ready" || string(stale.Payload) != `{"reason":"server-frozen"}` {
		t.Fatalf("stale recovery=%#v err=%v", stale, err)
	}
	for name, identity := range map[string][2]string{
		"other tenant": {uuid.NewString(), actor},
		"other actor":  {tenant, uuid.NewString()},
	} {
		isolated, err := commandreceipt.Recover(context.Background(), db, identity[0], identity[1], staleID, "score.")
		if err != nil || isolated.Status != "not_accepted" {
			t.Fatalf("%s command scope=%#v err=%v", name, isolated, err)
		}
	}

}

// 已拒绝、缺失业务回执、跨领域与身份歧义分别返回原终态，不能假定传输完成等于业务成功。
func e2eAssertTerminalCommandRecovery(t *testing.T, db *sql.DB, tenant, actor string, insert func(string, string, string, int, string)) {
	t.Helper()
	rejectedID := "rejected-" + uuid.NewString()
	insert(rejectedID, "/api/v1/exams/{examId}/publish", "completed", http.StatusBadRequest, `{"error":{"code":"publish_input_rejected"}}`)
	rejected, err := commandreceipt.Recover(context.Background(), db, tenant, actor, rejectedID, "score.")
	if err != nil || rejected.Status != "rejected" || rejected.HTTPStatus != http.StatusBadRequest || rejected.ErrorCode != "publish_input_rejected" {
		t.Fatalf("rejected recovery=%#v err=%v", rejected, err)
	}

	unknownID := "unknown-" + uuid.NewString()
	insert(unknownID, "/api/v1/exams/{examId}/reports/export", "completed", http.StatusOK, `{}`)
	unknown, err := commandreceipt.Recover(context.Background(), db, tenant, actor, unknownID, "report.")
	if err != nil || unknown.Status != "unknown" || unknown.ErrorCode != "business_receipt_missing" {
		t.Fatalf("unknown recovery=%#v err=%v", unknown, err)
	}

	unrelatedID := "unrelated-" + uuid.NewString()
	insert(unrelatedID, "/api/v1/exams/{examId}/publish", "processing", 0, "")
	unrelated, err := commandreceipt.Recover(context.Background(), db, tenant, actor, unrelatedID, "review.")
	if err != nil || unrelated.Status != "not_accepted" {
		t.Fatalf("cross-domain recovery=%#v err=%v", unrelated, err)
	}

	ambiguousID := "ambiguous-" + uuid.NewString()
	insert(ambiguousID, "/api/v1/review-tasks/{id}/submit", "processing", 0, "")
	insert(ambiguousID, "/api/v1/arbitration-tasks/{id}/submit", "processing", 0, "")
	ambiguous, err := commandreceipt.Recover(context.Background(), db, tenant, actor, ambiguousID, "review.")
	if err != nil || ambiguous.Status != "unknown" || ambiguous.ErrorCode != "ambiguous_command_identity" {
		t.Fatalf("ambiguous recovery=%#v err=%v", ambiguous, err)
	}
}

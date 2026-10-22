package capture

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// 通过真实身份 Handler 和 capture:manage 权限中间件验证边界，不能仅证明 Store 收到租户参数。
func TestMemoryIdentityHandlersRejectMissingAndForeignTenantSubmissions(t *testing.T) {
	for _, action := range []string{"confirm", "unknown"} {
		for _, target := range []string{"missing", "foreign_tenant"} {
			t.Run(action+"/"+target, func(t *testing.T) {
				ctx := context.Background()
				store := NewMemoryStore()
				batch, err := store.CreateBatch(ctx, "tenant-a", "exam-a", "operator-a", CreateBatchInput{Name: "identity scope", SourceType: "web_upload"})
				if err != nil {
					t.Fatal(err)
				}
				file, err := store.RegisterFile(ctx, "tenant-a", batch.ID, "operator-a", RegisterFileInput{FileAssetID: "source-a", IdempotencyKey: "source-a"}, FileAssetSnapshot{ID: "source-a", ExamID: "exam-a", SizeBytes: 1, SHA256: "source-hash"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.QueueBatch(ctx, "tenant-a", batch.ID, "operator-a"); err != nil {
					t.Fatal(err)
				}
				if _, err = store.ApplyFileResult(ctx, "tenant-a", file.ID, []DecodedPageInput{{SourceIndex: 1, FileAssetID: "decoded-a", SHA256: "decoded-hash", Width: 100, Height: 100}}); err != nil {
					t.Fatal(err)
				}
				pages, err := store.ListPages(ctx, "tenant-a", batch.ID)
				if err != nil || len(pages) != 1 {
					t.Fatalf("capture setup: pages=%v err=%v", pages, err)
				}
				submissionID := pages[0].SubmissionID
				handler := NewHandler(store, nil, nil, nil, nil)
				mux := http.NewServeMux()
				mux.Handle("POST /api/v1/submissions/{id}/student-match/confirm", auth.RequirePermission("capture:manage")(http.HandlerFunc(handler.ConfirmStudentMatch)))
				mux.Handle("POST /api/v1/submissions/{id}/student-match/unknown", auth.RequirePermission("capture:manage")(http.HandlerFunc(handler.MarkStudentUnknown)))
				invoke := func(tenant, id, command string, input any) *httptest.ResponseRecorder {
					t.Helper()
					body, marshalErr := json.Marshal(input)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					req := httptest.NewRequest(http.MethodPost, "/api/v1/submissions/"+id+"/student-match/"+command, strings.NewReader(string(body)))
					req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: tenant + "-operator", TenantID: tenant, Permissions: []string{"capture:manage"}}))
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, req)
					return response
				}
				positive := invoke("tenant-a", submissionID, "confirm", ConfirmStudentMatchInput{StudentID: "student-a", Revision: 1, Reason: "own tenant confirmation"})
				if positive.Code != http.StatusOK {
					t.Fatalf("own-tenant positive control: %d %s", positive.Code, positive.Body.String())
				}
				before := store.identities[submissionID]
				identityCount := len(store.identities)
				tenant, targetID := "tenant-a", "00000000-0000-0000-0000-000000000024"
				revision := 1
				if target == "foreign_tenant" {
					tenant, targetID, revision = "tenant-b", submissionID, before.IdentityRevision
				}
				var input any = ConfirmStudentMatchInput{StudentID: "attacker-student", Revision: revision, Reason: "invalid submission target"}
				if action == "unknown" {
					input = MarkStudentUnknownInput{Revision: revision, Reason: "invalid submission target"}
				}
				response := invoke(tenant, targetID, action, input)
				if response.Code != http.StatusNotFound {
					t.Errorf("%s identity command accepted %s target: status=%d body=%s", action, target, response.Code, response.Body.String())
				}
				after := store.identities[submissionID]
				if len(store.identities) != identityCount || after.StudentID != before.StudentID || after.IdentityStatus != before.IdentityStatus || after.IdentityRevision != before.IdentityRevision {
					t.Errorf("rejected request mutated identities: before=%+v after=%+v count=%d want=%d", before, after, len(store.identities), identityCount)
				}
				positive = invoke("tenant-a", submissionID, "unknown", MarkStudentUnknownInput{Revision: before.IdentityRevision, Reason: "own tenant still works"})
				if positive.Code != http.StatusOK {
					t.Fatalf("own-tenant unknown positive control: %d %s", positive.Code, positive.Body.String())
				}
			})
		}
	}
}

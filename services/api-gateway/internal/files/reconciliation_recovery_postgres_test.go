package files_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestReconciliationRestoresVerifiedUploadsToOrdinaryReadAndRetry(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "file_recovery_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	for _, table := range []string{"file_asset", "file_reconciliation_run", "file_reconciliation_finding", "submission", "question_bank_item_asset", "question_bank_item_version", "question_bank_item"} {
		if _, err = db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	store, objects := files.NewPostgresStore(db), files.NewMemoryObjectStorage()
	authStore := authStoreWithPermissions(t, []string{"file:manage"})
	router := testRouter(authStore, store, objects)
	token := login(t, router)
	tenant, actor := "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000201"
	for _, lifecycle := range []string{files.LifecyclePendingUpload, files.LifecycleUploadFailed, files.LifecycleOrphanRecovered, "wrong_hash", "wrong_size"} {
		t.Run(lifecycle, func(t *testing.T) {
			body := []byte("%PDF-1.4\nsynthetic " + lifecycle + "\n")
			digest := sha256.Sum256(body)
			asset, err := store.CreatePending(ctx, files.CreateAssetInput{TenantID: tenant, UploadedBy: actor,
				OwnerType: "import", OriginalName: lifecycle + ".pdf", ContentType: "application/pdf",
				SizeBytes: int64(len(body)), HashSHA256: hex.EncodeToString(digest[:]), StorageBucket: "test-files",
				StorageKey: "tenant/" + tenant + "/" + lifecycle + ".pdf", Visibility: "private"})
			if err != nil {
				t.Fatal(err)
			}
			storedBody := body
			if lifecycle == "wrong_hash" {
				storedBody = bytes.Repeat([]byte{'x'}, len(body))
			} else if lifecycle == "wrong_size" {
				storedBody = append(append([]byte{}, body...), 'x')
			}
			if err = objects.Put(ctx, asset.StorageBucket, asset.StorageKey, bytes.NewReader(storedBody), int64(len(storedBody)), asset.ContentType); err != nil {
				t.Fatal(err)
			}
			if lifecycle == files.LifecycleUploadFailed {
				asset, err = store.MarkUploadFailed(ctx, tenant, asset.ID, asset.Revision, "synthetic_activation_failure")
			} else if lifecycle == files.LifecycleOrphanRecovered {
				_, err = db.ExecContext(ctx, `UPDATE file_asset SET lifecycle_status='orphan_recovered',revision=revision+1 WHERE id=$1`, asset.ID)
			} else if lifecycle == files.LifecyclePendingUpload {
				// 模拟上传对象已落盘但激活 CAS 失败，元数据继续处于 pending。
				if _, activationErr := store.Activate(ctx, tenant, asset.ID, asset.Revision+1); !errors.Is(activationErr, files.ErrConflict) {
					t.Fatalf("activation failure control: %v", activationErr)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Get(ctx, tenant, asset.ID); !errors.Is(err, files.ErrNotFound) {
				t.Fatalf("unverified asset became readable: %v", err)
			}
			reconciler := files.NewReconciler(db, objects)
			if _, err = reconciler.Run(ctx, files.ReconciliationOptions{Bucket: "test-files"}); err != nil {
				t.Fatal(err)
			}
			if _, err = store.Get(ctx, tenant, asset.ID); !errors.Is(err, files.ErrNotFound) {
				t.Fatalf("report mode changed availability: %v", err)
			}
			if _, err = reconciler.Run(ctx, files.ReconciliationOptions{Bucket: "test-files", Repair: true}); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(lifecycle, "wrong_") {
				if _, err = store.Get(ctx, tenant, asset.ID); !errors.Is(err, files.ErrNotFound) {
					t.Fatalf("mismatch became readable: %v", err)
				}
				return
			}
			restored, err := store.Get(ctx, tenant, asset.ID)
			if err != nil || restored.Lifecycle != files.LifecycleActive || restored.ID != asset.ID || restored.StorageKey != asset.StorageKey {
				t.Fatalf("recovered identity=%+v err=%v", restored, err)
			}
			for _, suffix := range []string{"", "/download"} {
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, authedRequest(http.MethodGet, "/api/v1/files/"+asset.ID+suffix, nil, token))
				if recorder.Code != http.StatusOK || (suffix != "" && !bytes.Equal(recorder.Body.Bytes(), body)) {
					t.Fatalf("read%s: status=%d body=%s", suffix, recorder.Code, recorder.Body.String())
				}
			}
			// 正常重传已恢复的导入材料会复用原资产，不能再次走无法续接的 CreatePending。
			multipartBody, contentType := multipartUploadBody(t, asset.OriginalName, asset.ContentType, body, map[string]string{"owner_type": "import"})
			req := authedRequest(http.MethodPost, "/api/v1/files", multipartBody, token)
			req.Header.Set("Content-Type", contentType)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			var response struct {
				File files.FileResponse `json:"file"`
			}
			if err = json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || recorder.Code != http.StatusOK || response.File.ID != asset.ID {
				t.Fatalf("retry: status=%d body=%s err=%v", recorder.Code, recorder.Body.String(), err)
			}
		})
	}
}

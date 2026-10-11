package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

// 测试内存存储无法覆盖的 PostgreSQL 参数类型推断和时间戳持久化。
// 全部记录位于隔离测试数据库中。
func TestPostgresManagedConfigProbeTimestamps(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var tenantID, actorID string
	if err := db.QueryRowContext(ctx, `SELECT t.id::text,u.id::text FROM tenant t
JOIN app_user u ON u.tenant_id=t.id AND u.username='tenant_admin'
WHERE t.code='demo' AND t.deleted_at IS NULL AND u.deleted_at IS NULL`).Scan(&tenantID, &actorID); err != nil {
		t.Fatal(err)
	}
	cipher, err := modelgovernance.NewCredentialCipher("timestamp-regression-test-key-at-least-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	store := modelgovernance.NewPostgresStore(db, cipher)
	success := modelgovernance.ManagedAPIProbeResult{OK: true, ProbeMode: "quick", Message: "connected", LatencyMS: 12}
	failure := modelgovernance.ManagedAPIProbeResult{ProbeMode: "quick", ErrorCode: "credential_invalid", Message: "invalid credential"}
	fixture := managedConfigProbeFixture{TenantID: tenantID, ActorID: actorID, Success: success, Failure: failure}

	for _, tc := range []managedConfigProbeCase{
		{"untested", nil, "untested"},
		{"success", &success, "success"},
		{"failed", &failure, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertManagedConfigProbeLifecycle(t, ctx, store, fixture, tc)
		})
	}
}

type managedConfigProbeCase struct {
	name   string
	probe  *modelgovernance.ManagedAPIProbeResult
	status string
}

type managedConfigProbeFixture struct {
	TenantID, ActorID string
	Success, Failure  modelgovernance.ManagedAPIProbeResult
}

// 连接失败保留最后成功时间；真正修改连接参数时才清除此前探测事实。
func assertManagedConfigProbeLifecycle(t *testing.T, ctx context.Context, store *modelgovernance.PostgresStore, fixture managedConfigProbeFixture, tc managedConfigProbeCase) {
	t.Helper()
	tenantID, actorID := fixture.TenantID, fixture.ActorID
	success, failure := fixture.Success, fixture.Failure
	input := modelgovernance.ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "Timestamp test", AdapterType: "openai_compatible",
		BaseURL: "https://example.invalid/v1", ModelName: "timestamp-" + tc.name,
		ModelVersion: "v1", Region: "global", Status: "active",
		APIKey: "synthetic-timestamp-secret", InitialProbe: tc.probe,
	}
	item, err := store.CreateManagedAPIConfig(ctx, tenantID, actorID, input)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if item.LastTestStatus != tc.status || (item.LastTestedAt != nil) != (tc.probe != nil) ||
		(item.LastSuccessfulTestedAt != nil) != (tc.status == "success") {
		t.Fatalf("incorrect initial timestamps: status=%s tested=%v successful=%v", item.LastTestStatus, item.LastTestedAt, item.LastSuccessfulTestedAt)
	}
	connection, err := store.GetManagedAPIConnection(ctx, tenantID, item.ID)
	if err != nil || connection.APIKey != input.APIKey {
		t.Fatalf("saved credential did not round-trip: %v", err)
	}
	update := modelgovernance.ManagedAPIConfigUpdateInput{
		DisplayName: input.DisplayName, AdapterType: input.AdapterType, BaseURL: input.BaseURL,
		ModelName: input.ModelName, ModelVersion: input.ModelVersion, Region: input.Region,
		Status: input.Status, InitialProbe: &success,
	}
	item, err = store.UpdateManagedAPIConfig(ctx, tenantID, item.ID, update)
	if err != nil {
		t.Fatalf("update with successful probe: %v", err)
	}
	if item.LastTestedAt == nil || item.LastSuccessfulTestedAt == nil || !item.LastTestedAt.Equal(*item.LastSuccessfulTestedAt) {
		t.Fatal("successful update must persist matching test and success timestamps")
	}
	lastSuccess := *item.LastSuccessfulTestedAt
	item, err = store.RecordManagedAPIProbe(ctx, tenantID, item.ID, item.UpdatedAt, failure)
	if err != nil || item.LastTestStatus != "failed" || item.LastSuccessfulTestedAt == nil || !item.LastSuccessfulTestedAt.Equal(lastSuccess) {
		t.Fatalf("failed probe must preserve last success: %v", err)
	}
	update.InitialProbe = nil
	update.ModelName += "-changed"
	item, err = store.UpdateManagedAPIConfig(ctx, tenantID, item.ID, update)
	if err != nil || item.LastTestStatus != "untested" || item.LastTestedAt != nil || item.LastSuccessfulTestedAt != nil {
		t.Fatalf("changed connection must clear probe timestamps: %v", err)
	}
}

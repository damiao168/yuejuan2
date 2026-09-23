package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

// Exercise PostgreSQL parameter inference and timestamp persistence, which the
// in-memory store cannot cover. All records live in an isolated test database.
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
	for _, tc := range []struct {
		name   string
		probe  *modelgovernance.ManagedAPIProbeResult
		status string
	}{
		{"untested", nil, "untested"},
		{"success", &success, "success"},
		{"failed", &failure, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
		})
	}
}

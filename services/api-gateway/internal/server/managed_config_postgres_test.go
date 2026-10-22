package server

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/platformschools"
	"github.com/google/uuid"
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
		Status: input.Status, InitialProbe: &success, ExpectedUpdatedAt: item.UpdatedAt,
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
	update.ExpectedUpdatedAt = item.UpdatedAt
	update.ModelName += "-changed"
	item, err = store.UpdateManagedAPIConfig(ctx, tenantID, item.ID, update)
	if err != nil || item.LastTestStatus != "untested" || item.LastTestedAt != nil || item.LastSuccessfulTestedAt != nil {
		t.Fatalf("changed connection must clear probe timestamps: %v", err)
	}
}

// PostgreSQL 与内存共用生成证据规则，但参数绑定、NULL 扫描和更新时的保留逻辑需真实数据库验证。
func TestPostgresManagedConfigIndependentGenerationEvidence(t *testing.T) {
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
	if _, err := db.ExecContext(ctx, `INSERT INTO school(id,tenant_id,name,code,status)
VALUES($1::uuid,$2::uuid,'Probe evidence school','probe-evidence-school','active')`, uuid.NewString(), tenantID); err != nil {
		t.Fatalf("seed school for platform health projection: %v", err)
	}
	cipher, err := modelgovernance.NewCredentialCipher("independent-evidence-test-key-over-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	store := modelgovernance.NewPostgresStore(db, cipher)
	textProbe := modelgovernance.ManagedAPIProbeResult{
		OK: true, ProbeMode: "text", GeneratedRequest: true, Message: "text generated",
		TextCheck:  modelgovernance.ManagedAPICheckResult{OK: true, Message: "non-empty reply"},
		Usage:      modelgovernance.ManagedAPIProbeUsage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6},
		Diagnostic: modelgovernance.ManagedAPIProbeDiagnostic{ResponseFormat: "text", ContentLength: 2, ContentPreview: "OK"},
	}
	input := modelgovernance.ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "Independent evidence", AdapterType: "openai_compatible",
		BaseURL: "https://example.invalid/v1", ModelName: "independent-evidence", ModelVersion: "v1", Region: "global",
		Status: "active", IsDefault: true, APIKey: "synthetic-evidence-secret", InitialProbe: &textProbe,
	}
	item, err := store.CreateManagedAPIConfig(ctx, tenantID, actorID, input)
	if err != nil {
		t.Fatalf("create text evidence: %v", err)
	}
	if item.LastTextStatus != "success" || item.LastTextTestedAt == nil || item.LastTextUsage.TotalTokens != 6 ||
		item.LastTextDiagnostic.ResponseFormat != "text" || item.LastTextDiagnostic.ContentPreview != "" || item.LastCapabilityStatus != "untested" {
		t.Fatalf("incorrect initial text evidence: %+v", item)
	}
	if err := store.ValidateManagedProductionReadiness(ctx); err != nil {
		t.Fatalf("text-only default must satisfy production chat readiness: %v", err)
	}
	resolveDocument := newDocumentModelResolver(store)
	if _, err := resolveDocument(ctx, tenantID); !errors.Is(err, modelgovernance.ErrManagedCapabilityRequired) {
		t.Fatalf("text-only default must not bypass document structure gate: %v", err)
	}
	textTime := *item.LastTextTestedAt
	quick := modelgovernance.ManagedAPIProbeResult{OK: true, ProbeMode: "quick", Message: "listed models"}
	item, err = store.RecordManagedAPIProbe(ctx, tenantID, item.ID, item.UpdatedAt, quick)
	if err != nil || item.LastProbeMode != "quick" || item.LastTextTestedAt == nil || !item.LastTextTestedAt.Equal(textTime) ||
		item.LastTextStatus != "success" || item.LastTextUsage.TotalTokens != 6 || item.LastCapabilityTestedAt != nil {
		t.Fatalf("quick replaced generation evidence: item=%+v err=%v", item, err)
	}
	structured := textProbe
	structured.OK, structured.ProbeMode, structured.ErrorCode = false, "structured", "schema_mismatch"
	structured.Message = "invalid JSON fields"
	structured.CapabilityCheck = modelgovernance.ManagedAPICheckResult{Code: "schema_mismatch", Message: structured.Message}
	item, err = store.RecordManagedAPIProbe(ctx, tenantID, item.ID, item.UpdatedAt, structured)
	if err != nil || item.LastTextStatus != "success" || item.LastCapabilityStatus != "failed" ||
		item.LastCapabilityVersion != modelgovernance.ManagedCapabilityProbeVersion || item.LastCapabilityTestedAt == nil {
		t.Fatalf("structured failure erased successful text reply: item=%+v err=%v", item, err)
	}
	// 学校日常健康使用文本证据；结构化失败只影响解析和评分资格。
	schoolStore := platformschools.NewPostgresStore(db)
	health, err := schoolStore.GetModelHealth(ctx, tenantID)
	if err != nil || health.DefaultModel.Status != "healthy" || health.DefaultModel.TextStatus != "success" ||
		health.DefaultModel.CapabilityStatus != "failed" || health.DefaultModel.LastTextTestedAt == nil {
		t.Fatalf("school detail confused text and structured health: health=%+v err=%v", health, err)
	}
	summaries, err := schoolStore.ListSummaries(ctx, 7)
	if err != nil {
		t.Fatalf("school summaries query: %v", err)
	}
	found := false
	for _, summary := range summaries {
		if summary.TenantID == tenantID {
			found = true
			if summary.ModelHealth.ConfigID != item.ID || summary.ModelHealth.Status != "healthy" ||
				summary.ModelHealth.TextStatus != "success" || summary.ModelHealth.LastTextTestedAt == nil {
				t.Fatalf("school summary confused text and structured health: %+v", summary.ModelHealth)
			}
		}
	}
	if !found {
		t.Fatal("school summaries omitted the seeded school")
	}
	structured.OK, structured.ErrorCode = true, ""
	structured.CapabilityCheck = modelgovernance.ManagedAPICheckResult{OK: true}
	item, err = store.RecordManagedAPIProbe(ctx, tenantID, item.ID, item.UpdatedAt, structured)
	if err != nil || item.LastCapabilityStatus != "success" || item.LastTextTestedAt == nil || item.LastCapabilityTestedAt == nil {
		t.Fatalf("record structured success: item=%+v err=%v", item, err)
	}
	if document, err := resolveDocument(ctx, tenantID); err != nil || document == nil || document.ModelName != input.ModelName {
		t.Fatalf("structured default must resolve for document parsing: model=%+v err=%v", document, err)
	}
	textTime, capabilityTime := *item.LastTextTestedAt, *item.LastCapabilityTestedAt
	for _, mode := range []string{"quick", "text", "structured"} {
		item, err = store.RecordManagedAPIProbe(ctx, tenantID, item.ID, item.UpdatedAt, modelgovernance.ManagedAPIProbeResult{
			ProbeMode: mode, GeneratedRequest: mode != "quick", ErrorCode: "provider_rate_limited", StatusCode: 429, Message: "try later",
		})
		if err != nil || item.LastTestStatus != "temporary_unavailable" || item.LastTextStatus != "success" || item.LastCapabilityStatus != "success" ||
			item.LastTextTestedAt == nil || !item.LastTextTestedAt.Equal(textTime) ||
			item.LastCapabilityTestedAt == nil || !item.LastCapabilityTestedAt.Equal(capabilityTime) {
			t.Fatalf("%s transient failure replaced evidence: item=%+v err=%v", mode, item, err)
		}
	}
	if connection, err := modelgovernance.ResolveDefaultManagedAPI(ctx, store, tenantID); err != nil || connection == nil {
		t.Fatalf("temporary failure blocked independently verified default: %v", err)
	}
	update := modelgovernance.ManagedAPIConfigUpdateInput{
		DisplayName: "Renamed evidence model", AdapterType: input.AdapterType, BaseURL: input.BaseURL,
		ModelName: input.ModelName, ModelVersion: input.ModelVersion, Region: input.Region, Status: "active", IsDefault: true, ExpectedUpdatedAt: item.UpdatedAt,
	}
	item, err = store.UpdateManagedAPIConfig(ctx, tenantID, item.ID, update)
	if err != nil || item.LastTextStatus != "success" || item.LastCapabilityStatus != "success" ||
		item.LastTextTestedAt == nil || !item.LastTextTestedAt.Equal(textTime) {
		t.Fatalf("display-only edit replaced evidence: item=%+v err=%v", item, err)
	}
	update.ExpectedUpdatedAt, update.ModelName = item.UpdatedAt, input.ModelName+"-changed"
	item, err = store.UpdateManagedAPIConfig(ctx, tenantID, item.ID, update)
	if err != nil || item.LastTextStatus != "untested" || item.LastTextTestedAt != nil || item.LastTextUsage.TotalTokens != 0 ||
		item.LastCapabilityStatus != "untested" || item.LastCapabilityTestedAt != nil {
		t.Fatalf("identity change retained evidence: item=%+v err=%v", item, err)
	}
	update.ExpectedUpdatedAt, update.InitialProbe = item.UpdatedAt, &textProbe
	item, err = store.UpdateManagedAPIConfig(ctx, tenantID, item.ID, update)
	if err != nil || item.LastTextStatus != "success" || item.LastTextUsage.TotalTokens != 6 || item.LastCapabilityStatus != "untested" {
		t.Fatalf("text evidence update did not round-trip: item=%+v err=%v", item, err)
	}
}

func TestPostgresManagedTextEvidenceMigrationBackfill(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrationsThrough(t, db, "000186_exam_template_binding_revision.sql")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var tenantID string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM tenant WHERE code='demo' AND deleted_at IS NULL`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		id, mode, capability, version, wantText string
		isDefault                               bool
	}{
		{uuid.NewString(), "capability", "success", "structured-json-v3", "success", true},
		{uuid.NewString(), "capability", "success", "legacy-v1", "success", false},
		{uuid.NewString(), "quick", "untested", "", "untested", false},
	}
	for _, fixture := range fixtures {
		_, err := db.ExecContext(ctx, `INSERT INTO managed_model_api_config(
id,tenant_id,provider_key,display_name,adapter_type,base_url,model_name,model_version,region,
credential_ciphertext,credential_nonce,status,is_default,last_test_status,last_probe_mode,
last_capability_status,last_capability_probe_version,last_capability_tested_at,last_capability_usage)
VALUES($1::uuid,$2::uuid,'custom','Migration evidence','openai_compatible','https://example.invalid/v1',$1::text,'v1','global',
decode(repeat('ab',32),'hex'),decode(repeat('cd',12),'hex'),'active',$3,'success',$4,$5,$6,
CASE WHEN $5='success' THEN now() ELSE NULL END,'{"total_tokens":7}'::jsonb)`,
			fixture.id, tenantID, fixture.isDefault, fixture.mode, fixture.capability, fixture.version)
		if err != nil {
			t.Fatalf("seed pre-187 fixture: %v", err)
		}
	}
	e2eApplyPostgresMigrations(t, db)
	for _, fixture := range fixtures {
		var textStatus, capabilityVersion string
		var isDefault bool
		var textTime *time.Time
		var totalTokens *int
		if err := db.QueryRowContext(ctx, `SELECT last_text_status,last_capability_probe_version,is_default,
last_text_tested_at,(last_text_usage->>'total_tokens')::int FROM managed_model_api_config WHERE id=$1::uuid`, fixture.id).
			Scan(&textStatus, &capabilityVersion, &isDefault, &textTime, &totalTokens); err != nil {
			t.Fatal(err)
		}
		if textStatus != fixture.wantText || capabilityVersion != fixture.version || isDefault != fixture.isDefault ||
			(textTime != nil) != (fixture.wantText == "success") ||
			(fixture.wantText == "success" && (totalTokens == nil || *totalTokens != 7)) ||
			(fixture.wantText == "untested" && totalTokens != nil) {
			t.Fatalf("incorrect backfill for %s: text=%s version=%s default=%v time=%v tokens=%v", fixture.mode, textStatus, capabilityVersion, isDefault, textTime, totalTokens)
		}
	}
}

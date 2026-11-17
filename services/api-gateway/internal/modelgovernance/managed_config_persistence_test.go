package modelgovernance

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	sqlTenant  = "b356b67a-40fe-4236-bbba-ce71d2cb2691"
	sqlActor   = "bc7cbfa6-8a59-47ec-8ed9-53f3f494971a"
	sqlDefault = "2dabcbbd-8292-4399-8a6e-009a3154b83f"
	sqlOther   = "ae768747-7529-44d6-93ef-503e23365686"
)

func governanceSQLTime() time.Time {
	return time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
}

func governanceModelRow(id string, isDefault, isVerified bool) []driver.Value {
	now := governanceSQLTime()
	status := "untested"
	var evidenceAt, latency driver.Value
	if isVerified {
		status, evidenceAt, latency = "success", now, int64(21)
	}
	return []driver.Value{
		id, sqlTenant, "service", "校用模型", "openai_compatible", "https://example.test/v1", "model", "v1", "cn",
		true, "•••• tail", "active", isDefault, status, "connection", latency, evidenceAt, evidenceAt,
		"text", status, "", nil, "", []byte(`{}`), []byte(`{}`),
		status, "text", evidenceAt, []byte(`{"total_tokens":5}`), []byte(`{}`),
		[]byte(`["text"]`), "text", []byte(`{"currency":"CNY"}`), []byte(`{"training":false}`), "healthy",
		"manual", "", now, now,
	}
}

func governanceTestCipher(t *testing.T) *CredentialCipher {
	t.Helper()
	cipher, err := NewCredentialCipher("governance-fixture-master-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestPostgresManagedModelListAndConnectionKeepCredentialsTenantBound(t *testing.T) {
	cipher := governanceTestCipher(t)
	// 固定密文夹具让两种绑定可重复验证。真实写入依然使用随机 nonce。
	nonce := make([]byte, cipher.aead.NonceSize())
	secret := "fixture-secret-tail"
	boundCiphertext := cipher.aead.Seal(nil, nonce, []byte(secret), []byte("edugrade:model-api:v1:"+sqlTenant+":"+sqlDefault))
	unboundCiphertext := cipher.aead.Seal(nil, nonce, []byte(secret), []byte("edugrade:model-api:v1:"+sqlTenant+":"+sqlOther))
	row := governanceModelRow(sqlDefault, true, true)
	t.Run("tenant inventory contains metadata without secret", func(t *testing.T) {
		store, script := openGovernanceSQL(t, nil, governanceSQLStep{
			kind: "query", fragment: "WHERE tenant_id=$1::uuid AND deleted_at IS NULL", args: []any{sqlTenant},
			columns: len(row), rows: [][]driver.Value{row},
		})
		items, err := store.ListManagedAPIConfigs(context.Background(), sqlTenant)
		if err != nil || len(items) != 1 {
			t.Fatalf("inventory len=%d: %v", len(items), err)
		}
		got := items[0]
		if got.ID != sqlDefault || got.TenantID != sqlTenant || !got.IsDefault || got.CredentialHint != "•••• tail" ||
			got.LastTestLatencyMS != 21 || got.LastTextUsage.TotalTokens != 5 || !reflect.DeepEqual(got.Modalities, []string{"text"}) ||
			got.DataPolicy["training"] != false || got.PricingPolicy["currency"] != "CNY" {
			t.Fatalf("inventory item lost metadata: %+v", got)
		}
		script.requireDone(t)
	})
	t.Run("malformed storage policy cannot produce a publishable inventory", func(t *testing.T) {
		badRow := append([]driver.Value(nil), row...)
		badRow[33] = []byte(`[`)
		store, script := openGovernanceSQL(t, nil, governanceSQLStep{
			kind: "query", fragment: "ORDER BY is_default DESC", args: []any{sqlTenant},
			columns: len(row), rows: [][]driver.Value{row, badRow},
		})
		got, err := store.ListManagedAPIConfigs(context.Background(), sqlTenant)
		if err == nil || got != nil {
			t.Fatalf("invalid data policy returned inventory = %+v, error = %v", got, err)
		}
		script.requireDone(t)
	})
	for _, tc := range []struct {
		name       string
		ciphertext []byte
		want       error
	}{
		{"credential and config from same row", boundCiphertext, nil},
		{"ciphertext moved from another config", unboundCiphertext, ErrManagedConfigUnavailable},
		{"no record in tenant", nil, ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := governanceSQLStep{kind: "query", fragment: "credential_ciphertext,credential_nonce", args: []any{sqlTenant, sqlDefault}, columns: len(row) + 2}
			if tc.ciphertext != nil {
				step.rows = [][]driver.Value{append(append([]driver.Value(nil), row...), tc.ciphertext, nonce)}
			}
			store, script := openGovernanceSQL(t, cipher, step)
			got, err := store.GetManagedAPIConnection(context.Background(), sqlTenant, sqlDefault)
			if !errors.Is(err, tc.want) {
				t.Fatalf("connection error=%v, want %v", err, tc.want)
			}
			if tc.want == nil {
				if got.Config.ID != sqlDefault || got.Config.TenantID != sqlTenant || got.APIKey != secret {
					t.Fatal("connection didn't return credentials from the requested source")
				}
			} else if got.Config.ID != "" || got.APIKey != "" {
				t.Fatal("rejected connection exposed config or credentials")
			}
			script.requireDone(t)
		})
	}
}

func TestPostgresManagedProbePersistsOnlyVersionMatchedEvidence(t *testing.T) {
	failure := errors.New("probe write unavailable")
	longMessage := strings.Repeat("测", 100)
	for _, tc := range []struct {
		name, mode             string
		records, writeEvidence bool
		want                   error
	}{
		{"structured success records both evidence sets", "structured", true, true, nil},
		{"quick probe leaves text and structure evidence alone", "quick", true, false, nil},
		{"stale result cannot overwrite a changed configuration", "structured", false, true, ErrManagedProbeStale},
		{"write failure is observable", "quick", false, false, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := ManagedAPIProbeResult{
				OK: true, ProbeMode: tc.mode, GeneratedRequest: tc.writeEvidence, LatencyMS: 21, Message: longMessage,
				TextCheck: ManagedAPICheckResult{OK: tc.writeEvidence}, CapabilityCheck: ManagedAPICheckResult{OK: tc.writeEvidence},
				Usage: ManagedAPIProbeUsage{TotalTokens: 5}, Diagnostic: ManagedAPIProbeDiagnostic{ContentPreview: "private sample"},
			}
			row := governanceModelRow(sqlDefault, true, true)
			step := governanceSQLStep{kind: "query", fragment: "AND updated_at=$3 AND deleted_at IS NULL", columns: len(row), check: func(args []any) error {
				if len(args) != 21 || args[0] != sqlTenant || args[1] != sqlDefault || args[2] != governanceSQLTime() ||
					args[3] != "success" || args[5] != int64(21) || args[7] != tc.mode || args[8] != tc.writeEvidence || args[15] != tc.writeEvidence {
					return fmt.Errorf("probe write has incorrect scope, expected version or evidence flags")
				}
				message, ok := args[4].(string)
				if !ok || message != strings.Repeat("测", 85) || !utf8.ValidString(message) {
					return errors.New("probe message did not preserve UTF-8 size bound")
				}
				if tc.writeEvidence && (args[9] != "success" || args[12] != ManagedCapabilityProbeVersion || args[16] != "success" ||
					!strings.Contains(fmt.Sprint(args[13]), `"total_tokens":5`) || !strings.Contains(fmt.Sprint(args[19]), `"total_tokens":5`) ||
					strings.Contains(fmt.Sprint(args[14], args[20]), "private sample")) {
					return errors.New("structured evidence lost usage or included private model output")
				}
				return nil
			}}
			if tc.records {
				step.rows = [][]driver.Value{row}
			}
			if tc.want == failure {
				step.err = failure
			}
			store, script := openGovernanceSQL(t, nil, step)
			got, err := store.RecordManagedAPIProbe(context.Background(), sqlTenant, sqlDefault, governanceSQLTime(), probe)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			if tc.want == nil {
				if got.ID != sqlDefault || got.LastTextUsage.TotalTokens != 5 || got.LastTestStatus != "success" {
					t.Fatalf("stored probe result missing metadata: %+v", got)
				}
			} else if got.ID != "" || got.LastTestStatus != "" {
				t.Fatalf("failed probe returned a stored configuration: %+v", got)
			}
			script.requireDone(t)
		})
	}
}

func TestPostgresManagedConfigUpdateCommitsDefaultAndCredentialsTogether(t *testing.T) {
	cipher := governanceTestCipher(t)
	failure := errors.New("model update failed")
	now := governanceSQLTime()
	input := ManagedAPIConfigUpdateInput{DisplayName: "校用模型", AdapterType: "openai_compatible", BaseURL: "https://example.test/v1", ModelName: "model", ModelVersion: "v1", Status: "active", IsDefault: true, ExpectedUpdatedAt: now}
	t.Run("probe without a captured revision never opens a transaction", func(t *testing.T) {
		badInput := input
		badInput.ExpectedUpdatedAt = time.Time{}
		badInput.InitialProbe = &ManagedAPIProbeResult{OK: true, ProbeMode: "quick"}
		store, script := openGovernanceSQL(t, cipher)
		if got, err := store.UpdateManagedAPIConfig(context.Background(), sqlTenant, sqlOther, badInput); !errors.Is(err, ErrManagedProbeStale) || got.ID != "" {
			t.Fatalf("unversioned update = %+v, error=%v", got, err)
		}
		script.requireDone(t)
	})
	for _, tc := range []struct {
		name, secret, stopAfter string
		want                    error
	}{
		{"existing credential preserved if omitted", "", "", nil},
		{"new credential remains tenant and config bound", "fixture-replace-tail", "", nil},
		{"inherited access write failure leaves default untouched", "", "access", failure},
		{"default switch failure rolls back inherited access", "", "default", failure},
		{"stale version rolls back clearing previous default", "", "update", ErrManagedProbeStale},
		{"commit failure is returned", "", "commit", failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			update := input
			update.APIKey = tc.secret
			steps := []governanceSQLStep{
				{kind: "begin"},
				{kind: "query", fragment: "ORDER BY id FOR UPDATE", args: []any{sqlTenant}, columns: 3, rows: [][]driver.Value{{sqlDefault, true, "previous-actor"}, {sqlOther, false, sqlActor}}},
				{kind: "query", fragment: "SELECT EXISTS(SELECT 1 FROM tenant_chat_model_access", args: []any{sqlTenant}, columns: 1, rows: [][]driver.Value{{false}}},
				{kind: "exec", fragment: "pg_advisory_xact_lock", args: []any{sqlTenant}},
				{kind: "exec", fragment: "ON CONFLICT(tenant_id) DO NOTHING", args: []any{sqlTenant, `["` + sqlDefault + `"]`, "previous-actor"}},
				{kind: "exec", fragment: "AND id<>$2::uuid AND is_default", args: []any{sqlTenant, sqlOther}},
			}
			row := governanceModelRow(sqlOther, true, false)
			row[38] = now.Add(time.Microsecond)
			write := governanceSQLStep{
				kind: "query", fragment: "OR updated_at=$27::timestamptz", columns: len(row),
				check: func(args []any) error {
					if len(args) != 33 || args[0] != sqlTenant || args[1] != sqlOther || args[26] != now || args[12] != true {
						return errors.New("model update has incorrect scope, default or expected version")
					}
					if tc.secret == "" {
						if args[8] != nil || args[9] != nil {
							return errors.New("omitted credentials must not replace stored credentials")
						}
					} else {
						ciphertext, cipherOK := args[8].([]byte)
						nonce, nonceOK := args[9].([]byte)
						got, err := cipher.Decrypt(ciphertext, nonce, sqlTenant, sqlOther)
						if !cipherOK || !nonceOK || err != nil || got != tc.secret || args[10] != "•••• tail" {
							return errors.New("replacement credential is not encrypted and bound to its scope")
						}
					}
					return nil
				},
			}
			switch tc.stopAfter {
			case "access":
				steps = steps[:5]
				steps[4].err = failure
			case "default":
				steps[5].err = failure
			case "update":
				steps = append(steps, write)
			default:
				write.rows = [][]driver.Value{row}
				steps = append(steps, write)
			}
			end := governanceSQLStep{kind: "commit"}
			if tc.stopAfter == "commit" {
				end.err = failure
			} else if tc.want != nil {
				end.kind = "rollback"
			}
			steps = append(steps, end)
			store, script := openGovernanceSQL(t, cipher, steps...)
			got, err := store.UpdateManagedAPIConfig(context.Background(), sqlTenant, sqlOther, update)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			if tc.want == nil {
				if got.ID != sqlOther || !got.IsDefault || got.CredentialHint != "•••• tail" || !got.UpdatedAt.After(now) {
					t.Fatalf("updated model = %+v", got)
				}
			} else if got.ID != "" || got.IsDefault {
				t.Fatalf("failed update returned a default config: %+v", got)
			}
			script.requireDone(t)
		})
	}
}

func TestPostgresManagedConfigCreateKeepsDefaultPromotionAtomic(t *testing.T) {
	cipher := governanceTestCipher(t)
	failure := errors.New("model creation failed")
	input := ManagedAPIConfigInput{TenantID: "ignored", ProviderKey: "service", DisplayName: "校用模型", AdapterType: "openai_compatible",
		BaseURL: "https://example.test/v1", ModelName: "model", ModelVersion: "v1", APIKey: "fixture-create-tail", Status: "active", IsDefault: true}
	for _, tc := range []struct {
		name              string
		cipher            *CredentialCipher
		internalErr, want error
	}{
		{"service without a credential cipher", nil, nil, ErrManagedConfigUnavailable},
		{"new config and default promotion commit", cipher, nil, nil},
		{"failed insert rolls back previous default change", cipher, failure, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var steps []governanceSQLStep
			var createdID string
			if tc.cipher != nil {
				row := governanceModelRow("", true, false)
				end := "commit"
				if tc.want != nil {
					end = "rollback"
				}
				steps = []governanceSQLStep{
					{kind: "begin"},
					{kind: "query", fragment: "ORDER BY id FOR UPDATE", args: []any{sqlTenant}, columns: 3, rows: [][]driver.Value{{sqlDefault, true, sqlActor}}},
					{kind: "query", fragment: "SELECT EXISTS(SELECT 1 FROM tenant_chat_model_access", args: []any{sqlTenant}, columns: 1, rows: [][]driver.Value{{true}}},
					{kind: "exec", fragment: "AND is_default AND deleted_at IS NULL", args: []any{sqlTenant}},
					{kind: "query", fragment: "INSERT INTO managed_model_api_config", columns: len(row), rows: [][]driver.Value{row}, err: tc.internalErr, check: func(args []any) error {
						if len(args) != 33 || args[1] != sqlTenant || args[2] != "service" || args[12] != "active" || args[13] != true || args[27] != sqlActor {
							return errors.New("insert forgot tenant, actor, provider or default status")
						}
						id, idOK := args[0].(string)
						ciphertext, cipherOK := args[9].([]byte)
						nonce, nonceOK := args[10].([]byte)
						got, err := cipher.Decrypt(ciphertext, nonce, sqlTenant, id)
						if !idOK || uuid.Validate(id) != nil || !cipherOK || !nonceOK || err != nil || got != input.APIKey || args[11] != "•••• tail" {
							return errors.New("created model credentials were not encrypted and bound to their generated ID")
						}
						// 仅回显实际生成的 ID，断言不依赖随机值。
						createdID, row[0] = id, id
						return nil
					}},
					{kind: end},
				}
			}
			store, script := openGovernanceSQL(t, tc.cipher, steps...)
			got, err := store.CreateManagedAPIConfig(context.Background(), sqlTenant, sqlActor, input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("create error=%v, want %v", err, tc.want)
			}
			if tc.want == nil && (got.ID == "" || got.ID != createdID || got.TenantID != sqlTenant || !got.IsDefault) {
				t.Fatalf("created configuration = %+v", got)
			}
			if tc.want != nil && (got.ID != "" || got.IsDefault) {
				t.Fatalf("creation error returned persisted model: %+v", got)
			}
			script.requireDone(t)
		})
	}
}

func TestPostgresManagedConfigDeleteChecksDefaultAndReferencesUnderLock(t *testing.T) {
	failure := errors.New("delete failed")
	for _, tc := range []struct {
		name                     string
		exists, isDefault, inUse bool
		writeErr, want           error
	}{
		{"default cannot be removed", true, true, false, nil, ErrManagedDefaultMutation},
		{"active reference blocks removal", true, false, true, nil, ErrManagedConfigInUse},
		{"absent in this tenant", false, false, false, nil, ErrNotFound},
		{"unreferenced config is soft deleted", true, false, false, nil, nil},
		{"soft-delete error rolls back", true, false, false, failure, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := governanceSQLStep{kind: "query", fragment: "deleted_at IS NULL FOR UPDATE", args: []any{sqlTenant, sqlOther}, columns: 1}
			if tc.exists {
				first.rows = [][]driver.Value{{tc.isDefault}}
			}
			steps := []governanceSQLStep{{kind: "begin"}, first}
			if tc.exists && !tc.isDefault {
				steps = append(steps, governanceSQLStep{kind: "query", fragment: "FROM tenant_document_model_selection", args: []any{sqlTenant, sqlOther}, columns: 1, rows: [][]driver.Value{{tc.inUse}}})
				if !tc.inUse {
					steps = append(steps, governanceSQLStep{kind: "exec", fragment: "SET deleted_at=now(),deleted_by=$3::uuid", args: []any{sqlTenant, sqlOther, sqlActor}, err: tc.writeErr})
				}
			}
			end := "commit"
			if tc.want != nil {
				end = "rollback"
			}
			steps = append(steps, governanceSQLStep{kind: end})
			store, script := openGovernanceSQL(t, nil, steps...)
			if err := store.DeleteManagedAPIConfig(context.Background(), sqlTenant, sqlActor, sqlOther); !errors.Is(err, tc.want) {
				t.Fatalf("delete error=%v, want %v", err, tc.want)
			}
			script.requireDone(t)
		})
	}
}

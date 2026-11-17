package subjective

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	database "edugrade-enterprise/services/api-gateway/internal/db"
	"github.com/google/uuid"
)

const receiptTenant = "bd936ed3-1d08-49cf-a5a8-5e48217a92e2"

func TestMaterialPermitReplayNeverReplacesConsumedOrChangedEvidence(t *testing.T) {
	digest, wrapper := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, scenario := range []string{"identical", "consumed", "expired", "request changed", "wrapper changed", "media changed", "authorization revoked"} {
		t.Run(scenario, func(t *testing.T) {
			var consumed driver.Value
			oldDigest, oldWrapper, sameMedia, expired := digest, wrapper, true, false
			switch scenario {
			case "consumed":
				consumed = time.Now()
			case "expired":
				expired = true
			case "request changed":
				oldDigest = strings.Repeat("c", 64)
			case "wrapper changed":
				oldWrapper = strings.Repeat("c", 64)
			case "media changed":
				sameMedia = false
			}
			steps := []imageSQLStep{{kind: "begin"}, imageQuery("FROM subjective_grading_run", "run-1", "manifest-1", "manifest-hash", int64(2), "processing"), imageQuery("FROM question_reference_manifest", scenario != "authorization revoked")}
			if scenario != "authorization revoked" {
				steps = append(steps, imageQuery("FROM subjective_request_material_receipt", "permit-1", "attempt-1", consumed, oldDigest, oldWrapper, sameMedia, expired))
			}
			end := "rollback"
			if scenario == "identical" {
				end = "commit"
			}
			steps = append(steps, imageSQLStep{kind: end})
			store := NewMaterialReceiptStore(imageTestDB(t, steps...), strings.Repeat("s", 32))
			permit, err := store.Prepare(t.Context(), receiptTenant, "request-1", digest, wrapper, []ReferenceMedia{{MediaID: "media-1"}})
			if scenario == "identical" {
				if err != nil || permit.RunID != "run-1" || permit.PermitID != "permit-1" || permit.ExecutionAttemptID != "attempt-1" || permit.Credential != store.credential(receiptTenant, "run-1", "permit-1") {
					t.Fatalf("replay replaced its original execution identity: %+v, %v", permit, err)
				}
				return
			}
			code := "material_execution_conflict"
			if scenario == "consumed" || scenario == "expired" {
				code = "material_dispatch_unknown"
			} else if scenario == "authorization revoked" {
				code = "material_authorization_changed"
			}
			assertGradingAgentV2ErrorCode(t, err, code)
			if permit.Credential != "" {
				t.Fatal("rejected replay exposed a usable credential")
			}
		})
	}
}

func TestMaterialPermitCreationFreezesSanitizedMediaAndRollsBackWriteFailure(t *testing.T) {
	digest, wrapper := strings.Repeat("a", 64), strings.Repeat("b", 64)
	media := ReferenceMedia{MediaID: "media-1", Role: "reference", Ordinal: 1, SHA256: strings.Repeat("c", 64), WidthPixels: 10, HeightPixels: 20, ByteSize: 123, Data: []byte("private-image-bytes"), Encoding: "private-encoding", BindingHash: "private-binding"}
	for _, scenario := range []string{"created", "receipt write failed", "run write failed"} {
		t.Run(scenario, func(t *testing.T) {
			injected := errors.New("private database write error")
			var recordedPermit, recordedAttempt string
			insert := imageSQLStep{kind: "exec", contains: "INSERT INTO subjective_request_material_receipt", check: func(_ string, args []driver.NamedValue) error {
				recordedPermit, recordedAttempt = args[0].Value.(string), args[3].Value.(string)
				if _, err := uuid.Parse(recordedPermit); err != nil {
					return err
				}
				if _, err := uuid.Parse(recordedAttempt); err != nil {
					return err
				}
				raw := args[10].Value.([]byte)
				var saved []map[string]any
				if err := json.Unmarshal(raw, &saved); err != nil {
					return err
				}
				if len(saved) != 1 || len(saved[0]) != 7 || saved[0]["media_id"] != media.MediaID || saved[0]["sha256"] != media.SHA256 || saved[0]["byte_size"] != float64(123) || strings.Contains(string(raw), "private") {
					return fmt.Errorf("receipt included raw media or lost evidence: %s", raw)
				}
				if args[1].Value != receiptTenant || args[2].Value != "run-1" || args[4].Value != "request-1" || args[5].Value != digest || args[6].Value != "manifest-1" || args[7].Value != "manifest-hash" || args[8].Value != int64(2) || args[9].Value != wrapper {
					return errors.New("receipt identity or frozen version changed")
				}
				return nil
			}}
			update := imageSQLStep{kind: "exec", contains: "SET math_wrapper_hash", check: func(_ string, args []driver.NamedValue) error {
				if args[0].Value != receiptTenant || args[1].Value != "run-1" || args[2].Value != wrapper {
					return errors.New("run wrapper is not bound to the prepared receipt")
				}
				return nil
			}}
			steps := []imageSQLStep{{kind: "begin"}, imageQuery("FROM subjective_grading_run", "run-1", "manifest-1", "manifest-hash", int64(2), "processing"), imageQuery("FROM question_reference_manifest", true), imageQuery("FROM subjective_request_material_receipt")}
			if scenario == "receipt write failed" {
				insert.err = injected
			}
			steps = append(steps, insert)
			if scenario != "receipt write failed" {
				if scenario == "run write failed" {
					update.err = injected
				}
				steps = append(steps, update)
			}
			end := "rollback"
			if scenario == "created" {
				end = "commit"
			}
			steps = append(steps, imageSQLStep{kind: end})
			store := NewMaterialReceiptStore(imageTestDB(t, steps...), strings.Repeat("s", 32))
			permit, err := store.Prepare(t.Context(), receiptTenant, "request-1", digest, wrapper, []ReferenceMedia{media})
			if scenario == "created" {
				if err != nil || permit.PermitID != recordedPermit || permit.ExecutionAttemptID != recordedAttempt || permit.Credential != store.credential(receiptTenant, "run-1", recordedPermit) {
					t.Fatalf("created execution identity does not match persisted evidence: %+v %v", permit, err)
				}
			} else if !errors.Is(err, injected) {
				t.Fatalf("write failure was hidden: %v", err)
			}
		})
	}
}

func materialReceiptRequest(store *MaterialReceiptStore, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/material-receipts/run-1/permit-1", strings.NewReader(body))
	r.SetPathValue("runId", "run-1")
	r.SetPathValue("permitId", "permit-1")
	r.Header.Set("Authorization", "Bearer "+store.credential(receiptTenant, "run-1", "permit-1"))
	return r
}

func materialReceiptBody(stage, digest, wire string) string {
	return fmt.Sprintf(`{"stage":%q,"request_binding_hash":%q,"wire_payload_hash":%q,"usage":{"input_tokens":12,"output_tokens":-1,"total_tokens":0,"arbitrary_private_field":99},"usage_state":"reported"}`, stage, digest, wire)
}

func TestMaterialReceiptRejectsCrossRunCredentialsAndMalformedBodiesBeforeDatabase(t *testing.T) {
	for _, scenario := range []string{"wrong run", "wrong tenant", "no bearer", "unknown field", "trailing value", "invalid digest", "invalid stage"} {
		t.Run(scenario, func(t *testing.T) {
			store := NewMaterialReceiptStore(imageTestDB(t), strings.Repeat("s", 32))
			body := materialReceiptBody("provider_response_received", strings.Repeat("a", 64), strings.Repeat("b", 64))
			if scenario == "unknown field" {
				body = strings.TrimSuffix(body, "}") + `,"tenant_id":"forged"}`
			} else if scenario == "trailing value" {
				body += `{}`
			} else if scenario == "invalid digest" {
				body = materialReceiptBody("consume", "bad", strings.Repeat("b", 64))
			} else if scenario == "invalid stage" {
				body = materialReceiptBody("prepared", strings.Repeat("a", 64), strings.Repeat("b", 64))
			}
			r := materialReceiptRequest(store, body)
			want := http.StatusBadRequest
			switch scenario {
			case "wrong run":
				r.SetPathValue("runId", "other-run")
				want = http.StatusUnauthorized
			case "wrong tenant":
				r.Header.Set("Authorization", strings.Replace(r.Header.Get("Authorization"), receiptTenant, "1d3eb4a1-d69a-415c-b4c3-7695dca0b39a", 1))
				want = http.StatusUnauthorized
			case "no bearer":
				r.Header.Del("Authorization")
				want = http.StatusUnauthorized
			}
			w := httptest.NewRecorder()
			store.Callback(w, r)
			if w.Code != want {
				t.Fatalf("invalid receipt accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestMaterialReceiptAfterSendRequiresConsumedExactWireAndSanitizesUsage(t *testing.T) {
	digest, wire := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, scenario := range []string{"accepted", "usage unavailable", "not consumed", "wire changed", "digest changed", "write failed", "commit failed"} {
		t.Run(scenario, func(t *testing.T) {
			var consumed driver.Value = time.Now()
			storedDigest, storedWire := digest, wire
			switch scenario {
			case "not consumed":
				consumed = nil
			case "wire changed":
				storedWire = strings.Repeat("c", 64)
			case "digest changed":
				storedDigest = strings.Repeat("c", 64)
			}
			steps := []imageSQLStep{{kind: "begin"}, imageQuery("p.request_digest,p.consumed_at,p.wire_payload_hash", storedDigest, consumed, storedWire)}
			want := http.StatusConflict
			if scenario == "accepted" || scenario == "usage unavailable" || scenario == "write failed" || scenario == "commit failed" {
				insert := imageSQLStep{kind: "exec", contains: "INSERT INTO subjective_request_material_receipt", check: func(_ string, args []driver.NamedValue) error {
					wantState := "reported"
					if scenario == "usage unavailable" {
						wantState = "unavailable"
					}
					var usage map[string]int64
					if err := json.Unmarshal(args[3].Value.([]byte), &usage); err != nil {
						return err
					}
					if args[0].Value != receiptTenant || args[1].Value != "permit-1" || args[2].Value != "provider_response_received" || args[4].Value != wantState || !reflect.DeepEqual(usage, map[string]int64{"input_tokens": 12, "total_tokens": 0}) {
						return fmt.Errorf("receipt usage identity or filtering changed: %v %v", args, usage)
					}
					return nil
				}}
				if scenario == "write failed" {
					insert.err = errors.New("private database failure")
				}
				steps = append(steps, insert)
				if scenario != "write failed" {
					commit := imageSQLStep{kind: "commit"}
					if scenario == "commit failed" {
						commit.err = errors.New("private commit failure")
					} else {
						want = http.StatusOK
					}
					steps = append(steps, commit)
				}
			}
			if scenario == "not consumed" || scenario == "wire changed" || scenario == "digest changed" || scenario == "write failed" {
				steps = append(steps, imageSQLStep{kind: "rollback"})
			}
			store := NewMaterialReceiptStore(imageTestDB(t, steps...), strings.Repeat("s", 32))
			body := materialReceiptBody("provider_response_received", digest, wire)
			if scenario == "usage unavailable" {
				body = strings.Replace(body, `"usage_state":"reported"`, `"usage_state":"forged"`, 1)
			}
			w := httptest.NewRecorder()
			store.Callback(w, materialReceiptRequest(store, body))
			if w.Code != want || strings.Contains(w.Body.String(), "private") || want == http.StatusOK && !strings.Contains(w.Body.String(), `"accepted":true`) {
				t.Fatalf("late receipt result: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestMaterialReceiptConsumeRechecksAuthorizationAndAssetsBeforeFreezingWire(t *testing.T) {
	digest, wire, wrapper := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	for _, scenario := range []string{"accepted", "admission denied", "run finished", "manifest changed", "asset quarantined", "asset deleted"} {
		t.Run(scenario, func(t *testing.T) {
			steps := []imageSQLStep{imageQuery("SELECT wrapper_hash", wrapper)}
			if scenario != "admission denied" {
				status := "processing"
				if scenario == "run finished" {
					status = "succeeded"
				}
				steps = append(steps, imageSQLStep{kind: "begin"}, imageQuery("p.request_digest,p.consumed_at,p.wire_payload_hash", digest, nil, nil), imageQuery("SELECT status FROM subjective_grading_run", status))
				if scenario != "run finished" {
					manifest := imageQuery("FOR SHARE OF m", "manifest-1")
					if scenario == "manifest changed" {
						manifest.values = nil
					}
					steps = append(steps, manifest)
					if scenario != "manifest changed" {
						state := "active"
						if scenario == "asset quarantined" {
							state = "quarantined"
						}
						steps = append(steps, imageQuery("ORDER BY f.id FOR SHARE", state, scenario == "asset deleted"))
					}
				}
				if scenario == "accepted" {
					// 模拟边界返回有效行；测试冻结值及事务顺序，不宣称替代 PostgreSQL 并发验收。
					steps = append(steps, imageSQLStep{kind: "exec", contains: "SET consumed_at=now(),wire_payload_hash=$3", check: func(_ string, args []driver.NamedValue) error {
						if args[0].Value != receiptTenant || args[1].Value != "permit-1" || args[2].Value != wire {
							return errors.New("consumption persisted a different wire identity")
						}
						return nil
					}}, imageSQLStep{kind: "commit"})
				} else {
					steps = append(steps, imageSQLStep{kind: "rollback"})
				}
			}
			calls := 0
			store := NewMaterialReceiptStore(imageTestDB(t, steps...), strings.Repeat("s", 32)).WithImageAdmission(func(ctx context.Context, tenant, run, actualWrapper string) error {
				calls++
				boundTenant, ok := database.TenantFromContext(ctx)
				if !ok || boundTenant != receiptTenant || tenant != receiptTenant || run != "run-1" || actualWrapper != wrapper {
					t.Fatal("admission scope was not derived from the signed permit and frozen wrapper")
				}
				if scenario == "admission denied" {
					return errors.New("private admission denied")
				}
				return nil
			})
			w := httptest.NewRecorder()
			store.Callback(w, materialReceiptRequest(store, materialReceiptBody("consume", digest, wire)))
			want := http.StatusConflict
			if scenario == "accepted" {
				want = http.StatusOK
			} else if scenario == "admission denied" {
				want = http.StatusForbidden
			}
			if w.Code != want || calls != 1 || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("consume boundary: %d %s, admission calls=%d", w.Code, w.Body.String(), calls)
			}
		})
	}
}

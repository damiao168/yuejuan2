package modelgovernance

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

const selectionRevision = "6037b85e-738a-48e5-9b34-ae55af1ef8e0"

func TestPostgresChatAccessReadsExplicitOrInheritedTenantSelection(t *testing.T) {
	for _, tc := range []struct {
		name, revision string
		raw            []byte
		absent         bool
		corrupt        bool
	}{
		{name: "explicit allowlist", revision: selectionRevision, raw: []byte(`["` + sqlOther + `"]`)},
		{name: "old school inherits only its default", absent: true},
		{name: "invalid persisted allowlist", revision: selectionRevision, raw: []byte(`{"model_ids":true}`), corrupt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := governanceSQLStep{kind: "query", fragment: "FROM tenant_chat_model_access WHERE tenant_id=$1::uuid", args: []any{sqlTenant}, columns: 2}
			if !tc.absent {
				step.rows = [][]driver.Value{{tc.revision, tc.raw}}
			}
			steps := []governanceSQLStep{step}
			if tc.absent {
				steps = append(steps, governanceSQLStep{kind: "query", fragment: "WHERE tenant_id=$1::uuid AND deleted_at IS NULL",
					args: []any{sqlTenant}, columns: 39, rows: [][]driver.Value{governanceModelRow(sqlDefault, true, true), governanceModelRow(sqlOther, false, true)}})
			}
			store, script := openGovernanceSQL(t, nil, steps...)
			got, err := store.GetChatModelAccess(t.Context(), sqlTenant)
			if tc.corrupt {
				var decodeErr *json.UnmarshalTypeError
				if !errors.As(err, &decodeErr) || len(got.ModelIDs) != 0 {
					t.Fatalf("corrupt allowlist = %+v, %v", got, err)
				}
			} else {
				want := ChatModelAccess{Revision: selectionRevision, ModelIDs: []string{sqlOther}}
				if tc.absent {
					want = ChatModelAccess{Revision: "absent", ModelIDs: []string{sqlDefault}}
				}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("chat access = %+v, %v; want %+v", got, err, want)
				}
			}
			script.requireDone(t)
		})
	}
}

func TestPostgresChatAccessSaveChecksRevisionAndEligibilityBeforeWriting(t *testing.T) {
	failure := errors.New("chat access persistence unavailable")
	for _, tc := range []struct {
		name, stopAt     string
		absent, disabled bool
		keepExisting     bool
		foreign          bool
		wantErr          error
	}{
		{name: "new eligible member committed"},
		{name: "first save materializes inherited settings", absent: true},
		{name: "stale revision rolls back", stopAt: "revision", wantErr: ErrChatAccessChanged},
		{name: "foreign model never enters tenant allowlist", foreign: true, wantErr: ErrManagedConfigUnavailable},
		{name: "new disabled model rejected", disabled: true, wantErr: ErrManagedTextRequired},
		{name: "previously allowed disabled model may remain for later repair", disabled: true, keepExisting: true},
		{name: "inventory stream error cannot publish partial list", stopAt: "inventory", wantErr: failure},
		{name: "insert failure rolls back", stopAt: "insert", wantErr: failure},
		{name: "commit failure returned", stopAt: "commit", wantErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := governanceModelRow(sqlOther, false, true)
			if tc.disabled {
				other[11] = "disabled"
			}
			inventory := governanceSQLStep{kind: "query", fragment: "WHERE tenant_id=$1::uuid AND deleted_at IS NULL ORDER BY id FOR SHARE",
				args: []any{sqlTenant}, columns: 39, rows: [][]driver.Value{governanceModelRow(sqlDefault, true, true), other}}
			if tc.stopAt == "inventory" {
				inventory.streamErr = failure
			}
			steps := []governanceSQLStep{{kind: "begin"}, inventory}
			input := ChatModelAccessInput{ExpectedRevision: selectionRevision, ModelIDs: []string{sqlOther}}
			if tc.absent {
				input.ExpectedRevision = "absent"
			}
			if tc.foreign {
				input.ModelIDs = []string{sqlActor}
			}
			currentIDs := []string{sqlDefault}
			if tc.keepExisting {
				currentIDs = []string{sqlOther}
			}
			if tc.stopAt != "inventory" {
				raw, err := json.Marshal(currentIDs)
				if err != nil {
					t.Fatal(err)
				}
				current := governanceSQLStep{kind: "query", fragment: "tenant_id=$1::uuid FOR UPDATE", args: []any{sqlTenant}, columns: 2}
				if !tc.absent {
					revision := selectionRevision
					if tc.stopAt == "revision" {
						revision = sqlOther
					}
					current.rows = [][]driver.Value{{revision, raw}}
				}
				steps = append(steps,
					governanceSQLStep{kind: "exec", fragment: "pg_advisory_xact_lock(hashtextextended('chat-access:'||$1,0))", args: []any{sqlTenant}}, current)
			}
			var storedRevision string
			if tc.wantErr == nil || tc.stopAt == "insert" || tc.stopAt == "commit" {
				raw, err := json.Marshal(input.ModelIDs)
				if err != nil {
					t.Fatal(err)
				}
				row := []driver.Value{"", raw}
				insert := governanceSQLStep{kind: "query", fragment: "INSERT INTO tenant_chat_model_access(tenant_id,model_ids,revision,updated_by)",
					columns: 2, rows: [][]driver.Value{row}, check: func(args []any) error {
						if len(args) != 4 || args[0] != sqlTenant || args[1] != string(raw) || args[3] != sqlActor {
							return errors.New("chat allowlist write lost tenant, members or actor")
						}
						revision, ok := args[2].(string)
						if !ok || uuid.Validate(revision) != nil || revision == selectionRevision {
							return errors.New("chat allowlist did not issue a fresh valid revision")
						}
						storedRevision, row[0] = revision, revision
						return nil
					}}
				if tc.stopAt == "insert" {
					insert.err = failure
				}
				steps = append(steps, insert)
			}
			end := governanceSQLStep{kind: "commit"}
			if tc.stopAt == "commit" {
				end.err = failure
			} else if tc.wantErr != nil {
				end.kind = "rollback"
			}
			store, script := openGovernanceSQL(t, nil, append(steps, end)...)
			got, err := store.SaveChatModelAccess(t.Context(), sqlTenant, sqlActor, input)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("save chat access error=%v; want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if storedRevision == "" || got.Revision != storedRevision || !reflect.DeepEqual(got.ModelIDs, input.ModelIDs) {
					t.Fatalf("stored allowlist = %+v; want members %v and revision %q", got, input.ModelIDs, storedRevision)
				}
			} else if got.Revision != "" || got.ModelIDs != nil {
				t.Fatalf("failed save returned committed allowlist: %+v", got)
			}
			script.requireDone(t)
		})
	}
}

type selectionSQLApplication struct {
	name, table, initialMode string
	required, changed        error
	get                      func(*PostgresStore, context.Context, string) (DocumentModelSelection, error)
	save                     func(*PostgresStore, context.Context, string, string, DocumentModelSelectionInput) (DocumentModelSelection, error)
}

func selectionSQLApplications() []selectionSQLApplication {
	return []selectionSQLApplication{
		{name: "document", table: "tenant_document_model_selection", initialMode: "follow_chat",
			required: ErrManagedCapabilityRequired, changed: ErrDocumentSelectionChanged,
			get: (*PostgresStore).GetDocumentModelSelection, save: (*PostgresStore).SaveDocumentModelSelection},
		{name: "grading", table: "tenant_grading_model_selection", initialMode: "unconfigured",
			required: ErrGradingModelRequired, changed: ErrGradingSelectionChanged,
			get: (*PostgresStore).GetGradingModelSelection, save: (*PostgresStore).SaveGradingModelSelection},
	}
}

func TestPostgresApplicationSelectionReadsOnlyRequestedTenant(t *testing.T) {
	failure := errors.New("model selection read unavailable")
	for _, app := range selectionSQLApplications() {
		t.Run(app.name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				found   bool
				wantErr error
			}{
				{name: "saved explicit selection", found: true},
				{name: "school without a saved selection"},
				{name: "storage failure", wantErr: failure},
			} {
				t.Run(tc.name, func(t *testing.T) {
					now := governanceSQLTime()
					read := governanceSQLStep{kind: "query", fragment: "FROM " + app.table + " WHERE tenant_id=$1::uuid",
						args: []any{sqlTenant}, columns: 4, err: tc.wantErr}
					want := DocumentModelSelection{Revision: "absent", Mode: app.initialMode}
					if tc.found {
						read.rows = [][]driver.Value{{selectionRevision, "model", sqlDefault, now}}
						want = DocumentModelSelection{Revision: selectionRevision, Mode: "model", ModelConfigID: sqlDefault, UpdatedAt: &now}
					}
					if tc.wantErr != nil {
						want = DocumentModelSelection{}
					}
					store, script := openGovernanceSQL(t, nil, read)
					got, err := app.get(store, t.Context(), sqlTenant)
					if !errors.Is(err, tc.wantErr) || !reflect.DeepEqual(got, want) {
						t.Fatalf("selection = %+v, %v; want %+v, %v", got, err, want, tc.wantErr)
					}
					script.requireDone(t)
				})
			}
		})
	}
}

func TestPostgresApplicationSelectionValidatesLockedModelAndRevision(t *testing.T) {
	failure := errors.New("model selection persistence unavailable")
	for _, app := range selectionSQLApplications() {
		t.Run(app.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, stopAt string
				withoutModel bool
				disabled     bool
			}{
				{name: "qualified model committed"},
				{name: "clear explicit model", withoutModel: true},
				{name: "foreign missing or deleted model rejected", stopAt: "missing"},
				{name: "disabled model rejected", disabled: true},
				{name: "stale selection revision", stopAt: "revision"},
				{name: "selection read failure", stopAt: "read"},
				{name: "selection write failure", stopAt: "write"},
				{name: "selection commit failure", stopAt: "commit"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					input := DocumentModelSelectionInput{ExpectedRevision: selectionRevision, Mode: "model", ModelConfigID: sqlDefault}
					if tc.withoutModel {
						input.Mode, input.ModelConfigID = app.initialMode, ""
					}
					steps := []governanceSQLStep{{kind: "begin"}}
					wantErr := error(nil)
					if !tc.withoutModel {
						row := governanceModelRow(sqlDefault, true, true)
						row[21], row[22] = governanceSQLTime(), ManagedCapabilityProbeVersion
						if tc.disabled {
							row[11] = "disabled"
						}
						model := governanceSQLStep{kind: "query", fragment: "WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE",
							args: []any{sqlTenant, sqlDefault}, columns: 39, rows: [][]driver.Value{row}}
						if tc.stopAt == "missing" {
							model.rows = nil
						}
						steps = append(steps, model)
						if tc.stopAt == "missing" || tc.disabled {
							wantErr = app.required
						}
					}
					if wantErr == nil {
						currentRevision := selectionRevision
						if tc.stopAt == "revision" {
							currentRevision, wantErr = sqlOther, app.changed
						}
						read := governanceSQLStep{kind: "query", fragment: "FROM " + app.table + " WHERE tenant_id=$1::uuid FOR UPDATE",
							args: []any{sqlTenant}, columns: 4, rows: [][]driver.Value{{currentRevision, "model", sqlOther, governanceSQLTime()}}}
						if tc.stopAt == "read" {
							read.err, wantErr = failure, failure
						}
						steps = append(steps, governanceSQLStep{kind: "exec",
							fragment: "pg_advisory_xact_lock(hashtextextended('" + app.name + "-model:'||$1,0))", args: []any{sqlTenant}}, read)
					}
					var storedRevision string
					if wantErr == nil {
						row := []driver.Value{"", input.Mode, input.ModelConfigID, governanceSQLTime()}
						write := governanceSQLStep{kind: "query", fragment: "INSERT INTO " + app.table, columns: 4, rows: [][]driver.Value{row},
							check: func(args []any) error {
								if len(args) != 5 || args[0] != sqlTenant || args[1] != input.Mode || args[2] != input.ModelConfigID || args[4] != sqlActor {
									return errors.New("application model write lost tenant, actor or choice")
								}
								revision, ok := args[3].(string)
								if !ok || uuid.Validate(revision) != nil || revision == selectionRevision {
									return fmt.Errorf("invalid new selection revision %v", args[3])
								}
								storedRevision, row[0] = revision, revision
								return nil
							}}
						if tc.stopAt == "write" {
							write.err, wantErr = failure, failure
						}
						steps = append(steps, write)
					}
					end := governanceSQLStep{kind: "commit"}
					if tc.stopAt == "commit" {
						end.err, wantErr = failure, failure
					} else if wantErr != nil {
						end.kind = "rollback"
					}
					// 模型资格查询先于选择行锁；任何失败都不能提交新引用或新修订。
					store, script := openGovernanceSQL(t, nil, append(steps, end)...)
					got, err := app.save(store, t.Context(), sqlTenant, sqlActor, input)
					if !errors.Is(err, wantErr) {
						t.Fatalf("save selection error=%v; want %v", err, wantErr)
					}
					if wantErr == nil {
						if storedRevision == "" || got.Revision != storedRevision || got.Mode != input.Mode || got.ModelConfigID != input.ModelConfigID ||
							got.UpdatedAt == nil || !got.UpdatedAt.Equal(governanceSQLTime()) {
							t.Fatalf("stored selection = %+v", got)
						}
					} else if !reflect.DeepEqual(got, DocumentModelSelection{}) {
						t.Fatalf("failed save exposed committed selection: %+v", got)
					}
					script.requireDone(t)
				})
			}
		})
	}
}

package modelgovernance

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPostgresChatAccessCASAndTenantRLS(t *testing.T) {
	store, school, foreign, actor := newAnswerSheetSelectionPostgresFixture(t)
	ctx := t.Context()
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000192_chat_model_access.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	first := createSelectionPostgresConfig(t, store, school, actor)
	second := createSelectionPostgresConfig(t, store, school, actor)
	for _, id := range []string{first.ID, second.ID} {
		if _, err = store.db.ExecContext(ctx, `UPDATE managed_model_api_config SET last_text_status='success' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, e := store.SaveChatModelAccess(context.Background(), school, actor, ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{first.ID, second.ID}})
			results <- e
		}()
	}
	wait.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrChatAccessChanged) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS %d %d", success, conflict)
	}
	for _, id := range []string{first.ID, second.ID} {
		connection, err := ResolveChatManagedAPI(ctx, store, school, id)
		if err != nil || connection.Config.ID != id {
			t.Fatalf("resolve %s: %v", id, err)
		}
	}
	if _, err = store.SaveChatModelAccess(ctx, foreign, actor, ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{first.ID}}); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("foreign config: %v", err)
	}
	access, err := store.GetChatModelAccess(ctx, school)
	if err != nil {
		t.Fatal(err)
	}
	// 文本证据失效不能靠旧开放记录继续调用；撤销后即使恢复检测成功，也不能恢复已经撤销的授权。
	if _, err = store.db.ExecContext(ctx, `UPDATE managed_model_api_config SET last_text_status='untested' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = ResolveChatManagedAPI(ctx, store, school, second.ID); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("invalidated text evidence: %v", err)
	}
	access, err = store.SaveChatModelAccess(ctx, school, actor, ChatModelAccessInput{ExpectedRevision: access.Revision, ModelIDs: []string{first.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveChatModelAccess(ctx, school, actor, ChatModelAccessInput{ExpectedRevision: access.Revision, ModelIDs: []string{first.ID, second.ID}}); !errors.Is(err, ErrManagedTextRequired) {
		t.Fatalf("invalidated evidence granted: %v", err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE managed_model_api_config SET last_text_status='success' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = ResolveChatManagedAPI(ctx, store, school, second.ID); !errors.Is(err, ErrManagedConfigUnavailable) {
		t.Fatalf("revoked model restored by probe: %v", err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL ROLE edugrade_tenant_runtime`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('edugrade.tenant_id',$1,true)`, foreign); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM tenant_chat_model_access`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("RLS leaked: %d %v", count, err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tenant_chat_model_access(tenant_id,model_ids,updated_by) VALUES($1,'[]',$2)`, school, actor); err == nil {
		t.Fatal("RLS accepted cross-school policy")
	}
}

func TestPostgresChatAccessPreservesInheritedScopeAcrossDefaultChanges(t *testing.T) {
	for _, action := range []string{"rename", "clear", "disable and reenable", "switch", "create new default", "explicit empty", "stale rollback"} {
		t.Run(action, func(t *testing.T) {
			store, school, _, actor := newAnswerSheetSelectionPostgresFixture(t)
			migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000192_chat_model_access.sql"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.db.ExecContext(t.Context(), string(migration)); err != nil {
				t.Fatal(err)
			}
			verifyInheritedChatAccessDefaultChange(t, store, school, actor, action)
		})
	}
}

func TestPostgresChatAccessSaveAndDefaultChangeUseSameModelLockOrder(t *testing.T) {
	for _, action := range []string{"switch existing", "create new default"} {
		t.Run(action, func(t *testing.T) {
			store, school, _, actor := newAnswerSheetSelectionPostgresFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000192_chat_model_access.sql"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.db.ExecContext(ctx, string(migration)); err != nil {
				t.Fatal(err)
			}
			low := createSelectionPostgresConfig(t, store, school, actor)
			high := createSelectionPostgresConfig(t, store, school, actor)
			if low.ID > high.ID {
				low, high = high, low
			}
			if _, err = store.db.ExecContext(ctx, `UPDATE managed_model_api_config SET is_default=(id=$1::uuid),last_text_status='success' WHERE tenant_id=$2::uuid`, high.ID, school); err != nil {
				t.Fatal(err)
			}
			access, err := store.SaveChatModelAccess(ctx, school, actor, ChatModelAccessInput{ExpectedRevision: "absent", ModelIDs: []string{low.ID, high.ID}})
			if err != nil {
				t.Fatal(err)
			}
			// 默认切换写完高 ID 行后暂停，再让开放保存开始取共享锁。
			// 若已有显式名单时跳过升序写锁，保存会持有低 ID 行，双方下一步便会形成死锁。
			if _, err = store.db.ExecContext(ctx, `CREATE FUNCTION pause_default_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('test-chat-default:'||NEW.tenant_id::text,0));
  RETURN NEW;
END $$;
CREATE TRIGGER pause_default_change AFTER UPDATE OF is_default ON managed_model_api_config
FOR EACH ROW WHEN (OLD.is_default AND NOT NEW.is_default) EXECUTE FUNCTION pause_default_change()`); err != nil {
				t.Fatal(err)
			}
			gate, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback()
			var gatePID int
			if err = gate.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM pg_advisory_xact_lock(hashtextextended('test-chat-default:'||$1,0))`, school).Scan(&gatePID); err != nil {
				t.Fatal(err)
			}
			changedDefault := make(chan ManagedAPIConfig, 1)
			changeResult, saveResult := make(chan error, 1), make(chan error, 1)
			go func() {
				var config ManagedAPIConfig
				var e error
				if action == "switch existing" {
					update := chatDefaultUpdate(low)
					update.IsDefault = true
					config, e = store.UpdateManagedAPIConfig(ctx, school, low.ID, update)
				} else {
					probe := successfulManagedProbe()
					probe.ProbeMode, probe.GeneratedRequest, probe.TextCheck.OK = "text", true, true
					config, e = store.CreateManagedAPIConfig(ctx, school, actor, ManagedAPIConfigInput{ProviderKey: "custom", DisplayName: "Synthetic new default", AdapterType: low.AdapterType, BaseURL: low.BaseURL, ModelName: low.ModelName, ModelVersion: low.ModelVersion, Region: low.Region, Status: "active", APIKey: "synthetic-default-order-key", IsDefault: true, InitialProbe: &probe})
				}
				changedDefault <- config
				changeResult <- e
			}()
			waitForChatAccessPostgresLock(t, ctx, store.db, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%SET is_default=false%')`, gatePID)
			go func() {
				_, e := store.SaveChatModelAccess(ctx, school, actor, ChatModelAccessInput{ExpectedRevision: access.Revision, ModelIDs: []string{low.ID, high.ID}})
				saveResult <- e
			}()
			waitForChatAccessPostgresLock(t, ctx, store.db, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=current_setting('application_name') AND wait_event_type='Lock' AND query LIKE '%ORDER BY id FOR SHARE%')`)
			if err = gate.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = <-changeResult; err != nil {
				t.Fatalf("default change: %v", err)
			}
			if err = <-saveResult; err != nil {
				t.Fatalf("concurrent chat access save: %v", err)
			}
			config := <-changedDefault
			after, err := store.GetChatModelAccess(ctx, school)
			if err != nil || len(after.ModelIDs) != 2 || !contains(after.ModelIDs, low.ID) || !contains(after.ModelIDs, high.ID) {
				t.Fatalf("explicit access changed: %+v %v", after, err)
			}
			var defaultID string
			if err = store.db.QueryRowContext(ctx, `SELECT id::text FROM managed_model_api_config WHERE tenant_id=$1::uuid AND is_default AND deleted_at IS NULL`, school).Scan(&defaultID); err != nil || defaultID != config.ID {
				t.Fatalf("default change lost: %s %v", defaultID, err)
			}
		})
	}
}

func waitForChatAccessPostgresLock(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := db.QueryRowContext(waitCtx, query, args...).Scan(&waiting); err != nil {
			t.Fatalf("observe concurrent lock: %v", err)
		}
		if waiting {
			return
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("concurrent operation did not reach expected lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

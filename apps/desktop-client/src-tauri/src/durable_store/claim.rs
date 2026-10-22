use rusqlite::{params, Connection, OpenFlags, OptionalExtension};
use std::{
    fs,
    path::{Path, PathBuf},
};
use tauri::{AppHandle, Manager};
use uuid::Uuid;
use zeroize::Zeroize;

use super::{
    connection::sql_error,
    crypto::decrypt_json,
    paths::{
        account_root, active_scope_path_component, db_path, legacy_root, spool_path, store_root,
    },
};

#[cfg(windows)]
use super::crypto::{install_scoped_master_key, legacy_master_key};

/// 将隔离的 v1 存储归属到当前持有租约的账号。
///
/// 只有显式命令发起旧版认领；已持久记录的同账号中断认领
/// 会在普通读写前继续。密钥只通过Windows凭据管理器安装，
/// 日志不保存密钥，也不会替另一账号恢复认领。
#[cfg(windows)]
pub fn claim_legacy_store(app: AppHandle) -> Result<(), String> {
    let data_dir = app
        .path()
        .app_data_dir()
        .map_err(|error| error.to_string())?;
    let legacy = legacy_root(&data_dir);
    let scope = active_scope_path_component()?;
    let was_pending = pending_claim_for_scope(&data_dir, &scope)?;
    let target = store_root(&app)?;
    if was_pending {
        // store_root已在读写账号数据前继续了同一账号的持久认领，不再发起第二次迁移。
        return Ok(());
    }

    let mut legacy_key = legacy_master_key()?;
    let result = (|| {
        verify_legacy_key(&legacy, &legacy_key)?;
        move_legacy_store_with_commit(&legacy, &target, &scope, || {
            install_scoped_master_key(&target, &legacy_key)
        })
    })();
    legacy_key.zeroize();
    result
}

fn verify_legacy_key(legacy: &Path, key: &[u8; 32]) -> Result<(), String> {
    let database = db_path(legacy);
    if !database.exists() {
        return Ok(());
    }
    let conn = Connection::open_with_flags(database, OpenFlags::SQLITE_OPEN_READ_ONLY)
        .map_err(sql_error)?;
    for table in ["sync_event", "draft", "local_asset_ingest"] {
        if !table_exists(&conn, table)? {
            continue;
        }
        let sample: Option<(String, String)> = conn
            .query_row(
                &format!("SELECT payload_ciphertext, payload_nonce FROM {table} LIMIT 1"),
                [],
                |row| Ok((row.get(0)?, row.get(1)?)),
            )
            .optional()
            .map_err(sql_error)?;
        if let Some((ciphertext, nonce)) = sample {
            let _: serde_json::Value = decrypt_json(key, &ciphertext, &nonce).map_err(|_| {
                "legacy durable key cannot decrypt the retained records; claim was stopped without moving data".to_string()
            })?;
            return Ok(());
        }
    }
    if table_exists(&conn, "local_asset")? {
        let count: i64 = conn
            .query_row("SELECT COUNT(*) FROM local_asset", [], |row| row.get(0))
            .map_err(sql_error)?;
        if count > 0 {
            return Err("legacy assets have no verifiable encrypted metadata; claim needs manual recovery and no data was moved".into());
        }
    }
    Ok(())
}

#[cfg(not(windows))]
pub fn claim_legacy_store(_app: AppHandle) -> Result<(), String> {
    Err("legacy durable data can only be claimed through Windows Credential Manager".into())
}

fn move_legacy_store_with_commit(
    legacy: &Path,
    target: &Path,
    scope: &str,
    commit_key: impl FnOnce() -> Result<(), String>,
) -> Result<(), String> {
    let data_dir = legacy
        .parent()
        .ok_or("legacy store has no data directory")?;
    let conn = open_claim_journal(data_dir)?;
    if let Some(claim) = read_claim(&conn)? {
        validate_claim(&claim, data_dir, scope)?;
        return continue_claim(&conn, &claim, commit_key);
    }
    if !legacy.exists() {
        return Err("no quarantined legacy durable data is available to claim".into());
    }
    reject_symbolic_tree(legacy)?;
    ensure_target_is_empty(target)?;
    let parent = target
        .parent()
        .ok_or("durable account target has no parent directory")?;
    fs::create_dir_all(parent).map_err(|error| error.to_string())?;
    let backup = parent.join(format!(".empty-before-claim-{scope}-{}", Uuid::new_v4()));
    let claim = PendingClaim {
        legacy: legacy.to_path_buf(),
        target: target.to_path_buf(),
        scope: scope.to_string(),
        backup,
        had_target: target.exists(),
        phase: 0,
    };
    validate_claim(&claim, data_dir, scope)?;
    // 日志先FULL提交再改目录；不持久化任何密钥，旧版凭据仍是重启恢复的唯一来源。
    conn.execute("INSERT INTO claim_migration(id,legacy,target,scope,backup,had_target,phase) VALUES(1,?1,?2,?3,?4,?5,0)", params![claim.legacy.to_string_lossy(),claim.target.to_string_lossy(),claim.scope,claim.backup.to_string_lossy(),claim.had_target]).map_err(sql_error)?;
    claim_checkpoint("prepared");
    continue_claim(&conn, &claim, commit_key)
}

struct PendingClaim {
    legacy: PathBuf,
    target: PathBuf,
    scope: String,
    backup: PathBuf,
    had_target: bool,
    phase: u8,
}

fn claim_journal_path(data_dir: &Path) -> PathBuf {
    data_dir.join("legacy-claim.sqlite3")
}

fn open_claim_journal(data_dir: &Path) -> Result<Connection, String> {
    fs::create_dir_all(data_dir).map_err(|error| error.to_string())?;
    let path = claim_journal_path(data_dir);
    if path.exists() {
        reject_symbolic_tree(&path)?;
    }
    let conn = Connection::open(path).map_err(sql_error)?;
    conn.busy_timeout(std::time::Duration::from_secs(5))
        .map_err(sql_error)?;
    // EXCLUSIVE模式跨多次FULL提交仍保留进程锁，另一个客户端不能同时搬目录或改凭据。
    conn.execute_batch("PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA locking_mode=EXCLUSIVE; BEGIN EXCLUSIVE; COMMIT;
        CREATE TABLE IF NOT EXISTS claim_migration(id INTEGER PRIMARY KEY CHECK(id=1),legacy TEXT NOT NULL,target TEXT NOT NULL,scope TEXT NOT NULL,backup TEXT NOT NULL,had_target INTEGER NOT NULL CHECK(had_target IN (0,1)),phase INTEGER NOT NULL CHECK(phase BETWEEN 0 AND 4));").map_err(sql_error)?;
    Ok(conn)
}

fn read_claim(conn: &Connection) -> Result<Option<PendingClaim>, String> {
    conn.query_row(
        "SELECT legacy,target,scope,backup,had_target,phase FROM claim_migration WHERE id=1",
        [],
        |row| {
            Ok(PendingClaim {
                legacy: PathBuf::from(row.get::<_, String>(0)?),
                target: PathBuf::from(row.get::<_, String>(1)?),
                scope: row.get(2)?,
                backup: PathBuf::from(row.get::<_, String>(3)?),
                had_target: row.get(4)?,
                phase: row.get(5)?,
            })
        },
    )
    .optional()
    .map_err(sql_error)
}

fn validate_claim(claim: &PendingClaim, data_dir: &Path, scope: &str) -> Result<(), String> {
    let prefix = format!(".empty-before-claim-{scope}-");
    let backup_name = claim
        .backup
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or("");
    let valid_backup = backup_name
        .strip_prefix(&prefix)
        .is_some_and(|suffix| Uuid::parse_str(suffix).is_ok());
    if claim.scope != scope
        || scope.is_empty()
        || !scope
            .bytes()
            .all(|value| value.is_ascii_alphanumeric() || value == b'-')
        || claim.legacy != legacy_root(data_dir)
        || claim.target != account_root(data_dir, scope)
        || claim.backup.parent() != claim.target.parent()
        || !valid_backup
        || claim.phase > 4
    {
        return Err("legacy claim journal belongs to another account or has invalid paths; retained data was not changed".into());
    }
    Ok(())
}

fn update_claim_phase(conn: &Connection, phase: u8) -> Result<(), String> {
    conn.execute(
        "UPDATE claim_migration SET phase=MAX(phase,?1) WHERE id=1",
        params![phase],
    )
    .map_err(sql_error)?;
    Ok(())
}

fn continue_claim(
    conn: &Connection,
    claim: &PendingClaim,
    commit_key: impl FnOnce() -> Result<(), String>,
) -> Result<(), String> {
    // 每步靠实际目录布局对账，覆盖rename/SQLite提交已成功、阶段尚未写入时的进程退出。
    // 出错保留日志继续迁移；凭据可能已经安装，不能再按普通Err盲目恢复旧账号密钥。
    if claim.legacy.exists() {
        reject_symbolic_tree(&claim.legacy)?;
        if claim.had_target && !claim.backup.exists() {
            ensure_target_is_empty(&claim.target)?;
            fs::rename(&claim.target, &claim.backup).map_err(|error| error.to_string())?;
            claim_checkpoint("target_vacated");
        }
        if claim.target.exists() {
            return Err(
                "legacy claim has both source and target directories; retained data needs recovery"
                    .into(),
            );
        }
        update_claim_phase(conn, 1)?;
        fs::rename(&claim.legacy, &claim.target).map_err(|error| error.to_string())?;
        claim_checkpoint("store_moved");
    }
    if !claim.target.exists() {
        return Err("legacy claim retained directory is missing; recovery stopped".into());
    }
    reject_symbolic_tree(&claim.target)?;
    update_claim_phase(conn, 2)?;
    rewrite_stored_paths(&claim.target, &claim.legacy, &claim.target)?;
    claim_checkpoint("paths_committed");
    update_claim_phase(conn, 3)?;
    commit_key()?;
    claim_checkpoint("key_installed");
    update_claim_phase(conn, 4)?;
    if claim.backup.exists() {
        // 备份路径已绑定本账号且内容再次确认为空，清理绝不包含唯一的原件或草稿。
        ensure_target_is_empty(&claim.backup)?;
        fs::remove_dir_all(&claim.backup).map_err(|error| error.to_string())?;
        claim_checkpoint("backup_cleaned");
    }
    conn.execute("DELETE FROM claim_migration WHERE id=1", [])
        .map_err(sql_error)?;
    claim_checkpoint("journal_completed");
    Ok(())
}

fn pending_claim_for_scope(data_dir: &Path, scope: &str) -> Result<bool, String> {
    if !claim_journal_path(data_dir).exists() {
        return Ok(false);
    }
    let conn = open_claim_journal(data_dir)?;
    Ok(read_claim(&conn)?.is_some_and(|claim| claim.scope == scope))
}

#[cfg(windows)]
pub(crate) fn recover_pending_claim(data_dir: &Path, scope: &str) -> Result<(), String> {
    recover_pending_claim_with_key(
        data_dir,
        scope,
        legacy_master_key,
        install_scoped_master_key,
    )
}

fn recover_pending_claim_with_key(
    data_dir: &Path,
    scope: &str,
    load_key: impl FnOnce() -> Result<[u8; 32], String>,
    install_key: impl FnOnce(&Path, &[u8; 32]) -> Result<(), String>,
) -> Result<(), String> {
    if !claim_journal_path(data_dir).exists() {
        return Ok(());
    }
    let conn = open_claim_journal(data_dir)?;
    let Some(claim) = read_claim(&conn)? else {
        return Ok(());
    };
    // 其他账号可以正常使用自身目录，但不能替另一账号安装密钥或认领它保留的数据。
    if claim.scope != scope {
        return Ok(());
    }
    validate_claim(&claim, data_dir, scope)?;
    let mut key = load_key()?;
    let result = (|| {
        let retained = if claim.legacy.exists() {
            &claim.legacy
        } else {
            &claim.target
        };
        verify_legacy_key(retained, &key)?;
        continue_claim(&conn, &claim, || install_key(&claim.target, &key))
    })();
    key.zeroize();
    result
}

#[cfg(not(windows))]
pub(crate) fn recover_pending_claim(_data_dir: &Path, _scope: &str) -> Result<(), String> {
    Ok(())
}

#[cfg(test)]
fn claim_checkpoint(phase: &str) {
    if std::env::var("EDUGRADE_CLAIM_TEST_CRASH_AFTER")
        .ok()
        .as_deref()
        == Some(phase)
    {
        std::process::exit(73);
    }
}
#[cfg(not(test))]
fn claim_checkpoint(_phase: &str) {}

fn ensure_target_is_empty(target: &Path) -> Result<(), String> {
    if !target.exists() {
        return Ok(());
    }
    reject_symbolic_tree(target)?;
    let allowed = [
        "offline.sqlite3",
        "offline.sqlite3-wal",
        "offline.sqlite3-shm",
        "scanner-profiles.sqlite3",
        "scanner-profiles.sqlite3-wal",
        "scanner-profiles.sqlite3-shm",
        "spool",
    ];
    for entry in fs::read_dir(target).map_err(|error| error.to_string())? {
        let entry = entry.map_err(|error| error.to_string())?;
        let name = entry.file_name();
        if !allowed.iter().any(|allowed_name| name == *allowed_name) {
            return Err(
                "current account already has durable files; legacy claim was refused".into(),
            );
        }
    }
    let spool = spool_path(target);
    if spool.exists()
        && fs::read_dir(&spool)
            .map_err(|error| error.to_string())?
            .next()
            .transpose()
            .map_err(|error| error.to_string())?
            .is_some()
    {
        return Err("current account already has spooled assets; legacy claim was refused".into());
    }
    let database = db_path(target);
    if !database.exists() {
        return scanner_profile_store_is_empty(target);
    }
    let conn = Connection::open(database).map_err(sql_error)?;
    for table in ["local_asset", "sync_event", "draft"] {
        if table_exists(&conn, table)? {
            let count: i64 = conn
                .query_row(&format!("SELECT COUNT(*) FROM {table}"), [], |row| {
                    row.get(0)
                })
                .map_err(sql_error)?;
            if count != 0 {
                return Err(
                    "current account already has durable records; legacy claim was refused".into(),
                );
            }
        }
    }
    scanner_profile_store_is_empty(target)
}

fn scanner_profile_store_is_empty(target: &Path) -> Result<(), String> {
    let path = target.join("scanner-profiles.sqlite3");
    if !path.exists() {
        return Ok(());
    }
    let conn = Connection::open(path).map_err(sql_error)?;
    if table_exists(&conn, "scanner_profile")? {
        let count: i64 = conn
            .query_row("SELECT COUNT(*) FROM scanner_profile", [], |row| row.get(0))
            .map_err(sql_error)?;
        if count != 0 {
            return Err(
                "current account already has scanner profiles; legacy claim was refused".into(),
            );
        }
    }
    Ok(())
}

fn rewrite_stored_paths(root: &Path, from: &Path, to: &Path) -> Result<(), String> {
    let database = db_path(root);
    if !database.exists() {
        return Ok(());
    }
    let mut conn = Connection::open(database).map_err(sql_error)?;
    let transaction = conn.transaction().map_err(sql_error)?;
    for (table, column) in [
        ("local_asset", "local_path"),
        ("local_asset_chunk", "local_path"),
    ] {
        if !table_exists(&transaction, table)? {
            continue;
        }
        let mut statement = transaction
            .prepare(&format!("SELECT rowid, {column} FROM {table}"))
            .map_err(sql_error)?;
        let rows = statement
            .query_map([], |row| {
                Ok((row.get::<_, i64>(0)?, row.get::<_, String>(1)?))
            })
            .map_err(sql_error)?
            .collect::<Result<Vec<_>, _>>()
            .map_err(sql_error)?;
        drop(statement);
        for (row_id, value) in rows {
            let stored = PathBuf::from(&value);
            if stored.starts_with(to) {
                continue;
            }
            let suffix = stored.strip_prefix(from).map_err(|_| {
                format!("legacy durable path is outside its quarantined root: {value}")
            })?;
            let next = to.join(suffix).to_string_lossy().into_owned();
            transaction
                .execute(
                    &format!("UPDATE {table} SET {column}=?1 WHERE rowid=?2"),
                    params![next, row_id],
                )
                .map_err(sql_error)?;
        }
    }
    transaction.commit().map_err(sql_error)
}

fn table_exists(conn: &Connection, table: &str) -> Result<bool, String> {
    conn.query_row(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?1",
        params![table],
        |_| Ok(()),
    )
    .optional()
    .map(|value| value.is_some())
    .map_err(sql_error)
}

fn reject_symbolic_tree(root: &Path) -> Result<(), String> {
    if fs::symlink_metadata(root)
        .map_err(|error| error.to_string())?
        .file_type()
        .is_symlink()
    {
        return Err("refusing to claim a symbolic durable storage path".into());
    }
    if root.is_dir() {
        for entry in fs::read_dir(root).map_err(|error| error.to_string())? {
            reject_symbolic_tree(&entry.map_err(|error| error.to_string())?.path())?;
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{
        db_path, ensure_target_is_empty, move_legacy_store_with_commit, verify_legacy_key,
    };
    use crate::durable_store::crypto::encrypt_json;
    use rusqlite::{params, Connection};
    use std::{fs, path::PathBuf};
    use uuid::Uuid;

    #[test]
    fn explicit_claim_moves_legacy_rows_and_rewrites_spool_paths() {
        let data = std::env::temp_dir().join(format!("edugrade-legacy-claim-{}", Uuid::new_v4()));
        let legacy = data.join("durable-store-v1");
        let target = data.join("durable-store-v2/accounts/account-scope");
        let old_spool = legacy.join("spool");
        fs::create_dir_all(&old_spool).expect("legacy spool");
        fs::create_dir_all(target.join("spool")).expect("empty target");
        let asset_path = old_spool.join("asset-a");
        fs::write(&asset_path, b"ciphertext").expect("legacy ciphertext");
        let conn = Connection::open(db_path(&legacy)).expect("legacy db");
        conn.execute_batch(
            "CREATE TABLE local_asset (id TEXT PRIMARY KEY, local_path TEXT NOT NULL);
             CREATE TABLE draft (task_id TEXT PRIMARY KEY);
             CREATE TABLE sync_event (id TEXT PRIMARY KEY);",
        )
        .expect("legacy schema");
        conn.execute(
            "INSERT INTO local_asset(id,local_path) VALUES ('asset-a',?1)",
            params![asset_path.to_string_lossy()],
        )
        .expect("legacy asset");
        drop(conn);

        let mut key_committed = false;
        move_legacy_store_with_commit(&legacy, &target, "account-scope", || {
            key_committed = true;
            Ok(())
        })
        .expect("claim");

        assert!(key_committed);
        assert!(!legacy.exists());
        assert_eq!(
            fs::read(target.join("spool/asset-a")).expect("moved asset"),
            b"ciphertext"
        );
        let claimed = Connection::open(db_path(&target)).expect("claimed db");
        let stored: String = claimed
            .query_row(
                "SELECT local_path FROM local_asset WHERE id='asset-a'",
                [],
                |row| row.get(0),
            )
            .expect("claimed path");
        assert_eq!(PathBuf::from(stored), target.join("spool/asset-a"));
        drop(claimed);
        fs::remove_dir_all(data).expect("cleanup");
    }

    #[test]
    fn claim_refuses_to_merge_into_an_account_with_records() {
        let data = std::env::temp_dir().join(format!("edugrade-legacy-refuse-{}", Uuid::new_v4()));
        let target = data.join("durable-store-v2/accounts/account-scope");
        fs::create_dir_all(target.join("spool")).expect("target");
        let conn = Connection::open(db_path(&target)).expect("target db");
        conn.execute_batch(
            "CREATE TABLE draft (task_id TEXT PRIMARY KEY);
             INSERT INTO draft(task_id) VALUES ('existing');",
        )
        .expect("record");
        drop(conn);
        assert!(ensure_target_is_empty(&target).is_err());
        fs::remove_dir_all(data).expect("cleanup");
    }

    #[test]
    fn failed_claim_retains_journal_and_retry_finishes_with_original_data() {
        let data =
            std::env::temp_dir().join(format!("edugrade-legacy-rollback-{}", Uuid::new_v4()));
        let legacy = data.join("durable-store-v1");
        let target = data.join("durable-store-v2/accounts/account-scope");
        fs::create_dir_all(legacy.join("spool")).expect("legacy spool");
        let old_path = legacy.join("spool/asset-a");
        fs::write(&old_path, b"ciphertext").expect("ciphertext");
        let conn = Connection::open(db_path(&legacy)).expect("legacy db");
        conn.execute_batch(
            "CREATE TABLE local_asset (id TEXT PRIMARY KEY, local_path TEXT NOT NULL);",
        )
        .expect("legacy schema");
        conn.execute(
            "INSERT INTO local_asset(id,local_path) VALUES ('asset-a',?1)",
            params![old_path.to_string_lossy()],
        )
        .expect("legacy row");
        drop(conn);

        assert!(
            move_legacy_store_with_commit(&legacy, &target, "account-scope", || {
                Err("simulated key installation failure".into())
            })
            .is_err()
        );
        assert!(!legacy.exists());
        move_legacy_store_with_commit(&legacy, &target, "account-scope", || Ok(()))
            .expect("resume failed credential install");
        assert_eq!(
            fs::read(target.join("spool/asset-a")).expect("retained ciphertext"),
            b"ciphertext"
        );
        let restored = Connection::open(db_path(&target)).expect("recovered db");
        let stored: String = restored
            .query_row(
                "SELECT local_path FROM local_asset WHERE id='asset-a'",
                [],
                |row| row.get(0),
            )
            .expect("restored path");
        assert_eq!(PathBuf::from(stored), target.join("spool/asset-a"));
        drop(restored);
        fs::remove_dir_all(data).expect("cleanup");
    }

    #[test]
    fn pending_claim_cannot_be_resumed_for_a_different_account() {
        let data = std::env::temp_dir().join(format!("edugrade-claim-boundary-{}", Uuid::new_v4()));
        let legacy = data.join("durable-store-v1");
        let target = data.join("durable-store-v2/accounts/account-a");
        fs::create_dir_all(legacy.join("spool")).unwrap();
        fs::write(legacy.join("spool/retained"), b"synthetic ciphertext").unwrap();
        assert!(
            move_legacy_store_with_commit(&legacy, &target, "account-a", || Err(
                "credential unavailable".into()
            ))
            .is_err()
        );
        let other = data.join("durable-store-v2/accounts/account-b");
        let mut installed = false;
        let error = move_legacy_store_with_commit(&legacy, &other, "account-b", || {
            installed = true;
            Ok(())
        })
        .unwrap_err();
        assert!(error.contains("another account"));
        assert!(!installed);
        assert!(!other.exists());
        let mut read_key = false;
        super::recover_pending_claim_with_key(
            &data,
            "account-b",
            || {
                read_key = true;
                Ok([7; 32])
            },
            |_, _| panic!("another account credential must not be touched"),
        )
        .unwrap();
        assert!(!read_key);
        assert_eq!(
            fs::read(target.join("spool/retained")).unwrap(),
            b"synthetic ciphertext"
        );
        move_legacy_store_with_commit(&legacy, &target, "account-a", || Ok(())).unwrap();
        fs::remove_dir_all(data).unwrap();
    }

    #[cfg(windows)]
    const TEST_CREDENTIAL_SERVICE: &str = "com.edugrade.desktop.claim.round19.tests";

    #[cfg(windows)]
    #[test]
    fn claim_process_child() {
        let Ok(root) = std::env::var("EDUGRADE_CLAIM_TEST_ROOT") else {
            return;
        };
        let scope = std::env::var("EDUGRADE_CLAIM_TEST_SCOPE").unwrap();
        let data = PathBuf::from(root);
        if std::env::var("EDUGRADE_CLAIM_TEST_LOCK_CHECK").is_ok() {
            assert!(super::open_claim_journal(&data).is_err());
            return;
        }
        let legacy = data.join("durable-store-v1");
        let target = data.join("durable-store-v2/accounts").join(&scope);
        let entry = keyring::Entry::new(TEST_CREDENTIAL_SERVICE, &scope).unwrap();
        let legacy_entry =
            keyring::Entry::new(TEST_CREDENTIAL_SERVICE, &format!("{scope}-legacy")).unwrap();
        move_legacy_store_with_commit(&legacy, &target, &scope, || {
            entry
                .set_password(
                    &legacy_entry
                        .get_password()
                        .map_err(|error| error.to_string())?,
                )
                .map_err(|error| error.to_string())
        })
        .unwrap();
    }

    #[cfg(windows)]
    fn child_command(data: &std::path::Path, scope: &str) -> std::process::Command {
        let mut command = std::process::Command::new(std::env::current_exe().unwrap());
        command
            .args([
                "--exact",
                "durable_store::claim::tests::claim_process_child",
                "--nocapture",
            ])
            .env("EDUGRADE_CLAIM_TEST_ROOT", data)
            .env("EDUGRADE_CLAIM_TEST_SCOPE", scope);
        command
    }

    #[cfg(windows)]
    #[test]
    fn process_exit_at_every_claim_boundary_is_recoverable() {
        use crate::durable_store::crypto::{decrypt_bytes, decrypt_json, encrypt_bytes};
        use base64::{engine::general_purpose::STANDARD, Engine as _};
        struct IsolatedCredential(keyring::Entry);
        impl Drop for IsolatedCredential {
            fn drop(&mut self) {
                let _ = self.0.delete_credential();
            }
        }
        // 子进程直接exit，不执行Rust析构或Err回滚；凭据只写独立测试service中的随机账号。
        for phase in [
            "prepared",
            "target_vacated",
            "store_moved",
            "paths_committed",
            "key_installed",
            "backup_cleaned",
            "journal_completed",
        ] {
            let scope = Uuid::new_v4().simple().to_string().repeat(2);
            let data = std::env::temp_dir().join(format!("edugrade-claim-process-{scope}"));
            let legacy = data.join("durable-store-v1");
            let target = data.join("durable-store-v2/accounts").join(&scope);
            fs::create_dir_all(legacy.join("spool")).unwrap();
            fs::create_dir_all(target.join("spool")).unwrap();
            let credential =
                IsolatedCredential(keyring::Entry::new(TEST_CREDENTIAL_SERVICE, &scope).unwrap());
            let legacy_credential = IsolatedCredential(
                keyring::Entry::new(TEST_CREDENTIAL_SERVICE, &format!("{scope}-legacy")).unwrap(),
            );
            legacy_credential
                .0
                .set_password(&STANDARD.encode([7_u8; 32]))
                .unwrap();
            credential
                .0
                .set_password(&STANDARD.encode([8_u8; 32]))
                .unwrap();
            let (asset, asset_nonce) =
                encrypt_bytes(&[7_u8; 32], b"synthetic retained original").unwrap();
            let asset_path = legacy.join("spool/asset");
            fs::write(&asset_path, asset).unwrap();
            let (ciphertext, nonce) = encrypt_json(
                &[7_u8; 32],
                &serde_json::json!({"synthetic":true,"draft":"retained"}),
            )
            .unwrap();
            let conn = Connection::open(db_path(&legacy)).unwrap();
            conn.execute_batch("CREATE TABLE local_asset(local_path TEXT);CREATE TABLE draft(payload_ciphertext TEXT,payload_nonce TEXT);").unwrap();
            conn.execute(
                "INSERT INTO local_asset VALUES(?1)",
                params![asset_path.to_string_lossy()],
            )
            .unwrap();
            conn.execute(
                "INSERT INTO draft VALUES(?1,?2)",
                params![ciphertext, nonce],
            )
            .unwrap();
            drop(conn);
            let interrupted = child_command(&data, &scope)
                .env("EDUGRADE_CLAIM_TEST_CRASH_AFTER", phase)
                .output()
                .unwrap();
            assert_eq!(
                interrupted.status.code(),
                Some(73),
                "checkpoint {phase}: {}",
                String::from_utf8_lossy(&interrupted.stderr)
            );
            let journal = super::open_claim_journal(&data).unwrap();
            let pending = super::read_claim(&journal).unwrap().is_some();
            drop(journal);
            if pending {
                super::recover_pending_claim_with_key(
                    &data,
                    &scope,
                    || {
                        STANDARD
                            .decode(
                                legacy_credential
                                    .0
                                    .get_password()
                                    .map_err(|error| error.to_string())?,
                            )
                            .map_err(|error| error.to_string())?
                            .try_into()
                            .map_err(|_| "invalid isolated legacy test key".into())
                    },
                    |root, key| {
                        assert_eq!(root, target);
                        credential
                            .0
                            .set_password(&STANDARD.encode(key))
                            .map_err(|error| error.to_string())
                    },
                )
                .unwrap();
            }
            let key: [u8; 32] = STANDARD
                .decode(credential.0.get_password().unwrap())
                .unwrap()
                .try_into()
                .unwrap();
            let conn = Connection::open(db_path(&target)).unwrap();
            let path: String = conn
                .query_row("SELECT local_path FROM local_asset", [], |row| row.get(0))
                .unwrap();
            assert_eq!(PathBuf::from(&path), target.join("spool/asset"));
            assert_eq!(
                decrypt_bytes(&key, &asset_nonce, &fs::read(path).unwrap()).unwrap(),
                b"synthetic retained original"
            );
            let (encrypted, nonce): (String, String) = conn
                .query_row(
                    "SELECT payload_ciphertext,payload_nonce FROM draft",
                    [],
                    |row| Ok((row.get(0)?, row.get(1)?)),
                )
                .unwrap();
            let draft: serde_json::Value = decrypt_json(&key, &encrypted, &nonce).unwrap();
            assert_eq!(draft["draft"], "retained");
            assert!(decrypt_json::<serde_json::Value>(&[8_u8; 32], &encrypted, &nonce).is_err());
            drop(conn);
            assert!(!legacy.exists());
            let journal = super::open_claim_journal(&data).unwrap();
            assert!(super::read_claim(&journal).unwrap().is_none());
            drop(journal);
            fs::remove_dir_all(data).unwrap();
        }
    }

    #[cfg(windows)]
    #[test]
    fn journal_exclusive_lock_survives_phase_commits_across_processes() {
        let data = std::env::temp_dir().join(format!("edugrade-claim-lock-{}", Uuid::new_v4()));
        let connection = super::open_claim_journal(&data).unwrap();
        connection
            .execute(
                "INSERT INTO claim_migration VALUES(1,'legacy','target','lock-scope','backup',0,0)",
                [],
            )
            .unwrap();
        for phase in [1, 2, 3] {
            super::update_claim_phase(&connection, phase).unwrap();
        }
        let result = child_command(&data, "lock-scope")
            .env("EDUGRADE_CLAIM_TEST_LOCK_CHECK", "1")
            .output()
            .unwrap();
        assert!(
            result.status.success(),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        drop(connection);
        fs::remove_dir_all(data).unwrap();
    }

    #[test]
    fn claim_validates_legacy_credential_before_touching_files() {
        let data = std::env::temp_dir().join(format!("edugrade-legacy-key-{}", Uuid::new_v4()));
        let legacy = data.join("durable-store-v1");
        fs::create_dir_all(&legacy).expect("legacy root");
        let conn = Connection::open(db_path(&legacy)).expect("legacy db");
        conn.execute_batch("CREATE TABLE draft (payload_ciphertext TEXT, payload_nonce TEXT);")
            .expect("legacy table");
        let (ciphertext, nonce) =
            encrypt_json(&[7_u8; 32], &serde_json::json!({"private": true})).expect("encrypt");
        conn.execute(
            "INSERT INTO draft(payload_ciphertext,payload_nonce) VALUES (?1,?2)",
            params![ciphertext, nonce],
        )
        .expect("sample");
        drop(conn);
        assert!(verify_legacy_key(&legacy, &[7_u8; 32]).is_ok());
        assert!(verify_legacy_key(&legacy, &[8_u8; 32]).is_err());
        assert!(
            legacy.exists(),
            "failed verification never moves legacy data"
        );
        fs::remove_dir_all(data).expect("cleanup");
    }
}

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
    paths::{active_scope_path_component, db_path, legacy_root, spool_path, store_root},
};

#[cfg(windows)]
use super::crypto::{
    existing_scoped_master_key, install_scoped_master_key, legacy_master_key,
    restore_scoped_master_key,
};

/// Assign the quarantined v1 store to the currently leased account.
///
/// No legacy path is ever opened by normal queue/draft commands. This explicit
/// command moves it under the current account's v2 scope and copies the legacy
/// encryption key into that account's distinct Credential Manager entry.
#[cfg(windows)]
pub fn claim_legacy_store(app: AppHandle) -> Result<(), String> {
    let data_dir = app
        .path()
        .app_data_dir()
        .map_err(|error| error.to_string())?;
    let legacy = legacy_root(&data_dir);
    let target = store_root(&app)?;
    let scope = active_scope_path_component()?;

    let mut legacy_key = legacy_master_key()?;
    if let Err(error) = verify_legacy_key(&legacy, &legacy_key) {
        legacy_key.zeroize();
        return Err(error);
    }
    let mut previous_key = existing_scoped_master_key(&target)?;
    let result = move_legacy_store_with_commit(&legacy, &target, &scope, || {
        install_scoped_master_key(&target, &legacy_key)
    });
    if result.is_err() {
        let restore_result = restore_scoped_master_key(&target, previous_key.as_ref());
        legacy_key.zeroize();
        if let Some(key) = previous_key.as_mut() {
            key.zeroize();
        }
        if let Err(restore_error) = restore_result {
            return Err(format!(
                "legacy claim failed and the previous account key could not be restored: {restore_error}"
            ));
        }
        return result;
    }
    legacy_key.zeroize();
    if let Some(key) = previous_key.as_mut() {
        key.zeroize();
    }
    Ok(())
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
    let had_target = target.exists();
    if had_target {
        fs::rename(target, &backup).map_err(|error| error.to_string())?;
    }
    if let Err(error) = fs::rename(legacy, target) {
        if had_target {
            let _ = fs::rename(&backup, target);
        }
        return Err(error.to_string());
    }

    let complete = (|| -> Result<(), String> {
        // 文件位置、库内绝对路径和凭据必须一起生效；后续失败时按原位置回滚，保留旧数据可恢复性。
        rewrite_stored_paths(target, legacy, target)?;
        commit_key()?;
        Ok(())
    })();
    if let Err(error) = complete {
        let path_rollback = rewrite_stored_paths(target, target, legacy);
        let move_rollback = fs::rename(target, legacy).map_err(|move_error| move_error.to_string());
        let target_rollback = if had_target {
            fs::rename(&backup, target).map_err(|move_error| move_error.to_string())
        } else {
            Ok(())
        };
        if path_rollback.is_err() || move_rollback.is_err() || target_rollback.is_err() {
            return Err(format!(
                "legacy claim failed and filesystem rollback needs manual recovery: {error}"
            ));
        }
        return Err(error);
    }
    if had_target {
        // The target was proven to contain no queue, asset, draft, spool, or
        // unknown files before it was moved to this private backup path.
        // Claim has already committed at this point. A failed cleanup must
        // not cause the caller to restore the old key over the claimed data.
        let _ = fs::remove_dir_all(&backup);
    }
    Ok(())
}

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
    fn failed_claim_restores_legacy_spool_and_paths() {
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
        assert!(!target.exists());
        assert_eq!(
            fs::read(&old_path).expect("restored ciphertext"),
            b"ciphertext"
        );
        let restored = Connection::open(db_path(&legacy)).expect("restored db");
        let stored: String = restored
            .query_row(
                "SELECT local_path FROM local_asset WHERE id='asset-a'",
                [],
                |row| row.get(0),
            )
            .expect("restored path");
        assert_eq!(PathBuf::from(stored), old_path);
        drop(restored);
        fs::remove_dir_all(data).expect("cleanup");
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

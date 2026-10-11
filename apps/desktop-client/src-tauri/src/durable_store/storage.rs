use super::{
    connection::{open_connection_at, sql_error},
    crypto::master_key,
    paths::{db_path, ensure_controlled_path, reject_symbolic_path, spool_path, store_root},
    queue::read_queue_item,
    schema::initialize_schema,
};
use rusqlite::{params, Connection};
use serde::Serialize;
use std::{
    fs,
    path::{Path, PathBuf},
};
use tauri::AppHandle;

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct StorageUsage {
    pub spool_bytes: u64,
    pub database_bytes: u64,
    pub free_bytes: Option<u64>,
    pub asset_count: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CleanupCandidate {
    pub id: String,
    pub filename: String,
    pub confirmed_at: String,
    pub size_bytes: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CleanupResult {
    pub removed: usize,
    pub failures: Vec<String>,
}

pub fn storage_usage(app: AppHandle) -> Result<StorageUsage, String> {
    let root = store_root(&app)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    usage_at(&root, &conn)
}

pub(crate) fn usage_at(root: &Path, conn: &Connection) -> Result<StorageUsage, String> {
    let mut database_bytes = 0;
    for suffix in ["", "-wal", "-shm"] {
        let path = PathBuf::from(format!("{}{}", db_path(root).display(), suffix));
        reject_symbolic_path(&path)?;
        if path.exists() {
            database_bytes += fs::metadata(path).map_err(|error| error.to_string())?.len();
        }
    }
    Ok(StorageUsage {
        spool_bytes: disk_bytes(&spool_path(root))?,
        database_bytes,
        free_bytes: volume_free_bytes(root),
        asset_count: conn
            .query_row(
                "SELECT COUNT(*) FROM local_asset WHERE state <> 'purged'",
                [],
                |row| row.get(0),
            )
            .map_err(sql_error)?,
    })
}

fn disk_bytes(path: &Path) -> Result<u64, String> {
    reject_symbolic_path(path)?;
    let metadata = fs::metadata(path).map_err(|error| error.to_string())?;
    if metadata.is_file() {
        return Ok(metadata.len());
    }
    let mut total = 0;
    for entry in fs::read_dir(path).map_err(|error| error.to_string())? {
        total += disk_bytes(&entry.map_err(|error| error.to_string())?.path())?;
    }
    Ok(total)
}

// 空闲容量必须取当前账号 spool 所在卷，不能用其它盘的空闲量作保证；不可读取时明确返回未知。
fn volume_free_bytes(root: &Path) -> Option<u64> {
    #[cfg(windows)]
    {
        use std::os::windows::ffi::OsStrExt;
        #[link(name = "kernel32")]
        extern "system" {
            fn GetDiskFreeSpaceExW(
                path: *const u16,
                available: *mut u64,
                total: *mut u64,
                free: *mut u64,
            ) -> i32;
        }
        let path: Vec<u16> = root.as_os_str().encode_wide().chain(Some(0)).collect();
        let mut available = 0;
        // UTF-16 路径以零结尾，三个输出指针只在调用期间有效；只请求当前 Windows 用户可用字节数。
        if unsafe {
            GetDiskFreeSpaceExW(
                path.as_ptr(),
                &mut available,
                std::ptr::null_mut(),
                std::ptr::null_mut(),
            )
        } != 0
        {
            Some(available)
        } else {
            None
        }
    }
    #[cfg(not(windows))]
    {
        let _ = root;
        None
    }
}

pub fn cleanup_candidates(
    app: AppHandle,
    retain_days: i64,
) -> Result<Vec<CleanupCandidate>, String> {
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    candidates_at(&conn, &key, retain_days)
}

pub(crate) fn candidates_at(
    conn: &Connection,
    key: &[u8; 32],
    retain_days: i64,
) -> Result<Vec<CleanupCandidate>, String> {
    if !(1..=3650).contains(&retain_days) {
        return Err("保留天数须为 1 到 3650 天，请按学校要求填写。".into());
    }
    let cutoff = chrono::Utc::now() - chrono::Duration::days(retain_days);
    let mut statement = conn.prepare("SELECT a.id,a.confirmed_at,a.size_bytes FROM local_asset a JOIN sync_event e ON e.entity_id=a.id WHERE e.operation='scan_upload' AND e.state='archived' AND a.state IN ('retained','purging') AND a.confirmed_at IS NOT NULL").map_err(sql_error)?;
    let rows = statement
        .query_map([], |row| {
            Ok((
                row.get::<_, String>(0)?,
                row.get::<_, String>(1)?,
                row.get::<_, u64>(2)?,
            ))
        })
        .map_err(sql_error)?
        .collect::<Result<Vec<_>, _>>()
        .map_err(sql_error)?;
    let mut result = Vec::new();
    for (id, confirmed_at, size_bytes) in rows {
        let confirmed = chrono::DateTime::parse_from_rfc3339(&confirmed_at)
            .map_err(|_| "原件确认日期无效，拒绝清理")?;
        if confirmed > cutoff {
            continue;
        }
        let item = read_queue_item(conn, key, &id)?;
        // 移出未上传文件也会归档，但没有服务端回执；仅有真实远端上传和文件关联事实的原件可删除。
        if item.file_asset_id.as_deref().is_none_or(|id| id.is_empty())
            || item
                .remote_upload_id
                .as_deref()
                .is_none_or(|id| id.is_empty())
        {
            continue;
        }
        result.push(CleanupCandidate {
            id,
            filename: item.file_name.unwrap_or(item.title),
            confirmed_at,
            size_bytes,
        });
    }
    Ok(result)
}

pub fn cleanup_archived(
    app: AppHandle,
    retain_days: i64,
    ids: Vec<String>,
) -> Result<CleanupResult, String> {
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    cleanup_at(&root, &conn, &key, retain_days, ids)
}

pub(crate) fn cleanup_at(
    root: &Path,
    conn: &Connection,
    key: &[u8; 32],
    retain_days: i64,
    ids: Vec<String>,
) -> Result<CleanupResult, String> {
    if ids.len() > 500 {
        return Err("每次最多清理 500 份，请分批核对。".into());
    }
    let eligible = candidates_at(conn, key, retain_days)?;
    let mut targets = Vec::new();
    for id in ids {
        if !eligible.iter().any(|item| item.id == id) {
            return Err("原件状态或保留期限已变化，请刷新并重新确认。".into());
        }
        if targets.iter().any(|(previous, _)| previous == &id) {
            continue;
        }
        let path: String = conn
            .query_row(
                "SELECT local_path FROM local_asset WHERE id=?1",
                params![id],
                |row| row.get(0),
            )
            .map_err(sql_error)?;
        let path = PathBuf::from(path);
        uuid::Uuid::parse_str(&id).map_err(|_| "原件标识无效，拒绝清理")?;
        if path != spool_path(root).join(&id) && path != spool_path(root).join(format!("{id}.enc"))
        {
            return Err("原件路径与标识不一致，拒绝清理。".into());
        }
        // 在修改任何状态前校验全部存在路径；逐项删除文件和空目录，不跟随链接，也不接受数据库中的任意磁盘路径。
        if path.exists() {
            ensure_controlled_path(root, &path)?;
            validate_tree(root, &path)?;
        } else {
            let parent = path.parent().ok_or("原件路径无效")?;
            ensure_controlled_path(root, parent)?;
        }
        targets.push((id, path));
    }
    let mut result = CleanupResult {
        removed: 0,
        failures: Vec::new(),
    };
    for (id, path) in targets {
        // 先落盘 purging 再删文件；中断或局部失败时下次仍可重试，不能把已缺分块的原件当作可上传文件。
        conn.execute(
            "UPDATE local_asset SET state='purging' WHERE id=?1",
            params![id],
        )
        .map_err(sql_error)?;
        if let Err(error) = remove_tree(root, &path) {
            result.failures.push(format!("{id}：{error}"));
            continue;
        }
        let transaction = conn.unchecked_transaction().map_err(sql_error)?;
        transaction
            .execute(
                "DELETE FROM local_asset_chunk WHERE local_asset_id=?1",
                params![id],
            )
            .map_err(sql_error)?;
        transaction
            .execute(
                "UPDATE local_asset SET state='purged' WHERE id=?1",
                params![id],
            )
            .map_err(sql_error)?;
        transaction.commit().map_err(sql_error)?;
        // 队列密文、幂等键和服务端回执留在 SQLite；清理原件不能使同一上传被当作新任务重发。
        result.removed += 1;
    }
    Ok(result)
}

fn validate_tree(root: &Path, path: &Path) -> Result<(), String> {
    reject_symbolic_path(path)?;
    ensure_controlled_path(root, path)?;
    if path.is_dir() {
        for entry in fs::read_dir(path).map_err(|error| error.to_string())? {
            validate_tree(root, &entry.map_err(|error| error.to_string())?.path())?;
        }
    }
    Ok(())
}

fn remove_tree(root: &Path, path: &Path) -> Result<(), String> {
    reject_symbolic_path(path)?;
    if !path.exists() {
        return Ok(());
    }
    ensure_controlled_path(root, path)?;
    if path.is_dir() {
        for entry in fs::read_dir(path).map_err(|error| error.to_string())? {
            remove_tree(root, &entry.map_err(|error| error.to_string())?.path())?;
        }
        fs::remove_dir(path).map_err(|error| error.to_string())
    } else {
        fs::remove_file(path).map_err(|error| error.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::durable_store::crypto::encrypt_json;

    fn fixture() -> (PathBuf, Connection, [u8; 32]) {
        let root =
            std::env::temp_dir().join(format!("edugrade-storage-test-{}", uuid::Uuid::new_v4()));
        fs::create_dir_all(spool_path(&root)).unwrap();
        let conn = Connection::open_in_memory().unwrap();
        initialize_schema(&conn).unwrap();
        (root, conn, [61_u8; 32])
    }
    fn asset(
        root: &Path,
        conn: &Connection,
        key: &[u8; 32],
        state: &str,
        age_days: i64,
        receipt: bool,
    ) -> String {
        let id = uuid::Uuid::new_v4().to_string();
        let path = spool_path(root).join(&id);
        fs::create_dir(&path).unwrap();
        fs::write(path.join("0.chunk"), [0_u8; 25]).unwrap();
        let date = (chrono::Utc::now() - chrono::Duration::days(age_days)).to_rfc3339();
        let value = serde_json::json!({ "id": id, "title": "答卷.pdf", "kind": "scan_upload", "status": state, "progress": 100, "detail": "confirmed", "updatedAt": date, "fileName": "答卷.pdf", "fileAssetId": if receipt { Some("remote-file") } else { None }, "remoteUploadId": if receipt { Some("upload-1") } else { None } });
        let (payload, nonce) = encrypt_json(key, &value).unwrap();
        conn.execute("INSERT INTO local_asset(id,sha256,size_bytes,mime,local_path,file_nonce,state,created_at,updated_at,confirmed_at) VALUES(?1,'sha',9,'application/pdf',?2,'chunked-aes-gcm-v1','retained',?3,?3,?4)", params![id,path.to_string_lossy(),date,if receipt {Some(date.clone())} else {None}]).unwrap();
        conn.execute("INSERT INTO sync_event(id,operation,entity_id,idempotency_key,state,payload_ciphertext,payload_nonce,created_at,updated_at) VALUES(?1,'scan_upload',?1,?1,?2,?3,?4,?5,?5)", params![id,state,payload,nonce,date]).unwrap();
        id
    }

    #[test]
    fn cleanup_keeps_unconfirmed_and_recent_files_and_retains_confirmed_receipts() {
        let (root, conn, key) = fixture();
        let old = asset(&root, &conn, &key, "archived", 20, true);
        let pending = asset(&root, &conn, &key, "archived", 20, false);
        let recent = asset(&root, &conn, &key, "archived", 1, true);
        let unarchived = asset(&root, &conn, &key, "succeeded", 20, true);
        let candidates = candidates_at(&conn, &key, 7).unwrap();
        assert_eq!(candidates.len(), 1);
        assert_eq!(candidates[0].id, old);
        assert!(cleanup_at(&root, &conn, &key, 7, vec![old.clone(), pending.clone()]).is_err());
        assert!(
            spool_path(&root).join(&old).exists(),
            "invalid mixed selection changes no files"
        );
        let result = cleanup_at(&root, &conn, &key, 7, vec![old.clone()]).unwrap();
        assert_eq!(result.removed, 1);
        assert!(result.failures.is_empty());
        assert!(!spool_path(&root).join(&old).exists());
        for id in [pending, recent, unarchived] {
            assert!(spool_path(&root).join(id).exists());
        }
        assert_eq!(
            read_queue_item(&conn, &key, &old)
                .unwrap()
                .file_asset_id
                .as_deref(),
            Some("remote-file")
        );
        assert_eq!(
            conn.query_row(
                "SELECT state FROM local_asset WHERE id=?1",
                params![old],
                |row| row.get::<_, String>(0)
            )
            .unwrap(),
            "purged"
        );
        assert!(
            super::super::queue::existing_queue_item_for_import(&conn, &key, &old).is_err(),
            "cleaned originals cannot be requeued by duplicate import"
        );
        assert!(candidates_at(&conn, &key, 0).is_err());
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn usage_counts_actual_ciphertext_and_orphans_on_the_same_volume() {
        let (root, conn, key) = fixture();
        asset(&root, &conn, &key, "archived", 20, true);
        fs::write(spool_path(&root).join("incomplete.chunk"), [0_u8; 13]).unwrap();
        let usage = usage_at(&root, &conn).unwrap();
        assert_eq!(
            usage.spool_bytes, 38,
            "counts disk bytes instead of plaintext size metadata"
        );
        assert_eq!(usage.asset_count, 1);
        #[cfg(windows)]
        assert!(usage.free_bytes.is_some());
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn cleanup_refuses_account_root_and_recovers_interrupted_purging() {
        let (root, conn, key) = fixture();
        let id = asset(&root, &conn, &key, "archived", 20, true);
        let original = spool_path(&root).join(&id);
        conn.execute(
            "UPDATE local_asset SET local_path=?2 WHERE id=?1",
            params![id, spool_path(&root).to_string_lossy()],
        )
        .unwrap();
        assert!(cleanup_at(&root, &conn, &key, 7, vec![id.clone()]).is_err());
        assert!(original.exists());
        // 模拟进程在状态落盘、部分分块删除后中断；重试只完成已确认原件清理，不重建上传。
        conn.execute(
            "UPDATE local_asset SET local_path=?2,state='purging' WHERE id=?1",
            params![id, original.to_string_lossy()],
        )
        .unwrap();
        fs::remove_file(original.join("0.chunk")).unwrap();
        assert_eq!(
            cleanup_at(&root, &conn, &key, 7, vec![id]).unwrap().removed,
            1
        );
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn reselect_restores_only_unstarted_archived_files_with_the_same_identity() {
        let (root, conn, key) = fixture();
        let unstarted = asset(&root, &conn, &key, "archived", 20, false);
        let confirmed = asset(&root, &conn, &key, "archived", 20, true);
        let item =
            super::super::queue::existing_queue_item_for_import(&conn, &key, &unstarted).unwrap();
        assert_eq!(item.id, unstarted);
        assert_eq!(item.status, "pending");
        assert!(
            super::super::queue::existing_queue_item_for_import(&conn, &key, &confirmed).is_err()
        );
        assert_eq!(
            conn.query_row("SELECT COUNT(*) FROM sync_event", [], |row| row
                .get::<_, i64>(0))
                .unwrap(),
            2
        );
        fs::remove_dir_all(root).unwrap();
    }
}

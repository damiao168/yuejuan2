use rusqlite::{params, Connection, OptionalExtension, TransactionBehavior};
use serde_json::Value;
use tauri::AppHandle;

use super::{
    connection::{now_rfc3339, open_connection, open_connection_at, sql_error},
    crypto::{decrypt_json, encrypt_json, master_key},
    paths::store_root,
    schema::initialize_schema,
    types::OfflineDraftEnvelope,
};

const MAX_DRAFT_PAYLOAD_BYTES: usize = 4 * 1024 * 1024;

pub fn save_durable_draft(app: AppHandle, record: Value) -> Result<(), String> {
    if serde_json::to_vec(&record)
        .map_err(|error| error.to_string())?
        .len()
        > MAX_DRAFT_PAYLOAD_BYTES
    {
        return Err("offline draft exceeds the durable storage limit".into());
    }
    let metadata = draft_metadata(&record)?;
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    let (payload, nonce) = encrypt_json(&key, &record)?;
    conn.execute(
        "INSERT INTO draft (task_id, anonymous_code, saved_at, expires_at, sync_status, sync_message, payload_ciphertext, payload_nonce, updated_at)
         VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?3)
         ON CONFLICT(task_id) DO UPDATE SET anonymous_code = excluded.anonymous_code, saved_at = excluded.saved_at,
           expires_at = excluded.expires_at, sync_status = excluded.sync_status, sync_message = excluded.sync_message,
           payload_ciphertext = excluded.payload_ciphertext, payload_nonce = excluded.payload_nonce, updated_at = excluded.updated_at",
        params![metadata.task_id, metadata.anonymous_code, metadata.saved_at, metadata.expires_at, metadata.sync_status, metadata.sync_message, payload, nonce],
    ).map_err(sql_error)?;
    Ok(())
}

pub fn list_durable_drafts(app: AppHandle) -> Result<Vec<OfflineDraftEnvelope>, String> {
    let conn = open_connection(&app)?;
    initialize_schema(&conn)?;
    let mut statement = conn
        .prepare("SELECT task_id, anonymous_code, saved_at, expires_at, sync_status, sync_message FROM draft ORDER BY saved_at DESC")
        .map_err(sql_error)?;
    let rows = statement
        .query_map([], |row| {
            Ok(OfflineDraftEnvelope {
                task_id: row.get(0)?,
                anonymous_code: row.get(1)?,
                saved_at: row.get(2)?,
                expires_at: row.get(3)?,
                sync_status: row.get(4)?,
                sync_message: row.get(5)?,
            })
        })
        .map_err(sql_error)?
        .collect::<Result<Vec<_>, _>>()
        .map_err(sql_error);
    rows
}

pub fn load_durable_draft(app: AppHandle, task_id: String) -> Result<Option<Value>, String> {
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    let payload = conn
        .query_row(
            "SELECT payload_ciphertext, payload_nonce FROM draft WHERE task_id = ?1",
            params![task_id],
            |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
        )
        .optional()
        .map_err(sql_error)?;
    payload
        .map(|(ciphertext, nonce)| decrypt_json(&key, &ciphertext, &nonce))
        .transpose()
}

pub fn update_durable_draft_status(
    app: AppHandle,
    task_id: String,
    sync_status: String,
    sync_message: Option<String>,
) -> Result<(), String> {
    // 这里只更新可查询的状态元数据，不重加密正文；调用方必须先保存草稿，再更新同步状态。
    validate_draft_status(&sync_status)?;
    let conn = open_connection(&app)?;
    initialize_schema(&conn)?;
    let rows = conn.execute(
        "UPDATE draft SET sync_status = ?2, sync_message = ?3, saved_at = ?4, updated_at = ?4 WHERE task_id = ?1",
        params![task_id, sync_status, sync_message, now_rfc3339()],
    ).map_err(sql_error)?;
    if rows != 1 {
        return Err("offline draft was not found".into());
    }
    Ok(())
}

pub fn purge_expired_durable_drafts(app: AppHandle, now: String) -> Result<usize, String> {
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let mut conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    purge_expired_drafts(&mut conn, &key, &now)
}

fn purge_expired_drafts(conn: &mut Connection, key: &[u8; 32], now: &str) -> Result<usize, String> {
    // 查询到删除使用同一写事务，避免清理期间新保存的未确认命令被旧候选误删。
    let tx = conn
        .transaction_with_behavior(TransactionBehavior::Immediate)
        .map_err(sql_error)?;
    let candidates = {
        let mut statement = tx
            .prepare("SELECT task_id, sync_status, payload_ciphertext, payload_nonce FROM draft WHERE expires_at <= ?1 ORDER BY task_id")
            .map_err(sql_error)?;
        let rows = statement
            .query_map(params![now], |row| {
                Ok((
                    row.get::<_, String>(0)?,
                    row.get::<_, String>(1)?,
                    row.get::<_, String>(2)?,
                    row.get::<_, String>(3)?,
                ))
            })
            .map_err(sql_error)?
            .collect::<Result<Vec<_>, _>>()
            .map_err(sql_error)?;
        rows
    };
    let mut removed = 0;
    for (task_id, status, ciphertext, nonce) in candidates {
        // 保留期只约束可清理的草稿；响应丢失后唯一的命令 ID 和原输入必须留下。
        // 解密失败会回滚本次全部删除，不能把未知正文当成普通草稿。
        let record: Value = decrypt_json(key, &ciphertext, &nonce)?;
        record
            .as_object()
            .ok_or("expired offline draft payload is not an object; cleanup was stopped")?;
        // 状态元数据可能比正文新；服务端成功已持久化后，冻结正文也可按保留期清理。
        // 未提交的普通草稿也可能是唯一评分副本；不因过期而删除未同步记录。
        if status != "synced" {
            continue;
        }
        removed += tx
            .execute("DELETE FROM draft WHERE task_id = ?1", params![task_id])
            .map_err(sql_error)?;
    }
    tx.commit().map_err(sql_error)?;
    Ok(removed)
}

fn validate_draft_status(status: &str) -> Result<(), String> {
    if matches!(
        status,
        "draft" | "syncing" | "synced" | "failed" | "conflict"
    ) {
        Ok(())
    } else {
        Err("offline draft status is invalid".into())
    }
}

pub(crate) struct DraftMetadata {
    task_id: String,
    anonymous_code: String,
    saved_at: String,
    expires_at: String,
    sync_status: String,
    sync_message: Option<String>,
}

pub(crate) fn draft_metadata(value: &Value) -> Result<DraftMetadata, String> {
    let object = value.as_object().ok_or("offline draft must be an object")?;
    let required = |name: &str| {
        object
            .get(name)
            .and_then(Value::as_str)
            .filter(|value| !value.trim().is_empty())
            .map(str::to_owned)
            .ok_or_else(|| format!("offline draft {name} is required"))
    };
    let metadata = DraftMetadata {
        task_id: required("taskId")?,
        anonymous_code: required("anonymousCode")?,
        saved_at: required("savedAt")?,
        expires_at: required("expiresAt")?,
        sync_status: required("syncStatus")?,
        sync_message: object
            .get("syncMessage")
            .and_then(Value::as_str)
            .map(str::to_owned),
    };
    validate_draft_status(&metadata.sync_status)?;
    Ok(metadata)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    const KEY: [u8; 32] = [37; 32];
    const NOW: &str = "2026-10-04T00:00:00Z";

    fn fixture() -> Connection {
        let conn = Connection::open_in_memory().expect("open fixture");
        initialize_schema(&conn).expect("initialize fixture");
        conn
    }

    fn insert(conn: &Connection, id: &str, status: &str, command: bool, expires_at: &str) {
        let mut record = json!({"taskId": id, "syncStatus": "syncing", "draft": {"score": 0}});
        if command {
            record["submissionCommand"] = json!({"id": "frozen-command", "payload": {"score": 0}});
        }
        let (ciphertext, nonce) = encrypt_json(&KEY, &record).expect("encrypt fixture");
        conn.execute(
            "INSERT INTO draft (task_id, anonymous_code, saved_at, expires_at, sync_status, payload_ciphertext, payload_nonce, updated_at)
             VALUES (?1, 'anonymous', ?2, ?3, ?4, ?5, ?6, ?2)",
            params![id, NOW, expires_at, status, ciphertext, nonce],
        )
        .expect("insert fixture");
    }

    fn ids(conn: &Connection) -> Vec<String> {
        conn.prepare("SELECT task_id FROM draft ORDER BY task_id")
            .expect("prepare remaining IDs")
            .query_map([], |row| row.get(0))
            .expect("query remaining IDs")
            .collect::<Result<Vec<_>, _>>()
            .expect("read remaining IDs")
    }

    #[test]
    fn expired_cleanup_preserves_every_unsynced_draft_and_cleans_confirmed_records() {
        let mut conn = fixture();
        insert(&conn, "legacy-draft", "draft", false, NOW);
        insert(&conn, "ordinary-failed", "failed", false, NOW);
        insert(&conn, "confirmed", "synced", true, NOW);
        insert(&conn, "pending-failed", "failed", true, NOW);
        insert(&conn, "pending-syncing", "syncing", true, NOW);
        insert(&conn, "pending-conflict", "conflict", true, NOW);
        insert(&conn, "future", "draft", false, "2026-10-05T00:00:00Z");

        assert_eq!(purge_expired_drafts(&mut conn, &KEY, NOW).unwrap(), 1);
        assert_eq!(
            ids(&conn),
            vec![
                "future",
                "legacy-draft",
                "ordinary-failed",
                "pending-conflict",
                "pending-failed",
                "pending-syncing"
            ]
        );
        let (ciphertext, nonce): (String, String) = conn
            .query_row("SELECT payload_ciphertext, payload_nonce FROM draft WHERE task_id = 'pending-failed'", [], |row| Ok((row.get(0)?, row.get(1)?)))
            .unwrap();
        let record: Value = decrypt_json(&KEY, &ciphertext, &nonce).unwrap();
        assert_eq!(record["submissionCommand"]["id"], "frozen-command");
    }

    #[test]
    fn corrupt_expired_payload_rolls_back_other_deletions() {
        let mut conn = fixture();
        insert(&conn, "a-ordinary", "draft", false, NOW);
        insert(&conn, "z-pending", "failed", true, NOW);
        conn.execute(
            "UPDATE draft SET payload_ciphertext = 'corrupt' WHERE task_id = 'z-pending'",
            [],
        )
        .unwrap();

        assert!(purge_expired_drafts(&mut conn, &KEY, NOW).is_err());
        assert_eq!(ids(&conn), vec!["a-ordinary", "z-pending"]);
    }

    #[test]
    fn wrong_key_preserves_all_expired_records() {
        let mut conn = fixture();
        insert(&conn, "ordinary", "draft", false, NOW);
        insert(&conn, "pending", "failed", true, NOW);

        assert!(purge_expired_drafts(&mut conn, &[88; 32], NOW).is_err());
        assert_eq!(ids(&conn), vec!["ordinary", "pending"]);
    }
}

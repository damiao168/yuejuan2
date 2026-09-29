use rusqlite::{params, OptionalExtension};
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
    let conn = open_connection(&app)?;
    initialize_schema(&conn)?;
    conn.execute("DELETE FROM draft WHERE expires_at <= ?1", params![now])
        .map_err(sql_error)
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

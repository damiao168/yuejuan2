use rusqlite::{params, Connection, OptionalExtension};
use tauri::AppHandle;

use super::{
    connection::{now_rfc3339, open_connection, open_connection_at, sql_error},
    crypto::{decrypt_json, encrypt_json, master_key},
    paths::store_root,
    schema::initialize_schema,
    types::DurableQueueItem,
};

pub fn list_durable_scan_queue(app: AppHandle) -> Result<Vec<DurableQueueItem>, String> {
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    // 服务端确认一个或多个分块后，进程可能在下一次界面更新前
    // 停止。重启时只把本地临时的 `uploading`
    // 状态视为已中断；remote_upload_id 和 confirmed_offset
    // 仍以加密形式保存在队列载荷中。下一次初始化调用
    // 会向服务端查询权威偏移，不会重发已确认的分块。
    recover_interrupted_uploads(&conn)?;
    let mut statement = conn
        .prepare(
            "SELECT entity_id FROM sync_event
             WHERE operation = 'scan_upload' AND state <> 'archived'
             ORDER BY updated_at DESC",
        )
        .map_err(sql_error)?;
    let ids = statement
        .query_map([], |row| row.get::<_, String>(0))
        .map_err(sql_error)?
        .collect::<Result<Vec<_>, _>>()
        .map_err(sql_error)?;
    ids.iter()
        .map(|id| read_queue_item(&conn, &key, id))
        .collect()
}

pub(crate) fn recover_interrupted_uploads(conn: &Connection) -> Result<(), String> {
    conn.execute(
        "UPDATE sync_event
         SET state = 'pending', updated_at = ?1
         WHERE operation = 'scan_upload' AND state = 'uploading'",
        params![now_rfc3339()],
    )
    .map_err(sql_error)?;
    conn.execute(
        "UPDATE local_asset
         SET state = 'queued', updated_at = ?1
         WHERE state = 'uploading'",
        params![now_rfc3339()],
    )
    .map_err(sql_error)?;
    Ok(())
}

pub fn persist_durable_scan_queue_item(
    app: AppHandle,
    mut item: DurableQueueItem,
) -> Result<(), String> {
    if item.kind != "scan_upload" {
        return Err("only scan_upload records can enter the durable scan queue".into());
    }
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    let id = item
        .local_asset_id
        .clone()
        .unwrap_or_else(|| item.id.clone());
    let previous = read_queue_item(&conn, &key, &id)?;
    validate_transition(&previous.status, &item.status)?;
    item.id = id.clone();
    item.local_asset_id = Some(id.clone());
    // 显示名称允许重新核对，但持久化不能将同一原件改属到另一考试或批次。
    item.exam_id = previous.exam_id.clone();
    item.capture_batch_id = previous.capture_batch_id.clone();
    // 幂等键始终沿用原始入队记录，重试和界面更新都不能把同一原件变成新上传命令。
    item.idempotency_key = previous.idempotency_key.clone();
    item.retry_count = Some(
        previous.retry_count.unwrap_or_default()
            + i64::from(item.status == "failed" && previous.status != "failed"),
    );
    item.updated_at = now_rfc3339();
    let (payload_ciphertext, payload_nonce) = encrypt_json(&key, &item)?;
    let transaction = conn.unchecked_transaction().map_err(sql_error)?;
    transaction
        .execute(
            "UPDATE sync_event
             SET state = ?2, retry_count = ?3, last_error = ?4, payload_ciphertext = ?5, payload_nonce = ?6, updated_at = ?7
             WHERE entity_id = ?1 AND operation = 'scan_upload'",
            params![
                id,
                item.status,
                item.retry_count.unwrap_or_default(),
                if item.status == "failed" || item.status == "conflict" { Some(item.detail.as_str()) } else { None },
                payload_ciphertext,
                payload_nonce,
                item.updated_at,
            ],
        )
        .map_err(sql_error)?;
    if let Some(remote_id) = item.remote_upload_id.as_deref() {
        transaction
            .execute(
                "INSERT INTO upload_session (local_asset_id, remote_id, chunk_size, confirmed_offset, retry_count, last_error, updated_at)
                 VALUES (?1, ?2, NULL, ?3, ?4, ?5, ?6)
                 ON CONFLICT(local_asset_id) DO UPDATE SET remote_id = excluded.remote_id,
                   confirmed_offset = excluded.confirmed_offset, retry_count = excluded.retry_count,
                   last_error = excluded.last_error, updated_at = excluded.updated_at",
                params![
                    id,
                    remote_id,
                    item.confirmed_offset.unwrap_or_default(),
                    item.retry_count.unwrap_or_default(),
                    if item.status == "failed" || item.status == "conflict" { Some(item.detail.as_str()) } else { None },
                    item.updated_at,
                ],
            )
            .map_err(sql_error)?;
    }
    let asset_state = match item.status.as_str() {
        "succeeded" => "confirmed",
        "uploading" => "uploading",
        "conflict" => "conflict",
        "failed" => "failed",
        _ => "queued",
    };
    transaction
        .execute(
            "UPDATE local_asset
             SET state = ?2, updated_at = ?3, confirmed_at = CASE WHEN ?2 = 'confirmed' THEN ?3 ELSE confirmed_at END
             WHERE id = ?1",
            params![id, asset_state, item.updated_at],
        )
        .map_err(sql_error)?;
    transaction.commit().map_err(sql_error)
}

pub fn archive_durable_scan_queue_items(app: AppHandle, ids: Vec<String>) -> Result<(), String> {
    // 归档只隐藏已由服务端确认的队列项，原件仍保留；它不等同于删除扫描文件。
    if ids.is_empty() {
        return Ok(());
    }
    let conn = open_connection(&app)?;
    initialize_schema(&conn)?;
    let transaction = conn.unchecked_transaction().map_err(sql_error)?;
    for id in ids {
        let state = transaction
            .query_row(
                "SELECT state FROM sync_event WHERE operation = 'scan_upload' AND entity_id = ?1",
                params![id],
                |row| row.get::<_, String>(0),
            )
            .optional()
            .map_err(sql_error)?;
        if state.as_deref() != Some("succeeded") {
            return Err("only server-confirmed scan assets can be archived".into());
        }
        let now = now_rfc3339();
        transaction
            .execute(
                "UPDATE sync_event SET state = 'archived', updated_at = ?2 WHERE operation = 'scan_upload' AND entity_id = ?1",
                params![id, now],
            )
            .map_err(sql_error)?;
        transaction
            .execute(
                "UPDATE local_asset SET state = 'retained', updated_at = ?2 WHERE id = ?1",
                params![id, now],
            )
            .map_err(sql_error)?;
    }
    transaction.commit().map_err(sql_error)
}

pub fn remove_unstarted_scan_queue_item(app: AppHandle, id: String) -> Result<(), String> {
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    let transaction = conn.unchecked_transaction().map_err(sql_error)?;
    let item = read_queue_item(&transaction, &key, &id)?;
    // 远端上传一旦启动，不能把原件静默撤出后重新归属；未启动的移出仅隐藏队列，保留加密原件。
    if item.remote_upload_id.is_some()
        || item.file_asset_id.is_some()
        || ["uploading", "succeeded", "archived"].contains(&item.status.as_str())
    {
        return Err("已启动或确认上传的文件不能移出，请先核对服务端处理状态。".into());
    }
    transaction.execute("UPDATE sync_event SET state='archived',updated_at=?2 WHERE entity_id=?1 AND operation='scan_upload'", params![id, now_rfc3339()]).map_err(sql_error)?;
    transaction
        .execute(
            "UPDATE local_asset SET state='retained',updated_at=?2 WHERE id=?1",
            params![id, now_rfc3339()],
        )
        .map_err(sql_error)?;
    transaction.commit().map_err(sql_error)
}

pub(crate) fn read_queue_item(
    conn: &Connection,
    key: &[u8; 32],
    id: &str,
) -> Result<DurableQueueItem, String> {
    let (payload, nonce, state, retry_count) = conn
        .query_row(
            "SELECT payload_ciphertext, payload_nonce, state, retry_count
             FROM sync_event WHERE operation = 'scan_upload' AND entity_id = ?1",
            params![id],
            |row| {
                Ok((
                    row.get::<_, String>(0)?,
                    row.get::<_, String>(1)?,
                    row.get::<_, String>(2)?,
                    row.get::<_, i64>(3)?,
                ))
            },
        )
        .map_err(sql_error)?;
    let mut item: DurableQueueItem = decrypt_json(key, &payload, &nonce)?;
    // 重启恢复可能只更新数据库状态；用状态列覆盖密文中的旧快照。
    item.id = id.to_string();
    item.local_asset_id = Some(id.to_string());
    item.status = state;
    item.retry_count = Some(retry_count);
    Ok(item)
}

pub(crate) fn existing_queue_item_for_import(
    conn: &Connection,
    key: &[u8; 32],
    id: &str,
) -> Result<DurableQueueItem, String> {
    let mut item = read_queue_item(conn, key, id)?;
    let (state, confirmed_at): (String, Option<String>) = conn
        .query_row(
            "SELECT state,confirmed_at FROM local_asset WHERE id=?1",
            params![id],
            |row| Ok((row.get(0)?, row.get(1)?)),
        )
        .map_err(sql_error)?;
    if ["purging", "purged"].contains(&state.as_str()) {
        return Err(
            "该原件已按确认操作清理，上传回执仍保留；请到服务端核对答卷，不会重复上传。".into(),
        );
    }
    if item.status == "archived" {
        if confirmed_at.is_some() || item.remote_upload_id.is_some() || item.file_asset_id.is_some()
        {
            return Err("该原件已确认上传并归档，请刷新批次处理状态核对答卷。".into());
        }
        // 移出未启动的原件可显式重选恢复；沿用原资产和幂等键，绝不复活已确认或已清理的上传。
        let transaction = conn.unchecked_transaction().map_err(sql_error)?;
        transaction.execute("UPDATE sync_event SET state='pending' WHERE entity_id=?1 AND operation='scan_upload'", params![id]).map_err(sql_error)?;
        transaction
            .execute(
                "UPDATE local_asset SET state='queued' WHERE id=?1",
                params![id],
            )
            .map_err(sql_error)?;
        transaction.commit().map_err(sql_error)?;
        item.status = "pending".into();
        item.detail = "已重新选择本机原件，等待主动上传。".into();
    }
    Ok(item)
}

pub(crate) fn validate_transition(previous: &str, next: &str) -> Result<(), String> {
    let allowed = matches!(
        (previous, next),
        ("pending", "pending" | "uploading" | "failed" | "conflict")
            | (
                "uploading",
                "uploading" | "pending" | "failed" | "succeeded" | "conflict"
            )
            | ("failed", "failed" | "pending" | "uploading" | "conflict")
            | ("conflict", "conflict" | "pending" | "failed")
            | ("succeeded", "succeeded")
    );
    if allowed {
        Ok(())
    } else {
        Err(format!(
            "invalid durable queue transition: {previous} -> {next}"
        ))
    }
}

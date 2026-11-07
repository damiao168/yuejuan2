//! 本地持久数据的 Tauri 命令边界。
//! 账号数据命令在同一会话锁内核对身份并执行读写；离线授权的有效期与解锁交给 offline_access。

use crate::durable_store;
use tauri::AppHandle;

#[tauri::command]
pub(crate) fn claim_legacy_durable_store(app: AppHandle, session_id: String) -> Result<(), String> {
    durable_store::with_session(&session_id, || durable_store::claim_legacy_store(app))
}

#[tauri::command]
pub(crate) fn bind_durable_session(
    server: String,
    tenant_id: String,
    actor_id: String,
) -> Result<String, String> {
    durable_store::bind_session(&server, &tenant_id, &actor_id)
}

#[tauri::command]
pub(crate) fn clear_durable_session() -> Result<(), String> {
    durable_store::clear_session()
}

#[tauri::command]
pub(crate) fn remove_unstarted_scan_queue_item(
    app: AppHandle,
    session_id: String,
    id: String,
) -> Result<(), String> {
    durable_store::with_session(&session_id, || {
        durable_store::remove_unstarted_scan_queue_item(app, id)
    })
}

#[tauri::command]
pub(crate) fn durable_storage_usage(
    app: AppHandle,
    session_id: String,
) -> Result<durable_store::storage::StorageUsage, String> {
    durable_store::with_session(&session_id, || durable_store::storage::storage_usage(app))
}

#[tauri::command]
pub(crate) fn list_archived_cleanup_candidates(
    app: AppHandle,
    session_id: String,
    retain_days: i64,
) -> Result<Vec<durable_store::storage::CleanupCandidate>, String> {
    durable_store::with_session(&session_id, || {
        durable_store::storage::cleanup_candidates(app, retain_days)
    })
}

#[tauri::command]
pub(crate) fn cleanup_archived_originals(
    app: AppHandle,
    session_id: String,
    retain_days: i64,
    ids: Vec<String>,
) -> Result<durable_store::storage::CleanupResult, String> {
    durable_store::with_session(&session_id, || {
        durable_store::storage::cleanup_archived(app, retain_days, ids)
    })
}

#[tauri::command]
pub(crate) fn authorize_offline_access(
    session_id: String,
    access: durable_store::offline_access::OfflineAccess,
) -> Result<durable_store::offline_access::OfflineAccess, String> {
    durable_store::offline_access::authorize(&session_id, access)
}
#[tauri::command]
pub(crate) fn read_offline_access(
) -> Result<Option<durable_store::offline_access::OfflineAccess>, String> {
    durable_store::offline_access::read_access()
}
#[tauri::command]
pub(crate) fn unlock_offline_access(
    server: String,
    tenant_id: String,
    actor_id: String,
) -> Result<(String, durable_store::offline_access::OfflineAccess), String> {
    durable_store::offline_access::unlock(&server, &tenant_id, &actor_id)
}
#[tauri::command]
pub(crate) fn revoke_offline_access(session_id: Option<String>) -> Result<(), String> {
    match session_id {
        Some(id) => durable_store::with_session(&id, durable_store::offline_access::revoke),
        None => durable_store::offline_access::revoke(),
    }
}

#[tauri::command]
pub(crate) fn begin_spool_local_asset(
    app: AppHandle,
    session_id: String,
    input: durable_store::SpoolAssetInput,
) -> Result<durable_store::SpoolAssetSession, String> {
    // 本地数据命令统一在同一会话锁内完成身份核对和读写；新增命令也需保持这个边界。
    durable_store::with_session(&session_id, || {
        durable_store::begin_spool_local_asset(app, input)
    })
}

#[tauri::command]
pub(crate) fn write_spool_local_asset_chunk(
    app: AppHandle,
    session_id: String,
    local_asset_id: String,
    offset: i64,
    bytes: Vec<u8>,
) -> Result<i64, String> {
    durable_store::with_session(&session_id, || {
        durable_store::write_spool_local_asset_chunk(app, local_asset_id, offset, bytes)
    })
}

#[tauri::command]
pub(crate) fn complete_spool_local_asset(
    app: AppHandle,
    session_id: String,
    local_asset_id: String,
) -> Result<durable_store::DurableQueueItem, String> {
    durable_store::with_session(&session_id, || {
        durable_store::complete_spool_local_asset(app, local_asset_id)
    })
}

#[tauri::command]
pub(crate) fn list_durable_scan_queue(
    app: AppHandle,
    session_id: String,
) -> Result<Vec<durable_store::DurableQueueItem>, String> {
    durable_store::with_session(&session_id, || durable_store::list_durable_scan_queue(app))
}

#[tauri::command]
pub(crate) fn persist_durable_scan_queue_item(
    app: AppHandle,
    session_id: String,
    item: durable_store::DurableQueueItem,
) -> Result<(), String> {
    durable_store::with_session(&session_id, || {
        durable_store::persist_durable_scan_queue_item(app, item)
    })
}

#[tauri::command]
pub(crate) fn archive_durable_scan_queue_items(
    app: AppHandle,
    session_id: String,
    ids: Vec<String>,
) -> Result<(), String> {
    durable_store::with_session(&session_id, || {
        durable_store::archive_durable_scan_queue_items(app, ids)
    })
}

#[tauri::command]
pub(crate) fn read_durable_local_asset(
    app: AppHandle,
    session_id: String,
    local_asset_id: String,
) -> Result<durable_store::DurableSpoolFile, String> {
    durable_store::with_session(&session_id, || {
        durable_store::read_durable_local_asset(app, local_asset_id)
    })
}

#[tauri::command]
pub(crate) fn read_durable_local_asset_chunk(
    app: AppHandle,
    session_id: String,
    local_asset_id: String,
    offset: i64,
    length: usize,
) -> Result<Vec<u8>, String> {
    durable_store::with_session(&session_id, || {
        durable_store::read_durable_local_asset_chunk(app, local_asset_id, offset, length)
    })
}

#[tauri::command]
pub(crate) fn save_durable_draft(
    app: AppHandle,
    session_id: String,
    record: serde_json::Value,
) -> Result<(), String> {
    durable_store::with_session(&session_id, || {
        durable_store::save_durable_draft(app, record)
    })
}

#[tauri::command]
pub(crate) fn list_durable_drafts(
    app: AppHandle,
    session_id: String,
) -> Result<Vec<durable_store::OfflineDraftEnvelope>, String> {
    durable_store::with_session(&session_id, || durable_store::list_durable_drafts(app))
}

#[tauri::command]
pub(crate) fn load_durable_draft(
    app: AppHandle,
    session_id: String,
    task_id: String,
) -> Result<Option<serde_json::Value>, String> {
    durable_store::with_session(&session_id, || {
        durable_store::load_durable_draft(app, task_id)
    })
}

#[tauri::command]
pub(crate) fn update_durable_draft_status(
    app: AppHandle,
    session_id: String,
    task_id: String,
    sync_status: String,
    sync_message: Option<String>,
) -> Result<(), String> {
    durable_store::with_session(&session_id, || {
        durable_store::update_durable_draft_status(app, task_id, sync_status, sync_message)
    })
}

#[tauri::command]
pub(crate) fn purge_expired_durable_drafts(
    app: AppHandle,
    session_id: String,
    now: String,
) -> Result<usize, String> {
    durable_store::with_session(&session_id, || {
        durable_store::purge_expired_durable_drafts(app, now)
    })
}

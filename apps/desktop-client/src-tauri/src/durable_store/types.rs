use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SpoolAssetInput {
    pub filename: String,
    pub mime: String,
    pub size: i64,
    pub sha256: String,
    pub exam_id: Option<String>,
    pub capture_batch_id: Option<String>,
    pub submission_id: Option<String>,
    pub page_no: Option<i64>,
    pub quality_checks: Option<Value>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SpoolAssetSession {
    // 这里的确认偏移属于本地原件写入；不要与 DurableQueueItem 中服务端上传的确认偏移混用。
    pub local_asset_id: String,
    pub chunk_size: usize,
    pub confirmed_offset: i64,
    pub item: Option<DurableQueueItem>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DurableQueueItem {
    pub id: String,
    pub title: String,
    pub kind: String,
    pub status: String,
    pub progress: u8,
    pub detail: String,
    pub updated_at: String,
    pub exam_id: Option<String>,
    pub exam_name: Option<String>,
    pub capture_batch_id: Option<String>,
    pub submission_id: Option<String>,
    pub page_no: Option<i64>,
    pub file_name: Option<String>,
    pub file_size: Option<i64>,
    pub content_type: Option<String>,
    pub file_asset_id: Option<String>,
    pub server_status: Option<String>,
    pub requires_reselect: Option<bool>,
    pub quality_checks: Option<Value>,
    pub local_asset_id: Option<String>,
    pub idempotency_key: Option<String>,
    pub retry_count: Option<i64>,
    pub confirmed_offset: Option<i64>,
    pub remote_upload_id: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DurableSpoolFile {
    pub filename: String,
    pub mime: String,
    pub size: i64,
    pub sha256: String,
    pub chunk_size: usize,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DurableStoreStatus {
    pub ready: bool,
    pub database_path: String,
    pub spool_path: String,
    pub legacy_data_present: bool,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OfflineDraftEnvelope {
    pub task_id: String,
    pub anonymous_code: String,
    pub saved_at: String,
    pub expires_at: String,
    pub sync_status: String,
    pub sync_message: Option<String>,
}

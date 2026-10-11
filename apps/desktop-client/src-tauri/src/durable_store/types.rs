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
    pub exam_name: Option<String>,
    pub capture_batch_name: Option<String>,
    pub capture_batch_created_at: Option<String>,
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
    // 新字段为可选，旧密文队列仍能恢复；名称仅供核对，不替代上传归属。
    pub capture_batch_name: Option<String>,
    pub capture_batch_created_at: Option<String>,
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

#[cfg(test)]
mod tests {
    use super::DurableQueueItem;

    #[test]
    fn old_encrypted_queue_payload_without_names_still_decodes() {
        let payload = serde_json::json!({
            "id": "old-file", "title": "answer.pdf", "kind": "scan_upload", "status": "pending",
            "progress": 0, "detail": "", "updatedAt": "2026-10-10T00:00:00Z",
            "examId": "exam-original", "captureBatchId": "batch-original"
        });
        let item: DurableQueueItem = serde_json::from_value(payload).expect("old queue compatibility");
        assert!(item.capture_batch_name.is_none());
        assert!(item.capture_batch_created_at.is_none());
        assert_eq!(item.capture_batch_id.as_deref(), Some("batch-original"));
    }

    #[test]
    fn refreshed_batch_name_and_time_survive_native_queue_serialization() {
        let payload = serde_json::json!({
            "id": "file", "title": "answer.pdf", "kind": "scan_upload", "status": "pending",
            "progress": 0, "detail": "", "updatedAt": "2026-10-10T00:00:00Z",
            "examId": "exam-original", "captureBatchId": "batch-original",
            "examName": "英语月考", "captureBatchName": "教务处上午扫描",
            "captureBatchCreatedAt": "2026-10-10T01:00:00Z"
        });
        let item: DurableQueueItem = serde_json::from_value(payload).expect("metadata from frontend");
        let restored: DurableQueueItem = serde_json::from_slice(&serde_json::to_vec(&item).expect("serialize queue")).expect("restore queue");
        assert_eq!(restored.capture_batch_name.as_deref(), Some("教务处上午扫描"));
        assert_eq!(restored.capture_batch_created_at.as_deref(), Some("2026-10-10T01:00:00Z"));
        assert_eq!(restored.exam_id.as_deref(), Some("exam-original"));
        assert_eq!(restored.capture_batch_id.as_deref(), Some("batch-original"));
    }
}

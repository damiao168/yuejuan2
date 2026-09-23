mod claim;
mod connection;
mod crypto;
mod drafts;
mod paths;
mod queue;
mod schema;
mod spool;
mod types;

use tauri::AppHandle;
use tauri::Manager;

use connection::open_connection_at;
use crypto::master_key;
use paths::{db_path, legacy_store_exists, spool_path, store_root};
use schema::initialize_schema;

pub use claim::claim_legacy_store;
pub use drafts::{
    list_durable_drafts, load_durable_draft, purge_expired_durable_drafts, save_durable_draft,
    update_durable_draft_status,
};
pub use queue::{
    archive_durable_scan_queue_items, list_durable_scan_queue, persist_durable_scan_queue_item,
};
pub use spool::{
    begin_spool_local_asset, complete_spool_local_asset, read_durable_local_asset,
    read_durable_local_asset_chunk, write_spool_local_asset_chunk,
};
pub use types::{
    DurableQueueItem, DurableSpoolFile, DurableStoreStatus, OfflineDraftEnvelope, SpoolAssetInput,
    SpoolAssetSession,
};

pub fn bind_session(server: &str, tenant_id: &str, actor_id: &str) -> Result<String, String> {
    paths::bind_scope(server, tenant_id, actor_id)
}

pub fn clear_session() -> Result<(), String> {
    paths::clear_scope()
}

pub fn with_session<T>(
    session_id: &str,
    operation: impl FnOnce() -> Result<T, String>,
) -> Result<T, String> {
    paths::with_session(session_id, operation)
}

pub fn status(app: AppHandle, session_id: &str) -> Result<DurableStoreStatus, String> {
    paths::with_session(session_id, || status_for_active_session(app))
}

pub(crate) fn scoped_status(app: AppHandle) -> Result<DurableStoreStatus, String> {
    status_for_active_session(app)
}

pub(crate) fn scoped_root(app: &AppHandle) -> Result<std::path::PathBuf, String> {
    store_root(app)
}

fn status_for_active_session(app: AppHandle) -> Result<DurableStoreStatus, String> {
    let data_dir = app
        .path()
        .app_data_dir()
        .map_err(|error| error.to_string())?;
    let legacy_data_present = legacy_store_exists(&data_dir)?;
    let root = store_root(&app)?;
    let _ = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    Ok(DurableStoreStatus {
        ready: true,
        database_path: db_path(&root).to_string_lossy().into_owned(),
        spool_path: spool_path(&root).to_string_lossy().into_owned(),
        legacy_data_present,
    })
}

#[cfg(test)]
mod tests {
    use super::{
        crypto::{
            decrypt_json, durable_credential_account, encrypt_bytes, encrypt_json, sha256_hex,
        },
        drafts::draft_metadata,
        paths::{
            account_root, bind_scope, clear_scope, db_path, durable_data_exists,
            legacy_store_exists, scope_path_component, spool_path, with_session,
            write_new_private_file,
        },
        queue::{recover_interrupted_uploads, validate_transition},
        schema::initialize_schema,
        spool::{
            read_durable_local_asset_at, verify_chunked_asset, CHUNKED_FILE_NONCE,
            SPOOL_CHUNK_BYTES,
        },
        DurableQueueItem,
    };
    use rusqlite::{params, Connection};
    use serde_json::Value;
    use std::fs;
    use uuid::Uuid;

    #[test]
    fn encrypted_payload_requires_the_same_key_and_nonce() {
        let key = [7_u8; 32];
        let value = serde_json::json!({"answer": "敏感草稿", "score": 8});
        let (ciphertext, nonce) = encrypt_json(&key, &value).expect("encrypt");
        assert!(!ciphertext.contains("敏感草稿"));
        let decoded: Value = decrypt_json(&key, &ciphertext, &nonce).expect("decrypt");
        assert_eq!(decoded, value);
        assert!(decrypt_json::<Value>(&[8_u8; 32], &ciphertext, &nonce).is_err());
    }

    #[test]
    fn confirmed_assets_are_immutable_from_the_sync_queue() {
        assert!(validate_transition("pending", "uploading").is_ok());
        assert!(validate_transition("uploading", "succeeded").is_ok());
        assert!(validate_transition("succeeded", "failed").is_err());
        assert!(validate_transition("conflict", "pending").is_ok());
    }

    #[test]
    fn chunked_spool_verifies_order_size_and_content_without_a_whole_file_row() {
        let root = std::env::temp_dir().join(format!("edugrade-spool-test-{}", Uuid::new_v4()));
        let spool = spool_path(&root);
        fs::create_dir_all(&spool).expect("spool directory");
        let conn = Connection::open_in_memory().expect("in-memory SQLite");
        initialize_schema(&conn).expect("schema");
        let key = [17_u8; 32];
        let asset_id = "chunked-asset";
        let plaintext = (0..(SPOOL_CHUNK_BYTES * 2 + 37))
            .map(|index| (index % 251) as u8)
            .collect::<Vec<_>>();
        let expected_sha = sha256_hex(&plaintext);
        conn.execute(
            "INSERT INTO local_asset (id, sha256, size_bytes, mime, local_path, file_nonce, state, created_at, updated_at)
             VALUES (?1, ?2, ?3, 'application/pdf', '', ?4, 'ingesting', ?5, ?5)",
            params![
                asset_id,
                expected_sha,
                plaintext.len() as i64,
                CHUNKED_FILE_NONCE,
                "2026-09-06T00:00:00Z",
            ],
        )
        .expect("asset metadata");

        for (chunk_index, chunk) in plaintext.chunks(SPOOL_CHUNK_BYTES).enumerate() {
            let offset = chunk_index * SPOOL_CHUNK_BYTES;
            let (encrypted, nonce) = encrypt_bytes(&key, chunk).expect("encrypt chunk");
            let path = spool.join(format!("{asset_id}-{offset}.chunk"));
            write_new_private_file(&path, &encrypted).expect("write chunk");
            conn.execute(
                "INSERT INTO local_asset_chunk (local_asset_id, chunk_offset, size_bytes, sha256, local_path, nonce)
                 VALUES (?1, ?2, ?3, ?4, ?5, ?6)",
                params![
                    asset_id,
                    offset as i64,
                    chunk.len() as i64,
                    sha256_hex(chunk),
                    path.to_string_lossy(),
                    nonce,
                ],
            )
            .expect("chunk metadata");
        }

        verify_chunked_asset(
            &conn,
            &root,
            &key,
            asset_id,
            plaintext.len() as i64,
            &expected_sha,
        )
        .expect("complete chunk sequence");
        conn.execute(
            "DELETE FROM local_asset_chunk WHERE local_asset_id=?1 AND chunk_offset=?2",
            params![asset_id, SPOOL_CHUNK_BYTES as i64],
        )
        .expect("remove middle chunk metadata");
        assert!(verify_chunked_asset(
            &conn,
            &root,
            &key,
            asset_id,
            plaintext.len() as i64,
            &expected_sha,
        )
        .is_err());

        fs::remove_dir_all(root).expect("remove test spool");
    }

    #[test]
    fn restart_recovers_only_incomplete_uploads_and_never_reopens_confirmed_assets() {
        let conn = Connection::open_in_memory().expect("in-memory SQLite");
        initialize_schema(&conn).expect("schema");
        let now = "2026-08-12T00:00:00Z";
        for (id, state) in [
            ("asset-uploading", "uploading"),
            ("asset-confirmed", "confirmed"),
        ] {
            conn.execute(
                "INSERT INTO local_asset (id, sha256, size_bytes, mime, local_path, file_nonce, state, created_at, updated_at)
                 VALUES (?1, 'hash', 1, 'application/pdf', ?2, 'nonce', ?3, ?4, ?4)",
                params![id, format!("C:/spool/{id}.asset"), state, now],
            )
            .expect("asset");
        }
        for (id, state) in [
            ("asset-uploading", "uploading"),
            ("asset-confirmed", "succeeded"),
        ] {
            conn.execute(
                "INSERT INTO sync_event (id, operation, entity_id, idempotency_key, state, retry_count, last_error, payload_ciphertext, payload_nonce, created_at, updated_at)
                 VALUES (?2, 'scan_upload', ?1, ?3, ?4, 0, NULL, 'ciphertext', 'nonce', ?5, ?5)",
                params![id, format!("event-{id}"), format!("key-{id}"), state, now],
            )
            .expect("event");
        }

        recover_interrupted_uploads(&conn).expect("recover interrupted upload");

        let queue_state = |id: &str| -> String {
            conn.query_row(
                "SELECT state FROM sync_event WHERE entity_id = ?1",
                params![id],
                |row| row.get(0),
            )
            .expect("queue state")
        };
        let asset_state = |id: &str| -> String {
            conn.query_row(
                "SELECT state FROM local_asset WHERE id = ?1",
                params![id],
                |row| row.get(0),
            )
            .expect("asset state")
        };
        assert_eq!(queue_state("asset-uploading"), "pending");
        assert_eq!(asset_state("asset-uploading"), "queued");
        assert_eq!(queue_state("asset-confirmed"), "succeeded");
        assert_eq!(asset_state("asset-confirmed"), "confirmed");
    }

    #[test]
    fn recovery_keeps_a_500_page_spool_and_the_interrupted_page_checkpoint() {
        // This is a station-store fixture, rather than a scanner test.  It
        // models a process kill after page 173 has been accepted through a
        // remote upload offset but before the desktop can continue the batch.
        // The encrypted queue is the durable source of truth on restart.
        let conn = Connection::open_in_memory().expect("in-memory SQLite");
        initialize_schema(&conn).expect("schema");
        let key = [29_u8; 32];
        let now = "2026-08-13T00:00:00Z";

        for page_no in 1_i64..=500 {
            let id = format!("page-{page_no}");
            let status = match page_no {
                1..=172 => "succeeded",
                173 => "uploading",
                _ => "pending",
            };
            let asset_state = match status {
                "succeeded" => "confirmed",
                "uploading" => "uploading",
                _ => "queued",
            };
            let checkpoint = DurableQueueItem {
                id: id.clone(),
                title: format!("answer-page-{page_no}.pdf"),
                kind: "scan_upload".to_string(),
                status: status.to_string(),
                progress: if page_no <= 172 { 100 } else { 0 },
                detail: "fixture".to_string(),
                updated_at: now.to_string(),
                exam_id: Some("exam-500".to_string()),
                exam_name: None,
                capture_batch_id: Some("batch-500".to_string()),
                submission_id: None,
                page_no: Some(page_no),
                file_name: Some(format!("answer-page-{page_no}.pdf")),
                file_size: Some(4096),
                content_type: Some("application/pdf".to_string()),
                file_asset_id: None,
                server_status: None,
                requires_reselect: Some(false),
                quality_checks: None,
                local_asset_id: Some(id.clone()),
                idempotency_key: Some(format!("stable-page-{page_no}")),
                retry_count: Some(0),
                // Page 173 was confirmed remotely to this exact boundary
                // before the simulated kill.  It must survive recovery.
                confirmed_offset: (page_no == 173).then_some(1_730_000),
                remote_upload_id: (page_no == 173).then_some("remote-page-173".to_string()),
            };
            let (payload, nonce) =
                encrypt_json(&key, &checkpoint).expect("encrypt queue checkpoint");
            conn.execute(
                "INSERT INTO local_asset (id, sha256, size_bytes, mime, local_path, file_nonce, state, created_at, updated_at)
                 VALUES (?1, ?2, 4096, 'application/pdf', ?3, 'nonce', ?4, ?5, ?5)",
                params![
                    id,
                    format!("hash-{page_no}"),
                    format!("C:/spool/page-{page_no}.asset"),
                    asset_state,
                    now,
                ],
            )
            .expect("local asset");
            conn.execute(
                "INSERT INTO sync_event (id, operation, entity_id, idempotency_key, state, retry_count, last_error, payload_ciphertext, payload_nonce, created_at, updated_at)
                 VALUES (?1, 'scan_upload', ?1, ?2, ?3, 0, NULL, ?4, ?5, ?6, ?6)",
                params![
                    checkpoint.id,
                    checkpoint.idempotency_key.as_deref().expect("idempotency key"),
                    status,
                    payload,
                    nonce,
                    now,
                ],
            )
            .expect("sync event");
        }
        conn.execute(
            "INSERT INTO upload_session (local_asset_id, remote_id, chunk_size, confirmed_offset, retry_count, last_error, updated_at)
             VALUES ('page-173', 'remote-page-173', 65536, 1730000, 0, NULL, ?1)",
            params![now],
        )
        .expect("upload session");

        recover_interrupted_uploads(&conn).expect("recover interrupted upload");

        let queue_count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM sync_event WHERE operation = 'scan_upload' AND state <> 'archived'",
                [],
                |row| row.get(0),
            )
            .expect("queue count");
        let pending_count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM sync_event WHERE operation = 'scan_upload' AND state = 'pending'",
                [],
                |row| row.get(0),
            )
            .expect("pending count");
        assert_eq!(queue_count, 500, "a restart must not drop queued pages");
        assert_eq!(
            pending_count, 328,
            "the interrupted page returns to pending with untouched later pages"
        );

        let (payload, nonce): (String, String) = conn
            .query_row(
                "SELECT payload_ciphertext, payload_nonce FROM sync_event WHERE entity_id = 'page-173'",
                [],
                |row| Ok((row.get(0)?, row.get(1)?)),
            )
            .expect("checkpoint payload");
        let restored: DurableQueueItem =
            decrypt_json(&key, &payload, &nonce).expect("restore checkpoint");
        assert_eq!(
            restored.remote_upload_id.as_deref(),
            Some("remote-page-173")
        );
        assert_eq!(restored.confirmed_offset, Some(1_730_000));
        let resumed_state: String = conn
            .query_row(
                "SELECT state FROM sync_event WHERE entity_id = 'page-173'",
                [],
                |row| row.get(0),
            )
            .expect("recovered queue state");
        let (remote_id, confirmed_offset): (String, i64) = conn
            .query_row(
                "SELECT remote_id, confirmed_offset FROM upload_session WHERE local_asset_id = 'page-173'",
                [],
                |row| Ok((row.get(0)?, row.get(1)?)),
            )
            .expect("upload session checkpoint");
        assert_eq!(resumed_state, "pending");
        assert_eq!(remote_id, "remote-page-173");
        assert_eq!(confirmed_offset, 1_730_000);
    }

    #[test]
    fn draft_metadata_rejects_unknown_sync_states() {
        let value = serde_json::json!({
            "taskId": "task-1",
            "anonymousCode": "A001",
            "savedAt": "2026-08-11T00:00:00Z",
            "expiresAt": "2026-08-18T00:00:00Z",
            "syncStatus": "not-a-state"
        });
        assert!(draft_metadata(&value).is_err());
    }

    #[test]
    fn an_existing_station_store_is_not_treated_as_first_launch() {
        let root = std::env::temp_dir().join(format!("edugrade-durable-store-{}", Uuid::new_v4()));
        fs::create_dir_all(spool_path(&root)).expect("create controlled spool");
        assert!(!durable_data_exists(&root).expect("empty station store"));

        fs::write(
            spool_path(&root).join("retained.asset"),
            b"encrypted source",
        )
        .expect("write retained source");
        assert!(durable_data_exists(&root).expect("retained spool counts as existing data"));

        fs::remove_file(spool_path(&root).join("retained.asset")).expect("remove fixture");
        fs::write(db_path(&root), b"SQLite format 3\0").expect("write station database marker");
        assert!(durable_data_exists(&root).expect("database counts as existing data"));
        fs::remove_dir_all(root).expect("remove fixture root");
    }

    #[test]
    fn account_b_cannot_read_account_a_asset_and_v1_remains_quarantined() {
        let data_dir =
            std::env::temp_dir().join(format!("edugrade-account-test-{}", Uuid::new_v4()));
        let legacy = data_dir.join("durable-store-v1");
        fs::create_dir_all(&legacy).expect("legacy directory");
        let legacy_marker = legacy.join("offline.sqlite3");
        fs::write(&legacy_marker, b"legacy encrypted data").expect("legacy marker");
        assert!(legacy_store_exists(&data_dir).expect("detect legacy data"));

        let tenant = Uuid::new_v4().to_string();
        let actor_a = Uuid::new_v4().to_string();
        let actor_b = Uuid::new_v4().to_string();
        let scope_a =
            scope_path_component("https://example.test/api", &tenant, &actor_a).expect("A scope");
        let scope_b =
            scope_path_component("https://example.test/api", &tenant, &actor_b).expect("B scope");
        assert_ne!(scope_a, scope_b);
        assert_ne!(
            scope_a,
            scope_path_component("https://example.test/other", &tenant, &actor_a)
                .expect("other API path")
        );
        let root_a = account_root(&data_dir, &scope_a);
        let root_b = account_root(&data_dir, &scope_b);
        assert_ne!(
            durable_credential_account(&root_a).expect("A credential account"),
            durable_credential_account(&root_b).expect("B credential account")
        );
        assert_eq!(
            durable_credential_account(&legacy).expect("legacy credential account"),
            "desktop-offline-master-key-v1"
        );
        fs::create_dir_all(spool_path(&root_a)).expect("A spool");
        fs::create_dir_all(spool_path(&root_b)).expect("B spool");
        let conn_a = Connection::open(db_path(&root_a)).expect("A database");
        let conn_b = Connection::open(db_path(&root_b)).expect("B database");
        initialize_schema(&conn_a).expect("A schema");
        initialize_schema(&conn_b).expect("B schema");
        let key = [43_u8; 32];
        let asset_id = "account-a-asset";
        let record: DurableQueueItem = serde_json::from_value(serde_json::json!({
            "id": asset_id, "title": "a.pdf", "kind": "scan_upload", "status": "pending",
            "progress": 0, "detail": "", "updatedAt": "2026-09-22T00:00:00Z",
            "fileName": "a.pdf", "localAssetId": asset_id
        }))
        .expect("queue record");
        let (payload, nonce) = encrypt_json(&key, &record).expect("encrypted queue");
        conn_a.execute(
            "INSERT INTO local_asset (id, sha256, size_bytes, mime, local_path, file_nonce, state, created_at, updated_at)
             VALUES (?1, 'sha', 3, 'application/pdf', '', 'nonce', 'queued', ?2, ?2)",
            params![asset_id, "2026-09-22T00:00:00Z"],
        ).expect("A asset");
        conn_a.execute(
            "INSERT INTO sync_event (id, operation, entity_id, idempotency_key, state, retry_count, last_error, payload_ciphertext, payload_nonce, created_at, updated_at)
             VALUES (?1, 'scan_upload', ?1, 'key-a', 'pending', 0, NULL, ?2, ?3, ?4, ?4)",
            params![asset_id, payload, nonce, "2026-09-22T00:00:00Z"],
        ).expect("A queue");
        assert_eq!(
            read_durable_local_asset_at(&root_a, &key, asset_id)
                .expect("A reads own asset")
                .filename,
            "a.pdf"
        );
        assert!(
            read_durable_local_asset_at(&root_b, &key, asset_id).is_err(),
            "B cannot read A's asset ID"
        );

        let lease_a = bind_scope("https://example.test/api", &tenant, &actor_a).expect("bind A");
        assert!(with_session(&lease_a, || Ok(())).is_ok());
        let lease_b = bind_scope("https://example.test/api", &tenant, &actor_b).expect("bind B");
        assert!(
            with_session(&lease_a, || Ok(())).is_err(),
            "A's pending command must be rejected after B logs in"
        );
        assert!(with_session(&lease_b, || Ok(())).is_ok());
        clear_scope().expect("clear session");
        assert!(
            with_session(&lease_b, || Ok(())).is_err(),
            "logout revokes B lease"
        );
        let lease_other_server =
            bind_scope("https://other.example.test/api", &tenant, &actor_a).expect("server switch");
        assert!(with_session(&lease_other_server, || Ok(())).is_ok());
        assert_ne!(
            scope_a,
            scope_path_component("https://other.example.test/api", &tenant, &actor_a)
                .expect("other server scope")
        );
        clear_scope().expect("restart clears in-memory lease");
        assert!(with_session(&lease_other_server, || Ok(())).is_err());
        let lease_a_after_restart =
            bind_scope("https://example.test/api", &tenant, &actor_a).expect("return A");
        assert_ne!(lease_a, lease_a_after_restart);
        assert!(with_session(&lease_a, || Ok(())).is_err());
        assert!(with_session(&lease_a_after_restart, || Ok(())).is_ok());
        assert_eq!(
            read_durable_local_asset_at(&root_a, &key, asset_id)
                .expect("A recovers own asset")
                .filename,
            "a.pdf"
        );
        clear_scope().expect("clear A");
        assert!(
            legacy_marker.exists(),
            "the unscoped v1 data must remain untouched"
        );
        assert!(!root_a.starts_with(&legacy) && !root_b.starts_with(&legacy));
        drop(conn_a);
        drop(conn_b);
        fs::remove_dir_all(data_dir).expect("remove account fixture");
    }
}

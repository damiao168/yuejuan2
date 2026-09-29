use rusqlite::{params, Connection, OptionalExtension};
use sha2::{Digest, Sha256};
use std::{
    fs,
    path::{Path, PathBuf},
};
use tauri::AppHandle;
use uuid::Uuid;

use super::{
    connection::{now_rfc3339, open_connection_at, sql_error},
    crypto::{decrypt_bytes, decrypt_json, encrypt_bytes, encrypt_json, master_key, sha256_hex},
    paths::{
        ensure_controlled_path, reject_symbolic_path, spool_path, store_root,
        write_new_private_file,
    },
    queue::read_queue_item,
    schema::initialize_schema,
    types::{DurableQueueItem, DurableSpoolFile, SpoolAssetInput, SpoolAssetSession},
};

const MAX_SPOOL_ASSET_BYTES: usize = 110 * 1024 * 1024;
pub(crate) const SPOOL_CHUNK_BYTES: usize = 1024 * 1024;
const MAX_READ_CHUNK_BYTES: usize = 4 * 1024 * 1024;
pub(crate) const CHUNKED_FILE_NONCE: &str = "chunked-aes-gcm-v1";

pub fn begin_spool_local_asset(
    app: AppHandle,
    input: SpoolAssetInput,
) -> Result<SpoolAssetSession, String> {
    validate_spool_input(&input)?;
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;

    // 同一文件只有在业务归属和页码也相同时才复用，不能只按摘要合并不同答卷。
    let idempotency_key = format!(
        "scan-upload-v1:{}:{}:{}:{}:{}",
        input.sha256,
        input.exam_id.as_deref().unwrap_or(""),
        input.capture_batch_id.as_deref().unwrap_or(""),
        input.submission_id.as_deref().unwrap_or(""),
        input.page_no.unwrap_or_default()
    );
    if let Some(existing_id) = conn
        .query_row(
            "SELECT entity_id FROM sync_event WHERE operation = 'scan_upload' AND idempotency_key = ?1",
            params![idempotency_key],
            |row| row.get::<_, String>(0),
        )
        .optional()
        .map_err(sql_error)?
    {
        return Ok(SpoolAssetSession {
            local_asset_id: existing_id.clone(),
            chunk_size: SPOOL_CHUNK_BYTES,
            confirmed_offset: input.size,
            item: Some(read_queue_item(&conn, &key, &existing_id)?),
        });
    }

    if let Some((existing_id, received_size)) = conn
        .query_row(
            "SELECT local_asset_id, received_size FROM local_asset_ingest WHERE idempotency_key = ?1",
            params![idempotency_key],
            |row| Ok((row.get::<_, String>(0)?, row.get::<_, i64>(1)?)),
        )
        .optional()
        .map_err(sql_error)?
    {
        return Ok(SpoolAssetSession {
            local_asset_id: existing_id,
            chunk_size: SPOOL_CHUNK_BYTES,
            confirmed_offset: received_size,
            item: None,
        });
    }

    let id = Uuid::new_v4().to_string();
    let file_path = spool_path(&root).join(&id);
    fs::create_dir(&file_path).map_err(|error| error.to_string())?;
    reject_symbolic_path(&file_path)?;
    let file_path_text = file_path.to_string_lossy().into_owned();
    let now = now_rfc3339();
    let item = DurableQueueItem {
        id: id.clone(),
        title: input.filename.clone(),
        kind: "scan_upload".to_string(),
        status: "pending".to_string(),
        progress: 0,
        detail: "已安全写入本地扫描队列，等待上传".to_string(),
        updated_at: now.clone(),
        exam_id: input.exam_id,
        exam_name: None,
        capture_batch_id: input.capture_batch_id,
        submission_id: input.submission_id,
        page_no: input.page_no,
        file_name: Some(input.filename),
        file_size: Some(input.size),
        content_type: Some(input.mime.clone()),
        file_asset_id: None,
        server_status: None,
        requires_reselect: Some(false),
        quality_checks: input.quality_checks,
        local_asset_id: Some(id.clone()),
        idempotency_key: Some(idempotency_key.clone()),
        retry_count: Some(0),
        confirmed_offset: Some(0),
        remote_upload_id: None,
    };
    let (payload_ciphertext, payload_nonce) = encrypt_json(&key, &item)?;
    let transaction = conn.unchecked_transaction().map_err(sql_error)?;
    transaction
        .execute(
            "INSERT INTO local_asset (id, sha256, size_bytes, mime, local_path, file_nonce, state, created_at, updated_at)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, 'ingesting', ?7, ?7)",
            params![
                id,
                input.sha256,
                input.size,
                input.mime,
                file_path_text,
                CHUNKED_FILE_NONCE,
                now,
            ],
        )
        .map_err(sql_error)?;
    transaction
        .execute(
            "INSERT INTO local_asset_ingest (local_asset_id, idempotency_key, expected_size, received_size, payload_ciphertext, payload_nonce, created_at, updated_at)
             VALUES (?1, ?2, ?3, 0, ?4, ?5, ?6, ?6)",
            params![id, idempotency_key, input.size, payload_ciphertext, payload_nonce, now],
        )
        .map_err(sql_error)?;
    transaction.commit().map_err(sql_error)?;
    Ok(SpoolAssetSession {
        local_asset_id: id,
        chunk_size: SPOOL_CHUNK_BYTES,
        confirmed_offset: 0,
        item: None,
    })
}

pub fn write_spool_local_asset_chunk(
    app: AppHandle,
    local_asset_id: String,
    offset: i64,
    bytes: Vec<u8>,
) -> Result<i64, String> {
    if offset < 0 || bytes.is_empty() || bytes.len() > SPOOL_CHUNK_BYTES {
        return Err("local spool chunk boundary is invalid".into());
    }
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    let (expected_size, received_size, local_path): (i64, i64, String) = conn
        .query_row(
            "SELECT i.expected_size,i.received_size,a.local_path
             FROM local_asset_ingest i JOIN local_asset a ON a.id=i.local_asset_id
             WHERE i.local_asset_id=?1 AND a.state='ingesting'",
            params![local_asset_id],
            |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)),
        )
        .map_err(sql_error)?;
    let end = offset
        .checked_add(bytes.len() as i64)
        .ok_or("local spool chunk offset overflow")?;
    if offset != received_size || end > expected_size {
        return Err(format!(
            "local spool chunk offset mismatch: expected {received_size}, received {offset}"
        ));
    }
    let directory = PathBuf::from(local_path);
    ensure_controlled_path(&root, &directory)?;
    let path = directory.join(format!("{offset:012}.chunk"));
    // 该偏移尚未被数据库确认；同名文件可能是上次落盘后、提交前崩溃留下的孤立分块。
    if path.exists() {
        fs::remove_file(&path).map_err(|error| error.to_string())?;
    }
    let plaintext_sha256 = sha256_hex(&bytes);
    let (encrypted, nonce) = encrypt_bytes(&key, &bytes)?;
    write_new_private_file(&path, &encrypted)?;
    let transaction = conn.unchecked_transaction().map_err(sql_error)?;
    let result = (|| -> Result<(), String> {
        transaction
            .execute(
                "INSERT INTO local_asset_chunk (local_asset_id,chunk_offset,size_bytes,sha256,local_path,nonce)
                 VALUES (?1,?2,?3,?4,?5,?6)",
                params![local_asset_id, offset, bytes.len() as i64, plaintext_sha256, path.to_string_lossy(), nonce],
            )
            .map_err(sql_error)?;
        let changed = transaction
            .execute(
                "UPDATE local_asset_ingest SET received_size=?2,updated_at=?3
                 WHERE local_asset_id=?1 AND received_size=?4",
                params![local_asset_id, end, now_rfc3339(), received_size],
            )
            .map_err(sql_error)?;
        if changed != 1 {
            return Err("local spool ingest changed concurrently".into());
        }
        transaction.commit().map_err(sql_error)
    })();
    if result.is_err() {
        let _ = fs::remove_file(path);
    }
    result.map(|()| end)
}

pub fn complete_spool_local_asset(
    app: AppHandle,
    local_asset_id: String,
) -> Result<DurableQueueItem, String> {
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    let (expected_size, received_size, payload, payload_nonce, idempotency_key, expected_sha): (i64, i64, String, String, String, String) = conn
        .query_row(
            "SELECT i.expected_size,i.received_size,i.payload_ciphertext,i.payload_nonce,i.idempotency_key,a.sha256
             FROM local_asset_ingest i JOIN local_asset a ON a.id=i.local_asset_id
             WHERE i.local_asset_id=?1 AND a.state='ingesting'",
            params![local_asset_id],
            |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?, row.get(3)?, row.get(4)?, row.get(5)?)),
        )
        .map_err(sql_error)?;
    if received_size != expected_size {
        return Err(format!(
            "local spool is incomplete: {received_size} / {expected_size}"
        ));
    }
    // 入队前重新校验分块连续性、总长度和整体摘要，避免把缺块原件交给上传流程。
    verify_chunked_asset(
        &conn,
        &root,
        &key,
        &local_asset_id,
        expected_size,
        &expected_sha,
    )?;
    let mut item: DurableQueueItem = decrypt_json(&key, &payload, &payload_nonce)?;
    let now = now_rfc3339();
    item.updated_at = now.clone();
    let (queue_payload, queue_nonce) = encrypt_json(&key, &item)?;
    let transaction = conn.unchecked_transaction().map_err(sql_error)?;
    transaction
        .execute(
            "INSERT INTO sync_event (id, operation, entity_id, idempotency_key, state, retry_count, last_error, payload_ciphertext, payload_nonce, created_at, updated_at)
             VALUES (?1, 'scan_upload', ?1, ?2, 'pending', 0, NULL, ?3, ?4, ?5, ?5)",
            params![local_asset_id, idempotency_key, queue_payload, queue_nonce, now],
        )
        .map_err(sql_error)?;
    transaction
        .execute(
            "UPDATE local_asset SET state='queued',updated_at=?2 WHERE id=?1 AND state='ingesting'",
            params![local_asset_id, now],
        )
        .map_err(sql_error)?;
    transaction
        .execute(
            "DELETE FROM local_asset_ingest WHERE local_asset_id=?1",
            params![local_asset_id],
        )
        .map_err(sql_error)?;
    transaction.commit().map_err(sql_error)?;
    Ok(item)
}

pub fn read_durable_local_asset(
    app: AppHandle,
    local_asset_id: String,
) -> Result<DurableSpoolFile, String> {
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    read_durable_local_asset_at(&root, &key, &local_asset_id)
}

pub(crate) fn read_durable_local_asset_at(
    root: &std::path::Path,
    key: &[u8; 32],
    local_asset_id: &str,
) -> Result<DurableSpoolFile, String> {
    let conn = open_connection_at(root)?;
    initialize_schema(&conn)?;
    let (sha256, mime, size_bytes) = conn
        .query_row(
            "SELECT sha256, mime, size_bytes FROM local_asset WHERE id = ?1 AND state <> 'ingesting'",
            params![local_asset_id],
            |row| {
                Ok((
                    row.get::<_, String>(0)?,
                    row.get::<_, String>(1)?,
                    row.get::<_, i64>(2)?,
                ))
            },
        )
        .map_err(sql_error)?;
    let item = read_queue_item(&conn, key, local_asset_id)?;
    Ok(DurableSpoolFile {
        filename: item.file_name.unwrap_or(item.title),
        mime,
        size: size_bytes,
        sha256,
        chunk_size: SPOOL_CHUNK_BYTES,
    })
}

pub fn read_durable_local_asset_chunk(
    app: AppHandle,
    local_asset_id: String,
    offset: i64,
    length: usize,
) -> Result<Vec<u8>, String> {
    if offset < 0 || length == 0 || length > MAX_READ_CHUNK_BYTES {
        return Err("durable read chunk boundary is invalid".into());
    }
    let root = store_root(&app)?;
    let key = master_key(&root)?;
    let conn = open_connection_at(&root)?;
    initialize_schema(&conn)?;
    let (size_bytes, local_path, file_nonce): (i64, String, String) = conn
        .query_row(
            "SELECT size_bytes,local_path,file_nonce FROM local_asset WHERE id=?1 AND state <> 'ingesting'",
            params![local_asset_id],
            |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)),
        )
        .map_err(sql_error)?;
    if offset >= size_bytes {
        return Ok(Vec::new());
    }
    let requested_end = offset
        .checked_add(length as i64)
        .ok_or("durable read chunk offset overflow")?;
    let end = requested_end.min(size_bytes);
    let path = PathBuf::from(local_path);
    ensure_controlled_path(&root, &path)?;
    if file_nonce != CHUNKED_FILE_NONCE {
        // Legacy whole-file records remain readable during the rolling
        // desktop upgrade. Newly spooled files always use bounded chunks.
        let encrypted = fs::read(&path).map_err(|error| error.to_string())?;
        let bytes = decrypt_bytes(&key, &file_nonce, &encrypted)?;
        return Ok(bytes[offset as usize..end as usize].to_vec());
    }

    let mut statement = conn
        .prepare(
            "SELECT chunk_offset,size_bytes,sha256,local_path,nonce
             FROM local_asset_chunk
             WHERE local_asset_id=?1 AND chunk_offset < ?3 AND chunk_offset + size_bytes > ?2
             ORDER BY chunk_offset",
        )
        .map_err(sql_error)?;
    let chunks = statement
        .query_map(params![local_asset_id, offset, end], |row| {
            Ok((
                row.get::<_, i64>(0)?,
                row.get::<_, i64>(1)?,
                row.get::<_, String>(2)?,
                row.get::<_, String>(3)?,
                row.get::<_, String>(4)?,
            ))
        })
        .map_err(sql_error)?
        .collect::<Result<Vec<_>, _>>()
        .map_err(sql_error)?;
    let mut result = Vec::with_capacity((end - offset) as usize);
    for (chunk_offset, chunk_size, expected_hash, chunk_path, nonce) in chunks {
        let chunk_path = PathBuf::from(chunk_path);
        ensure_controlled_path(&root, &chunk_path)?;
        let encrypted = fs::read(chunk_path).map_err(|error| error.to_string())?;
        let plaintext = decrypt_bytes(&key, &nonce, &encrypted)?;
        if plaintext.len() != chunk_size as usize || sha256_hex(&plaintext) != expected_hash {
            return Err(
                "local spool chunk integrity check failed; source file is retained for recovery"
                    .into(),
            );
        }
        let copy_start = offset.max(chunk_offset) - chunk_offset;
        let copy_end = end.min(chunk_offset + chunk_size) - chunk_offset;
        result.extend_from_slice(&plaintext[copy_start as usize..copy_end as usize]);
    }
    if result.len() != (end - offset) as usize {
        return Err("local spool chunk sequence is incomplete".into());
    }
    Ok(result)
}

fn validate_spool_input(input: &SpoolAssetInput) -> Result<(), String> {
    if input.filename.trim().is_empty() || input.filename.len() > 512 {
        return Err("spool filename is invalid".into());
    }
    if input.mime.trim().is_empty() || input.mime.len() > 256 {
        return Err("spool MIME type is invalid".into());
    }
    if input.size <= 0 || input.size > MAX_SPOOL_ASSET_BYTES as i64 {
        return Err("spool asset size is invalid".into());
    }
    if input.sha256.len() != 64 || !input.sha256.bytes().all(|value| value.is_ascii_hexdigit()) {
        return Err("spool asset SHA-256 is invalid".into());
    }
    if input.page_no.is_some_and(|page| page <= 0) {
        return Err("spool page number must be positive".into());
    }
    Ok(())
}

pub(crate) fn verify_chunked_asset(
    conn: &Connection,
    root: &Path,
    key: &[u8; 32],
    local_asset_id: &str,
    expected_size: i64,
    expected_sha: &str,
) -> Result<(), String> {
    let mut statement = conn
        .prepare(
            "SELECT chunk_offset,size_bytes,sha256,local_path,nonce
             FROM local_asset_chunk WHERE local_asset_id=?1 ORDER BY chunk_offset",
        )
        .map_err(sql_error)?;
    let chunks = statement
        .query_map(params![local_asset_id], |row| {
            Ok((
                row.get::<_, i64>(0)?,
                row.get::<_, i64>(1)?,
                row.get::<_, String>(2)?,
                row.get::<_, String>(3)?,
                row.get::<_, String>(4)?,
            ))
        })
        .map_err(sql_error)?
        .collect::<Result<Vec<_>, _>>()
        .map_err(sql_error)?;
    let mut next_offset = 0_i64;
    let mut digest = Sha256::new();
    for (offset, size, expected_chunk_sha, path, nonce) in chunks {
        if offset != next_offset {
            return Err("local spool chunk sequence is incomplete".into());
        }
        let path = PathBuf::from(path);
        ensure_controlled_path(root, &path)?;
        let encrypted = fs::read(path).map_err(|error| error.to_string())?;
        let plaintext = decrypt_bytes(key, &nonce, &encrypted)?;
        if plaintext.len() != size as usize || sha256_hex(&plaintext) != expected_chunk_sha {
            return Err("local spool chunk integrity check failed".into());
        }
        digest.update(&plaintext);
        next_offset += size;
    }
    let actual_sha: String = digest
        .finalize()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    if next_offset != expected_size || actual_sha != expected_sha {
        return Err(
            "local spool integrity check failed; source file is retained for recovery".into(),
        );
    }
    Ok(())
}

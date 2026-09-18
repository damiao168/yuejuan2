use super::connection::sql_error;
use rusqlite::Connection;

pub(crate) fn initialize_schema(conn: &Connection) -> Result<(), String> {
    conn.execute_batch(
        "PRAGMA journal_mode = WAL;
         PRAGMA foreign_keys = ON;
         CREATE TABLE IF NOT EXISTS local_asset (
           id TEXT PRIMARY KEY,
           sha256 TEXT NOT NULL,
           size_bytes INTEGER NOT NULL,
           mime TEXT NOT NULL,
           local_path TEXT NOT NULL UNIQUE,
           file_nonce TEXT NOT NULL,
           state TEXT NOT NULL,
           created_at TEXT NOT NULL,
           updated_at TEXT NOT NULL,
           confirmed_at TEXT,
           retention_until TEXT
         );
         CREATE TABLE IF NOT EXISTS scan_batch_local (
           id TEXT PRIMARY KEY,
           exam_id TEXT,
           submission_id TEXT,
           state TEXT NOT NULL,
           created_at TEXT NOT NULL,
           updated_at TEXT NOT NULL
         );
         CREATE TABLE IF NOT EXISTS upload_session (
           local_asset_id TEXT PRIMARY KEY REFERENCES local_asset(id) ON DELETE RESTRICT,
           remote_id TEXT,
           chunk_size INTEGER,
           confirmed_offset INTEGER NOT NULL DEFAULT 0,
           retry_count INTEGER NOT NULL DEFAULT 0,
           last_error TEXT,
           updated_at TEXT NOT NULL
         );
         CREATE TABLE IF NOT EXISTS upload_chunk (
           upload_session_asset_id TEXT NOT NULL REFERENCES upload_session(local_asset_id) ON DELETE RESTRICT,
           offset INTEGER NOT NULL,
           size_bytes INTEGER NOT NULL,
           sha256 TEXT NOT NULL,
           state TEXT NOT NULL,
           created_at TEXT NOT NULL,
           PRIMARY KEY(upload_session_asset_id, offset)
         );
         CREATE TABLE IF NOT EXISTS local_asset_ingest (
           local_asset_id TEXT PRIMARY KEY REFERENCES local_asset(id) ON DELETE RESTRICT,
           idempotency_key TEXT NOT NULL UNIQUE,
           expected_size INTEGER NOT NULL,
           received_size INTEGER NOT NULL DEFAULT 0,
           payload_ciphertext TEXT NOT NULL,
           payload_nonce TEXT NOT NULL,
           created_at TEXT NOT NULL,
           updated_at TEXT NOT NULL
         );
         CREATE TABLE IF NOT EXISTS local_asset_chunk (
           local_asset_id TEXT NOT NULL REFERENCES local_asset(id) ON DELETE RESTRICT,
           chunk_offset INTEGER NOT NULL,
           size_bytes INTEGER NOT NULL,
           sha256 TEXT NOT NULL,
           local_path TEXT NOT NULL UNIQUE,
           nonce TEXT NOT NULL,
           PRIMARY KEY(local_asset_id, chunk_offset)
         );
         CREATE TABLE IF NOT EXISTS sync_event (
           id TEXT PRIMARY KEY,
           operation TEXT NOT NULL,
           entity_id TEXT NOT NULL REFERENCES local_asset(id) ON DELETE RESTRICT,
           idempotency_key TEXT NOT NULL UNIQUE,
           state TEXT NOT NULL,
           retry_count INTEGER NOT NULL DEFAULT 0,
           last_error TEXT,
           payload_ciphertext TEXT NOT NULL,
           payload_nonce TEXT NOT NULL,
           created_at TEXT NOT NULL,
           updated_at TEXT NOT NULL
         );
         CREATE INDEX IF NOT EXISTS idx_sync_event_pending ON sync_event(operation, state, updated_at);
         CREATE TABLE IF NOT EXISTS draft (
           task_id TEXT PRIMARY KEY,
           anonymous_code TEXT NOT NULL,
           saved_at TEXT NOT NULL,
           expires_at TEXT NOT NULL,
           sync_status TEXT NOT NULL,
           sync_message TEXT,
           payload_ciphertext TEXT NOT NULL,
           payload_nonce TEXT NOT NULL,
           updated_at TEXT NOT NULL
         );",
    ).map_err(sql_error)
}

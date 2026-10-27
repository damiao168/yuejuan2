//! 账号作用域内的扫描 Profile 存储；调用方须在 with_session 守卫内执行。

use super::preflight::required;
use super::{SaveScannerProfileInput, ScannerProfile, MAX_PRESET_BYTES};
use chrono::Utc;
use rusqlite::{params, Connection};
use tauri::AppHandle;
use uuid::Uuid;

const MAX_PROFILE_NAME_BYTES: usize = 120;
const MAX_FINGERPRINT_BYTES: usize = 128;

pub(super) fn list_scanner_profiles_scoped(app: AppHandle) -> Result<Vec<ScannerProfile>, String> {
    let conn = open_profile_store(&app)?;
    let mut statement = conn
        .prepare(
            "SELECT id, name, device_fingerprint, dpi, duplex, color_mode, paper_size,
                    auto_rotate, compression, template_preset, created_at, updated_at
             FROM scanner_profile ORDER BY updated_at DESC, name ASC",
        )
        .map_err(sql_error)?;
    let profiles = statement
        .query_map([], |row| {
            Ok(ScannerProfile {
                id: row.get(0)?,
                name: row.get(1)?,
                device_fingerprint: row.get(2)?,
                dpi: row.get(3)?,
                duplex: row.get(4)?,
                color_mode: row.get(5)?,
                paper_size: row.get(6)?,
                auto_rotate: row.get(7)?,
                compression: row.get(8)?,
                template_preset: row.get(9)?,
                created_at: row.get(10)?,
                updated_at: row.get(11)?,
            })
        })
        .map_err(sql_error)?
        .collect::<Result<Vec<_>, _>>()
        .map_err(sql_error)?;
    Ok(profiles)
}

pub(super) fn save_scanner_profile_scoped(
    app: AppHandle,
    input: SaveScannerProfileInput,
) -> Result<ScannerProfile, String> {
    validate_profile_input(&input)?;
    let conn = open_profile_store(&app)?;
    let now = now_rfc3339();
    let id = input.id.unwrap_or_else(|| Uuid::new_v4().to_string());
    let existing_created_at = conn
        .query_row(
            "SELECT created_at FROM scanner_profile WHERE id = ?1",
            params![id],
            |row| row.get::<_, String>(0),
        )
        .ok();
    let created_at = existing_created_at.unwrap_or_else(|| now.clone());
    conn.execute(
        "INSERT INTO scanner_profile (
             id, name, device_fingerprint, dpi, duplex, color_mode, paper_size,
             auto_rotate, compression, template_preset, created_at, updated_at
         ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12)
         ON CONFLICT(id) DO UPDATE SET
             name = excluded.name,
             device_fingerprint = excluded.device_fingerprint,
             dpi = excluded.dpi,
             duplex = excluded.duplex,
             color_mode = excluded.color_mode,
             paper_size = excluded.paper_size,
             auto_rotate = excluded.auto_rotate,
             compression = excluded.compression,
             template_preset = excluded.template_preset,
             updated_at = excluded.updated_at",
        params![
            id,
            input.name.trim(),
            input.device_fingerprint.trim(),
            input.dpi,
            input.duplex,
            input.color_mode.trim(),
            input.paper_size.trim(),
            input.auto_rotate,
            input.compression.trim(),
            input.template_preset.trim(),
            created_at,
            now,
        ],
    )
    .map_err(sql_error)?;
    read_profile(&conn, &id)
}

pub(super) fn delete_scanner_profile_scoped(
    app: AppHandle,
    profile_id: String,
) -> Result<(), String> {
    if profile_id.trim().is_empty() || profile_id.len() > 128 {
        return Err("scanner profile ID is invalid".into());
    }
    let conn = open_profile_store(&app)?;
    if conn
        .execute(
            "DELETE FROM scanner_profile WHERE id = ?1",
            params![profile_id],
        )
        .map_err(sql_error)?
        != 1
    {
        return Err("scanner profile was not found".into());
    }
    Ok(())
}

pub(super) fn validate_profile_input(input: &SaveScannerProfileInput) -> Result<(), String> {
    required("profile name", &input.name, MAX_PROFILE_NAME_BYTES)?;
    required(
        "device fingerprint",
        &input.device_fingerprint,
        MAX_FINGERPRINT_BYTES,
    )?;
    required("color mode", &input.color_mode, 32)?;
    required("paper size", &input.paper_size, 32)?;
    required("compression", &input.compression, 32)?;
    required("template preset", &input.template_preset, MAX_PRESET_BYTES)?;
    if !(150..=1200).contains(&input.dpi) {
        return Err("scanner DPI must be between 150 and 1200; the final setting must be calibrated with real answer sheets".into());
    }
    if !matches!(
        input.color_mode.as_str(),
        "color" | "grayscale" | "black_white"
    ) {
        return Err("scanner color mode is invalid".into());
    }
    if !matches!(
        input.paper_size.as_str(),
        "A3" | "A4" | "A5" | "Letter" | "Legal"
    ) {
        return Err("scanner paper size is invalid".into());
    }
    if !matches!(input.compression.as_str(), "jpeg" | "png" | "tiff" | "pdf") {
        return Err("scanner compression is invalid".into());
    }
    Ok(())
}

pub(super) fn open_profile_store(app: &AppHandle) -> Result<Connection, String> {
    let root = crate::durable_store::scoped_root(app)?;
    let conn = Connection::open(root.join("scanner-profiles.sqlite3")).map_err(sql_error)?;
    conn.execute_batch(
        "PRAGMA journal_mode = WAL;
         CREATE TABLE IF NOT EXISTS scanner_profile (
           id TEXT PRIMARY KEY,
           name TEXT NOT NULL,
           device_fingerprint TEXT NOT NULL,
           dpi INTEGER NOT NULL,
           duplex INTEGER NOT NULL,
           color_mode TEXT NOT NULL,
           paper_size TEXT NOT NULL,
           auto_rotate INTEGER NOT NULL,
           compression TEXT NOT NULL,
           template_preset TEXT NOT NULL,
           created_at TEXT NOT NULL,
           updated_at TEXT NOT NULL
         );
         CREATE INDEX IF NOT EXISTS idx_scanner_profile_device ON scanner_profile(device_fingerprint);",
    )
    .map_err(sql_error)?;
    Ok(conn)
}

pub(super) fn read_profile(conn: &Connection, id: &str) -> Result<ScannerProfile, String> {
    conn.query_row(
        "SELECT id, name, device_fingerprint, dpi, duplex, color_mode, paper_size,
                auto_rotate, compression, template_preset, created_at, updated_at
         FROM scanner_profile WHERE id = ?1",
        params![id],
        |row| {
            Ok(ScannerProfile {
                id: row.get(0)?,
                name: row.get(1)?,
                device_fingerprint: row.get(2)?,
                dpi: row.get(3)?,
                duplex: row.get(4)?,
                color_mode: row.get(5)?,
                paper_size: row.get(6)?,
                auto_rotate: row.get(7)?,
                compression: row.get(8)?,
                template_preset: row.get(9)?,
                created_at: row.get(10)?,
                updated_at: row.get(11)?,
            })
        },
    )
    .map_err(|error| match error {
        rusqlite::Error::QueryReturnedNoRows => "scanner profile was not found".to_string(),
        other => sql_error(other),
    })
}

fn now_rfc3339() -> String {
    Utc::now().to_rfc3339()
}

fn sql_error(error: rusqlite::Error) -> String {
    error.to_string()
}

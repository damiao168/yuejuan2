//! Scanner integration boundary for the Windows scan station.
//!
//! The product layer only knows about [`ScannerProfile`] and preflight results.
//! It never imports a vendor SDK.  The first Windows bridge uses the operating
//! system's WIA inventory solely to discover supported drivers; acquisition is
//! deliberately kept behind [`ScannerBridge`] until it is validated against a
//! real device.  This prevents a detected printer/driver from being advertised
//! as a working production scanner.

use chrono::Utc;
use rusqlite::{params, Connection};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
#[cfg(windows)]
use std::process::Command;
use tauri::AppHandle;
use uuid::Uuid;

const DEFAULT_REQUIRED_FREE_BYTES: u64 = 2 * 1024 * 1024 * 1024;
const MAX_PROFILE_NAME_BYTES: usize = 120;
const MAX_FINGERPRINT_BYTES: usize = 128;
const MAX_PRESET_BYTES: usize = 160;

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct ScannerProfile {
    pub id: String,
    pub name: String,
    pub device_fingerprint: String,
    pub dpi: u32,
    pub duplex: bool,
    pub color_mode: String,
    pub paper_size: String,
    pub auto_rotate: bool,
    pub compression: String,
    pub template_preset: String,
    pub created_at: String,
    pub updated_at: String,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SaveScannerProfileInput {
    pub id: Option<String>,
    pub name: String,
    pub device_fingerprint: String,
    pub dpi: u32,
    pub duplex: bool,
    pub color_mode: String,
    pub paper_size: String,
    pub auto_rotate: bool,
    pub compression: String,
    pub template_preset: String,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct ScannerDevice {
    /// Stable, non-reversible identity derived from the driver supplied ID.
    pub fingerprint: String,
    pub display_name: String,
    pub driver_status: String,
    /// "wia_inventory" means the Windows driver is visible.  It is not a
    /// promise that an acquisition workflow has passed hardware validation.
    pub bridge: String,
}

/// A scanner adapter is intentionally small so WIA, TWAIN or a school-supplied
/// bridge can be substituted without spreading vendor types through the app.
pub trait ScannerBridge {
    fn inventory(&self) -> Result<Vec<ScannerDevice>, String>;
    fn bridge_name(&self) -> &'static str;
}

pub struct SystemWiaInventoryBridge;

impl ScannerBridge for SystemWiaInventoryBridge {
    fn inventory(&self) -> Result<Vec<ScannerDevice>, String> {
        inventory_devices()
    }

    fn bridge_name(&self) -> &'static str {
        "wia_inventory"
    }
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ScannerPreflightRequest {
    pub profile_id: String,
    /// The answer-sheet template selected for this batch.  It is deliberately
    /// passed by the caller rather than inferred from a filename.
    pub expected_paper_size: String,
    pub expected_template_preset: String,
    pub expected_duplex: bool,
    pub expected_dpi: Option<u32>,
    pub network_available: bool,
    pub required_free_bytes: Option<u64>,
}

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct ScannerPreflightCheck {
    pub key: String,
    pub label: String,
    pub status: String,
    pub blocking: bool,
    pub detail: String,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScannerPreflightResult {
    pub ready_to_scan: bool,
    pub profile: ScannerProfile,
    pub checks: Vec<ScannerPreflightCheck>,
    /// Direct WIA/TWAIN acquisition is a separately validated driver bridge.
    /// File intake remains available after a passing preflight.
    pub acquisition_status: String,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScannerIntegrationStatus {
    pub bridge: String,
    pub direct_acquisition: String,
    pub detail: String,
}

#[tauri::command]
pub fn list_scanner_devices() -> Result<Vec<ScannerDevice>, String> {
    SystemWiaInventoryBridge.inventory()
}

#[tauri::command]
pub fn scanner_integration_status() -> ScannerIntegrationStatus {
    ScannerIntegrationStatus {
        bridge: SystemWiaInventoryBridge.bridge_name().to_string(),
        direct_acquisition: "requires_device_validation".to_string(),
        detail: "Windows WIA is used to discover scanner drivers without a vendor SDK. Direct acquisition is not enabled until the installed device has passed an on-site compatibility check; file-based intake remains available.".to_string(),
    }
}

#[tauri::command]
pub fn list_scanner_profiles(
    app: AppHandle,
    session_id: String,
) -> Result<Vec<ScannerProfile>, String> {
    crate::durable_store::with_session(&session_id, || list_scanner_profiles_scoped(app))
}

fn list_scanner_profiles_scoped(app: AppHandle) -> Result<Vec<ScannerProfile>, String> {
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

#[tauri::command]
pub fn save_scanner_profile(
    app: AppHandle,
    session_id: String,
    input: SaveScannerProfileInput,
) -> Result<ScannerProfile, String> {
    crate::durable_store::with_session(&session_id, || save_scanner_profile_scoped(app, input))
}

fn save_scanner_profile_scoped(
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

#[tauri::command]
pub fn delete_scanner_profile(
    app: AppHandle,
    session_id: String,
    profile_id: String,
) -> Result<(), String> {
    crate::durable_store::with_session(&session_id, || {
        delete_scanner_profile_scoped(app, profile_id)
    })
}

fn delete_scanner_profile_scoped(app: AppHandle, profile_id: String) -> Result<(), String> {
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

#[tauri::command]
pub fn run_scanner_preflight(
    app: AppHandle,
    session_id: String,
    request: ScannerPreflightRequest,
) -> Result<ScannerPreflightResult, String> {
    crate::durable_store::with_session(&session_id, || run_scanner_preflight_scoped(app, request))
}

fn run_scanner_preflight_scoped(
    app: AppHandle,
    request: ScannerPreflightRequest,
) -> Result<ScannerPreflightResult, String> {
    validate_preflight_request(&request)?;
    let conn = open_profile_store(&app)?;
    let profile = read_profile(&conn, &request.profile_id)?;
    let inventory = SystemWiaInventoryBridge.inventory();
    // The outer lease guard holds the operation lock while preflight runs.
    // A scoped store root proves that a logged-in account is selected.
    let durable_status = crate::durable_store::scoped_status(app.clone());
    let free_bytes = available_disk_bytes();
    Ok(evaluate_preflight(
        profile,
        inventory,
        durable_status.is_ok(),
        free_bytes,
        &request,
    ))
}

fn evaluate_preflight(
    profile: ScannerProfile,
    inventory: Result<Vec<ScannerDevice>, String>,
    durable_storage_ready: bool,
    free_bytes: Option<u64>,
    request: &ScannerPreflightRequest,
) -> ScannerPreflightResult {
    let mut checks = Vec::new();
    match inventory {
        Ok(devices)
            if devices
                .iter()
                .any(|device| device.fingerprint == profile.device_fingerprint) =>
        {
            checks.push(pass(
                "device_connected",
                "设备连接",
                "已在 Windows WIA 设备清单中找到当前设备。",
            ))
        }
        Ok(_) => checks.push(fail(
            "device_connected",
            "设备连接",
            "未找到配置的扫描设备；请重新连接设备或选择与当前驱动匹配的 Profile。",
        )),
        Err(error) => checks.push(fail(
            "device_connected",
            "设备连接",
            format!("无法读取 Windows WIA 设备清单：{error}"),
        )),
    }

    if profile
        .paper_size
        .eq_ignore_ascii_case(request.expected_paper_size.trim())
    {
        checks.push(pass(
            "paper_size",
            "答题卡纸张",
            format!("{} 与答题卡模板一致。", profile.paper_size),
        ));
    } else {
        checks.push(fail(
            "paper_size",
            "答题卡纸张",
            format!(
                "Profile 为 {}，当前答题卡模板要求 {}。",
                profile.paper_size, request.expected_paper_size
            ),
        ));
    }
    if profile.template_preset == request.expected_template_preset.trim() {
        checks.push(pass(
            "template_preset",
            "答题卡模板",
            format!("{} 与本批答题卡模板一致。", profile.template_preset),
        ));
    } else {
        checks.push(fail(
            "template_preset",
            "答题卡模板",
            format!(
                "Profile 使用 {}，本批答题卡要求 {}。",
                profile.template_preset, request.expected_template_preset
            ),
        ));
    }
    if profile.duplex == request.expected_duplex {
        checks.push(pass(
            "duplex",
            "单双面",
            if profile.duplex {
                "Profile 使用双面扫描。"
            } else {
                "Profile 使用单面扫描。"
            },
        ));
    } else {
        checks.push(fail(
            "duplex",
            "单双面",
            "Profile 的单双面设置与本批答题卡不一致。",
        ));
    }
    match request.expected_dpi {
        Some(expected) if expected == profile.dpi => checks.push(pass(
            "dpi",
            "分辨率",
            format!("{} DPI 与本批实验配置一致。", profile.dpi),
        )),
        Some(expected) => checks.push(fail(
            "dpi",
            "分辨率",
            format!(
                "Profile 为 {} DPI，本批配置要求 {} DPI。",
                profile.dpi, expected
            ),
        )),
        None => checks.push(warning(
            "dpi",
            "分辨率",
            format!(
                "当前使用 {} DPI。300 DPI 仅为实验起点，需依据真实答题卡 OCR/公式识别结果校准。",
                profile.dpi
            ),
        )),
    }
    if durable_storage_ready {
        checks.push(pass(
            "secure_storage",
            "加密本地存储",
            "SQLite 队列、加密 spool 与系统密钥可用。",
        ));
    } else {
        checks.push(fail(
            "secure_storage",
            "加密本地存储",
            "本地加密存储不可用，不能开始生产扫描。",
        ));
    }
    let required_free = request
        .required_free_bytes
        .unwrap_or(DEFAULT_REQUIRED_FREE_BYTES);
    match free_bytes {
        Some(value) if value >= required_free => checks.push(pass(
            "disk_space",
            "本地磁盘空间",
            format!(
                "可用 {}，满足扫描 spool 最低要求 {}。",
                format_bytes(value),
                format_bytes(required_free)
            ),
        )),
        Some(value) => checks.push(fail(
            "disk_space",
            "本地磁盘空间",
            format!(
                "可用 {}，低于扫描 spool 最低要求 {}。",
                format_bytes(value),
                format_bytes(required_free)
            ),
        )),
        None => checks.push(warning(
            "disk_space",
            "本地磁盘空间",
            "无法从当前系统读取可用磁盘空间；开始扫描前请人工确认 spool 所在磁盘空间。",
        )),
    }
    checks.push(if request.network_available {
        pass("network", "网络连接", "网络可用；上传会在扫描后异步进行。")
    } else {
        warning(
            "network",
            "网络连接",
            "当前离线；扫描可继续，本地 spool 会在网络恢复后续传。",
        )
    });
    checks.push(warning(
        "sample_quality",
        "样张质量预检",
        "请在开始批量扫描前选择一张扫描样张，运行模糊、曝光、裁边和倾斜检查。该检查是筛查，不替代服务端质量门禁。",
    ));
    let ready_to_scan = !checks.iter().any(|check| check.blocking);
    ScannerPreflightResult {
        ready_to_scan,
        profile,
        checks,
        acquisition_status: "file_intake_ready; direct_wia_acquisition_requires_device_validation"
            .to_string(),
    }
}

fn pass(key: &str, label: &str, detail: impl Into<String>) -> ScannerPreflightCheck {
    ScannerPreflightCheck {
        key: key.into(),
        label: label.into(),
        status: "passed".into(),
        blocking: false,
        detail: detail.into(),
    }
}

fn warning(key: &str, label: &str, detail: impl Into<String>) -> ScannerPreflightCheck {
    ScannerPreflightCheck {
        key: key.into(),
        label: label.into(),
        status: "warning".into(),
        blocking: false,
        detail: detail.into(),
    }
}

fn fail(key: &str, label: &str, detail: impl Into<String>) -> ScannerPreflightCheck {
    ScannerPreflightCheck {
        key: key.into(),
        label: label.into(),
        status: "failed".into(),
        blocking: true,
        detail: detail.into(),
    }
}

fn validate_profile_input(input: &SaveScannerProfileInput) -> Result<(), String> {
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

fn validate_preflight_request(request: &ScannerPreflightRequest) -> Result<(), String> {
    required("profile ID", &request.profile_id, 128)?;
    required("expected paper size", &request.expected_paper_size, 32)?;
    required(
        "expected template preset",
        &request.expected_template_preset,
        MAX_PRESET_BYTES,
    )?;
    if request
        .expected_dpi
        .is_some_and(|dpi| !(150..=1200).contains(&dpi))
    {
        return Err("expected DPI is invalid".into());
    }
    if request
        .required_free_bytes
        .is_some_and(|bytes| bytes == 0 || bytes > 500 * 1024 * 1024 * 1024)
    {
        return Err("required free disk space is invalid".into());
    }
    Ok(())
}

fn required(name: &str, value: &str, limit: usize) -> Result<(), String> {
    if value.trim().is_empty() || value.len() > limit {
        return Err(format!("scanner {name} is invalid"));
    }
    Ok(())
}

fn open_profile_store(app: &AppHandle) -> Result<Connection, String> {
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

fn read_profile(conn: &Connection, id: &str) -> Result<ScannerProfile, String> {
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

fn inventory_devices() -> Result<Vec<ScannerDevice>, String> {
    #[cfg(windows)]
    {
        // This script is static (no device/profile data is interpolated).  WIA
        // is part of Windows, which lets us avoid a vendor SDK at this boundary.
        let output = Command::new("powershell")
            .args([
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                "$m=New-Object -ComObject WIA.DeviceManager; @($m.DeviceInfos | Where-Object {$_.Type -eq 1} | ForEach-Object {[PSCustomObject]@{name=$_.Properties['Name'].Value; id=$_.DeviceID; status='ready'}}) | ConvertTo-Json -Compress",
            ])
            .output()
            .map_err(|error| format!("cannot start Windows WIA inventory: {error}"))?;
        if !output.status.success() {
            return Err(String::from_utf8_lossy(&output.stderr).trim().to_string());
        }
        parse_windows_inventory(&String::from_utf8_lossy(&output.stdout))
    }
    #[cfg(not(windows))]
    {
        Err("scanner WIA inventory is only available on Windows".into())
    }
}

#[cfg(windows)]
#[derive(Deserialize)]
struct WindowsInventoryEntry {
    #[serde(default)]
    name: String,
    #[serde(default)]
    id: String,
    #[serde(default)]
    status: String,
}

#[cfg(windows)]
fn parse_windows_inventory(json: &str) -> Result<Vec<ScannerDevice>, String> {
    if json.trim().is_empty() || json.trim() == "null" {
        return Ok(Vec::new());
    }
    let entries: Vec<WindowsInventoryEntry> = match serde_json::from_str(json) {
        Ok(items) => items,
        Err(_) => vec![serde_json::from_str(json)
            .map_err(|_| "Windows WIA inventory returned invalid device data")?],
    };
    Ok(entries
        .into_iter()
        .filter(|entry| !entry.id.trim().is_empty())
        .map(|entry| ScannerDevice {
            fingerprint: fingerprint(&entry.id),
            display_name: if entry.name.trim().is_empty() {
                "Unnamed WIA scanner".into()
            } else {
                entry.name
            },
            driver_status: if entry.status.trim().is_empty() {
                "unknown".into()
            } else {
                entry.status
            },
            bridge: "wia_inventory".into(),
        })
        .collect())
}

fn available_disk_bytes() -> Option<u64> {
    #[cfg(windows)]
    {
        let output = Command::new("powershell")
            .args([
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                "Get-PSDrive -PSProvider FileSystem | Sort-Object Free -Descending | Select-Object -First 1 -ExpandProperty Free",
            ])
            .output()
            .ok()?;
        if !output.status.success() {
            return None;
        }
        String::from_utf8_lossy(&output.stdout).trim().parse().ok()
    }
    #[cfg(not(windows))]
    {
        None
    }
}

fn fingerprint(value: &str) -> String {
    let mut digest = Sha256::new();
    digest.update(value.as_bytes());
    let hex = digest
        .finalize()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    format!("wia:{}", &hex[..24])
}

fn now_rfc3339() -> String {
    Utc::now().to_rfc3339()
}

fn format_bytes(value: u64) -> String {
    const GIB: u64 = 1024 * 1024 * 1024;
    const MIB: u64 = 1024 * 1024;
    if value >= GIB {
        format!("{:.1} GB", value as f64 / GIB as f64)
    } else {
        format!("{:.0} MB", value as f64 / MIB as f64)
    }
}

fn sql_error(error: rusqlite::Error) -> String {
    error.to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn profile() -> ScannerProfile {
        ScannerProfile {
            id: "profile-1".into(),
            name: "A4 双面答题卡".into(),
            device_fingerprint: "wia:test-device".into(),
            dpi: 300,
            duplex: true,
            color_mode: "grayscale".into(),
            paper_size: "A4".into(),
            auto_rotate: true,
            compression: "jpeg".into(),
            template_preset: "math-a4-v1".into(),
            created_at: "2026-08-11T00:00:00Z".into(),
            updated_at: "2026-08-11T00:00:00Z".into(),
        }
    }

    fn request() -> ScannerPreflightRequest {
        ScannerPreflightRequest {
            profile_id: "profile-1".into(),
            expected_paper_size: "A4".into(),
            expected_template_preset: "math-a4-v1".into(),
            expected_duplex: true,
            expected_dpi: Some(300),
            network_available: false,
            required_free_bytes: Some(1024),
        }
    }

    #[test]
    fn preflight_blocks_a_missing_device_before_batch_scan() {
        let result = evaluate_preflight(profile(), Ok(Vec::new()), true, Some(4096), &request());
        assert!(!result.ready_to_scan);
        assert!(result
            .checks
            .iter()
            .any(|check| check.key == "device_connected" && check.blocking));
        assert!(result
            .checks
            .iter()
            .any(|check| check.key == "network" && !check.blocking));
    }

    #[test]
    fn preflight_blocks_mismatched_template_settings() {
        let mut mismatched = request();
        mismatched.expected_dpi = Some(200);
        mismatched.expected_duplex = false;
        mismatched.expected_paper_size = "A3".into();
        mismatched.expected_template_preset = "math-a3-v1".into();
        let devices = vec![ScannerDevice {
            fingerprint: "wia:test-device".into(),
            display_name: "fixture".into(),
            driver_status: "ready".into(),
            bridge: "wia_inventory".into(),
        }];
        let result = evaluate_preflight(profile(), Ok(devices), true, Some(4096), &mismatched);
        assert!(!result.ready_to_scan);
        assert_eq!(
            result.checks.iter().filter(|check| check.blocking).count(),
            4
        );
    }

    #[test]
    fn profile_validation_keeps_dpi_as_a_calibrated_range_not_a_fixed_national_rule() {
        let mut input = SaveScannerProfileInput {
            id: None,
            name: "fixture".into(),
            device_fingerprint: "wia:test".into(),
            dpi: 300,
            duplex: false,
            color_mode: "grayscale".into(),
            paper_size: "A4".into(),
            auto_rotate: true,
            compression: "jpeg".into(),
            template_preset: "fixture".into(),
        };
        assert!(validate_profile_input(&input).is_ok());
        input.dpi = 100;
        assert!(validate_profile_input(&input).is_err());
    }
}

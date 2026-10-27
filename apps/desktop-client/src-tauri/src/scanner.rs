//! Windows 扫描站的扫描器集成边界。
//!
//! 产品层只依赖 [`ScannerProfile`] 和预检结果。
//! 它不导入厂商 SDK。首个 Windows 桥接仅使用操作系统的
//! WIA 设备清单发现受支持的驱动；采集功能
//! 保留在 [`ScannerBridge`] 后面，只有在真实设备上
//! 验证后才可启用。这样可避免将检测到的打印机或驱动
//! 宣传为已经可用于生产的扫描器。

use serde::{Deserialize, Serialize};
use tauri::AppHandle;

mod preflight;
mod profile_store;
mod wia;

use preflight::{evaluate_preflight, validate_preflight_request};
use profile_store::{
    delete_scanner_profile_scoped, list_scanner_profiles_scoped, open_profile_store, read_profile,
    save_scanner_profile_scoped,
};
use wia::inventory_devices;

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
    /// 从驱动提供的 ID 派生的稳定、不可逆标识。
    pub fingerprint: String,
    pub display_name: String,
    pub driver_status: String,
    /// "wia_inventory" 表示 Windows 驱动可见，
    /// 不表示采集流程已经通过硬件验证。
    pub bridge: String,
}

/// 扫描器适配器保持精简，便于替换 WIA、TWAIN 或学校提供的
/// 桥接实现，避免厂商类型扩散到整个应用。
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
    /// 本批次选用的答题卡模板，由调用方显式传入，
    /// 不从文件名推断。
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
    /// 直接 WIA/TWAIN 采集由单独验证的驱动桥接实现。
    /// 预检通过后仍可使用文件导入。
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

#[tauri::command]
pub fn save_scanner_profile(
    app: AppHandle,
    session_id: String,
    input: SaveScannerProfileInput,
) -> Result<ScannerProfile, String> {
    crate::durable_store::with_session(&session_id, || save_scanner_profile_scoped(app, input))
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
    // 预检期间，外层租约守卫持有操作锁。
    // 带账号作用域的存储根目录表明已选定登录账号。
    let durable_status = crate::durable_store::scoped_status(app.clone());
    let free_bytes = available_disk_bytes(&durable_status);
    Ok(evaluate_preflight(
        profile,
        inventory,
        durable_status.is_ok(),
        free_bytes,
        &request,
    ))
}

fn available_disk_bytes(
    durable_status: &Result<crate::durable_store::DurableStoreStatus, String>,
) -> Option<u64> {
    // 预检外层持有账号操作锁；只查询本次状态实际打开的 spool 卷，其他盘空闲不能替它放行。
    // 状态或目录不可用时保留“未知”提示，不退回系统盘或任意最大空闲盘。
    let status = durable_status.as_ref().ok()?;
    if !status.ready || status.spool_path.trim().is_empty() {
        return None;
    }
    crate::durable_store::storage::volume_free_bytes(std::path::Path::new(&status.spool_path))
}

#[cfg(test)]
mod tests;

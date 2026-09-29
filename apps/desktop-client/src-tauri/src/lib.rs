mod durable_store;
mod scanner;

use regex::Regex;
use serde::{Deserialize, Serialize};
use std::{fs, fs::OpenOptions, io::Write, path::Path, sync::LazyLock};
use tauri::{AppHandle, Manager};
use url::Url;
use zeroize::Zeroize;

const MAX_LOG_FILE_BYTES: u64 = 5 * 1024 * 1024;
const MAX_LOG_ID_BYTES: usize = 128;
const MAX_LOG_TIMESTAMP_BYTES: usize = 64;
const MAX_LOG_MESSAGE_BYTES: usize = 4 * 1024;
const MAX_LOG_CONTEXT_BYTES: usize = 32 * 1024;
const MAX_SERVER_URL_BYTES: usize = 2 * 1024;
const MAX_TENANT_CODE_BYTES: usize = 128;
const MAX_USERNAME_BYTES: usize = 256;
const MAX_PASSWORD_BYTES: usize = 1024;
#[cfg(windows)]
const CREDENTIAL_SERVICE: &str = "com.edugrade.enterprise.desktop";
#[cfg(windows)]
const CREDENTIAL_ACCOUNT: &str = "desktop-auto-login";

static SENSITIVE_ASSIGNMENT: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(
        r#"(?i)[\"']?\b(authorization|access[_-]?token|refresh[_-]?token|id[_-]?token|password|secret|credential)\b[\"']?\s*[:=]\s*(?:\"[^\"]*\"|'[^']*'|[^\s,;}]+)"#,
    )
    .expect("sensitive assignment regex must compile")
});
static BEARER_TOKEN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+").expect("bearer token regex must compile")
});

#[derive(Serialize)]
struct CapabilityProbe {
    key: String,
    name: String,
    status: String,
    detail: String,
}

#[derive(Serialize)]
struct RuntimeDiagnostics {
    runtime: String,
    platform: String,
    #[serde(rename = "appVersion")]
    app_version: String,
    #[serde(rename = "logPath")]
    log_path: Option<String>,
}

#[derive(Deserialize, Serialize)]
struct StoredDesktopCredentials {
    server_url: String,
    tenant_code: String,
    username: String,
    password: String,
}

impl StoredDesktopCredentials {
    fn validate(&self) -> Result<(), String> {
        validate_credential_field("server_url", &self.server_url, MAX_SERVER_URL_BYTES)?;
        validate_credential_field("tenant_code", &self.tenant_code, MAX_TENANT_CODE_BYTES)?;
        validate_credential_field("username", &self.username, MAX_USERNAME_BYTES)?;
        validate_credential_field("password", &self.password, MAX_PASSWORD_BYTES)?;
        validate_server_url(&self.server_url)
    }
}

impl Drop for StoredDesktopCredentials {
    // 结构释放时清除密码缓冲区，避免自动登录凭据在进程内存中长期残留。
    fn drop(&mut self) {
        self.password.zeroize();
    }
}

#[derive(Deserialize, Serialize)]
struct LocalLogEntry {
    id: String,
    at: String,
    level: String,
    message: String,
    context: Option<String>,
}

impl LocalLogEntry {
    fn validate(&self) -> Result<(), String> {
        validate_required("id", &self.id, MAX_LOG_ID_BYTES)?;
        validate_required("at", &self.at, MAX_LOG_TIMESTAMP_BYTES)?;
        validate_required("message", &self.message, MAX_LOG_MESSAGE_BYTES)?;
        if !matches!(self.level.as_str(), "info" | "warning" | "error") {
            return Err("log level is invalid".into());
        }
        if self
            .context
            .as_ref()
            .is_some_and(|context| context.len() > MAX_LOG_CONTEXT_BYTES)
        {
            return Err("log context exceeds the size limit".into());
        }
        Ok(())
    }

    fn redact(mut self) -> Self {
        self.message = redact_sensitive_text(&self.message);
        self.context = self.context.as_deref().map(redact_sensitive_text);
        self
    }
}

fn validate_required(name: &str, value: &str, max_bytes: usize) -> Result<(), String> {
    if value.trim().is_empty() {
        return Err(format!("log {name} is required"));
    }
    if value.len() > max_bytes {
        return Err(format!("log {name} exceeds the size limit"));
    }
    Ok(())
}

fn validate_credential_field(name: &str, value: &str, max_bytes: usize) -> Result<(), String> {
    if value.trim().is_empty() {
        return Err(format!("credential {name} is required"));
    }
    if value.len() > max_bytes {
        return Err(format!("credential {name} exceeds the size limit"));
    }
    Ok(())
}

fn validate_server_url(value: &str) -> Result<(), String> {
    let parsed = Url::parse(value.trim()).map_err(|_| "credential server_url is invalid")?;
    if !parsed.username().is_empty() || parsed.password().is_some() {
        return Err("credential server_url must not contain user information".into());
    }
    match parsed.scheme() {
        "https" => Ok(()),
        "http" if parsed.host_str().is_some_and(is_loopback_host) => Ok(()),
        "http" => Err("unencrypted HTTP is allowed only for loopback development servers".into()),
        _ => Err("credential server_url must use HTTPS".into()),
    }
}

fn is_loopback_host(host: &str) -> bool {
    host.eq_ignore_ascii_case("localhost")
        || host
            .parse::<std::net::IpAddr>()
            .is_ok_and(|address| address.is_loopback())
}

fn redact_sensitive_text(value: &str) -> String {
    let without_bearer = BEARER_TOKEN.replace_all(value, "Bearer [REDACTED]");
    SENSITIVE_ASSIGNMENT
        .replace_all(&without_bearer, "$1=[REDACTED]")
        .into_owned()
}

#[tauri::command]
fn capability_statuses(app: AppHandle, session_id: Option<String>) -> Vec<CapabilityProbe> {
    let local_cache = match session_id
        .as_deref()
        .ok_or_else(|| "log in before inspecting durable local records".to_string())
        .and_then(|session_id| durable_store::status(app, session_id))
    {
        Ok(status) => CapabilityProbe {
            key: "local_cache".into(),
            name: "SQLite 加密本地存储".into(),
            status: if status.legacy_data_present {
                "legacy_data"
            } else {
                "ready"
            }
            .into(),
            detail: if status.legacy_data_present {
                format!("当前账号的 SQLite 队列与加密 spool 已就绪：{}；检测到旧版未绑定账号的本地数据，已隔离保留，需由管理员核对归属后恢复。", status.spool_path)
            } else {
                format!("SQLite 队列与加密 spool 已就绪：{}", status.spool_path)
            },
        },
        Err(error) => CapabilityProbe {
            key: "local_cache".into(),
            name: "SQLite 加密本地存储".into(),
            status: "unavailable".into(),
            detail: format!("本地安全存储不可用，客户端不会回退到明文扫描队列：{error}"),
        },
    };
    vec![
        secure_config_capability(),
        local_cache,
        CapabilityProbe {
            key: "device_binding".into(),
            name: "设备绑定".into(),
            status: "not_configured".into(),
            detail: "未配置/待接入后端设备登记、吊销和绑定校验接口。".into(),
        },
        CapabilityProbe {
            key: "auto_update".into(),
            name: "自动更新".into(),
            status: "not_configured".into(),
            detail: "未配置/待接入内网更新源、签名校验和灰度策略。".into(),
        },
    ]
}

#[tauri::command]
fn claim_legacy_durable_store(app: AppHandle, session_id: String) -> Result<(), String> {
    durable_store::with_session(&session_id, || durable_store::claim_legacy_store(app))
}

fn secure_config_capability() -> CapabilityProbe {
    #[cfg(windows)]
    {
        CapabilityProbe {
            key: "secure_config".into(),
            name: "Windows 系统凭据库".into(),
            status: "ready".into(),
            detail: "保存的登录密码由当前 Windows 用户的 Credential Manager 保护；不会写入 WebView 存储或日志。".into(),
        }
    }
    #[cfg(not(windows))]
    {
        CapabilityProbe {
            key: "secure_config".into(),
            name: "Windows 系统凭据库".into(),
            status: "unavailable".into(),
            detail: "此客户端只在 Windows 上启用系统凭据库，其他平台不会降级保存密码。".into(),
        }
    }
}

#[tauri::command]
fn runtime_diagnostics(app: AppHandle) -> RuntimeDiagnostics {
    let log_path = app
        .path()
        .app_log_dir()
        .ok()
        .map(|path| path.to_string_lossy().to_string());
    RuntimeDiagnostics {
        runtime: "tauri".into(),
        platform: std::env::consts::OS.into(),
        app_version: app.package_info().version.to_string(),
        log_path,
    }
}

#[tauri::command]
fn append_local_log(app: AppHandle, entry: LocalLogEntry) -> Result<(), String> {
    // 原生入口仍需自行校验和脱敏，不能依赖 WebView 调用方已经处理过敏感字段。
    entry.validate()?;
    let entry = entry.redact();
    entry.validate()?;
    let log_dir = app.path().app_log_dir().map_err(|err| err.to_string())?;
    fs::create_dir_all(&log_dir).map_err(|err| err.to_string())?;
    let log_path = log_dir.join("client.log");
    reject_symbolic_path(&log_path)?;
    if fs::metadata(&log_path)
        .map(|metadata| metadata.len() >= MAX_LOG_FILE_BYTES)
        .unwrap_or(false)
    {
        let archived_path = log_dir.join("client.log.1");
        match fs::remove_file(&archived_path) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.to_string()),
        }
        fs::rename(&log_path, &archived_path).map_err(|err| err.to_string())?;
    }
    let mut file = OpenOptions::new()
        .create(true)
        .append(true)
        .open(log_path)
        .map_err(|err| err.to_string())?;
    let line = serde_json::to_string(&entry).map_err(|err| err.to_string())?;
    writeln!(file, "{line}").map_err(|err| err.to_string())
}

#[tauri::command]
fn clear_local_logs(app: AppHandle) -> Result<(), String> {
    let log_dir = app.path().app_log_dir().map_err(|err| err.to_string())?;
    for name in ["client.log", "client.log.1"] {
        let path = log_dir.join(name);
        reject_symbolic_path(&path)?;
        match fs::remove_file(path) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.to_string()),
        }
    }
    Ok(())
}

fn reject_symbolic_path(path: &Path) -> Result<(), String> {
    if fs::symlink_metadata(path)
        .map(|metadata| metadata.file_type().is_symlink())
        .unwrap_or(false)
    {
        return Err("refusing to access a symbolic log path".into());
    }
    Ok(())
}

#[tauri::command]
fn save_desktop_credentials(credentials: StoredDesktopCredentials) -> Result<(), String> {
    credentials.validate()?;
    #[cfg(windows)]
    {
        let entry = credential_entry()?;
        let mut payload = serde_json::to_string(&credentials).map_err(|err| err.to_string())?;
        let result = entry.set_password(&payload).map_err(|err| err.to_string());
        payload.zeroize();
        result
    }
    #[cfg(not(windows))]
    {
        Err("Windows Credential Manager is unavailable on this platform".into())
    }
}

#[tauri::command]
fn load_desktop_credentials() -> Result<Option<StoredDesktopCredentials>, String> {
    #[cfg(windows)]
    {
        let entry = credential_entry()?;
        let mut payload = match entry.get_password() {
            Ok(payload) => payload,
            Err(keyring::Error::NoEntry) => return Ok(None),
            Err(error) => return Err(error.to_string()),
        };
        let parsed = serde_json::from_str::<StoredDesktopCredentials>(&payload)
            .map_err(|_| "stored desktop credentials are invalid".to_string());
        payload.zeroize();
        let credentials = parsed?;
        credentials.validate()?;
        Ok(Some(credentials))
    }
    #[cfg(not(windows))]
    {
        Err("Windows Credential Manager is unavailable on this platform".into())
    }
}

#[tauri::command]
fn delete_desktop_credentials() -> Result<(), String> {
    #[cfg(windows)]
    {
        let entry = credential_entry()?;
        match entry.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(error) => Err(error.to_string()),
        }
    }
    #[cfg(not(windows))]
    {
        Err("Windows Credential Manager is unavailable on this platform".into())
    }
}

#[tauri::command]
fn bind_durable_session(
    server: String,
    tenant_id: String,
    actor_id: String,
) -> Result<String, String> {
    durable_store::bind_session(&server, &tenant_id, &actor_id)
}

#[tauri::command]
fn clear_durable_session() -> Result<(), String> {
    durable_store::clear_session()
}

#[tauri::command]
fn begin_spool_local_asset(
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
fn write_spool_local_asset_chunk(
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
fn complete_spool_local_asset(
    app: AppHandle,
    session_id: String,
    local_asset_id: String,
) -> Result<durable_store::DurableQueueItem, String> {
    durable_store::with_session(&session_id, || {
        durable_store::complete_spool_local_asset(app, local_asset_id)
    })
}

#[tauri::command]
fn list_durable_scan_queue(
    app: AppHandle,
    session_id: String,
) -> Result<Vec<durable_store::DurableQueueItem>, String> {
    durable_store::with_session(&session_id, || durable_store::list_durable_scan_queue(app))
}

#[tauri::command]
fn persist_durable_scan_queue_item(
    app: AppHandle,
    session_id: String,
    item: durable_store::DurableQueueItem,
) -> Result<(), String> {
    durable_store::with_session(&session_id, || {
        durable_store::persist_durable_scan_queue_item(app, item)
    })
}

#[tauri::command]
fn archive_durable_scan_queue_items(
    app: AppHandle,
    session_id: String,
    ids: Vec<String>,
) -> Result<(), String> {
    durable_store::with_session(&session_id, || {
        durable_store::archive_durable_scan_queue_items(app, ids)
    })
}

#[tauri::command]
fn read_durable_local_asset(
    app: AppHandle,
    session_id: String,
    local_asset_id: String,
) -> Result<durable_store::DurableSpoolFile, String> {
    durable_store::with_session(&session_id, || {
        durable_store::read_durable_local_asset(app, local_asset_id)
    })
}

#[tauri::command]
fn read_durable_local_asset_chunk(
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
fn save_durable_draft(
    app: AppHandle,
    session_id: String,
    record: serde_json::Value,
) -> Result<(), String> {
    durable_store::with_session(&session_id, || {
        durable_store::save_durable_draft(app, record)
    })
}

#[tauri::command]
fn list_durable_drafts(
    app: AppHandle,
    session_id: String,
) -> Result<Vec<durable_store::OfflineDraftEnvelope>, String> {
    durable_store::with_session(&session_id, || durable_store::list_durable_drafts(app))
}

#[tauri::command]
fn load_durable_draft(
    app: AppHandle,
    session_id: String,
    task_id: String,
) -> Result<Option<serde_json::Value>, String> {
    durable_store::with_session(&session_id, || {
        durable_store::load_durable_draft(app, task_id)
    })
}

#[tauri::command]
fn update_durable_draft_status(
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
fn purge_expired_durable_drafts(
    app: AppHandle,
    session_id: String,
    now: String,
) -> Result<usize, String> {
    durable_store::with_session(&session_id, || {
        durable_store::purge_expired_durable_drafts(app, now)
    })
}

#[cfg(windows)]
fn credential_entry() -> Result<keyring::Entry, String> {
    keyring::Entry::new(CREDENTIAL_SERVICE, CREDENTIAL_ACCOUNT).map_err(|err| err.to_string())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    if let Err(error) = tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            bind_durable_session,
            clear_durable_session,
            capability_statuses,
            claim_legacy_durable_store,
            begin_spool_local_asset,
            write_spool_local_asset_chunk,
            complete_spool_local_asset,
            list_durable_scan_queue,
            persist_durable_scan_queue_item,
            archive_durable_scan_queue_items,
            read_durable_local_asset,
            read_durable_local_asset_chunk,
            save_durable_draft,
            list_durable_drafts,
            load_durable_draft,
            update_durable_draft_status,
            purge_expired_durable_drafts,
            scanner::list_scanner_devices,
            scanner::scanner_integration_status,
            scanner::list_scanner_profiles,
            scanner::save_scanner_profile,
            scanner::delete_scanner_profile,
            scanner::run_scanner_preflight,
            runtime_diagnostics,
            append_local_log,
            clear_local_logs,
            save_desktop_credentials,
            load_desktop_credentials,
            delete_desktop_credentials
        ])
        .run(tauri::generate_context!())
    {
        eprintln!("error while running EduGrade desktop client: {error}");
        std::process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn redacts_bearer_and_named_secrets_from_logs() {
        let input =
            r#"authorization: Bearer abc.def password=cleartext {"access_token":"token-value"}"#;
        let output = redact_sensitive_text(input);
        assert!(!output.contains("abc.def"));
        assert!(!output.contains("cleartext"));
        assert!(!output.contains("token-value"));
        assert!(output.contains("[REDACTED]"));
    }

    #[test]
    fn credential_validation_requires_tls_except_loopback() {
        let valid = StoredDesktopCredentials {
            server_url: "https://grading.example.edu".into(),
            tenant_code: "school".into(),
            username: "grader".into(),
            password: "not-a-real-password".into(),
        };
        assert!(valid.validate().is_ok());
        assert!(validate_server_url("http://127.0.0.1:8080").is_ok());
        assert!(validate_server_url("http://grading.example.edu").is_err());
        assert!(validate_server_url("https://user:password@example.edu").is_err());
    }
}

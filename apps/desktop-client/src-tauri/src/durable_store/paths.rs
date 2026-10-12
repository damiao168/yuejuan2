use sha2::{Digest, Sha256};
use std::sync::{LazyLock, Mutex, RwLock};
use std::{
    fs,
    io::Write,
    path::{Path, PathBuf},
};
use tauri::{AppHandle, Manager};

struct ActiveScope {
    path_component: String,
    session_id: String,
    offline_valid_until: Option<chrono::DateTime<chrono::Utc>>,
}

static ACTIVE_SCOPE: LazyLock<RwLock<Option<ActiveScope>>> = LazyLock::new(|| RwLock::new(None));
// 命令同步执行。此锁让命令在原账号下完成后，
// 新的账号绑定才能生效。
static OPERATION_LOCK: LazyLock<Mutex<()>> = LazyLock::new(|| Mutex::new(()));

pub(crate) fn bind_scope(server: &str, tenant_id: &str, actor_id: &str) -> Result<String, String> {
    bind_scope_with_expiry(server, tenant_id, actor_id, None)
}

pub(crate) fn bind_scope_with_expiry(
    server: &str,
    tenant_id: &str,
    actor_id: &str,
    valid_until: Option<chrono::DateTime<chrono::Utc>>,
) -> Result<String, String> {
    let scope = scope_path_component(server, tenant_id, actor_id)?;
    let session_id = uuid::Uuid::new_v4().to_string();
    let _operation = OPERATION_LOCK
        .lock()
        .map_err(|_| "session operation lock failed")?;
    *ACTIVE_SCOPE
        .write()
        .map_err(|_| "session scope lock failed")? = Some(ActiveScope {
        path_component: scope,
        session_id: session_id.clone(),
        offline_valid_until: valid_until,
    });
    Ok(session_id)
}

pub(crate) fn scope_path_component(
    server: &str,
    tenant_id: &str,
    actor_id: &str,
) -> Result<String, String> {
    let url = url::Url::parse(server.trim()).map_err(|_| "invalid session server")?;
    if !matches!(url.scheme(), "https" | "http")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err("invalid session server".into());
    }
    let tenant = uuid::Uuid::parse_str(tenant_id).map_err(|_| "invalid tenant ID")?;
    let actor = uuid::Uuid::parse_str(actor_id).map_err(|_| "invalid actor ID")?;
    let base_url = url.as_str().trim_end_matches('/');
    // 服务地址、租户和用户共同隔离本地目录；零字节分隔可避免不同字段拼接成相同输入。
    let digest = Sha256::digest(format!("{base_url}\0{tenant}\0{actor}").as_bytes());
    Ok(digest
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>())
}

pub(crate) fn clear_scope() -> Result<(), String> {
    let _operation = OPERATION_LOCK
        .lock()
        .map_err(|_| "session operation lock failed")?;
    *ACTIVE_SCOPE
        .write()
        .map_err(|_| "session scope lock failed")? = None;
    Ok(())
}

pub(crate) fn with_session<T>(
    session_id: &str,
    operation: impl FnOnce() -> Result<T, String>,
) -> Result<T, String> {
    let _guard = OPERATION_LOCK
        .lock()
        .map_err(|_| "session operation lock failed")?;
    let active = ACTIVE_SCOPE
        .read()
        .map_err(|_| "session scope lock failed")?;
    if active.as_ref().map(|scope| scope.session_id.as_str()) != Some(session_id) {
        return Err("durable session changed; retry after login".into());
    }
    // 离线授权只决定本机解密；每次原生读写都检查有效期，前端定时器不能延长已过期的租约。
    if active
        .as_ref()
        .and_then(|scope| scope.offline_valid_until)
        .is_some_and(|until| until <= chrono::Utc::now())
    {
        return Err("本机离线授权已过期，请联网登录后续期。".into());
    }
    drop(active);
    operation()
}

// 这里有意采取保守判断。只要 SQLite 存储或受控暂存资产
// 已经存在，密钥缺失就不能视为首次启动。
// 应要求恢复凭据管理器条目，
// 而不是创建无法解密现有证据的新密钥。
pub(crate) fn durable_data_exists(root: &Path) -> Result<bool, String> {
    let database = db_path(root);
    if database.exists() {
        return Ok(true);
    }
    let spool = spool_path(root);
    let mut entries = fs::read_dir(&spool).map_err(|error| error.to_string())?;
    Ok(entries
        .next()
        .transpose()
        .map_err(|error| error.to_string())?
        .is_some())
}

pub(crate) fn legacy_store_exists(data_dir: &Path) -> Result<bool, String> {
    let legacy = legacy_root(data_dir);
    if !legacy.exists() {
        return Ok(false);
    }
    if db_path(&legacy).exists() {
        return Ok(true);
    }
    let spool = spool_path(&legacy);
    if !spool.exists() {
        return Ok(false);
    }
    Ok(fs::read_dir(spool)
        .map_err(|error| error.to_string())?
        .next()
        .is_some())
}

pub(crate) fn legacy_root(data_dir: &Path) -> PathBuf {
    data_dir.join("durable-store-v1")
}

pub(crate) fn active_scope_path_component() -> Result<String, String> {
    ACTIVE_SCOPE
        .read()
        .map_err(|_| "session scope lock failed".to_string())?
        .as_ref()
        .map(|scope| scope.path_component.clone())
        .ok_or_else(|| "log in before accessing durable local records".to_string())
}

pub(crate) fn store_root(app: &AppHandle) -> Result<PathBuf, String> {
    let scope = active_scope_path_component()?;
    let data_dir = app
        .path()
        .app_data_dir()
        .map_err(|error| error.to_string())?;
    let root = account_root(&data_dir, &scope);
    fs::create_dir_all(spool_path(&root)).map_err(|error| error.to_string())?;
    reject_symbolic_path(&root)?;
    reject_symbolic_path(&spool_path(&root))?;
    Ok(root)
}

pub(crate) fn account_root(data_dir: &Path, scope: &str) -> PathBuf {
    data_dir
        .join("durable-store-v2")
        .join("accounts")
        .join(scope)
}

pub(crate) fn db_path(root: &Path) -> PathBuf {
    root.join("offline.sqlite3")
}
pub(crate) fn spool_path(root: &Path) -> PathBuf {
    root.join("spool")
}

pub(crate) fn ensure_controlled_path(root: &Path, path: &Path) -> Result<(), String> {
    let canonical_root = root.canonicalize().map_err(|error| error.to_string())?;
    let canonical_path = path.canonicalize().map_err(|error| error.to_string())?;
    if !canonical_path.starts_with(spool_path(&canonical_root)) {
        return Err("refusing to access a spool path outside the durable store".into());
    }
    reject_symbolic_path(&canonical_path)
}

pub(crate) fn write_new_private_file(path: &Path, bytes: &[u8]) -> Result<(), String> {
    // 只允许创建新文件，写完并刷新到磁盘后再返回，避免覆盖原件或提前确认落盘。
    reject_symbolic_path(path)?;
    let mut file = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
        .map_err(|error| error.to_string())?;
    file.write_all(bytes).map_err(|error| error.to_string())?;
    file.sync_all().map_err(|error| error.to_string())
}

pub(crate) fn reject_symbolic_path(path: &Path) -> Result<(), String> {
    if fs::symlink_metadata(path)
        .map(|metadata| metadata.file_type().is_symlink())
        .unwrap_or(false)
    {
        return Err("refusing to access a symbolic durable storage path".into());
    }
    Ok(())
}

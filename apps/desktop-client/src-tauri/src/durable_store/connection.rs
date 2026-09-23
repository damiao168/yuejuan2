use super::paths::{db_path, store_root};
use rusqlite::Connection;
use std::path::Path;
use tauri::AppHandle;

pub(crate) fn open_connection(app: &AppHandle) -> Result<Connection, String> {
    let root = store_root(app)?;
    open_connection_at(&root)
}

pub(crate) fn open_connection_at(root: &Path) -> Result<Connection, String> {
    Connection::open(db_path(root)).map_err(sql_error)
}

pub(crate) fn now_rfc3339() -> String {
    chrono::Utc::now().to_rfc3339()
}

pub(crate) fn sql_error(error: rusqlite::Error) -> String {
    error.to_string()
}

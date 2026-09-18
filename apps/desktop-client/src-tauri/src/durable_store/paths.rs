use std::{
    fs,
    io::Write,
    path::{Path, PathBuf},
};
use tauri::{AppHandle, Manager};

// This is intentionally conservative.  A missing key must not be treated as
// a first launch if either the SQLite store or a controlled spool asset is
// already present.  It is safer to require recovery of the Credential Manager
// entry than to create a key that can never decrypt the retained evidence.
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

pub(crate) fn store_root(app: &AppHandle) -> Result<PathBuf, String> {
    let root = app
        .path()
        .app_data_dir()
        .map_err(|error| error.to_string())?
        .join("durable-store-v1");
    fs::create_dir_all(spool_path(&root)).map_err(|error| error.to_string())?;
    reject_symbolic_path(&root)?;
    reject_symbolic_path(&spool_path(&root))?;
    Ok(root)
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

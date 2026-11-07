use aes_gcm::{
    aead::{rand_core::RngCore, Aead, OsRng},
    Aes256Gcm, KeyInit, Nonce,
};
use base64::{engine::general_purpose::STANDARD as BASE64, Engine as _};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::path::Path;
use zeroize::Zeroize;

use super::paths::durable_data_exists;

// SQLite 的文件锁由操作系统在进程退出时释放；不能用进程内 Mutex 保护凭据库的首次创建。
#[cfg(any(windows, test))]
fn with_master_key_lock<T>(
    root: &Path,
    action: impl FnOnce() -> Result<T, String>,
) -> Result<T, String> {
    // 锁文件放在账号目录外，旧数据认领搬动空目录时仍使用同一把锁。
    let lock_path = root
        .parent()
        .ok_or("durable account root has no parent")?
        .join(format!(
            ".master-key-{}.sqlite3",
            durable_credential_account(root)?
        ));
    if lock_path.exists() {
        super::paths::reject_symbolic_path(&lock_path)?;
    }
    let connection = rusqlite::Connection::open(lock_path).map_err(|error| error.to_string())?;
    connection
        .busy_timeout(std::time::Duration::from_secs(15))
        .map_err(|error| error.to_string())?;
    connection
        .execute_batch("PRAGMA journal_mode=DELETE; BEGIN EXCLUSIVE;")
        .map_err(|error| error.to_string())?;
    let result = action();
    let released = connection
        .execute_batch("ROLLBACK;")
        .map_err(|error| error.to_string());
    match result {
        Ok(value) => {
            released?;
            Ok(value)
        }
        Err(error) => Err(error),
    }
}

const DURABLE_CREDENTIAL_SERVICE: &str = "com.edugrade.enterprise.desktop";
const LEGACY_DURABLE_CREDENTIAL_ACCOUNT: &str = "desktop-offline-master-key-v1";

pub(crate) fn durable_credential_account(root: &Path) -> Result<String, String> {
    let scope = root
        .file_name()
        .and_then(|value| value.to_str())
        .ok_or("durable account path is invalid")?;
    let is_v2_account = root
        .parent()
        .and_then(Path::file_name)
        .and_then(|value| value.to_str())
        == Some("accounts")
        && scope.len() == 64
        && scope.bytes().all(|value| value.is_ascii_hexdigit());
    if is_v2_account {
        Ok(format!("desktop-offline-master-key-v2-{scope}"))
    } else {
        Ok(LEGACY_DURABLE_CREDENTIAL_ACCOUNT.into())
    }
}

#[cfg(windows)]
fn read_key_for_account(account: &str) -> Result<Option<[u8; 32]>, String> {
    let entry = keyring::Entry::new(DURABLE_CREDENTIAL_SERVICE, account)
        .map_err(|error| error.to_string())?;
    let mut stored = match entry.get_password() {
        Ok(stored) => stored,
        Err(keyring::Error::NoEntry) => return Ok(None),
        Err(error) => {
            return Err(format!(
                "cannot access Windows Credential Manager for the durable local key: {error}"
            ))
        }
    };
    let decoded = BASE64.decode(&stored);
    stored.zeroize();
    let decoded = decoded.map_err(|_| "durable master key is corrupt")?;
    decoded
        .try_into()
        .map(Some)
        .map_err(|_| "durable master key has an invalid length".to_string())
}

#[cfg(windows)]
fn write_key_for_account(account: &str, key: &[u8; 32]) -> Result<(), String> {
    let entry = keyring::Entry::new(DURABLE_CREDENTIAL_SERVICE, account)
        .map_err(|error| error.to_string())?;
    let mut encoded = BASE64.encode(key);
    let result = entry
        .set_password(&encoded)
        .map_err(|error| error.to_string());
    encoded.zeroize();
    result
}

#[cfg(windows)]
pub(crate) fn legacy_master_key() -> Result<[u8; 32], String> {
    read_key_for_account(LEGACY_DURABLE_CREDENTIAL_ACCOUNT)?.ok_or_else(|| {
        "legacy durable data exists but its Windows Credential Manager key is missing; claim was stopped without moving data".into()
    })
}

#[cfg(windows)]
pub(crate) fn install_scoped_master_key(root: &Path, key: &[u8; 32]) -> Result<(), String> {
    with_master_key_lock(root, || {
        write_key_for_account(&durable_credential_account(root)?, key)
    })
}

pub(crate) fn encrypt_json<T: Serialize>(
    key: &[u8; 32],
    value: &T,
) -> Result<(String, String), String> {
    let plaintext = serde_json::to_vec(value).map_err(|error| error.to_string())?;
    let (ciphertext, nonce) = encrypt_bytes(key, &plaintext)?;
    Ok((BASE64.encode(ciphertext), nonce))
}

pub(crate) fn decrypt_json<T: for<'de> Deserialize<'de>>(
    key: &[u8; 32],
    ciphertext: &str,
    nonce: &str,
) -> Result<T, String> {
    let ciphertext = BASE64
        .decode(ciphertext)
        .map_err(|_| "durable encrypted payload is invalid")?;
    let plaintext = decrypt_bytes(key, nonce, &ciphertext)?;
    serde_json::from_slice(&plaintext)
        .map_err(|_| "durable encrypted payload cannot be decoded".into())
}

pub(crate) fn encrypt_bytes(key: &[u8; 32], plaintext: &[u8]) -> Result<(Vec<u8>, String), String> {
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|_| "durable encryption key is invalid")?;
    let mut nonce = [0_u8; 12];
    OsRng.fill_bytes(&mut nonce);
    let ciphertext = cipher
        .encrypt(Nonce::from_slice(&nonce), plaintext)
        .map_err(|_| "durable payload encryption failed")?;
    Ok((ciphertext, BASE64.encode(nonce)))
}

pub(crate) fn decrypt_bytes(
    key: &[u8; 32],
    nonce: &str,
    ciphertext: &[u8],
) -> Result<Vec<u8>, String> {
    let nonce = BASE64
        .decode(nonce)
        .map_err(|_| "durable payload nonce is invalid")?;
    if nonce.len() != 12 {
        return Err("durable payload nonce has an invalid length".into());
    }
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|_| "durable encryption key is invalid")?;
    cipher
        .decrypt(Nonce::from_slice(&nonce), ciphertext)
        .map_err(|_| {
            "durable payload cannot be decrypted; secure local key may be unavailable or corrupted"
                .into()
        })
}

#[cfg(windows)]
pub(crate) fn master_key(root: &Path) -> Result<[u8; 32], String> {
    // 每个 v2 账号作用域都有独立的凭据管理器密钥，使账号密文
    // 在加密层面彼此隔离，同时不会覆盖
    // 恢复隔离数据所需的旧版 v1 凭据。
    let account = durable_credential_account(root)?;
    // 取得跨进程锁后重新读取；第二个首次启动者复用已经写入的密钥，不得覆盖。
    with_master_key_lock(root, || match read_key_for_account(&account)? {
        Some(key) => Ok(key),
        None => {
            // 站点已有存储时，不能用新密钥替代丢失的凭据。
            // 否则队列中的扫描件会看似可恢复，
            // 直到后续解密失败才暴露问题，还可能让操作人员
            // 误删唯一的加密原件副本。
            if durable_data_exists(root)? {
                return Err("durable master key is missing while encrypted local data exists; recovery requires the original Windows Credential Manager entry and the client will not create a replacement key".into());
            }
            let mut key = [0_u8; 32];
            OsRng.fill_bytes(&mut key);
            write_key_for_account(&account, &key)?;
            Ok(key)
        }
    })
}

#[cfg(not(windows))]
pub(crate) fn master_key(_root: &Path) -> Result<[u8; 32], String> {
    Err("durable local storage requires Windows Credential Manager and will not fall back to plaintext storage".into())
}

pub(crate) fn sha256_hex(bytes: &[u8]) -> String {
    let digest = Sha256::digest(bytes);
    digest.iter().map(|byte| format!("{byte:02x}")).collect()
}

#[cfg(all(test, windows))]
mod tests {
    use super::*;
    use std::{
        fs,
        process::Command,
        time::{Duration, Instant},
    };

    #[test]
    fn master_key_process_child() {
        let Some(root) = std::env::var_os("EDUGRADE_MASTER_KEY_TEST_ROOT") else {
            return;
        };
        let root = std::path::PathBuf::from(root);
        let output = root.join(std::env::var("EDUGRADE_MASTER_KEY_TEST_OUTPUT").unwrap());
        let key = master_key(&root).expect("initialize isolated test account key");
        fs::write(output, sha256_hex(&key)).unwrap();
    }

    #[test]
    fn simultaneous_processes_keep_the_same_initial_master_key() {
        let scope = uuid::Uuid::new_v4().simple().to_string().repeat(2);
        let data = std::env::temp_dir().join(format!("edugrade-master-key-{scope}"));
        let root = data.join("accounts").join(scope);
        fs::create_dir_all(root.join("spool")).unwrap();
        let credential = keyring::Entry::new(
            DURABLE_CREDENTIAL_SERVICE,
            &durable_credential_account(&root).unwrap(),
        )
        .unwrap();
        let command = |output: &str| {
            let mut child = Command::new(std::env::current_exe().unwrap());
            child
                .args([
                    "--exact",
                    "durable_store::crypto::tests::master_key_process_child",
                    "--nocapture",
                ])
                .env("EDUGRADE_MASTER_KEY_TEST_ROOT", &root)
                .env("EDUGRADE_MASTER_KEY_TEST_OUTPUT", output);
            child
        };
        // 父进程先持有真实 SQLite 排他锁，让两个首次启动者都等待，然后同时放行。
        let lock = rusqlite::Connection::open(root.parent().unwrap().join(format!(
            ".master-key-{}.sqlite3",
            durable_credential_account(&root).unwrap()
        )))
        .unwrap();
        lock.execute_batch("BEGIN EXCLUSIVE;").unwrap();
        let mut first = command("first.txt").spawn().unwrap();
        let mut second = command("second.txt").spawn().unwrap();
        std::thread::sleep(Duration::from_millis(250));
        assert!(!root.join("first.txt").exists() && !root.join("second.txt").exists());
        lock.execute_batch("ROLLBACK;").unwrap();
        let deadline = Instant::now() + Duration::from_secs(20);
        while (first.try_wait().unwrap().is_none() || second.try_wait().unwrap().is_none())
            && Instant::now() < deadline
        {
            std::thread::sleep(Duration::from_millis(20));
        }
        // 凭据服务异常时结束子进程，避免超时后 wait 再次无限等待并阻塞整组测试。
        if first.try_wait().unwrap().is_none() {
            let _ = first.kill();
        }
        if second.try_wait().unwrap().is_none() {
            let _ = second.kill();
        }
        assert!(first.wait().unwrap().success());
        assert!(second.wait().unwrap().success());
        let a = fs::read_to_string(root.join("first.txt")).unwrap();
        let b = fs::read_to_string(root.join("second.txt")).unwrap();
        let actual = sha256_hex(
            &read_key_for_account(&durable_credential_account(&root).unwrap())
                .unwrap()
                .unwrap(),
        );
        let _ = credential.delete_credential();
        assert_eq!(a, b);
        assert_eq!(a, actual);
        drop(lock);
        fs::remove_dir_all(data).unwrap();
    }
}

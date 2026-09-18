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

const DURABLE_CREDENTIAL_SERVICE: &str = "com.edugrade.enterprise.desktop";
const DURABLE_CREDENTIAL_ACCOUNT: &str = "desktop-offline-master-key-v1";

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
    let entry = keyring::Entry::new(DURABLE_CREDENTIAL_SERVICE, DURABLE_CREDENTIAL_ACCOUNT)
        .map_err(|error| error.to_string())?;
    match entry.get_password() {
        Ok(mut stored) => {
            let decoded = BASE64
                .decode(&stored)
                .map_err(|_| "durable master key is corrupt")?;
            stored.zeroize();
            decoded
                .try_into()
                .map_err(|_| "durable master key has an invalid length".to_string())
        }
        Err(keyring::Error::NoEntry) => {
            // Never replace a missing credential with a fresh key when a
            // previous station store exists.  Doing so would make queued
            // scans look recoverable until a later decrypt fails, and could
            // tempt an operator to discard the only encrypted source copy.
            if durable_data_exists(root)? {
                return Err("durable master key is missing while encrypted local data exists; recovery requires the original Windows Credential Manager entry and the client will not create a replacement key".into());
            }
            let mut key = [0_u8; 32];
            OsRng.fill_bytes(&mut key);
            let mut encoded = BASE64.encode(key);
            let result = entry
                .set_password(&encoded)
                .map_err(|error| error.to_string());
            encoded.zeroize();
            result.map(|()| key)
        }
        Err(error) => Err(format!(
            "cannot access Windows Credential Manager for the durable local key: {error}"
        )),
    }
}

#[cfg(not(windows))]
pub(crate) fn master_key(_root: &Path) -> Result<[u8; 32], String> {
    Err("durable local storage requires Windows Credential Manager and will not fall back to plaintext storage".into())
}

pub(crate) fn sha256_hex(bytes: &[u8]) -> String {
    let digest = Sha256::digest(bytes);
    digest.iter().map(|byte| format!("{byte:02x}")).collect()
}

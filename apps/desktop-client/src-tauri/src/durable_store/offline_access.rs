//! 本机离线授权只保存最近一次在线确认的身份和有限有效期。
//! 授权与账号密钥均留在当前 Windows 用户的凭据库；解锁不会恢复网络访问令牌。
use chrono::{DateTime, Duration, Utc};
use serde::{Deserialize, Serialize};
#[cfg(windows)]
use zeroize::Zeroize;
use super::paths::{active_scope_path_component, bind_scope_with_expiry, scope_path_component, with_session};

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OfflineAccess {
    pub server: String,
    pub tenant_id: String,
    pub actor_id: String,
    pub tenant_code: String,
    pub username: String,
    pub display_name: String,
    pub authorized_at: String,
    pub expires_at: String,
}

impl OfflineAccess {
    fn validate_identity(&self, server: &str, tenant_id: &str, actor_id: &str) -> Result<(), String> {
        if scope_path_component(server, tenant_id, actor_id)? != scope_path_component(&self.server, &self.tenant_id, &self.actor_id)? {
            return Err("本机离线授权与所选账号或学校服务器不一致。".into());
        }
        Ok(())
    }
    fn validate_at(&self, now: DateTime<Utc>) -> Result<DateTime<Utc>, String> {
        scope_path_component(&self.server, &self.tenant_id, &self.actor_id)?;
        crate::validate_server_url(&self.server)?;
        for value in [&self.tenant_code, &self.username, &self.display_name] {
            if value.len() > 256 { return Err("本机离线身份字段过长。".into()); }
        }
        if self.tenant_code.trim().is_empty() || self.username.trim().is_empty() { return Err("本机离线身份不完整。".into()); }
        let authorized = DateTime::parse_from_rfc3339(&self.authorized_at).map_err(|_| "本机授权时间无效。")?.with_timezone(&Utc);
        let expires = DateTime::parse_from_rfc3339(&self.expires_at).map_err(|_| "本机授权有效期无效。")?.with_timezone(&Utc);
        if authorized > now + Duration::minutes(1) || expires <= now || expires <= authorized || expires > authorized + Duration::days(7) {
            return Err("本机离线授权已过期或时间无效，请联网登录后续期。".into());
        }
        Ok(expires)
    }
}

pub fn authorize(session_id: &str, mut access: OfflineAccess) -> Result<OfflineAccess, String> {
    let now = Utc::now();
    let requested = DateTime::parse_from_rfc3339(&access.expires_at).map_err(|_| "登录有效期无效。")?.with_timezone(&Utc);
    access.authorized_at = now.to_rfc3339();
    // 本机授权不超过这次在线登录的服务端有效期，并另设七天上限；不能因离线解锁而续期。
    access.expires_at = requested.min(now + Duration::days(7)).to_rfc3339();
    access.validate_at(now)?;
    with_session(session_id, || {
        if active_scope_path_component()? != scope_path_component(&access.server, &access.tenant_id, &access.actor_id)? {
            return Err("登录身份已变化，未建立本机离线授权。".into());
        }
        write_access(&access)
    })?;
    Ok(access)
}

pub fn read_access() -> Result<Option<OfflineAccess>, String> {
    let access = read_stored_access()?;
    if let Some(value) = &access { value.validate_at(Utc::now())?; }
    Ok(access)
}

pub fn unlock(server: &str, tenant_id: &str, actor_id: &str) -> Result<(String, OfflineAccess), String> {
    let access = read_access()?.ok_or("当前 Windows 用户没有有效本机离线授权，请先联网登录并启用。")?;
    access.validate_identity(server, tenant_id, actor_id)?;
    let until = access.validate_at(Utc::now())?;
    let lease = bind_scope_with_expiry(&access.server, &access.tenant_id, &access.actor_id, Some(until))?;
    Ok((lease, access))
}

#[cfg(windows)]
fn entry() -> Result<keyring::Entry, String> {
    keyring::Entry::new("com.edugrade.enterprise.desktop", "desktop-local-offline-access-v1").map_err(|error| error.to_string())
}
#[cfg(windows)]
fn write_access(access: &OfflineAccess) -> Result<(), String> {
    write_access_at(&entry()?, access)
}
#[cfg(windows)]
fn write_access_at(entry: &keyring::Entry, access: &OfflineAccess) -> Result<(), String> {
    let mut json = serde_json::to_string(access).map_err(|error| error.to_string())?;
    let result = entry.set_password(&json).map_err(|error| error.to_string());
    json.zeroize(); result
}
#[cfg(not(windows))]
fn write_access(_access: &OfflineAccess) -> Result<(), String> { Err("本机离线授权仅支持 Windows 系统凭据库。".into()) }
#[cfg(windows)]
fn read_stored_access() -> Result<Option<OfflineAccess>, String> {
    read_access_at(&entry()?)
}
#[cfg(windows)]
fn read_access_at(entry: &keyring::Entry) -> Result<Option<OfflineAccess>, String> {
    let mut json = match entry.get_password() {
        Ok(value) => value, Err(keyring::Error::NoEntry) => return Ok(None), Err(error) => return Err(error.to_string())
    };
    let result = serde_json::from_str(&json).map(Some).map_err(|_| "本机离线授权损坏，请联网登录后重建。".into());
    json.zeroize(); result
}
#[cfg(not(windows))]
fn read_stored_access() -> Result<Option<OfflineAccess>, String> { Err("本机离线授权仅支持 Windows 系统凭据库。".into()) }
pub fn revoke() -> Result<(), String> {
    #[cfg(windows)] {
        match entry()?.delete_credential() { Ok(()) | Err(keyring::Error::NoEntry) => Ok(()), Err(error) => Err(error.to_string()) }
    }
    #[cfg(not(windows))] { Err("本机离线授权仅支持 Windows 系统凭据库。".into()) }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn access(now: DateTime<Utc>) -> OfflineAccess {
        OfflineAccess { server: "https://school.example/api".into(), tenant_id: "11111111-1111-1111-1111-111111111111".into(), actor_id: "22222222-2222-2222-2222-222222222222".into(), tenant_code: "school".into(), username: "teacher".into(), display_name: "教师".into(), authorized_at: now.to_rfc3339(), expires_at: (now + Duration::hours(1)).to_rfc3339() }
    }
    #[test]
    fn grant_rejects_expiry_clock_rollback_and_unbounded_renewal() {
        let now = Utc::now(); let mut value = access(now);
        assert!(value.validate_at(now).is_ok());
        assert!(value.validate_at(now + Duration::hours(2)).is_err());
        assert!(value.validate_at(now - Duration::minutes(2)).is_err());
        value.expires_at = (now + Duration::days(8)).to_rfc3339();
        assert!(value.validate_at(now).is_err());
        value = access(now); value.actor_id = "other".into(); assert!(value.validate_at(now).is_err());
        value = access(now); value.server = "http://school.example".into(); assert!(value.validate_at(now).is_err());
    }
    #[test]
    fn grant_rejects_other_account_tenant_and_server() {
        let value = access(Utc::now());
        assert!(value.validate_identity(&value.server, &value.tenant_id, &value.actor_id).is_ok());
        assert!(value.validate_identity("https://other.example/api", &value.tenant_id, &value.actor_id).is_err());
        assert!(value.validate_identity(&value.server, "33333333-3333-3333-3333-333333333333", &value.actor_id).is_err());
        assert!(value.validate_identity(&value.server, &value.tenant_id, "33333333-3333-3333-3333-333333333333").is_err());
    }
    #[cfg(windows)]
    #[test]
    fn windows_credentials_recover_valid_identity_after_memory_is_dropped() {
        // 使用独立临时凭据条目，验证真实 Windows 读回；不接触教师已有授权或密码。
        struct Fixture(keyring::Entry);
        impl Drop for Fixture { fn drop(&mut self) { let _ = self.0.delete_credential(); } }
        let fixture = Fixture(keyring::Entry::new("com.edugrade.enterprise.desktop.tests", &uuid::Uuid::new_v4().to_string()).expect("test entry"));
        let value = access(Utc::now()); let actor = value.actor_id.clone();
        write_access_at(&fixture.0, &value).expect("write isolated Windows credential");
        drop(value);
        let recovered = read_access_at(&fixture.0).expect("read isolated credential").expect("authorized identity");
        assert_eq!(recovered.actor_id, actor);
        assert!(recovered.validate_at(Utc::now()).is_ok());
        assert!(!serde_json::to_string(&recovered).expect("identity JSON").contains("access_token"));
    }
}

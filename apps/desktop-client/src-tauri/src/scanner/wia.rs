//! WIA 只发现系统驱动清单；设备可见不代表已经验证直接采集能力。

use super::ScannerDevice;
#[cfg(windows)]
use serde::Deserialize;
use sha2::{Digest, Sha256};
#[cfg(windows)]
use std::process::Command;

pub(super) fn inventory_devices() -> Result<Vec<ScannerDevice>, String> {
    #[cfg(windows)]
    {
        // 此脚本是静态内容，不插入设备或配置数据。WIA
        // 属于 Windows 系统组件，因此这个边界无需厂商 SDK。
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
        // PowerShell 在单台设备时可能输出对象，多台时输出数组；两种形式都要接受。
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

//! 预检纯策略只消费冻结的设备、Profile、存储状态和容量；不读取设备或账号目录。

use super::{
    ScannerDevice, ScannerPreflightCheck, ScannerPreflightRequest, ScannerPreflightResult,
    ScannerProfile, MAX_PRESET_BYTES,
};

const DEFAULT_REQUIRED_FREE_BYTES: u64 = 2 * 1024 * 1024 * 1024;

pub(super) fn evaluate_preflight(
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
    checks.push(duplex_preflight_check(
        profile.duplex,
        request.expected_duplex,
    ));
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
    // warning 不阻止本机扫描；开始扫描只取决于 blocking 检查，离线提示仍允许本地扫描。
    let ready_to_scan = !checks.iter().any(|check| check.blocking);
    ScannerPreflightResult {
        ready_to_scan,
        profile,
        checks,
        acquisition_status: "file_intake_ready; direct_wia_acquisition_requires_device_validation"
            .to_string(),
    }
}

fn duplex_preflight_check(actual_duplex: bool, expected_duplex: bool) -> ScannerPreflightCheck {
    if actual_duplex == expected_duplex {
        pass(
            "duplex",
            "单双面",
            if actual_duplex {
                "Profile 使用双面扫描。"
            } else {
                "Profile 使用单面扫描。"
            },
        )
    } else {
        fail(
            "duplex",
            "单双面",
            "Profile 的单双面设置与本批答题卡不一致。",
        )
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

pub(super) fn validate_preflight_request(request: &ScannerPreflightRequest) -> Result<(), String> {
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

pub(super) fn required(name: &str, value: &str, limit: usize) -> Result<(), String> {
    if value.trim().is_empty() || value.len() > limit {
        return Err(format!("scanner {name} is invalid"));
    }
    Ok(())
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

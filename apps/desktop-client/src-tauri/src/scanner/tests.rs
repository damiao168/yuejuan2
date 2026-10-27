//! 保留拆分前的预检与作用域边界回归。

use super::profile_store::validate_profile_input;
use super::*;
use uuid::Uuid;

fn profile() -> ScannerProfile {
    ScannerProfile {
        id: "profile-1".into(),
        name: "A4 双面答题卡".into(),
        device_fingerprint: "wia:test-device".into(),
        dpi: 300,
        duplex: true,
        color_mode: "grayscale".into(),
        paper_size: "A4".into(),
        auto_rotate: true,
        compression: "jpeg".into(),
        template_preset: "math-a4-v1".into(),
        created_at: "2026-08-11T00:00:00Z".into(),
        updated_at: "2026-08-11T00:00:00Z".into(),
    }
}

fn request() -> ScannerPreflightRequest {
    ScannerPreflightRequest {
        profile_id: "profile-1".into(),
        expected_paper_size: "A4".into(),
        expected_template_preset: "math-a4-v1".into(),
        expected_duplex: true,
        expected_dpi: Some(300),
        network_available: false,
        required_free_bytes: Some(1024),
    }
}

#[test]
fn disk_preflight_does_not_guess_a_volume_without_ready_spool() {
    assert_eq!(available_disk_bytes(&Err("store unavailable".into())), None);
    for ready in [false, true] {
        assert_eq!(
            available_disk_bytes(&Ok(crate::durable_store::DurableStoreStatus {
                ready,
                database_path: String::new(),
                spool_path: String::new(),
                legacy_data_present: false,
            })),
            None
        );
    }
}

#[cfg(windows)]
#[test]
fn disk_preflight_reads_only_the_selected_spool_directory() {
    let root = std::env::temp_dir().join(format!("edugrade-scanner-disk-{}", Uuid::new_v4()));
    std::fs::create_dir(&root).unwrap();
    let status_for = |path: std::path::PathBuf| crate::durable_store::DurableStoreStatus {
        ready: true,
        database_path: String::new(),
        spool_path: path.to_string_lossy().into_owned(),
        legacy_data_present: false,
    };
    assert!(available_disk_bytes(&Ok(status_for(root.clone()))).is_some());
    // 目标目录不可用时，即使机器其他卷仍有空间，也不能把它们的容量当成 spool 容量。
    let free = available_disk_bytes(&Ok(status_for(root.join("missing-spool"))));
    assert_eq!(free, None);
    let result = evaluate_preflight(profile(), Ok(connected_devices()), true, free, &request());
    let disk = result
        .checks
        .iter()
        .find(|check| check.key == "disk_space")
        .unwrap();
    assert_eq!(disk.status, "warning");
    std::fs::remove_dir(&root).unwrap();
}

#[test]
fn preflight_blocks_a_missing_device_before_batch_scan() {
    let result = evaluate_preflight(profile(), Ok(Vec::new()), true, Some(4096), &request());
    assert!(!result.ready_to_scan);
    assert!(result
        .checks
        .iter()
        .any(|check| check.key == "device_connected" && check.blocking));
    assert!(result
        .checks
        .iter()
        .any(|check| check.key == "network" && !check.blocking));
}

#[test]
fn preflight_blocks_mismatched_template_settings() {
    let mut mismatched = request();
    mismatched.expected_dpi = Some(200);
    mismatched.expected_duplex = false;
    mismatched.expected_paper_size = "A3".into();
    mismatched.expected_template_preset = "math-a3-v1".into();
    let devices = vec![ScannerDevice {
        fingerprint: "wia:test-device".into(),
        display_name: "fixture".into(),
        driver_status: "ready".into(),
        bridge: "wia_inventory".into(),
    }];
    let result = evaluate_preflight(profile(), Ok(devices), true, Some(4096), &mismatched);
    assert!(!result.ready_to_scan);
    assert_eq!(
        result.checks.iter().filter(|check| check.blocking).count(),
        4
    );
}

fn connected_devices() -> Vec<ScannerDevice> {
    vec![ScannerDevice {
        fingerprint: "wia:test-device".into(),
        display_name: "fixture".into(),
        driver_status: "ready".into(),
        bridge: "wia_inventory".into(),
    }]
}

#[test]
fn preflight_preserves_check_order_and_nonblocking_warnings() {
    let mut input = request();
    input.expected_dpi = None;
    // 无磁盘探测值、未校准 DPI 与离线网络都只提示；设备和模板匹配时仍允许本机扫描。
    let result = evaluate_preflight(profile(), Ok(connected_devices()), true, None, &input);
    assert!(result.ready_to_scan);
    assert_eq!(
        result
            .checks
            .iter()
            .map(|check| check.key.as_str())
            .collect::<Vec<_>>(),
        vec![
            "device_connected",
            "paper_size",
            "template_preset",
            "duplex",
            "dpi",
            "secure_storage",
            "disk_space",
            "network",
            "sample_quality",
        ]
    );
    assert_eq!(
        result
            .checks
            .iter()
            .filter(|check| check.status == "warning")
            .map(|check| check.key.as_str())
            .collect::<Vec<_>>(),
        vec!["dpi", "disk_space", "network", "sample_quality"]
    );
    assert!(result.checks.iter().all(|check| !check.blocking));
}

#[test]
fn preflight_duplex_check_preserves_actual_mode_and_template_gate() {
    for actual_duplex in [false, true] {
        for expected_duplex in [false, true] {
            let mut scanner_profile = profile();
            scanner_profile.duplex = actual_duplex;
            let mut input = request();
            input.expected_duplex = expected_duplex;
            let result = evaluate_preflight(
                scanner_profile,
                Ok(connected_devices()),
                true,
                Some(4096),
                &input,
            );
            let duplex = result
                .checks
                .iter()
                .find(|check| check.key == "duplex")
                .unwrap();
            assert_eq!(result.ready_to_scan, actual_duplex == expected_duplex);
            assert_eq!(duplex.blocking, actual_duplex != expected_duplex);
            if actual_duplex == expected_duplex {
                assert_eq!(duplex.status, "passed");
                assert_eq!(
                    duplex.detail,
                    if actual_duplex {
                        "Profile 使用双面扫描。"
                    } else {
                        "Profile 使用单面扫描。"
                    }
                );
            } else {
                assert_eq!(duplex.status, "failed");
                assert_eq!(duplex.detail, "Profile 的单双面设置与本批答题卡不一致。");
            }
        }
    }
}

#[test]
fn profile_validation_keeps_dpi_as_a_calibrated_range_not_a_fixed_national_rule() {
    let mut input = SaveScannerProfileInput {
        id: None,
        name: "fixture".into(),
        device_fingerprint: "wia:test".into(),
        dpi: 300,
        duplex: false,
        color_mode: "grayscale".into(),
        paper_size: "A4".into(),
        auto_rotate: true,
        compression: "jpeg".into(),
        template_preset: "fixture".into(),
    };
    assert!(validate_profile_input(&input).is_ok());
    input.dpi = 100;
    assert!(validate_profile_input(&input).is_err());
}

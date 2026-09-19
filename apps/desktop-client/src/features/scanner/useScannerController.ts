import { useEffect, useState } from "react";
import { getUserErrorMessage } from "../../api/userError";
import { isTauriRuntime } from "../../lib/localRuntime";
import {
  listScannerDevices,
  listScannerProfiles,
  runScannerPreflight,
  saveScannerProfile,
  scannerIntegrationStatus,
  type ScannerDevice,
  type ScannerIntegrationStatus,
  type ScannerPreflightResult,
  type ScannerProfile
} from "../../lib/scannerProfile";
import type { LocalLogEntry, WorkspaceKey } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useScannerController({ workspace, isOnline, logEvent }: {
  workspace: WorkspaceKey;
  isOnline: boolean;
  logEvent: LogEvent;
}) {
  const [scannerProfiles, setScannerProfiles] = useState<ScannerProfile[]>([]);
  const [selectedScannerProfileId, setSelectedScannerProfileId] = useState("");
  const [scannerPreflight, setScannerPreflight] = useState<ScannerPreflightResult | null>(null);
  const [scannerIntegration, setScannerIntegration] = useState<ScannerIntegrationStatus | null>(null);
  const [scannerPreflightError, setScannerPreflightError] = useState<string | null>(null);
  const [isCheckingScannerPreflight, setIsCheckingScannerPreflight] = useState(false);
  const [expectedPaperSize, setExpectedPaperSize] = useState<ScannerProfile["paperSize"]>("A4");
  const [expectedTemplatePreset, setExpectedTemplatePreset] = useState("");
  const [expectedDuplex, setExpectedDuplex] = useState(false);
  const [expectedDpi, setExpectedDpi] = useState(300);
  const [isScannerProfileModalOpen, setIsScannerProfileModalOpen] = useState(false);
  const [scannerDevices, setScannerDevices] = useState<ScannerDevice[]>([]);
  const [isLoadingScannerDevices, setIsLoadingScannerDevices] = useState(false);
  const [isSavingScannerProfile, setIsSavingScannerProfile] = useState(false);
  const [scannerProfileName, setScannerProfileName] = useState("");
  const [scannerDeviceFingerprint, setScannerDeviceFingerprint] = useState("");

  const handleLoadScannerProfiles = async () => {
    if (!isTauriRuntime()) return;
    try {
      const [profiles, integration] = await Promise.all([listScannerProfiles(), scannerIntegrationStatus()]);
      setScannerProfiles(profiles);
      setScannerIntegration(integration);
      setSelectedScannerProfileId((current) => current || profiles[0]?.id || "");
    } catch (error) {
      setScannerPreflightError(getUserErrorMessage(error, "扫描设备配置无法读取"));
    }
  };

  useEffect(() => {
    if (workspace === "scan" && isTauriRuntime()) void handleLoadScannerProfiles();
  }, [workspace]);

  const handleOpenScannerProfileSetup = async () => {
    if (!isTauriRuntime()) return;
    setScannerPreflightError(null);
    setScannerProfileName((current) => current || `扫描站 ${expectedPaperSize} ${expectedDpi} DPI`);
    setIsLoadingScannerDevices(true);
    try {
      const devices = await listScannerDevices();
      setScannerDevices(devices);
      setScannerDeviceFingerprint((current) => current || devices[0]?.fingerprint || "");
      setIsScannerProfileModalOpen(true);
    } catch (error) {
      const message = getUserErrorMessage(error, "无法读取 Windows 扫描设备。");
      setScannerPreflightError(message);
      await logEvent("warning", "scanner device inventory failed", message);
    } finally {
      setIsLoadingScannerDevices(false);
    }
  };

  const handleSaveScannerProfile = async () => {
    const name = scannerProfileName.trim();
    const templatePreset = expectedTemplatePreset.trim();
    if (!name || !scannerDeviceFingerprint || !templatePreset) {
      setScannerPreflightError("请填写档案名称，选择可用扫描设备，并填写锁定答题卡模板标识。");
      return;
    }
    setIsSavingScannerProfile(true);
    setScannerPreflightError(null);
    try {
      const profile = await saveScannerProfile({
        name,
        deviceFingerprint: scannerDeviceFingerprint,
        dpi: expectedDpi,
        duplex: expectedDuplex,
        colorMode: "grayscale",
        paperSize: expectedPaperSize,
        autoRotate: true,
        compression: "jpeg",
        templatePreset
      });
      setScannerProfiles((current) => [profile, ...current.filter((item) => item.id !== profile.id)]);
      setSelectedScannerProfileId(profile.id);
      setScannerPreflight(null);
      setIsScannerProfileModalOpen(false);
      await logEvent("info", "scanner profile saved", profile.name);
    } catch (error) {
      const message = getUserErrorMessage(error, "扫描设备档案保存失败。");
      setScannerPreflightError(message);
      await logEvent("warning", "scanner profile save failed", message);
    } finally {
      setIsSavingScannerProfile(false);
    }
  };

  const handleScannerPreflight = async () => {
    if (!isTauriRuntime()) return;
    if (!selectedScannerProfileId || !expectedTemplatePreset.trim()) {
      setScannerPreflightError("请选择扫描设备 Profile，并填写锁定答题卡模板的 Profile 标识。");
      return;
    }
    setScannerPreflightError(null);
    setIsCheckingScannerPreflight(true);
    try {
      const result = await runScannerPreflight({
        profileId: selectedScannerProfileId,
        expectedPaperSize,
        expectedTemplatePreset: expectedTemplatePreset.trim(),
        expectedDuplex,
        expectedDpi,
        networkAvailable: isOnline
      });
      setScannerPreflight(result);
      await logEvent("info", "scanner preflight completed", result.readyToScan ? "ready" : "blocked");
    } catch (error) {
      const message = getUserErrorMessage(error, "扫描前检查失败");
      setScannerPreflightError(message);
      await logEvent("warning", "scanner preflight failed", message);
    } finally {
      setIsCheckingScannerPreflight(false);
    }
  };

  return {
    scannerProfiles, selectedScannerProfileId, setSelectedScannerProfileId,
    scannerPreflight, setScannerPreflight, scannerIntegration, scannerPreflightError,
    isCheckingScannerPreflight, expectedPaperSize, setExpectedPaperSize,
    expectedTemplatePreset, setExpectedTemplatePreset, expectedDuplex,
    setExpectedDuplex, expectedDpi, setExpectedDpi, isScannerProfileModalOpen,
    setIsScannerProfileModalOpen, scannerDevices, isLoadingScannerDevices,
    isSavingScannerProfile, scannerProfileName, setScannerProfileName,
    scannerDeviceFingerprint, setScannerDeviceFingerprint,
    handleLoadScannerProfiles, handleOpenScannerProfileSetup,
    handleSaveScannerProfile, handleScannerPreflight
  };
}

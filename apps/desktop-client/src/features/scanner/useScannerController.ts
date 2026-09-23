import { useEffect, useReducer, useRef, useState } from "react";
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
import { initialScannerDraft, scannerDraftReducer } from "./scannerDraft";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useScannerController({ workspace, isOnline, durableScopeKey, logEvent }: {
  workspace: WorkspaceKey;
  isOnline: boolean;
  durableScopeKey: string;
  logEvent: LogEvent;
}) {
  const scopeRef = useRef(durableScopeKey);
  scopeRef.current = durableScopeKey;
  const [scannerProfiles, setScannerProfiles] = useState<ScannerProfile[]>([]);
  const [selectedScannerProfileId, setSelectedScannerProfileId] = useState("");
  const [scannerPreflight, setScannerPreflight] = useState<ScannerPreflightResult | null>(null);
  const [scannerIntegration, setScannerIntegration] = useState<ScannerIntegrationStatus | null>(null);
  const [scannerPreflightError, setScannerPreflightError] = useState<string | null>(null);
  const [isCheckingScannerPreflight, setIsCheckingScannerPreflight] = useState(false);
  const [draft, dispatchDraft] = useReducer(scannerDraftReducer, initialScannerDraft);
  const { expectedPaperSize, expectedTemplatePreset, expectedDuplex, expectedDpi,
    scannerProfileName, scannerDeviceFingerprint } = draft;
  const setExpectedPaperSize = (value: ScannerProfile["paperSize"]) => dispatchDraft({ type: "patch", patch: { expectedPaperSize: value } });
  const setExpectedTemplatePreset = (value: string) => dispatchDraft({ type: "patch", patch: { expectedTemplatePreset: value } });
  const setExpectedDuplex = (value: boolean) => dispatchDraft({ type: "patch", patch: { expectedDuplex: value } });
  const setExpectedDpi = (value: number) => dispatchDraft({ type: "patch", patch: { expectedDpi: value } });
  const setScannerProfileName = (value: string) => dispatchDraft({ type: "patch", patch: { scannerProfileName: value } });
  const setScannerDeviceFingerprint = (value: string) => dispatchDraft({ type: "patch", patch: { scannerDeviceFingerprint: value } });
  const [isScannerProfileModalOpen, setIsScannerProfileModalOpen] = useState(false);
  const [scannerDevices, setScannerDevices] = useState<ScannerDevice[]>([]);
  const [isLoadingScannerDevices, setIsLoadingScannerDevices] = useState(false);
  const [isSavingScannerProfile, setIsSavingScannerProfile] = useState(false);

  const handleLoadScannerProfiles = async () => {
    if (!isTauriRuntime() || !durableScopeKey) return;
    try {
      const [profiles, integration] = await Promise.all([listScannerProfiles(durableScopeKey), scannerIntegrationStatus()]);
      if (scopeRef.current !== durableScopeKey) return;
      setScannerProfiles(profiles);
      setScannerIntegration(integration);
      setSelectedScannerProfileId((current) => current || profiles[0]?.id || "");
    } catch (error) {
      if (scopeRef.current === durableScopeKey) setScannerPreflightError(getUserErrorMessage(error, "扫描设备配置无法读取"));
    }
  };

  useEffect(() => {
    setScannerProfiles([]);
    setSelectedScannerProfileId("");
    setScannerPreflight(null);
    if (workspace === "scan" && isTauriRuntime() && durableScopeKey) void handleLoadScannerProfiles();
  }, [workspace, durableScopeKey]);

  const handleOpenScannerProfileSetup = async () => {
    if (!isTauriRuntime() || !durableScopeKey) return;
    setScannerPreflightError(null);
    dispatchDraft({ type: "nameIfEmpty", defaultName: `扫描站 ${expectedPaperSize} ${expectedDpi} DPI` });
    setIsLoadingScannerDevices(true);
    try {
      const devices = await listScannerDevices();
      if (scopeRef.current !== durableScopeKey) return;
      setScannerDevices(devices);
      dispatchDraft({ type: "deviceIfEmpty", fingerprint: devices[0]?.fingerprint || "" });
      setIsScannerProfileModalOpen(true);
    } catch (error) {
      if (scopeRef.current !== durableScopeKey) return;
      const message = getUserErrorMessage(error, "无法读取 Windows 扫描设备。");
      setScannerPreflightError(message);
      await logEvent("warning", "scanner device inventory failed", message);
    } finally {
      if (scopeRef.current === durableScopeKey) setIsLoadingScannerDevices(false);
    }
  };

  const handleSaveScannerProfile = async () => {
    if (!durableScopeKey) return;
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
      }, durableScopeKey);
      if (scopeRef.current !== durableScopeKey) return;
      setScannerProfiles((current) => [profile, ...current.filter((item) => item.id !== profile.id)]);
      setSelectedScannerProfileId(profile.id);
      setScannerPreflight(null);
      setIsScannerProfileModalOpen(false);
      await logEvent("info", "scanner profile saved", profile.name);
    } catch (error) {
      if (scopeRef.current !== durableScopeKey) return;
      const message = getUserErrorMessage(error, "扫描设备档案保存失败。");
      setScannerPreflightError(message);
      await logEvent("warning", "scanner profile save failed", message);
    } finally {
      if (scopeRef.current === durableScopeKey) setIsSavingScannerProfile(false);
    }
  };

  const handleScannerPreflight = async () => {
    if (!isTauriRuntime() || !durableScopeKey) return;
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
      }, durableScopeKey);
      if (scopeRef.current !== durableScopeKey) return;
      setScannerPreflight(result);
      await logEvent("info", "scanner preflight completed", result.readyToScan ? "ready" : "blocked");
    } catch (error) {
      if (scopeRef.current !== durableScopeKey) return;
      const message = getUserErrorMessage(error, "扫描前检查失败");
      setScannerPreflightError(message);
      await logEvent("warning", "scanner preflight failed", message);
    } finally {
      if (scopeRef.current === durableScopeKey) setIsCheckingScannerPreflight(false);
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

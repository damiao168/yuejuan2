// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useScannerController } from "./useScannerController";

const api = vi.hoisted(() => ({
  listScannerDevices: vi.fn(), listScannerProfiles: vi.fn(),
  runScannerPreflight: vi.fn(), saveScannerProfile: vi.fn(),
  scannerIntegrationStatus: vi.fn()
}));
vi.mock("../../lib/localRuntime", () => ({ isTauriRuntime: () => true }));
vi.mock("../../lib/scannerProfile", () => ({
  listScannerDevices: api.listScannerDevices,
  listScannerProfiles: api.listScannerProfiles,
  runScannerPreflight: api.runScannerPreflight,
  saveScannerProfile: api.saveScannerProfile,
  scannerIntegrationStatus: api.scannerIntegrationStatus
}));

describe("scanner controller", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useScannerController>;
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    api.listScannerProfiles.mockResolvedValue([{ id: "profile-1", name: "existing" }]);
    api.scannerIntegrationStatus.mockResolvedValue({ available: true });
    api.listScannerDevices.mockResolvedValue([]);
    api.runScannerPreflight.mockResolvedValue({ readyToScan: true });
    api.saveScannerProfile.mockResolvedValue({ id: "profile-2", name: "saved" });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });
  async function mount(initialScope = "lease-A") {
    let rerender!: (scope: string) => Promise<void>;
    function Probe({ durableScopeKey }: { durableScopeKey: string }) {
      current = useScannerController({
        workspace: "scan", isOnline: true, durableScopeKey, logEvent: vi.fn().mockResolvedValue(undefined)
      });
      return null;
    }
    rerender = async (scope) => { await act(async () => { root.render(<Probe durableScopeKey={scope} />); }); };
    await rerender(initialScope);
    return rerender;
  }

  it("loads profiles but blocks save when no device is available", async () => {
    await mount();
    expect(current.selectedScannerProfileId).toBe("profile-1");
    await act(async () => { await current.handleOpenScannerProfileSetup(); });
    expect(current.scannerDevices).toEqual([]);
    expect(current.isScannerProfileModalOpen).toBe(true);
    await act(async () => { await current.handleSaveScannerProfile(); });
    expect(api.saveScannerProfile).not.toHaveBeenCalled();
    expect(current.scannerPreflightError).toBeTruthy();
  });

  it("saves a profile and handles preflight pass and failure", async () => {
    api.listScannerDevices.mockResolvedValueOnce([{ fingerprint: "device-1" }]);
    await mount();
    await act(async () => { await current.handleOpenScannerProfileSetup(); });
    act(() => { current.setExpectedTemplatePreset("locked-template"); });
    await act(async () => { await current.handleSaveScannerProfile(); });
    expect(api.saveScannerProfile).toHaveBeenCalledWith(expect.objectContaining({
      deviceFingerprint: "device-1", templatePreset: "locked-template"
    }), "lease-A");
    expect(current.selectedScannerProfileId).toBe("profile-2");
    await act(async () => { await current.handleScannerPreflight(); });
    expect(current.scannerPreflight?.readyToScan).toBe(true);
    api.runScannerPreflight.mockRejectedValueOnce(new Error("device disconnected"));
    await act(async () => { await current.handleScannerPreflight(); });
    expect(current.scannerPreflightError).toBeTruthy();
  });

  it("clears A profiles and ignores A's late response after switching to B", async () => {
    let finishA!: (profiles: Array<{ id: string; name: string }>) => void;
    api.listScannerProfiles.mockImplementationOnce(() => new Promise((resolve) => { finishA = resolve; }));
    api.listScannerProfiles.mockResolvedValueOnce([]);
    const switchScope = await mount();
    await switchScope("lease-B");
    await act(async () => { finishA([{ id: "A-secret-profile", name: "A" }]); });
    expect(current.scannerProfiles).toEqual([]);
    expect(current.selectedScannerProfileId).toBe("");
    expect(api.listScannerProfiles).toHaveBeenCalledWith("lease-B");
  });
});

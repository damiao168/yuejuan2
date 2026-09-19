import { describe, expect, it } from "vitest";
import { initialScannerDraft, scannerDraftReducer } from "./scannerDraft";

describe("scanner profile draft", () => {
  it("groups related profile settings in one transition", () => {
    const draft = scannerDraftReducer(initialScannerDraft, {
      type: "patch", patch: {
        expectedPaperSize: "A3", expectedTemplatePreset: "locked-profile",
        expectedDuplex: true, expectedDpi: 600
      }
    });
    expect(draft).toMatchObject({
      expectedPaperSize: "A3", expectedTemplatePreset: "locked-profile",
      expectedDuplex: true, expectedDpi: 600
    });
  });

  it("defaults name and device only when the user has not selected one", () => {
    const named = scannerDraftReducer(initialScannerDraft, {
      type: "nameIfEmpty", defaultName: "扫描站 A4 300 DPI"
    });
    const selected = scannerDraftReducer(named, {
      type: "patch", patch: { scannerProfileName: "教务处", scannerDeviceFingerprint: "device-1" }
    });
    expect(scannerDraftReducer(selected, {
      type: "nameIfEmpty", defaultName: "other"
    }).scannerProfileName).toBe("教务处");
    expect(scannerDraftReducer(selected, {
      type: "deviceIfEmpty", fingerprint: "device-2"
    }).scannerDeviceFingerprint).toBe("device-1");
  });
});

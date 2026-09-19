import type { ScannerProfile } from "../../lib/scannerProfile";

export interface ScannerDraft {
  scannerProfileName: string;
  scannerDeviceFingerprint: string;
  expectedPaperSize: ScannerProfile["paperSize"];
  expectedTemplatePreset: string;
  expectedDuplex: boolean;
  expectedDpi: number;
}

export const initialScannerDraft: ScannerDraft = {
  scannerProfileName: "",
  scannerDeviceFingerprint: "",
  expectedPaperSize: "A4",
  expectedTemplatePreset: "",
  expectedDuplex: false,
  expectedDpi: 300
};

export type ScannerDraftAction =
  | { type: "patch"; patch: Partial<ScannerDraft> }
  | { type: "nameIfEmpty"; defaultName: string }
  | { type: "deviceIfEmpty"; fingerprint: string };

export function scannerDraftReducer(draft: ScannerDraft, action: ScannerDraftAction): ScannerDraft {
  switch (action.type) {
    case "patch": return { ...draft, ...action.patch };
    case "nameIfEmpty": return draft.scannerProfileName
      ? draft : { ...draft, scannerProfileName: action.defaultName };
    case "deviceIfEmpty": return draft.scannerDeviceFingerprint
      ? draft : { ...draft, scannerDeviceFingerprint: action.fingerprint };
  }
}

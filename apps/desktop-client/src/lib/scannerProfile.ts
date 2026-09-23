import { invoke } from "@tauri-apps/api/core";
import { isTauriRuntime } from "./localRuntime";
import { invokeScoped } from "./durableStore";

/**
 * Product-facing scanner contract.  It deliberately describes driver/profile
 * facts, rather than binding the React app to WIA, TWAIN or a vendor SDK.
 */
export interface ScannerProfile {
  id: string;
  name: string;
  deviceFingerprint: string;
  dpi: number;
  duplex: boolean;
  colorMode: "color" | "grayscale" | "black_white";
  paperSize: "A3" | "A4" | "A5" | "Letter" | "Legal";
  autoRotate: boolean;
  compression: "jpeg" | "png" | "tiff" | "pdf";
  templatePreset: string;
  createdAt: string;
  updatedAt: string;
}

export interface ScannerDevice {
  fingerprint: string;
  displayName: string;
  driverStatus: string;
  bridge: string;
}

export interface SaveScannerProfileInput {
  id?: string;
  name: string;
  deviceFingerprint: string;
  dpi: number;
  duplex: boolean;
  colorMode: ScannerProfile["colorMode"];
  paperSize: ScannerProfile["paperSize"];
  autoRotate: boolean;
  compression: ScannerProfile["compression"];
  templatePreset: string;
}

export interface ScannerPreflightRequest {
  profileId: string;
  expectedPaperSize: ScannerProfile["paperSize"];
  expectedTemplatePreset: string;
  expectedDuplex: boolean;
  expectedDpi?: number;
  networkAvailable: boolean;
  requiredFreeBytes?: number;
}

export interface ScannerPreflightCheck {
  key: string;
  label: string;
  status: "passed" | "failed" | "warning";
  blocking: boolean;
  detail: string;
}

export interface ScannerPreflightResult {
  readyToScan: boolean;
  profile: ScannerProfile;
  checks: ScannerPreflightCheck[];
  acquisitionStatus: string;
}

export interface ScannerIntegrationStatus {
  bridge: string;
  directAcquisition: "requires_device_validation";
  detail: string;
}

export async function listScannerDevices(): Promise<ScannerDevice[]> {
  requireDesktopRuntime();
  return invoke<ScannerDevice[]>("list_scanner_devices");
}

export async function scannerIntegrationStatus(): Promise<ScannerIntegrationStatus> {
  requireDesktopRuntime();
  return invoke<ScannerIntegrationStatus>("scanner_integration_status");
}

export async function listScannerProfiles(sessionId: string): Promise<ScannerProfile[]> {
  requireDesktopRuntime();
  return invokeScoped<ScannerProfile[]>("list_scanner_profiles", {}, sessionId);
}

export async function saveScannerProfile(input: SaveScannerProfileInput, sessionId: string): Promise<ScannerProfile> {
  requireDesktopRuntime();
  return invokeScoped<ScannerProfile>("save_scanner_profile", { input }, sessionId);
}

export async function deleteScannerProfile(profileId: string, sessionId: string): Promise<void> {
  requireDesktopRuntime();
  await invokeScoped("delete_scanner_profile", { profileId }, sessionId);
}

export async function runScannerPreflight(input: ScannerPreflightRequest, sessionId: string): Promise<ScannerPreflightResult> {
  requireDesktopRuntime();
  return invokeScoped<ScannerPreflightResult>("run_scanner_preflight", { request: input }, sessionId);
}

export type ScannerSampleQualityStatus = "passed" | "warning" | "failed" | "not_configured";

export interface ScannerSampleQualityCheck {
  key: "dpi" | "blur" | "exposure" | "edge_crop" | "skew";
  label: string;
  status: ScannerSampleQualityStatus;
  detail: string;
}

/**
 * Fast, local sample screening before a batch enters the spool.  The checks
 * only screen for obvious capture faults and intentionally remain separate
 * from the server-side image-quality gate.  Their thresholds must be adjusted
 * from answer-sheet/OCR experiments, not treated as a national fixed rule.
 */
export async function inspectScannerSample(file: File, profile: ScannerProfile): Promise<ScannerSampleQualityCheck[]> {
  if (!file.type.startsWith("image/")) {
    return unavailableSampleChecks("PDF 样张未在本地解码；请以服务端质量门禁为准。");
  }

  // Chromium does not decode every scanner image format (notably TIFF) on
  // every Windows build. A successful scanner preflight plus a supported
  // server upload must not be turned into a false local failure just because
  // this optional, advisory sample decoder is unavailable.
  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(file);
  } catch {
    return unavailableSampleChecks("该图片格式无法由本机样张预检解码；已保留原件，上传后由服务端质量门禁检查。");
  }
  try {
    const paper = paperDimensionsMm(profile.paperSize);
    const dpiX = (bitmap.width * 25.4) / paper.width;
    const dpiY = (bitmap.height * 25.4) / paper.height;
    const measuredDpi = Math.min(dpiX, dpiY);
    const expectedDpi = profile.dpi;
    const dpiDelta = Math.abs(measuredDpi - expectedDpi) / expectedDpi;
    const sample = await samplePixels(bitmap);
    const checks: ScannerSampleQualityCheck[] = [
      {
        key: "dpi",
        label: "分辨率",
        status: dpiDelta <= 0.15 ? "passed" : "failed",
        detail:
          dpiDelta <= 0.15
            ? `估算 ${Math.round(measuredDpi)} DPI，接近 Profile 的 ${expectedDpi} DPI。`
            : `估算 ${Math.round(measuredDpi)} DPI，与 Profile 的 ${expectedDpi} DPI 偏差超过 15%；请检查扫描仪设置。`
      },
      exposureCheck(sample),
      blurCheck(sample),
      edgeCropCheck(sample),
      skewCheck(sample)
    ];
    return checks;
  } finally {
    bitmap.close();
  }
}

function unavailableSampleChecks(detail: string): ScannerSampleQualityCheck[] {
  return [
    { key: "dpi", label: "分辨率", status: "not_configured", detail },
    { key: "blur", label: "模糊", status: "not_configured", detail },
    { key: "exposure", label: "曝光", status: "not_configured", detail },
    { key: "edge_crop", label: "裁边", status: "not_configured", detail },
    { key: "skew", label: "倾斜", status: "not_configured", detail }
  ];
}

interface SamplePixels {
  width: number;
  height: number;
  gray: Uint8Array;
  average: number;
  blackClip: number;
  whiteClip: number;
  laplacianVariance: number;
  edgeInk: number;
  principalAngle: number | null;
}

async function samplePixels(bitmap: ImageBitmap): Promise<SamplePixels> {
  const maxEdge = 512;
  const ratio = Math.min(1, maxEdge / Math.max(bitmap.width, bitmap.height));
  const width = Math.max(1, Math.round(bitmap.width * ratio));
  const height = Math.max(1, Math.round(bitmap.height * ratio));
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext("2d", { willReadFrequently: true });
  if (!context) throw new Error("浏览器无法读取扫描样张像素。");
  context.drawImage(bitmap, 0, 0, width, height);
  const pixels = context.getImageData(0, 0, width, height).data;
  const gray = new Uint8Array(width * height);
  let total = 0;
  let black = 0;
  let white = 0;
  let sumX = 0;
  let sumY = 0;
  let darkCount = 0;
  for (let index = 0; index < gray.length; index += 1) {
    const offset = index * 4;
    const value = Math.round(pixels[offset] * 0.2126 + pixels[offset + 1] * 0.7152 + pixels[offset + 2] * 0.0722);
    gray[index] = value;
    total += value;
    if (value <= 12) black += 1;
    if (value >= 243) white += 1;
    if (value < 110) {
      const x = index % width;
      const y = Math.floor(index / width);
      sumX += x;
      sumY += y;
      darkCount += 1;
    }
  }
  let laplacianSum = 0;
  let laplacianSquared = 0;
  let laplacianCount = 0;
  let edgeDark = 0;
  let edgeCount = 0;
  const border = Math.max(3, Math.round(Math.min(width, height) * 0.03));
  for (let y = 1; y < height - 1; y += 1) {
    for (let x = 1; x < width - 1; x += 1) {
      const index = y * width + x;
      const laplacian = 4 * gray[index] - gray[index - 1] - gray[index + 1] - gray[index - width] - gray[index + width];
      laplacianSum += laplacian;
      laplacianSquared += laplacian * laplacian;
      laplacianCount += 1;
      if (x < border || y < border || x >= width - border || y >= height - border) {
        edgeCount += 1;
        if (gray[index] < 110) edgeDark += 1;
      }
    }
  }
  const laplacianMean = laplacianSum / Math.max(1, laplacianCount);
  const laplacianVariance = laplacianSquared / Math.max(1, laplacianCount) - laplacianMean * laplacianMean;
  let principalAngle: number | null = null;
  if (darkCount >= Math.max(80, (width * height) / 500)) {
    const centerX = sumX / darkCount;
    const centerY = sumY / darkCount;
    let xx = 0;
    let yy = 0;
    let xy = 0;
    for (let index = 0; index < gray.length; index += 1) {
      if (gray[index] >= 110) continue;
      const x = index % width - centerX;
      const y = Math.floor(index / width) - centerY;
      xx += x * x;
      yy += y * y;
      xy += x * y;
    }
    principalAngle = (Math.atan2(2 * xy, xx - yy) * 180) / (2 * Math.PI);
  }
  return {
    width,
    height,
    gray,
    average: total / gray.length,
    blackClip: black / gray.length,
    whiteClip: white / gray.length,
    laplacianVariance,
    edgeInk: edgeDark / Math.max(1, edgeCount),
    principalAngle
  };
}

function exposureCheck(sample: SamplePixels): ScannerSampleQualityCheck {
  const suspicious = sample.average < 45 || sample.average > 235 || sample.blackClip > 0.18 || sample.whiteClip > 0.9;
  return {
    key: "exposure",
    label: "曝光",
    status: suspicious ? "warning" : "passed",
    detail: suspicious
      ? `平均亮度 ${Math.round(sample.average)}，暗部/亮部饱和比例 ${(sample.blackClip * 100).toFixed(1)}% / ${(sample.whiteClip * 100).toFixed(1)}%；请人工查看样张。`
      : `平均亮度 ${Math.round(sample.average)}，未发现明显曝光异常。`
  };
}

function blurCheck(sample: SamplePixels): ScannerSampleQualityCheck {
  const suspicious = sample.laplacianVariance < 80;
  return {
    key: "blur",
    label: "模糊",
    status: suspicious ? "warning" : "passed",
    detail: suspicious
      ? `边缘清晰度指标 ${Math.round(sample.laplacianVariance)} 偏低；请放大确认条码、填涂区和文字笔画。`
      : `边缘清晰度指标 ${Math.round(sample.laplacianVariance)}，未发现明显模糊。`
  };
}

function edgeCropCheck(sample: SamplePixels): ScannerSampleQualityCheck {
  const suspicious = sample.edgeInk > 0.14;
  return {
    key: "edge_crop",
    label: "裁边",
    status: suspicious ? "warning" : "passed",
    detail: suspicious
      ? `页面边缘深色像素比例 ${(sample.edgeInk * 100).toFixed(1)}% 偏高；请确认答题卡边框和定位标记未被裁切。`
      : `页面边缘深色像素比例 ${(sample.edgeInk * 100).toFixed(1)}%，未发现明显裁边风险。`
  };
}

function skewCheck(sample: SamplePixels): ScannerSampleQualityCheck {
  if (sample.principalAngle === null) {
    return { key: "skew", label: "倾斜", status: "not_configured", detail: "样张中的有效笔迹/边框不足，无法估算倾斜；请人工确认定位标记。" };
  }
  const nearestRightAngle = Math.min(...[0, 90, -90].map((target) => Math.abs(sample.principalAngle! - target)));
  const suspicious = nearestRightAngle > 4;
  return {
    key: "skew",
    label: "倾斜",
    status: suspicious ? "warning" : "passed",
    detail: suspicious
      ? `估算主轴偏转 ${sample.principalAngle.toFixed(1)}°；请检查进纸和自动旋转。`
      : `估算主轴偏转 ${sample.principalAngle.toFixed(1)}°，未发现明显倾斜。`
  };
}

function paperDimensionsMm(size: ScannerProfile["paperSize"]) {
  switch (size) {
    case "A3":
      return { width: 297, height: 420 };
    case "A4":
      return { width: 210, height: 297 };
    case "A5":
      return { width: 148, height: 210 };
    case "Letter":
      return { width: 215.9, height: 279.4 };
    case "Legal":
      return { width: 215.9, height: 355.6 };
  }
}

function requireDesktopRuntime() {
  if (!isTauriRuntime()) {
    throw new Error("扫描仪 Profile 仅在 Windows 桌面扫描站可用；浏览器开发模式不会伪装成生产扫描能力。");
  }
}

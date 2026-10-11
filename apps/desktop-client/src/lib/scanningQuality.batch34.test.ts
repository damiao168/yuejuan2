// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { estimateLocalCacheBytes, inspectScanFile } from "./scanFiles";
import { inspectScannerSample, type ScannerProfile } from "./scannerProfile";

const profile: ScannerProfile = {
  id: "profile-1", name: "扫描站", deviceFingerprint: "device-1", dpi: 300,
  duplex: true, colorMode: "grayscale", paperSize: "A4", autoRotate: true,
  compression: "tiff", templatePreset: "locked-template", createdAt: "", updatedAt: ""
};

type SampleShade = (x: number, y: number, width: number, height: number) => number;

function sampleImage(width: number, height: number, shade: SampleShade) {
  const data = new Uint8ClampedArray(width * height * 4);
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const offset = (y * width + x) * 4;
      const value = shade(x, y, width, height);
      data.set([value, value, value, 255], offset);
    }
  }
  return { data };
}

describe("scan file admission and browser cache estimate", () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it.each([
    ["sample.PDF", "passed"], ["sample.PNG", "passed"], ["sample.JPG", "passed"],
    ["sample.jpeg", "passed"], ["sample.TIF", "passed"], ["sample.tiff", "passed"],
    ["sample.exe", "failed"], ["sample", "failed"]
  ])("keeps extension admission for %s", async (name, status) => {
    const checks = await inspectScanFile(new File(["scan"], name));
    expect(checks.map((check) => check.key)).toEqual(["file_type", "file_size", "page_count", "resolution"]);
    expect(checks[0].status).toBe(status);
    expect(checks[1].status).toBe("passed");
  });

  it("keeps TIFF resolution deferred to the scanner sample and server checks", async () => {
    const checks = await inspectScanFile(new File(["scan"], "sample.TIFF", { type: "image/tiff" }));
    expect(checks[3]).toMatchObject({
      status: "not_configured", detail: "TIFF 本机分辨率解码未配置；已交由扫描样张预检和服务端质量门禁处理"
    });
  });

  it("skips null, empty and unrelated keys while counting matching keys with missing values", () => {
    const keys = [null, "", "unrelated", "edugrade.desktop.scan_queue", "edugrade.desktop.empty"];
    const getItem = vi.fn((key: string) => key === "edugrade.desktop.scan_queue" ? "queue-value" : null);
    vi.spyOn(window, "localStorage", "get").mockReturnValue({
      length: keys.length, key: (index: number) => keys[index], getItem
    } as unknown as Storage);
    expect(estimateLocalCacheBytes()).toBe("edugrade.desktop.scan_queue".length + "queue-value".length + "edugrade.desktop.empty".length);
    expect(getItem.mock.calls.map(([key]) => key)).toEqual(["edugrade.desktop.scan_queue", "edugrade.desktop.empty"]);
  });
});

describe("scanner sample public quality checks", () => {
  const close = vi.fn();
  const drawImage = vi.fn();
  const decode = vi.fn();
  const getImageData = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    decode.mockResolvedValue({ width: 2480, height: 3508, close });
    vi.stubGlobal("createImageBitmap", decode);
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
      drawImage, getImageData
    } as unknown as CanvasRenderingContext2D);
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  // 合成画布覆盖统计边界和公开输出，不表示已验证真实扫描设备或图像解码器。
  it.each([
    ["white", () => 255, "warning", "warning", "passed", "not_configured", "0", "0.0"],
    ["black", () => 0, "warning", "warning", "warning", "passed", "0", "100.0"],
    ["gray", () => 128, "passed", "warning", "passed", "not_configured", "0", "0.0"],
    ["checkerboard", (x: number, y: number) => (x + y) % 2 === 0 ? 0 : 255,
      "warning", "passed", "warning", "passed", "1040400", "50.0"],
    ["outer ring", (x: number, y: number, width: number, height: number) => x === 0 || y === 0 || x === width - 1 || y === height - 1 ? 0 : 128,
      "passed", "passed", "passed", "passed", null, "0.0"]
  ] as const)("keeps quality output for %s pixels", async (_name, shade, exposure, blur, edge, skew, clarity, ink) => {
    getImageData.mockImplementation((_x: number, _y: number, width: number, height: number) => sampleImage(width, height, shade));
    const checks = await inspectScannerSample(new File(["scan"], "sample.png", { type: "image/png" }), profile);
    expect(checks.map((check) => [check.key, check.status])).toEqual([
      ["dpi", "passed"], ["exposure", exposure], ["blur", blur], ["edge_crop", edge], ["skew", skew]
    ]);
    expect(checks[0].detail).toBe("估算 300 DPI，接近 Profile 的 300 DPI。");
    if (clarity !== null) expect(checks[2].detail).toContain(`边缘清晰度指标 ${clarity}`);
    expect(checks[3].detail).toContain(`页面边缘深色像素比例 ${ink}%`);
    expect(drawImage).toHaveBeenCalledWith(expect.objectContaining({ width: 2480, height: 3508 }), 0, 0, 362, 512);
    expect(getImageData).toHaveBeenCalledWith(0, 0, 362, 512);
    expect(close).toHaveBeenCalledOnce();
  });

  it("keeps a one-pixel sample finite when no four-neighbor statistic is available", async () => {
    decode.mockResolvedValueOnce({ width: 1, height: 1, close });
    getImageData.mockImplementation((_x: number, _y: number, width: number, height: number) => sampleImage(width, height, () => 128));
    const checks = await inspectScannerSample(new File(["scan"], "sample.png", { type: "image/png" }), profile);
    expect(checks.map((check) => [check.key, check.status])).toEqual([
      ["dpi", "failed"], ["exposure", "passed"], ["blur", "warning"], ["edge_crop", "passed"], ["skew", "not_configured"]
    ]);
    expect(checks[2].detail).toContain("边缘清晰度指标 0");
    expect(checks[3].detail).toContain("页面边缘深色像素比例 0.0%");
    expect(close).toHaveBeenCalledOnce();
  });

  it("keeps PDF samples unavailable without trying to decode them", async () => {
    const checks = await inspectScannerSample(new File(["scan"], "sample.pdf", { type: "application/pdf" }), profile);
    expect(checks).toHaveLength(5);
    expect(checks.every((check) => check.status === "not_configured")).toBe(true);
    expect(checks[0].detail).toBe("PDF 样张未在本地解码；请以服务端质量门禁为准。");
    expect(decode).not.toHaveBeenCalled();
  });

  it("keeps an undecodable TIFF available for later server inspection", async () => {
    decode.mockRejectedValueOnce(new Error("unsupported decoder"));
    const checks = await inspectScannerSample(new File(["scan"], "sample.tiff", { type: "image/tiff" }), profile);
    expect(checks).toHaveLength(5);
    expect(checks.every((check) => check.status === "not_configured")).toBe(true);
    expect(checks[0].detail).toBe("该图片格式无法由本机样张预检解码；已保留原件，上传后由服务端质量门禁检查。");
    expect(drawImage).not.toHaveBeenCalled();
  });

  it("closes a decoded bitmap when canvas inspection fails", async () => {
    vi.mocked(HTMLCanvasElement.prototype.getContext).mockReturnValueOnce(null);
    await expect(inspectScannerSample(new File(["scan"], "sample.png", { type: "image/png" }), profile))
      .rejects.toThrow("浏览器无法读取扫描样张像素。");
    expect(close).toHaveBeenCalledOnce();
  });
});

// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { inspectScannerSample, type ScannerProfile } from "./scannerProfile";

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it.each([
  [2480, 3508, "passed"], [3508, 2480, "passed"],
  [1240, 3508, "failed"], [3508, 1240, "failed"]
])("matches A4 orientation while retaining a low-resolution axis: %s × %s", async (width, height, expected) => {
  const close = vi.fn();
  vi.stubGlobal("createImageBitmap", vi.fn().mockResolvedValue({ width, height, close }));
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
    drawImage() {},
    getImageData(_x: number, _y: number, sampleWidth: number, sampleHeight: number) {
      const data = new Uint8ClampedArray(sampleWidth * sampleHeight * 4);
      for (let index = 0; index < data.length; index += 4) data.set([128, 128, 128, 255], index);
      return { data };
    }
  } as unknown as CanvasRenderingContext2D);
  const profile: ScannerProfile = {
    id: "fixture", name: "合成A4", deviceFingerprint: "", dpi: 300, duplex: false,
    colorMode: "grayscale", paperSize: "A4", autoRotate: false, compression: "png",
    templatePreset: "locked-template", createdAt: "", updatedAt: ""
  };
  // 仅替换解码和像素读取，真实检查器仍计算两轴分辨率和释放图像资源。
  const checks = await inspectScannerSample(new File(["synthetic"], "synthetic.png", { type: "image/png" }), profile);
  expect(checks.find(check => check.key === "dpi")?.status).toBe(expected);
  expect(close).toHaveBeenCalledOnce();
});

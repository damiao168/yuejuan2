// @vitest-environment jsdom
import { webcrypto } from "node:crypto";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";
import { objectiveGridPageImage } from "./objectiveGridPageImage";

const jpeg = (bytes: number) => `data:image/jpeg;base64,${btoa("x".repeat(bytes))}`;
const png = (bytes: number) => `data:image/png;base64,${btoa("p".repeat(bytes))}`;

describe("objective grid dedicated page image", () => {
  let imageWidth = 4000; let imageHeight = 3000;
  let context: { drawImage: ReturnType<typeof vi.fn>; fillRect: ReturnType<typeof vi.fn>; fillStyle: string };
  let serialize: MockInstance<HTMLCanvasElement["toDataURL"]>;
  function pdf(width: number, height: number) {
    const render = vi.fn((_parameters: unknown) => ({ promise: Promise.resolve() }));
    const getPage = vi.fn(async () => ({ getViewport: ({ scale }: { scale: number }) => ({ width: width * scale, height: height * scale }), render }));
    return { document: { getPage } as unknown as PDFDocumentProxy, render, getPage };
  }
  beforeEach(() => {
    imageWidth = 4000; imageHeight = 3000;
    context = { drawImage: vi.fn(), fillRect: vi.fn(), fillStyle: "" };
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(context as unknown as CanvasRenderingContext2D);
    serialize = vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockImplementation(type => type === "image/png" ? png(24) : jpeg(24));
    vi.stubGlobal("crypto", webcrypto);
    class LoadedImage {
      naturalWidth = imageWidth; naturalHeight = imageHeight; onload?: () => void;
      set src(_url: string) { queueMicrotask(() => this.onload?.()); }
    }
    vi.stubGlobal("Image", LoadedImage);
  });
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it.each([[842, 595], [595, 842], [3000, 1000]])("renders PDF %s×%s near 2300px while preserving aspect and page number", async (width, height) => {
    const source = pdf(width, height);
    const result = await objectiveGridPageImage(source.document, 2);
    expect(source.getPage).toHaveBeenCalledWith(2);
    expect(Math.max(result.width, result.height)).toBeGreaterThanOrEqual(2299);
    expect(Math.max(result.width, result.height)).toBeLessThanOrEqual(2300);
    expect(result.width * result.height).toBeLessThanOrEqual(4_000_000);
    expect(Math.abs(result.width - result.height * width / height)).toBeLessThan(3);
    expect(source.render.mock.calls[0][0]).toMatchObject({ canvas: result.canvas, background: "white" });
  });

  it.each([[1000, 1000], [1000, 1001], [1001, 1000]])("caps a near-square PDF %s×%s to 4MP despite rounding", async (width, height) => {
    const source = pdf(width, height);
    const result = await objectiveGridPageImage(source.document, 1);
    expect(result.width * result.height).toBeLessThanOrEqual(4_000_000);
    expect(Math.max(result.width, result.height)).toBeLessThanOrEqual(2300);
    expect(Math.abs(result.width - result.height * width / height)).toBeLessThan(2);
  });

  it("resizes a large image with a white background and keeps a small image at its native resolution", async () => {
    const large = await objectiveGridPageImage(undefined, 1, "blob:large");
    expect(large.width).toBe(2300); expect(large.height).toBeGreaterThanOrEqual(1724); expect(large.height).toBeLessThanOrEqual(1725);
    expect(context.fillRect).toHaveBeenCalledWith(0, 0, large.width, large.height);
    expect(context.fillStyle).toBe("white");
    imageWidth = 1000; imageHeight = 600;
    const small = await objectiveGridPageImage(undefined, 1, "blob:small");
    expect(small).toMatchObject({ width: 1000, height: 600 });
  });

  it("preserves a lossless PNG at exactly 1MiB and hashes the transmitted PNG instead of converting to JPEG", async () => {
    serialize.mockImplementation(type => type === "image/png" ? png(1024 * 1024) : jpeg(24));
    const result = await objectiveGridPageImage(pdf(842, 595).document, 1);
    expect(result.media_type).toBe("image/png"); expect(atob(result.data_base64).length).toBe(1024 * 1024);
    expect(serialize).toHaveBeenCalledExactlyOnceWith("image/png", undefined);
    const digest = await webcrypto.subtle.digest("SHA-256", new TextEncoder().encode("p".repeat(1024 * 1024)));
    expect(result.sha256).toBe([...new Uint8Array(digest)].map(value => value.toString(16).padStart(2, "0")).join(""));
  });

  it("falls back to a JPEG within 1MiB only after PNG exceeds the bound and hashes that JPEG", async () => {
    serialize.mockImplementation(type => type === "image/png" ? png(1024 * 1024 + 1) : jpeg(1024 * 1024));
    const result = await objectiveGridPageImage(pdf(842, 595).document, 1);
    expect(atob(result.data_base64).length).toBe(1024 * 1024);
    expect(result.media_type).toBe("image/jpeg");
    expect(serialize.mock.calls).toEqual([["image/png", undefined], ["image/jpeg", .85]]);
    const digest = await webcrypto.subtle.digest("SHA-256", new TextEncoder().encode("x".repeat(1024 * 1024)));
    expect(result.sha256).toBe([...new Uint8Array(digest)].map(value => value.toString(16).padStart(2, "0")).join(""));
  });

  it("tries a lower JPEG quality before returning, and rejects images still above 1MiB", async () => {
    serialize.mockReturnValueOnce(png(1024 * 1024 + 1)).mockReturnValueOnce(jpeg(1024 * 1024 + 1)).mockReturnValueOnce(jpeg(200));
    const result = await objectiveGridPageImage(pdf(842, 595).document, 1);
    expect(atob(result.data_base64).length).toBe(200);
    expect(serialize.mock.calls.map(call => call[1])).toEqual([undefined, .85, .7]);
    serialize.mockClear().mockImplementation(type => type === "image/png" ? png(1024 * 1024 + 1) : jpeg(1024 * 1024 + 1));
    await expect(objectiveGridPageImage(pdf(842, 595).document, 1)).rejects.toThrow("当前页图像过大");
    expect(serialize.mock.calls.map(call => call[1])).toEqual([undefined, .85, .7, .55, .4]);
  });

  it("rejects invalid dimensions and an invalid image serialization before issuing an analysis request", async () => {
    await expect(objectiveGridPageImage(pdf(0, 842).document, 1)).rejects.toThrow("当前页尺寸无效");
    serialize.mockReturnValue("data:,");
    await expect(objectiveGridPageImage(pdf(842, 595).document, 1)).rejects.toThrow("无法压缩");
  });
});

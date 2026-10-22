import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  createWorker: vi.fn(), setParameters: vi.fn(), recognize: vi.fn(), terminate: vi.fn(),
  drawImage: vi.fn(),
}));
vi.mock("./localAnswerSheetOCRWorker", () => ({ createLocalAnswerSheetOCRWorker: mocks.createWorker }));
vi.mock("tesseract.js/dist/worker.min.js?url", () => ({ default: "/assets/ocr-worker.js" }));
vi.mock("tesseract.js-core/tesseract-core-lstm.wasm.js?url", () => ({ default: "/assets/ocr-core.wasm.js" }));
vi.mock("@tesseract.js-data/eng/4.0.0_best_int/eng.traineddata.gz?url", () => ({ default: "/ocr/eng.traineddata.gz" }));

import { createAnswerSheetOCR } from "./answerSheetOCR";

const canvas = { width: 1000, height: 2000 } as HTMLCanvasElement;
const frame = { x: .1, y: .2, width: .8, height: .6, confidence: .95 };
const pageParameters = { tessedit_pageseg_mode: "11", tessedit_char_whitelist: "0123456789().、ABCDEFGH[]", user_defined_dpi: "200" };
const headerParameters = { tessedit_pageseg_mode: "7", tessedit_char_whitelist: "" };
const word = (text: string, x0 = 120, y0 = 420, confidence = 95) => ({ text, confidence, bbox: { x0, y0, x1: x0 + 30, y1: y0 + 30 } });
const result = (...words: ReturnType<typeof word>[]) => ({ data: { blocks: [{ paragraphs: [{ lines: [{ words }] }] }] } });

beforeEach(() => {
  vi.resetAllMocks();
  mocks.setParameters.mockResolvedValue(undefined);
  mocks.terminate.mockResolvedValue(undefined);
  mocks.createWorker.mockResolvedValue({ setParameters: mocks.setParameters, recognize: mocks.recognize, terminate: mocks.terminate });
  vi.stubGlobal("window", { location: { href: "http://127.0.0.1:4173/#/admin/exams/exam1/template" } });
  vi.stubGlobal("document", { createElement: vi.fn(() => ({ width: 0, height: 0, getContext: () => ({ drawImage: mocks.drawImage }) })) });
});
afterEach(() => vi.unstubAllGlobals());

describe("answer sheet OCR worker wrapper", () => {
  it("passes local resources and cancellation to the native worker bridge without importing the Tesseract main API", async () => {
    const cancellation = new AbortController();
    const ocr = await createAnswerSheetOCR(cancellation.signal);
    expect(mocks.createWorker).toHaveBeenCalledExactlyOnceWith({
      workerPath: "/assets/ocr-worker.js", corePath: "/assets/ocr-core.wasm.js",
      langPath: "http://127.0.0.1:4173/ocr/", signal: cancellation.signal,
    });
    expect(mocks.setParameters).toHaveBeenCalledExactlyOnceWith(pageParameters);
    await ocr.terminate();
  });

  it("normalizes words from every nested block and keeps OCR line identities", async () => {
    mocks.recognize.mockResolvedValue({ data: { blocks: [
      { paragraphs: [{ lines: [{ words: [word("(15)", 100, 400), word("16.", 600, 400)] }] }] },
      { paragraphs: [{ lines: [{ words: [word("17.", 200, 1200)] }] }] },
    ] } });
    const ocr = await createAnswerSheetOCR();
    expect(await ocr.recognize(canvas)).toEqual([
      { text: "(15)", confidence: 95, x: .1, y: .2, width: .03, height: .015, source: "ocr", lineId: "0:0:0" },
      { text: "16.", confidence: 95, x: .6, y: .2, width: .03, height: .015, source: "ocr", lineId: "0:0:0" },
      { text: "17.", confidence: 95, x: .2, y: .6, width: .03, height: .015, source: "ocr", lineId: "1:0:0" },
    ]);
    expect(mocks.recognize).toHaveBeenCalledExactlyOnceWith(canvas);
    await ocr.terminate();
  });

  it("rechecks only the frame header when full-page OCR drops a visible marker and restores page parameters", async () => {
    mocks.recognize.mockResolvedValueOnce(result(word("15"))).mockResolvedValueOnce(result(word("15.", 22, 18)));
    const ocr = await createAnswerSheetOCR();
    expect(await ocr.recognize(canvas, [frame])).toEqual([
      { text: "15", confidence: 95, x: .12, y: .21, width: .03, height: .015, source: "ocr", lineId: "0:0:0" },
      { text: "15.", confidence: 95, x: .12, y: .21, width: .03, height: .015, source: "ocr", lineId: "header:0:0:0:0" },
    ]);
    expect(mocks.drawImage).toHaveBeenCalledExactlyOnceWith(canvas, 98, 402, 363, 63, 0, 0, 363, 63);
    expect(mocks.setParameters.mock.calls.map(call => call[0])).toEqual([pageParameters, headerParameters, pageParameters]);
    expect(mocks.recognize.mock.calls[1][0]).not.toBe(canvas);
    await ocr.terminate();
  });

  it.each(["15", "16.", "15.1", "15.(1)"])("does not invent a marker or change a question number from header output %s", async text => {
    mocks.recognize.mockResolvedValueOnce(result(word("15"))).mockResolvedValueOnce(result(word(text, 22, 18)));
    const ocr = await createAnswerSheetOCR();
    expect((await ocr.recognize(canvas, [frame])).map(item => item.text)).toEqual(["15"]);
    await ocr.terminate();
  });

  it("does not rescan a header that already has the same explicit question marker", async () => {
    mocks.recognize.mockResolvedValue(result(word("15"), word("15.", 160)));
    const ocr = await createAnswerSheetOCR();
    expect(await ocr.recognize(canvas, [frame])).toHaveLength(2);
    expect(mocks.recognize).toHaveBeenCalledTimes(1);
    expect(mocks.setParameters).toHaveBeenCalledTimes(1);
    await ocr.terminate();
  });

  it("does not rescan handwritten body numbers or low-confidence header digits", async () => {
    mocks.recognize.mockResolvedValue(result(word("15", 120, 900), word("16", 120, 420, 40)));
    const ocr = await createAnswerSheetOCR();
    await ocr.recognize(canvas, [frame]);
    expect(mocks.recognize).toHaveBeenCalledTimes(1);
    expect(mocks.drawImage).not.toHaveBeenCalled();
    await ocr.terminate();
  });

  it("rejects a marker recovered at a different position from the observed header number", async () => {
    mocks.recognize.mockResolvedValueOnce(result(word("15"))).mockResolvedValueOnce(result(word("15.", 220, 18)));
    const ocr = await createAnswerSheetOCR();
    expect((await ocr.recognize(canvas, [frame])).map(item => item.text)).toEqual(["15"]);
    await ocr.terminate();
  });

  it("keeps a second page outside another page's temporary PSM7 parameter window", async () => {
    let resolveHeader!: (value: ReturnType<typeof result>) => void;
    mocks.recognize.mockResolvedValueOnce(result(word("15")))
      .mockImplementationOnce(() => new Promise(resolve => { resolveHeader = resolve; }))
      .mockResolvedValueOnce(result(word("16.")));
    const ocr = await createAnswerSheetOCR();
    const first = ocr.recognize(canvas, [frame]);
    const second = ocr.recognize(canvas);
    await vi.waitFor(() => expect(mocks.recognize).toHaveBeenCalledTimes(2));
    resolveHeader(result(word("15.", 22, 18)));
    await Promise.all([first, second]);
    expect(mocks.recognize).toHaveBeenCalledTimes(3);
    expect(mocks.setParameters.mock.invocationCallOrder[2]).toBeLessThan(mocks.recognize.mock.invocationCallOrder[2]);
    await ocr.terminate();
  });

  it("restores parameters after header recognition fails and preserves that error", async () => {
    mocks.recognize.mockResolvedValueOnce(result(word("15"))).mockRejectedValueOnce(new Error("header failed"));
    const ocr = await createAnswerSheetOCR();
    await expect(ocr.recognize(canvas, [frame])).rejects.toThrow("header failed");
    expect(mocks.setParameters.mock.calls.map(call => call[0])).toEqual([pageParameters, headerParameters, pageParameters]);
    await ocr.terminate();
  });

  it("terminates a worker that cannot restore page parameters rather than reuse unknown OCR state", async () => {
    mocks.setParameters.mockResolvedValueOnce(undefined).mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("restore failed"));
    mocks.recognize.mockResolvedValueOnce(result(word("15"))).mockResolvedValueOnce(result(word("15.", 22, 18)));
    const ocr = await createAnswerSheetOCR();
    await expect(ocr.recognize(canvas, [frame])).rejects.toThrow("restore failed");
    expect(mocks.terminate).toHaveBeenCalledTimes(1);
    await expect(ocr.recognize(canvas)).rejects.toMatchObject({ name: "AbortError" });
  });

  it("returns no matches when the worker finds no text blocks", async () => {
    mocks.recognize.mockResolvedValue({ data: {} });
    const ocr = await createAnswerSheetOCR();
    expect(await ocr.recognize(canvas, [frame])).toEqual([]);
    await ocr.terminate();
  });

  it.each(["language load failed", "core initialization failed"])("propagates bridge initialization rejection: %s", async errorMessage => {
    mocks.createWorker.mockRejectedValue(new Error(errorMessage));
    await expect(createAnswerSheetOCR()).rejects.toThrow(errorMessage);
    expect(mocks.setParameters).not.toHaveBeenCalled();
    expect(mocks.terminate).not.toHaveBeenCalled();
  });

  it("terminates an initialized worker when initial parameters fail", async () => {
    mocks.setParameters.mockRejectedValue(new Error("unsupported parameter"));
    await expect(createAnswerSheetOCR()).rejects.toThrow("unsupported parameter");
    expect(mocks.terminate).toHaveBeenCalledTimes(1);
  });

  it("uses bridge cancellation to settle the active page, then rejects queued pages without contacting the stopped worker", async () => {
    let rejectActive!: (error: Error) => void;
    mocks.recognize.mockImplementation(() => new Promise((_, reject) => { rejectActive = reject; }));
    mocks.terminate.mockImplementation(async () => { rejectActive(new DOMException("cancelled", "AbortError")); });
    const ocr = await createAnswerSheetOCR();
    const first = expect(ocr.recognize(canvas)).rejects.toMatchObject({ name: "AbortError" });
    const second = expect(ocr.recognize(canvas)).rejects.toMatchObject({ name: "AbortError" });
    await vi.waitFor(() => expect(mocks.recognize).toHaveBeenCalledTimes(1));
    await ocr.terminate();
    await Promise.all([first, second]);
    await ocr.terminate();
    expect(mocks.terminate).toHaveBeenCalledTimes(1);
    expect(mocks.recognize).toHaveBeenCalledTimes(1);
  });

  it("does not contact the worker after its external signal is cancelled", async () => {
    const cancellation = new AbortController();
    const ocr = await createAnswerSheetOCR(cancellation.signal);
    cancellation.abort();
    await expect(ocr.recognize(canvas, [frame])).rejects.toMatchObject({ name: "AbortError" });
    expect(mocks.recognize).not.toHaveBeenCalled();
    await ocr.terminate();
  });

  it("propagates page recognition errors and permits explicit cleanup", async () => {
    mocks.recognize.mockRejectedValue(new Error("image decoding failed"));
    const ocr = await createAnswerSheetOCR();
    await expect(ocr.recognize(canvas)).rejects.toThrow("image decoding failed");
    await ocr.terminate();
    expect(mocks.terminate).toHaveBeenCalledTimes(1);
  });
});

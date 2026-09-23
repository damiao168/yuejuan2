// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useExamCaptureContext } from "./useExamCaptureContext";

const api = vi.hoisted(() => ({ listCaptureBatches: vi.fn(), listExams: vi.fn() }));
vi.mock("../../api/exams", () => ({
  listCaptureBatches: api.listCaptureBatches, listExams: api.listExams
}));
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("exam capture context controller", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useExamCaptureContext>;
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });
  async function mount() {
    function Probe() {
      current = useExamCaptureContext({
        client: {} as never, token: "token", durableScopeKey: "test-session", workspace: "scan",
        logEvent: vi.fn().mockResolvedValue(undefined)
      });
      return null;
    }
    await act(async () => { root.render(<Probe />); });
  }

  it("ignores a previous exam's late batch response", async () => {
    const old = deferred<{ batches: Array<{ id: string; status: string }> }>();
    api.listCaptureBatches.mockImplementation((_client, examId: string) =>
      examId === "exam-a" ? old.promise : Promise.resolve({
        batches: [{ id: "batch-b", status: "open" }]
      }));
    await mount();
    await act(async () => { current.setSelectedExamId("exam-a"); });
    await act(async () => { current.setSelectedExamId("exam-b"); });
    expect(current.captureBatchId).toBe("batch-b");
    await act(async () => {
      old.resolve({ batches: [{ id: "batch-a", status: "open" }] });
    });
    expect(current.captureBatchId).toBe("batch-b");
    expect(current.captureBatches.map((batch) => batch.id)).toEqual(["batch-b"]);
  });

  it("invalidates an old batch request when exam selection is cleared", async () => {
    const old = deferred<{ batches: Array<{ id: string; status: string }> }>();
    api.listCaptureBatches.mockReturnValueOnce(old.promise);
    await mount();
    await act(async () => { current.setSelectedExamId("exam-a"); });
    await act(async () => { current.setSelectedExamId(""); });
    await act(async () => { old.resolve({ batches: [{ id: "batch-a", status: "open" }] }); });
    expect(current.captureBatches).toEqual([]);
    expect(current.captureBatchId).toBe("");
  });
});

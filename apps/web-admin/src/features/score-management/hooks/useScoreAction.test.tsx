// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../api/client";
import { StepUpCancelledError } from "../../../auth/stepUpContext";
import { useScoreAction } from "./useScoreAction";

const messages = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: messages }) } }));

describe("score action recovery", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useScoreAction>;
  const loadExamList = vi.fn<() => Promise<void>>();
  const loadScores = vi.fn<(examId: string) => Promise<void>>();

  beforeEach(() => {
    vi.clearAllMocks();
    loadExamList.mockResolvedValue(undefined);
    loadScores.mockResolvedValue(undefined);
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });

  async function mount(selectedExamId = "exam-1") {
    function Probe() {
      current = useScoreAction({ selectedExamId, loadExamList, loadScores });
      return null;
    }
    await act(async () => { root.render(<Probe />); });
  }

  it("keeps the operation pending until both its action and score reload finish", async () => {
    await mount();
    let finish!: () => void;
    const pending = new Promise<void>((resolve) => { finish = resolve; });
    let operation!: Promise<void>;
    await act(async () => { operation = current.runAction("finalize", () => pending, "done"); });
    expect(current.actioning).toBe("finalize");
    expect(messages.success).not.toHaveBeenCalled();
    await act(async () => { finish(); await operation; });
    expect(messages.success).toHaveBeenCalledWith("done");
    expect(loadScores).toHaveBeenCalledWith("exam-1");
    expect(current.actioning).toBeNull();
  });

  it.each(["publish", "release-publish"])("refreshes both publication facts after an uncertain %s result", async (key) => {
    await mount();
    const order: string[] = [];
    loadExamList.mockImplementation(async () => { order.push("exams"); });
    loadScores.mockImplementation(async () => { order.push("scores"); });
    messages.warning.mockImplementationOnce(() => { order.push("warning"); });
    await act(async () => { await current.runAction(key, async () => { throw new Error("network lost"); }, "done"); });
    expect(order).toEqual(["exams", "scores", "warning"]);
    expect(messages.error).not.toHaveBeenCalled();
    expect(messages.success).not.toHaveBeenCalled();
    expect(current.actioning).toBeNull();
  });

  it("does not treat cancellation of second-factor verification as a failed publication", async () => {
    await mount();
    await act(async () => { await current.runAction("publish", async () => { throw new StepUpCancelledError(); }, "done"); });
    expect(messages.error).not.toHaveBeenCalled();
    expect(messages.warning).not.toHaveBeenCalled();
    expect(loadExamList).not.toHaveBeenCalled();
    expect(loadScores).not.toHaveBeenCalled();
    expect(current.actioning).toBeNull();
  });

  it("retains ordinary error handling for rejected commands and non-publication failures", async () => {
    await mount();
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      await act(async () => { await current.runAction("publish", async () => { throw new ApiClientError(403, "forbidden", "denied"); }, "done"); });
      await act(async () => { await current.runAction("export", async () => { throw new Error("failed"); }, "done"); });
      expect(messages.error).toHaveBeenCalledTimes(2);
      expect(messages.warning).not.toHaveBeenCalled();
      expect(loadExamList).not.toHaveBeenCalled();
      expect(current.actioning).toBeNull();
    } finally {
      consoleError.mockRestore();
    }
  });

  it("releases the pending state even if uncertain-outcome recovery itself rejects", async () => {
    await mount();
    loadExamList.mockRejectedValue(new Error("reload failed"));
    await act(async () => {
      await expect(current.runAction("publish", async () => { throw new Error("network lost"); }, "done")).rejects.toThrow("reload failed");
    });
    expect(current.actioning).toBeNull();
    expect(loadScores).not.toHaveBeenCalled();
  });
});

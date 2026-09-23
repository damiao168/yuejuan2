// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSubmissionQuality } from "./useSubmissionQuality";

const api = vi.hoisted(() => ({ runSubmissionQualityCheck: vi.fn() }));
vi.mock("../../api/submissions", () => ({
  runSubmissionQualityCheck: api.runSubmissionQualityCheck
}));

describe("submission quality controller", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useSubmissionQuality>;
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
  async function mount(submissionId: string) {
    function Probe() {
      current = useSubmissionQuality({} as never, submissionId, "test-session", vi.fn().mockResolvedValue(undefined));
      return null;
    }
    await act(async () => { root.render(<Probe />); });
  }

  it("publishes a successful service quality result", async () => {
    api.runSubmissionQualityCheck.mockResolvedValueOnce({ result: { passed: true } });
    await mount("submission-a");
    await act(async () => { await current.handleRunQualityCheck(); });
    expect(api.runSubmissionQualityCheck).toHaveBeenCalledWith(expect.anything(), "submission-a");
    expect(current.qualityResult).toEqual({ passed: true });
    expect(current.qualityError).toBeNull();
  });
  it("reports service failure without inventing a result", async () => {
    api.runSubmissionQualityCheck.mockRejectedValueOnce(new Error("unavailable"));
    await mount("submission-a");
    await act(async () => { await current.handleRunQualityCheck(); });
    expect(current.qualityResult).toBeNull();
    expect(current.qualityError).toBeTruthy();
  });
  it("does not call the service when submission id is empty", async () => {
    await mount(" ");
    await act(async () => { await current.handleRunQualityCheck(); });
    expect(api.runSubmissionQualityCheck).not.toHaveBeenCalled();
    expect(current.qualityError).toContain("submission_id");
  });
});

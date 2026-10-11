// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../api/client";
import type { PaperImportJob } from "../../api/papers";
import { usePaperImportWorkflow } from "./usePaperImportWorkflow";

const mocks = vi.hoisted(() => ({ save: vi.fn(), read: vi.fn(), apply: vi.fn(), load: vi.fn(),
  confirm: vi.fn(), warning: vi.fn(), error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: mocks, modal: { confirm: mocks.confirm } }) } }));
vi.mock("../../api/papers", () => ({ savePaperImportReview: mocks.save, getPaperImport: mocks.read,
  applyPaperImport: mocks.apply, uploadFile: vi.fn(), createPaperImport: vi.fn(), addPaperImportSources: vi.fn(),
  replacePaperImportSources: vi.fn(), cancelPaperImport: vi.fn(), retryPaperImportParse: vi.fn(), fillPaperImportContent: vi.fn() }));

function review(): PaperImportJob {
  return { id: "review", exam_id: "exam", generation: 7, source_revision: "source7", status: "review_required",
    updated_at: "2026-10-09T03:00:00.123456Z", sources: [], questions: [1, 2].map(number => ({
      candidate_id: `q${number}`, question_no: String(number), question_type: "fill_blank", score: 5,
      stem: `原第${number}题`, human_confirmed_fields: [], answer_key: { standard_answer: "答案", equivalent_answers: [], tolerance: {} }
    })) } as unknown as PaperImportJob;
}

describe("review content conflict recovery", () => {
  let root: Root, host: HTMLDivElement, current: ReturnType<typeof usePaperImportWorkflow>, active: PaperImportJob;
  function Probe() {
    current = usePaperImportWorkflow({ canManage: true, selectedExam: { id: "exam" } as never,
      papers: [], paperImports: [active], loadConfig: mocks.load });
    return null;
  }
  async function mount(job: PaperImportJob) { active = job; await act(async () => root.render(<Probe />)); }
  const firstPrompt = () => mocks.confirm.mock.calls[0][0];
  const comparison = () => mocks.confirm.mock.calls[1][0];
  beforeEach(async () => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.load.mockResolvedValue(undefined);
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    await mount(review());
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("keeps inputs after 409 and requires comparison before retrying with the exact new content time", async () => {
    const initial = active;
    act(() => current.updateReviewDraft(1, "stem", { stem: "B本地第二题" }));
    mocks.save.mockRejectedValueOnce(new ApiClientError(409, "configuration_conflict", "conflict"));
    await act(async () => current.saveImportReview(initial, 1));
    expect(mocks.save.mock.calls[0][3]).toBe(initial.updated_at);
    expect(current.reviewDrafts[1].stem).toBe("B本地第二题"); expect(current.reviewDirty).toBe(true);
    await act(async () => current.saveImportReview(initial, 1));
    expect(mocks.save).toHaveBeenCalledTimes(1);
    const latest = structuredClone(initial); latest.updated_at = "2026-10-09T03:00:00.123457Z";
    latest.questions[0].stem = "A已保存第一题"; latest.questions[0].human_confirmed_fields = ["stem", "score"];
    mocks.read.mockResolvedValue({ import: latest });
    await act(async () => firstPrompt().onOk());
    expect(comparison().content.props.local[1].stem).toBe("B本地第二题");
    expect(comparison().content.props.latest[0].stem).toBe("A已保存第一题");
    await act(async () => comparison().onOk());
    expect(mocks.save).toHaveBeenCalledTimes(1); expect(mocks.apply).not.toHaveBeenCalled();
    expect(current.reviewDrafts[0].stem).toBe("A已保存第一题"); expect(current.reviewDrafts[1].stem).toBe("B本地第二题");
    mocks.save.mockImplementationOnce(async (_id, _generation, questions) => ({ import: { ...latest, questions } }));
    await act(async () => current.saveImportReview(initial, 1));
    expect(mocks.save.mock.calls[1][3]).toBe(latest.updated_at);
    expect(mocks.save.mock.calls[1][2][0]).toEqual(latest.questions[0]);
  });

  it("does not refresh a dirty edit baseline when another client changes the same question and polling arrives first", async () => {
    const initial = active;
    act(() => current.updateReviewDraft(1, "stem", { stem: "B尚未保存第二题" }));
    const latest = structuredClone(initial); latest.updated_at = "2026-10-09T03:00:01Z";
    latest.questions[1].stem = "A已保存第二题";
    await mount(latest);
    expect(current.reviewDrafts[1].stem).toBe("B尚未保存第二题");
    await act(async () => current.saveImportReview(latest));
    expect(mocks.save).not.toHaveBeenCalled(); expect(mocks.confirm).toHaveBeenCalledOnce();
    mocks.read.mockResolvedValue({ import: latest });
    await act(async () => firstPrompt().onOk());
    expect(comparison().content.props.latest[1].stem).toBe("A已保存第二题");
    await act(async () => comparison().onOk());
    expect(mocks.save).not.toHaveBeenCalled();
    mocks.save.mockImplementationOnce(async (_id, _generation, questions) => ({ import: { ...latest, questions } }));
    await act(async () => current.saveImportReview(latest));
    expect(mocks.save.mock.calls[0][3]).toBe(latest.updated_at);
    expect(mocks.save.mock.calls[0][2][1].stem).toBe("B尚未保存第二题");
  });

  it("keeps a conflict blocked and inputs intact when rereading fails or the comparison is cancelled", async () => {
    act(() => current.updateReviewDraft(0, "stem", { stem: "保留的输入" }));
    mocks.save.mockRejectedValueOnce(new ApiClientError(409, "configuration_conflict", "conflict"));
    await act(async () => current.saveImportReview(active, 0));
    mocks.read.mockRejectedValueOnce(new Error("offline"));
    await act(async () => { await expect(firstPrompt().onOk()).rejects.toThrow("offline"); });
    expect(current.reviewDrafts[0].stem).toBe("保留的输入");
    firstPrompt().onCancel(); await act(async () => current.confirmPaperImport(active));
    expect(mocks.save).toHaveBeenCalledTimes(1); expect(mocks.apply).not.toHaveBeenCalled();
  });
});

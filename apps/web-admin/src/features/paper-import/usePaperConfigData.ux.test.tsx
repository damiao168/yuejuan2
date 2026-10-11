// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { usePaperConfigData } from "./usePaperConfigData";

const api = vi.hoisted(() => ({ questions: vi.fn() }));
vi.mock("../../api/exams", () => ({ listExams: async () => ({ exams: [{ id: "exam", name: "考试" }] }), getExam: async () => ({ exam: { id: "exam", name: "考试" } }) }));
vi.mock("../../api/papers", () => ({ listQuestions: api.questions, listPapers: async () => ({ papers: [] }), listPaperImports: async () => ({ imports: [] }) }));
vi.mock("./usePaperImportPolling", () => ({ usePaperImportPolling: () => undefined }));

describe("真实试卷刷新区分读取失败与已保存回执", () => {
  let root: Root; let host: HTMLDivElement; let workflow: ReturnType<typeof usePaperConfigData>;
  function Probe() { workflow = usePaperConfigData({ initialExamId: "exam" }); return null; }
  beforeEach(() => { vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); api.questions.mockResolvedValue({ questions: [{ id: "q", stem: "已加载题干", score: 5 }] }); host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function open() { await act(async () => root.render(<Probe />)); }

  it("rejects a failed final save refresh and retains the last loaded question for a later retry", async () => {
    await open(); const failure = new Error("question list unavailable"); api.questions.mockRejectedValueOnce(failure);
    let caught: unknown; await act(async () => { try { await workflow.loadConfig("exam", { silent: true, propagateError: true }); } catch (error) { caught = error; } });
    expect(caught).toBe(failure); expect(workflow.questions[0].stem).toBe("已加载题干"); expect(workflow.configLoading).toBe(false);
    api.questions.mockResolvedValue({ questions: [{ id: "q", stem: "服务端已保存题干", score: 8 }] });
    await act(async () => workflow.loadConfig("exam", { silent: true, propagateError: true })); expect(workflow.questions[0].score).toBe(8);
  });

  it("retains the ordinary silent polling behavior without producing an uncaught rejection", async () => {
    await open(); api.questions.mockRejectedValueOnce(new Error("temporary read failure"));
    await act(async () => workflow.loadConfig("exam", { silent: true })); expect(workflow.questions[0].stem).toBe("已加载题干"); expect(workflow.configError).toBeNull();
  });
});

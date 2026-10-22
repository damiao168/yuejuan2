// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useModelApplications } from "./useModelApplications";
import { ModelOperationQueue } from "../lib/modelOperationQueue";

const mocks = vi.hoisted(() => ({ get: vi.fn(), grading: vi.fn(), document: vi.fn(), answerSheet: vi.fn() }));
vi.mock("../../../api/modelApiConfig", () => ({ getModelApplications: mocks.get, saveGradingModelSelection: mocks.grading, saveDocumentModelSelection: mocks.document, saveAnswerSheetModelSelection: mocks.answerSheet }));

let current: ReturnType<typeof useModelApplications>;
const queue = new ModelOperationQueue();
function Harness() { current = useModelApplications("school-a", queue); return null; }

describe("独立自动阅卷用途保存", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    vi.resetAllMocks(); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.get.mockResolvedValue({ document_selection: { revision: "doc-r1", mode: "follow_chat" }, answer_sheet_selection: { revision: "sheet-r1", mode: "unconfigured" }, grading_selection: { revision: "grading-r1", mode: "unconfigured" }, grading_model_ids: [] });
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); });
  it("只调用阅卷专用接口并保留解析与答题卡用途，取消使用最新阅卷版本", async () => {
    await act(async () => root.render(<Harness />));
    mocks.grading.mockResolvedValueOnce({ selection: { revision: "grading-r2", mode: "model", model_config_id: "vision" } });
    await act(async () => { expect(await current.saveGrading("model", "vision")).toBe(true); });
    expect(mocks.grading).toHaveBeenLastCalledWith("school-a", { expected_revision: "grading-r1", mode: "model", model_config_id: "vision" });
    expect(mocks.document).not.toHaveBeenCalled(); expect(mocks.answerSheet).not.toHaveBeenCalled();
    expect(current.data?.document_selection.revision).toBe("doc-r1"); expect(current.data?.answer_sheet_selection?.revision).toBe("sheet-r1");
    mocks.grading.mockResolvedValueOnce({ selection: { revision: "grading-r3", mode: "unconfigured" } });
    await act(async () => { expect(await current.saveGrading("unconfigured")).toBe(true); });
    expect(mocks.grading).toHaveBeenLastCalledWith("school-a", { expected_revision: "grading-r2", mode: "unconfigured", model_config_id: undefined });
  });
});

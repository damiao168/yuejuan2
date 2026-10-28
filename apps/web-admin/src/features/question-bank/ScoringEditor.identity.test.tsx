// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import type { QuestionBankScoring, QuestionBankVersion } from "@edugrade/sdk";
import { ScoringEditor } from "./ScoringEditor";

const mocks = vi.hoisted(() => ({ update: vi.fn(), success: vi.fn(), error: vi.fn() }));
vi.mock("../../api/questionBank", () => ({ questionBankApi: {
  getQuestionBankItem: async () => ({ item: { bank_id: "bank" } }), getQuestionBank: async () => ({ bank: { school_id: "school" } }),
  listQuestionBankRubricTemplates: async () => ({ items: [] }), updateQuestionBankScoring: mocks.update,
} }));
vi.mock("antd", async importOriginal => {
  const { withFixedTextAreaLayout } = await import("../../test-utils/antdTextAreaLayout");
  return { ...withFixedTextAreaLayout(await importOriginal<typeof import("antd")>()), App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error } }) } };
});
const scoring = { answer: null, solution: null, rubric: { status: "approved", max_score: 3,
  points: [1, 2, 3].map(index => ({ id: `P${index}`, description: `point${index}`, score: 1, required: false, evidence_requirements: [] })), deductions: [], examples: [] },
  assets: [], template_version_id: null, use_policy: "practice_only" } as QuestionBankScoring;
const version = { id: "version", tenant_id: "tenant", item_id: "item", revision: 1, question_type: "short_answer", options: [], default_score: 3, scoring } as unknown as QuestionBankVersion;
let host: HTMLDivElement;
let root: Root;
let client: QueryClient;
beforeEach(() => {
  vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const original = window.getComputedStyle.bind(window); vi.spyOn(window, "getComputedStyle").mockImplementation(element => original(element));
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  mocks.update.mockImplementation(async (request: { body: { scoring: QuestionBankScoring } }) => ({ version: { ...version, revision: 2, scoring: request.body.scoring } }));
});
afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
async function flush() { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); }); }
async function click(text: string, index = 0) {
  const buttons = [...host.querySelectorAll("button")].filter(element => element.textContent?.replace(/\s/g, "") === text);
  expect(buttons[index]).toBeDefined(); await act(async () => buttons[index].click()); await flush();
}
async function edit(selector: string, value: string) {
  const field = host.querySelector<HTMLInputElement | HTMLTextAreaElement>(selector)!; expect(field).not.toBeNull();
  await act(async () => { Object.getOwnPropertyDescriptor(field instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(field, value); field.dispatchEvent(new Event("input", { bubbles: true })); }); await flush();
}
async function changedPoints(removed: number) {
  await act(async () => root.render(<QueryClientProvider client={client}><ScoringEditor version={version} disabled={false} onSaved={() => {}} onDirty={() => {}} canManageFiles={false} actorId="actor" /></QueryClientProvider>));
  await flush(); await click("移除评分点", removed); await click("添加评分点"); await edit("#points_2_description", "new point");
}
it.each([1, 2])("真实评分点列表删除第%s点再新增，保留旧身份并保存唯一新身份", async removed => {
  await changedPoints(removed); await click("保存评分标准");
  await vi.waitFor(() => expect(mocks.update).toHaveBeenCalledOnce());
  const points = mocks.update.mock.calls[0][0].body.scoring.rubric.points as { id: string }[];
  expect(points.slice(0, 2).map(point => point.id)).toEqual(removed === 1 ? ["P1", "P3"] : ["P1", "P2"]);
  expect(points[2].id).toMatch(/^p-/); expect(new Set(points.map(point => point.id)).size).toBe(3);
  expect(mocks.success).toHaveBeenCalledWith("评分标准已保存"); expect(mocks.error).not.toHaveBeenCalled();
});
it("手工修改新增点为已有身份时保留草稿并在发送前拒绝重复身份", async () => {
  await changedPoints(1); await edit("#points_2_id", "P3"); await click("保存评分标准");
  expect(mocks.update).not.toHaveBeenCalled(); expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("标识重复"));
  expect(host.querySelector<HTMLInputElement>("#points_2_id")!.value).toBe("P3"); expect(mocks.success).not.toHaveBeenCalled();
});

it("非数值判分的历史误差初次打开可见，显式清除后才从保存内容移除", async () => {
  const historical = { ...version, assessment_archetype: "short_constructed", scoring: { ...scoring,
    answer: { standard_answer: "10", equivalent_answers: [], tolerance: { absolute: 0.1, relative: 0.01 } }
  } } as QuestionBankVersion;
  const dirty = vi.fn();
  await act(async () => root.render(<QueryClientProvider client={client}><ScoringEditor version={historical} disabled={false} onSaved={() => {}} onDirty={dirty} canManageFiles={false} actorId="actor" /></QueryClientProvider>));
  await flush();
  expect(host.textContent).toContain("此判分方式不支持误差");
  expect(host.querySelector<HTMLInputElement>("#absolute")?.value).toBe("0.1");
  expect(host.querySelector<HTMLInputElement>("#relative")?.value).toBe("1");
  expect(host.querySelector<HTMLDetailsElement>("details")?.open).toBe(true);
  await click("清除误差");
  expect(dirty).toHaveBeenCalledWith(true);
  await click("保存评分标准");
  await vi.waitFor(() => expect(mocks.update).toHaveBeenCalledOnce());
  expect(mocks.update.mock.calls[0][0].body.scoring.answer.tolerance).toEqual({});
});

it("整题保存从真实评分表单读取答案与评分点，使用当前题目满分且不另发评分命令", async () => {
  const saveCombined = vi.fn(async (next: QuestionBankScoring) => ({ ...version, revision: 2, default_score: 6, scoring: next }));
  await act(async () => root.render(<QueryClientProvider client={client}><ScoringEditor version={version} disabled={false} draftScore={6} onSaveCombined={saveCombined} onSaved={() => {}} onDirty={() => {}} canManageFiles={false} actorId="actor" /></QueryClientProvider>));
  await flush(); await edit('[aria-label="标准答案"]', "新的答案"); await edit("#points_2_score", "4"); await click("保存本题草稿");
  await vi.waitFor(() => expect(saveCombined).toHaveBeenCalledOnce());
  expect(saveCombined.mock.calls[0][0].answer?.standard_answer).toBe("新的答案");
  expect(saveCombined.mock.calls[0][0].rubric?.max_score).toBe(6);
  expect(saveCombined.mock.calls[0][0].rubric?.points.map(point => point.id)).toEqual(["P1", "P2", "P3"]);
  expect(mocks.update).not.toHaveBeenCalled();
});

it("相对误差按百分比显示和编辑，提交仍保留比例单位", async () => {
  const numeric = { ...version, assessment_archetype: "numeric_expression", scoring: { ...scoring,
    answer: { standard_answer: "10", equivalent_answers: [], tolerance: { relative: 0.01 } }
  } } as QuestionBankVersion;
  await act(async () => root.render(<QueryClientProvider client={client}><ScoringEditor version={numeric} disabled={false} onSaved={() => {}} onDirty={() => {}} canManageFiles={false} actorId="actor" /></QueryClientProvider>));
  await flush(); expect(host.querySelector<HTMLInputElement>("#relative")?.value).toBe("1");
  await edit("#relative", "2"); await click("保存评分标准");
  await vi.waitFor(() => expect(mocks.update).toHaveBeenCalledOnce());
  expect(mocks.update.mock.calls[0][0].body.scoring.answer.tolerance.relative).toBe(0.02);
});

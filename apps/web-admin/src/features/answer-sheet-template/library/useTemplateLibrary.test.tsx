// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../api/client";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import type { AnswerSheetTemplateLibrary } from "../../../api/answerSheetTemplateLibrary";
import { answerSource, answerTemplate } from "../testing/answerSheetFixtures";
import { useTemplateLibrary } from "./useTemplateLibrary";

const mocks = vi.hoisted(() => ({ list: vi.fn(), resolve: vi.fn(), instantiate: vi.fn(), publish: vi.fn(), save: vi.fn(), lock: vi.fn() }));
vi.mock("../../../api/answerSheetTemplateLibrary", () => ({ listAnswerSheetTemplateLibrary: mocks.list,
  resolveAnswerSheetTemplate: mocks.resolve, instantiateAnswerSheetTemplate: mocks.instantiate, publishAnswerSheetTemplate: mocks.publish }));
vi.mock("../../../api/configuration", () => ({ lockAnswerSheetTemplate: mocks.lock }));
vi.mock("../editor/saveTemplateDraft", () => ({ saveTemplateDraft: mocks.save }));
const library = { id: "library", name: "数学卡", version_no: 1 } as AnswerSheetTemplateLibrary;
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }

describe("school template library workflow", () => {
  let root: Root;
  let host: HTMLDivElement;
  let current: ReturnType<typeof useTemplateLibrary>;
  let props: Parameters<typeof useTemplateLibrary>[0];
  function Probe() { current = useTemplateLibrary(props); return null; }
  async function render(changes: Partial<typeof props> = {}) { props = { ...props, ...changes }; await act(async () => root.render(<Probe />)); }
  beforeEach(() => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.list.mockResolvedValue({ libraries: [library] });
    props = { examId: "exam", scopeKey: "tenant:user", enabled: true, canManage: true, canEditSources: true,
      source: answerSource, template: answerTemplate, layout: answerTemplate.layout, name: answerTemplate.name,
      onInstance: vi.fn(), onTemplateChanged: vi.fn() };
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("identifies a missing library service, preserves the current template, and recovers on refresh", async () => {
    mocks.list.mockRejectedValueOnce(new ApiClientError(404, "route_not_found", "route not found"));
    await render();
    expect(current.listError).toBe("学校模板库未能加载：当前功能的服务尚未更新，请联系管理员更新服务后重试。");
    expect(props.onTemplateChanged).not.toHaveBeenCalled();
    expect(props.onInstance).not.toHaveBeenCalled();
    expect(mocks.save).not.toHaveBeenCalled();
    expect(current.loading).toBe(false);
    await act(async () => { await current.refresh(); });
    expect(current.listError).toBeUndefined();
    expect(current.libraries).toEqual([library]);
  });

  it("keeps matching conflicts distinct from an unknown source", async () => {
    mocks.resolve.mockResolvedValue({ status: "ambiguous", candidates: [library] });
    await render(); await act(async () => { await current.resolveSource(); });
    expect(current.resolution?.status).toBe("ambiguous");
    expect(props.onInstance).not.toHaveBeenCalled();
    expect(mocks.publish).not.toHaveBeenCalled();
  });

  it.each(["account", "exam", "source", "permission"])("discards a late matched instance after a %s change", async boundary => {
    const response = deferred<{ status: "matched"; candidates: never[]; instance: { template: AnswerSheetTemplate; paper: typeof answerSource; reused: boolean } }>();
    mocks.resolve.mockReturnValue(response.promise);
    await render();
    let pending!: Promise<unknown>;
    await act(async () => { pending = current.resolveSource(); });
    const changes = boundary === "account" ? { scopeKey: "tenant:other" } : boundary === "exam" ? { examId: "other" }
      : boundary === "source" ? { source: { ...answerSource, id: "other" } } : { canManage: false };
    await render(changes);
    if (boundary === "permission") await render({ canManage: true });
    await act(async () => { response.resolve({ status: "matched", candidates: [], instance: { template: answerTemplate, paper: answerSource, reused: false } }); await pending; });
    expect(props.onInstance).not.toHaveBeenCalled();
    expect(current.resolution).toBeUndefined();
    expect(current.busy).toBe(false);
  });

  it("saves current geometry, locks it, and collects independently of exam binding", async () => {
    const edited = { ...answerTemplate.layout, pages: answerTemplate.layout.pages.map(page => ({ ...page,
      question_regions: page.question_regions.map(region => ({ ...region, x: .23 })) })) };
    const saved = { ...answerTemplate, revision: 2, layout: edited };
    const locked = { ...saved, revision: 3, status: "locked" as const };
    mocks.save.mockResolvedValue({ status: "saved", template: saved }); mocks.lock.mockResolvedValue({ template: locked });
    mocks.publish.mockResolvedValue({ library });
    await render({ layout: edited }); await act(async () => { await current.publish(); });
    expect(mocks.save.mock.calls[0][0]).toMatchObject({ expectedRevision: 1, payload: { layout: edited } });
    expect(mocks.lock).toHaveBeenCalledWith(answerTemplate.id, saved.revision);
    expect(mocks.publish.mock.calls[0].slice(0, 2)).toEqual([answerTemplate.id, { name: locked.name, expected_revision: 3,
      confirmed_blank_reference: true, confirmed_reusable_layout: true }]);
    expect(props.onTemplateChanged).toHaveBeenNthCalledWith(1, saved);
    expect(props.onTemplateChanged).toHaveBeenNthCalledWith(2, locked);
    expect(current.published).toEqual(library);
    expect(props.onInstance).not.toHaveBeenCalled();
  });

  it("keeps a successful lock when collection fails and retries with the same idempotency key", async () => {
    const locked = { ...answerTemplate, status: "locked" as const };
    mocks.publish.mockRejectedValueOnce(new Error("timeout")).mockResolvedValueOnce({ library });
    await render({ template: locked });
    await act(async () => { await current.publish(); });
    expect(current.error).toContain("模板已锁定");
    await act(async () => { await current.publish(); });
    expect(mocks.publish.mock.calls[0][2]).toBe(mocks.publish.mock.calls[1][2]);
    expect(mocks.lock).not.toHaveBeenCalled();
    expect(mocks.save).not.toHaveBeenCalled();
    expect(current.published).toEqual(library);
  });

  it("does not lock or publish after a save conflict", async () => {
    mocks.save.mockResolvedValue({ status: "revision_conflict", error: new Error("conflict") });
    await render(); await act(async () => { await current.publish(); });
    expect(mocks.lock).not.toHaveBeenCalled(); expect(mocks.publish).not.toHaveBeenCalled();
    expect(props.onTemplateChanged).not.toHaveBeenCalled();
  });

  it("allows collecting an existing locked template while the exam is frozen, but blocks reuse", async () => {
    mocks.publish.mockResolvedValue({ library });
    await render({ canEditSources: false, template: { ...answerTemplate, status: "locked" } });
    await act(async () => { await current.instantiate(library.id); await current.publish(); });
    expect(mocks.instantiate).not.toHaveBeenCalled(); expect(mocks.publish).toHaveBeenCalledOnce();
  });
});

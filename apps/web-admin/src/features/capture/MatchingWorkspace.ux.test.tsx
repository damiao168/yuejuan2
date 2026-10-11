// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MatchingQueue } from "../../api/capture";
import { MatchingWorkspace } from "./MatchingWorkspace";

const mocks = vi.hoisted(() => ({ confirm: vi.fn(), merge: vi.fn(), preview: vi.fn() }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) },
  Alert: ({ message }: { message: ReactNode }) => <div role="alert">{message}</div>,
  Button: ({ children, onClick, disabled }: { children: ReactNode; onClick?: () => void; disabled?: boolean }) => <button type="button" disabled={disabled} onClick={onClick}>{children}</button>,
  Empty: ({ description }: { description: string }) => <p>{description}</p>,
  InputNumber: () => null,
  Select: ({ value, options, onChange, placeholder, disabled }: { value?: string; options: { value: string; label: string }[]; onChange: (value: string) => void; placeholder: string; disabled?: boolean }) =>
    <select aria-label={placeholder} value={value ?? ""} disabled={disabled} onChange={(event) => onChange(event.target.value)}><option value="">请选择</option>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Tag: ({ children }: { children: ReactNode }) => <span>{children}</span>,
}));
vi.mock("../../api/client", () => ({ getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback }));

function queue(): MatchingQueue {
  return {
    batch_id: "batch", exam_id: "exam",
    candidates: [{ id: "student-a", name: "张同学", student_no: "001", class_id: "class", class_name: "高一一班" }, { id: "student-b", name: "李同学", student_no: "002", class_id: "class", class_name: "高一一班" }],
    submissions: ["a", "b", "c"].map((id, index) => ({ id, student_id: index === 0 ? "student-a" : index === 1 ? "student-b" : undefined, identity_status: index < 2 ? "matched" : "unassigned", identity_revision: 1, identity_evidence: {}, pages: [{ id: `page-${id}`, sequence_no: index + 1, status: "normalized", assigned_page_no: 1, revision: 1 }] })),
  } as MatchingQueue;
}

describe("合并答卷对象核对", () => {
  let root: Root;
  let host: HTMLDivElement;
  let current = queue();
  beforeEach(() => {
    vi.resetAllMocks(); mocks.merge.mockResolvedValue(undefined); current = queue();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render(canManage = true, actioning = false) {
    await act(async () => root.render(<MatchingWorkspace queue={current} canManage={canManage} actioning={actioning} onPreview={mocks.preview} onConfirmStudent={vi.fn()} onUnknown={vi.fn()} onConfirmPage={vi.fn()} onMerge={mocks.merge} />));
  }
  function button(text: string) { const found = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === text); expect(found).toBeDefined(); return found!; }
  async function chooseTarget(id: string) {
    const input = host.querySelector<HTMLSelectElement>('[aria-label="合并到其他答卷"]')!;
    await act(async () => { input.value = id; input.dispatchEvent(new Event("change", { bubbles: true })); });
  }

  it("过滤当前答卷后目标编号与左栏一致，可查看目标页，确认框明确来源和目标", async () => {
    await render();
    const input = host.querySelector<HTMLSelectElement>('[aria-label="合并到其他答卷"]')!;
    expect(input.options[1].textContent).toBe("答卷 2 · 李同学 · 002 · 1 页");
    await chooseTarget("b");
    await act(async () => button("查看目标第 1 页").click());
    expect(mocks.preview).toHaveBeenCalledWith(current.submissions[1].pages[0]);
    await act(async () => button("合并").click());
    expect(mocks.merge).not.toHaveBeenCalled();
    const options = mocks.confirm.mock.calls[0][0] as { content: ReactNode; onOk: () => Promise<void> };
    const confirmation = document.createElement("div"); const confirmationRoot = createRoot(confirmation);
    await act(async () => confirmationRoot.render(options.content));
    expect(confirmation.textContent).toContain("来源：答卷 1 · 张同学 · 001 · 1 页");
    expect(confirmation.textContent).toContain("目标：答卷 2 · 李同学 · 002 · 1 页");
    await act(async () => options.onOk());
    expect(mocks.merge).toHaveBeenCalledWith("b", "a");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("已将答卷 1");
    await act(async () => confirmationRoot.unmount());
    current = { ...current, submissions: current.submissions.slice(1) }; await render();
    expect(host.querySelector(".matching-submissions strong")?.textContent).toBe("答卷");
    expect([...host.querySelectorAll(".capture-batch-row strong")].map((item) => item.textContent)).toEqual(["答卷 2", "答卷 3"]);
  });

  it("合并失败保留对象并显示失败，切换答卷清除上次合并目标", async () => {
    await render(); await chooseTarget("b");
    mocks.merge.mockRejectedValueOnce(new Error("目标答卷已变化，请重新核对"));
    await act(async () => button("合并").click());
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("目标答卷已变化");
    expect(host.querySelector(".capture-batch-row.active strong")?.textContent).toBe("答卷 1");
    await act(async () => host.querySelectorAll<HTMLButtonElement>(".capture-batch-row")[2].click());
    expect(host.querySelector<HTMLSelectElement>('[aria-label="合并到其他答卷"]')?.value).toBe("");
    expect(button("合并").disabled).toBe(true);
  });

  it.each([[false, false], [true, true]])("权限或处理中状态禁止合并", async (canManage, actioning) => {
    await render(); await chooseTarget("b"); await render(canManage, actioning);
    expect(button("合并").disabled).toBe(true);
    await act(async () => button("合并").click()); expect(mocks.confirm).not.toHaveBeenCalled();
  });
});

// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../auth/session";
import { QuestionAppealCenterPage } from "./QuestionAppealCenterPage";

const mocks = vi.hoisted(() => ({ workspace: vi.fn(), legacy: vi.fn(), exams: vi.fn(), confirm: vi.fn() }));
vi.mock("../components/QuestionAppealWorkspace", () => ({ QuestionAppealWorkspace: (props: Record<string, unknown>) => { mocks.workspace(props); return <p>新门户题目复核队列</p>; } }));
vi.mock("./AppealCenterPage", () => ({ AppealCenterPage: (props: Record<string, unknown>) => { mocks.legacy(props); return <p>旧记录处理队列</p>; } }));
vi.mock("../api/exams", () => ({ listExams: mocks.exams }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) },
  Alert: ({ message, description }: { message: ReactNode; description: ReactNode }) => <aside>{message}{description}</aside>,
  Tabs: ({ items, onChange }: { items: Array<{ key: string; label: string }>; onChange: (value: string) => void }) => <div>{items.map((item) => <button key={item.key} onClick={() => onChange(item.key)}>{item.label}</button>)}</div>,
  Select: () => <div>考试筛选</div>
}));

describe("生产成绩复核新旧入口", () => {
  let host: HTMLDivElement; let root: Root;
  beforeEach(() => { vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  const render = async () => act(async () => root.render(<QuestionAppealCenterPage mode="teacher" canRead canManage={false} canWork canReadIdentities={false} canReadExams={false} currentUser={{ id: "teacher", tenant: "school" } as SessionUser} initialExamId="exam" />));

  it("默认挂载学生提交的新队列，旧记录只在明确历史入口读取", async () => {
    await render();
    expect(host.textContent).toContain("我的成绩复核"); expect(mocks.exams).not.toHaveBeenCalled();
    expect(host.textContent).toContain("新门户题目复核队列"); expect(mocks.legacy).not.toHaveBeenCalled();
    expect(mocks.workspace).toHaveBeenLastCalledWith(expect.objectContaining({ examId: "exam", personalScope: true, currentUserId: "teacher", canManage: false, canWork: true }));
    await act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent === "历史申诉记录")!.click());
    expect(host.textContent).toContain("旧记录处理队列"); expect(host.textContent).toContain("学生门户新提交的题目复核申请请返回"); expect(mocks.legacy).toHaveBeenCalled();
  });

  it("新队列有未提交回复时切历史入口先确认，取消不会卸载输入", async () => {
    await render();
    const props = mocks.workspace.mock.calls[mocks.workspace.mock.calls.length - 1][0];
    await act(async () => props.onEditorStateChange({ dirty: true, busy: false }));
    await act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent === "历史申诉记录")!.click());
    expect(mocks.confirm).toHaveBeenCalledTimes(1); expect(mocks.legacy).not.toHaveBeenCalled(); expect(host.textContent).toContain("新门户题目复核队列");
  });
});

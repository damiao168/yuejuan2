// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import type { SchoolAssistantAction, SchoolAssistantFact, SchoolAssistantResult as Result } from "../../api/schoolAssistant";
import type { ConversationEvidence } from "./conversationState";
import { ConversationEvidenceView } from "./ConversationEvidenceView";
import { SchoolAssistantResult } from "./SchoolAssistantResult";

const mocks = vi.hoisted(() => ({ path: vi.fn() }));
// 导航替身只验证参数和 allowActions，不替代真实权限检查。
vi.mock("./navigation", () => ({ assistantActionPath: mocks.path }));
vi.mock("antd", () => ({
  Button: ({ children, onClick, icon }: { children?: ReactNode; onClick?: () => void; icon?: ReactNode }) => <button onClick={onClick}>{icon}{children}</button>,
  Tag: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
  Collapse: ({ items, className }: { items: { key: string; label: ReactNode; children: ReactNode }[]; className?: string }) => <div className={className}>{items.map((item) => <section key={item.key}>{item.label}{item.children}</section>)}</div>
}));

const user: SessionUser = {
  id: "admin-26", username: "admin-26", displayName: "管理员", name: "管理员", role: "school_admin", roles: ["school_admin"],
  tenant: "tenant-26", school: "学校", currentExam: "", permissions: ["exam:manage"], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school-26"], gradeIds: [], classIds: [] }
};
function action(id = "allowed"): SchoolAssistantAction {
  return { id, label: `入口 ${id}`, target: { kind: "exam_section", exam_id: "exam-26", section: "students" } };
}
function evidence(): ConversationEvidence {
  return { tools: [{ call_id: "tool-26", tool_name: "exam_progress", label: "查询考试", status: "completed" }], sources: [], actions: [] };
}
function result(fact?: SchoolAssistantFact): Result {
  return { request_id: "result-26", intent: "exam_progress", status: "completed", scope: { exam_id: "exam-26", class_ids: [], data_basis: "exam_live", scope_label: "当前考试" },
    facts: fact ? [fact] : [], blocks: [], sources: [], actions: [], limitations: [] };
}

describe("学校助手工具与事实展示边界", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.path.mockImplementation((target: SchoolAssistantAction, examId: string) => target.id === "allowed" ? `/exams/${examId}/students` : null);
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render(node: ReactNode) { await act(async () => root.render(node)); }

  it.each([
    ["running", "忽略的说明", "正在查询", "lucide-loader-circle"],
    ["failed", "来源无法读取", "来源无法读取", "lucide-triangle-alert"],
    ["failed", "", "查询失败", "lucide-triangle-alert"],
    ["completed", "忽略的说明", "已查询", "lucide-check"]
  ] as const)("工具 %s 在 message=%s 时保留原图标和短状态", async (status, message, label, iconClass) => {
    const current = evidence();
    current.tools[0] = { ...current.tools[0], status, message };
    await render(<ConversationEvidenceView evidence={current} user={user} />);
    const item = host.querySelector(".school-conversation-tools li")!;
    expect(item.className).toBe(`is-${status}`);
    expect(item.querySelector("span")?.textContent).toBe("查询考试");
    expect(item.querySelector("small")?.textContent).toBe(label);
    expect(item.querySelector("svg")?.classList.contains(iconClass)).toBe(true);
    expect(host.querySelector("ul")?.getAttribute("aria-label")).toBe("业务查询进度");
  });

  it("缺少 evidence 或只有 actions 时沿用空展示，不调用导航过滤", async () => {
    const navigate = vi.fn();
    await render(<ConversationEvidenceView user={user} onNavigate={navigate} />);
    expect(host.childElementCount).toBe(0);
    await render(<ConversationEvidenceView evidence={{ tools: [], sources: [], actions: [action()] }} user={user} onNavigate={navigate} />);
    expect(host.childElementCount).toBe(0);
    expect(mocks.path).not.toHaveBeenCalled();
  });

  it.each([false, true])("allowActions 为 %s 时保留受控路径过滤与回调参数", async (allowActions) => {
    const allowed = action();
    const blocked = action("blocked");
    const navigate = vi.fn();
    await render(<ConversationEvidenceView evidence={{ ...evidence(), actions: [allowed, blocked] }} user={user} allowActions={allowActions} onNavigate={navigate} />);
    const buttons = host.querySelectorAll("button");
    if (!allowActions) {
      expect(buttons).toHaveLength(0);
      expect(mocks.path).not.toHaveBeenCalled();
      return;
    }
    expect(mocks.path.mock.calls).toEqual([[allowed, allowed.target.exam_id, user], [blocked, blocked.target.exam_id, user]]);
    expect(buttons).toHaveLength(1);
    expect(buttons[0].textContent).toBe(allowed.label);
    await act(async () => buttons[0].click());
    expect(navigate).toHaveBeenCalledExactlyOnceWith("/exams/exam-26/students");
  });

  it("未提供导航回调时不显示 action 或查询路径", async () => {
    await render(<ConversationEvidenceView evidence={{ ...evidence(), actions: [action()] }} user={user} />);
    expect(host.querySelectorAll("button")).toHaveLength(0);
    expect(mocks.path).not.toHaveBeenCalled();
  });

  it.each([
    [true, 0, "可用时隐藏的原因", "0 ", false],
    [true, 7, undefined, "7 ", false],
    [true, null, "可用时隐藏的原因", "暂不可用", false],
    [false, 7, "来源不可用", "暂不可用", true],
    [false, null, undefined, "暂不可用", false]
  ] as const)("事实 available=%s value=%s 保留数值降级、count 格式和原因条件", async (available, value, reason, label, showReason) => {
    const fact: SchoolAssistantFact = { id: "fact-26", source_id: "source-26", metric_key: "pending", label: "待处理数", available, value, unit: "count", definition_id: "definition-26", reason };
    await render(<SchoolAssistantResult result={result(fact)} user={user} onNavigate={vi.fn()} />);
    const row = host.querySelector(".school-assistant-facts div")!;
    expect(row.querySelector("dt")?.textContent).toBe(fact.label);
    expect(row.querySelector("dd")?.textContent).toBe(label);
    expect(Boolean(row.querySelector("p"))).toBe(showReason);
    if (showReason) expect(row.querySelector("p")?.textContent).toBe(reason);
  });

  it("没有限制说明时不显示空列表", async () => {
    await render(<SchoolAssistantResult result={result()} user={user} onNavigate={vi.fn()} />);
    expect(host.querySelector(".school-assistant-limitations")).toBeNull();
  });

  it("限制说明保留重复、空文本和原顺序，不产生重复 key", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      const current = { ...result(), limitations: ["重复说明", "含:分隔符", "重复说明", "", "[0]"] };
      await render(<SchoolAssistantResult result={current} user={user} onNavigate={vi.fn()} />);
      expect([...host.querySelectorAll(".school-assistant-limitations li")].map(item => item.textContent)).toEqual(current.limitations);
      expect(error.mock.calls).toEqual([]);
    } finally {
      error.mockRestore();
    }
  });

  it("不同说明插入或重排时沿用对应文本的节点，重复说明仍逐条保留", async () => {
    const current = { ...result(), limitations: ["重复说明", "其他说明", "重复说明"] };
    const navigate = vi.fn();
    await render(<SchoolAssistantResult result={current} user={user} onNavigate={navigate} />);
    const previous = [...host.querySelectorAll(".school-assistant-limitations li")];
    await render(<SchoolAssistantResult result={{ ...current, limitations: ["新增说明", ...current.limitations] }} user={user} onNavigate={navigate} />);
    const inserted = [...host.querySelectorAll(".school-assistant-limitations li")];
    expect(inserted.slice(1)).toEqual(previous);
    inserted.slice(1).forEach((node, index) => expect(node).toBe(previous[index]));
    await render(<SchoolAssistantResult result={{ ...current, limitations: ["其他说明", "重复说明", "重复说明"] }} user={user} onNavigate={navigate} />);
    const reordered = [...host.querySelectorAll(".school-assistant-limitations li")];
    expect(reordered[0]).toBe(previous[1]);
    expect(reordered[1]).toBe(previous[0]);
    expect(reordered[2]).toBe(previous[2]);
  });
});

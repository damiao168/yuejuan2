// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArbitrationTask, FinalGrade } from "../api/review";
import { ArbitrationTaskRail } from "./ArbitrationTaskRail";
import { ArbitrationDecisionPanel } from "./ArbitrationDecisionPanel";
import { ArbitrationWorkspaceMain } from "./ArbitrationWorkspaceMain";

vi.mock("antd", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  const Input = Object.assign(() => null, { TextArea: () => null });
  return {
    Input, InputNumber: () => null, Select: () => null, Descriptions: () => null, Tabs: () => null, Alert: () => null,
    Button: ({ children, onClick, disabled }: { children: ReactNode; onClick?: () => void; disabled?: boolean }) => React.createElement("button", { onClick, disabled }, children),
    Space: ({ children }: { children: ReactNode }) => React.createElement("div", null, children),
    Tag: ({ children }: { children: ReactNode }) => React.createElement("span", null, children)
  };
});
vi.mock("./ArbitrationPanels", () => ({ ArbitrationAiTab: () => null, ArbitrationContextTab: () => null, ArbitrationRubricTab: () => null, ArbitrationScoreComparison: () => null, ArbitrationAuditList: () => null }));
vi.mock("../components/PageState", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    LoadingState: ({ label }: { label: string }) => React.createElement("div", null, label),
    ErrorState: ({ message }: { message: string }) => React.createElement("div", null, message),
    EmptyState: ({ title }: { title: string }) => React.createElement("div", null, title)
  };
});

function task(id: string): ArbitrationTask {
  return { id, exam_id: "exam", question_no: id, anonymous_code: id, status: "assigned", reason: "已有说明", first_score: 0, second_score: 5, score_difference: 5 } as ArbitrationTask;
}

describe("batch9 native arbitration semantics (mock controls, DOM assertions only)", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => { Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); host = document.createElement("div"); root = createRoot(host); });
  afterEach(async () => { await act(async () => root.unmount()); });
  const railProps = (): ComponentProps<typeof ArbitrationTaskRail> => ({ personalScope: false, scope: "mine", statusFilter: "active", keyword: "", totalTasks: 2, tasks: [task("A"), task("B")], selectedTaskId: "A", examNames: { exam: "考试" }, canReadExams: true, actioning: false, loading: false, error: null, hasMoreTasks: false, loadingMoreTasks: false, onRetry: vi.fn(), onSelect: vi.fn(), onLoadMore: vi.fn(), onScopeChange: vi.fn(), onStatusChange: vi.fn(), onKeywordChange: vi.fn() });
  const decisionProps = (): ComponentProps<typeof ArbitrationDecisionPanel> => ({ selectedTask: task("A"), detail: { task: task("A"), warnings: [] }, draft: { finalScore: null, reason: "", studentFeedback: "" }, draftTouched: false, finalGrade: null, maxScore: 10, actioning: null, editingDisabled: false, canAssign: true, canSubmit: true, auditLoading: false, auditError: null, auditLogs: [], setDraft: vi.fn(), setDraftTouched: vi.fn(), onAssign: vi.fn(), onSubmit: vi.fn(), onAuditRetry: vi.fn() });

  it("keeps the scroll div while using named native ul/li and the original row divider", async () => {
    const props = railProps(); await act(async () => root.render(<ArbitrationTaskRail {...props} />));
    const scroll = host.querySelector("div.arbitration-task-list")!;
    const list = scroll.querySelector<HTMLUListElement>('ul[aria-label="仲裁任务"]')!;
    expect(list.getAttribute("role")).toBe("list"); expect(list.style.margin).toBe("0px"); expect(list.style.padding).toBe("0px"); expect(list.style.listStyle).toBe("none");
    const rows = list.querySelectorAll<HTMLLIElement>(":scope > li"); expect(rows).toHaveLength(2);
    expect(rows[0].style.borderTop).toBe(""); expect(rows[1].style.borderTop).toContain("var(--border)");
    expect(rows[0].querySelector("button")?.getAttribute("aria-current")).toBe("true");
    await act(async () => rows[1].querySelector<HTMLButtonElement>("button")!.click());
    expect(props.onSelect).toHaveBeenCalledWith("B"); expect(host.querySelector('div[role="listitem"]')).toBeNull();
  });

  it("keeps every task button disabled during an action", async () => {
    await act(async () => root.render(<ArbitrationTaskRail {...railProps()} actioning />));
    expect([...host.querySelectorAll<HTMLButtonElement>(".arbitration-task-item")].every((button) => button.disabled)).toBe(true);
  });

  it("places exactly one text-only output inside the original styled draft paragraph", async () => {
    const props = decisionProps(); await act(async () => root.render(<ArbitrationDecisionPanel {...props} />));
    const note = host.querySelector("p.arbitration-draft-note")!;
    const output = note.querySelector("output")!;
    expect(note.getAttribute("role")).toBeNull(); expect(note.getAttribute("aria-live")).toBeNull();
    expect(output.textContent).toBe("最终分和说明在提交仲裁后才写入服务端。"); expect(output.children).toHaveLength(0);
    await act(async () => root.render(<ArbitrationDecisionPanel {...props} draftTouched />));
    expect(host.querySelectorAll("output")).toHaveLength(1); expect(host.querySelector("output")).toBe(output);
    expect(output.textContent).toBe("未提交内容仅在本页；切换任务或刷新会丢失。");
  });

  it("does not introduce a status region for absent or submitted detail and still shows zero", async () => {
    const props = decisionProps(); await act(async () => root.render(<ArbitrationDecisionPanel {...props} detail={null} />));
    expect(host.querySelector("output")).toBeNull();
    await act(async () => root.render(<ArbitrationDecisionPanel {...props} detail={{ task: { ...task("A"), status: "submitted", final_score: 0 }, warnings: [] }} />));
    expect(host.querySelector("output")).toBeNull(); expect(host.querySelector(".final-grade-result strong")?.textContent).toBe("0");
  });

  it("keeps returned final grade ahead of the task's prior score", async () => {
    const props = decisionProps(); const grade = { score: 0, max_score: 10, source: "appeal", locked: false } as FinalGrade;
    await act(async () => root.render(<ArbitrationDecisionPanel {...props} detail={{ task: { ...task("A"), final_score: 8 }, warnings: [] }} finalGrade={grade} />));
    expect(host.querySelector(".final-grade-result")?.textContent).toContain("0 / 10"); expect(host.querySelector(".final-grade-result")?.textContent).toContain("申诉改分 · 未锁定");
    expect(host.querySelector(".final-grade-result")?.textContent).not.toContain("已有说明");
  });

  it("keeps workspace loading ahead of error, then error ahead of empty", async () => {
    const props: ComponentProps<typeof ArbitrationWorkspaceMain> = { detail: null, loading: true, error: "错误", examNames: {}, canReadExams: true, currentUserId: "teacher", maxScore: 10, onRetry: vi.fn() };
    await act(async () => root.render(<ArbitrationWorkspaceMain {...props} />)); expect(host.textContent).toBe("正在读取仲裁详情");
    await act(async () => root.render(<ArbitrationWorkspaceMain {...props} loading={false} />)); expect(host.textContent).toBe("错误");
    await act(async () => root.render(<ArbitrationWorkspaceMain {...props} loading={false} error={null} />)); expect(host.textContent).toBe("请选择仲裁任务");
  });
});

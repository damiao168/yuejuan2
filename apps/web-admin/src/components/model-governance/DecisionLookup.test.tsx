// @vitest-environment jsdom
import type { ChangeEvent, ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { DecisionLookup } from "./DecisionLookup";
import type { AIEligibilityDecision, AIEligibilityDecisionSummary } from "../../api/scoringAssurance";

const mocks = vi.hoisted(() => ({ list: vi.fn(), read: vi.fn() }));
vi.mock("../../api/scoringAssurance", () => ({ listAIEligibilityDecisions: mocks.list, getAIEligibilityDecision: mocks.read }));
vi.mock("antd", () => {
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return { Space: Object.assign(Box, { Compact: Box }), Divider: Box, Tag: Box,
    Alert: ({ message }: { message: ReactNode }) => <div role="alert">{message}</div>,
    Empty: ({ description }: { description: string }) => <p>{description}</p>,
    Input: ({ value, onChange, placeholder, "aria-label": label }: { value: string; onChange: (event: ChangeEvent<HTMLInputElement>) => void; placeholder: string; "aria-label"?: string }) => <input value={value} onChange={onChange} placeholder={placeholder} aria-label={label} />,
    Button: ({ children, onClick, disabled }: { children?: ReactNode; onClick?: () => void; disabled?: boolean }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
    Drawer: ({ children, open, onClose }: { children: ReactNode; open: boolean; onClose: () => void }) => open ? <div aria-label="目录"><button onClick={onClose}>关闭目录</button>{children}</div> : null,
    Descriptions: ({ items }: { items: { key: string; children: ReactNode }[] }) => <div>{items.map(item => <span key={item.key}>{item.children}</span>)}</div>
  };
});
const summary: AIEligibilityDecisionSummary = { id: "decision-a", tenant_id: "school-a", run_item_id: "opaque-run", subject_code: "mathematics", education_stage: "junior", archetype_code: "short_constructed", risk_tier: "R2", decision: "HUMAN_PRIMARY", external_ai_allowed: false, created_at: "2026-10-28T00:00:00Z" };
function detail(tenant="school-a"): AIEligibilityDecision { return { id: "decision-a", tenant_id: tenant, run_item_id: "opaque-run", policy_version: 1, decision: "HUMAN_PRIMARY", external_ai_allowed: false, input_snapshot: {}, reasons: [{ code: "blocked", message: "需要人工处理", blocking: true }], output_constraint: { criteria_evidence_only: true, allow_model_final_score: false, final_score_authority: "human", max_severe_error_risk: 0.02 }, created_at: summary.created_at }; }
beforeEach(() => { vi.resetAllMocks(); mocks.list.mockResolvedValue({ decisions: [summary], next_cursor: "next", has_more: true }); mocks.read.mockResolvedValue({ decision: detail() }); });
afterEach(cleanup);

it("按目录选择后直接读决策，搜索和翻页在服务端同一条件内", async () => {
  render(<DecisionLookup canRead tenantID="school-a" />);
  fireEvent.click(screen.getByRole("button", { name: "选择评分运行" }));
  const item=await screen.findByRole("button", { name: /数学 · 初中 · 简答题/ });
  expect(mocks.list).toHaveBeenCalledWith("school-a", { q: "", cursor: undefined });
  fireEvent.change(screen.getByLabelText("搜索评分运行"), { target: { value: "math" } });
  fireEvent.click(screen.getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(mocks.list).toHaveBeenLastCalledWith("school-a", { q: "math", cursor: undefined }));
  fireEvent.change(screen.getByLabelText("搜索评分运行"), { target: { value: "未应用" } });
  fireEvent.click(screen.getByRole("button", { name: "加载更多运行" }));
  await waitFor(() => expect(mocks.list).toHaveBeenLastCalledWith("school-a", { q: "math", cursor: "next" }));
  fireEvent.click(item);
  await screen.findByText("需要人工处理");
  expect(mocks.read).toHaveBeenCalledWith("opaque-run", "school-a");
});

it("切校后忽略旧详情响应，越校目录直接拒绝", async () => {
  let resolve!:(value:{decision:AIEligibilityDecision})=>void;
  mocks.read.mockReturnValue(new Promise(done=>{resolve=done}));
  const view=render(<DecisionLookup canRead tenantID="school-a" />);
  fireEvent.click(screen.getByRole("button", { name: "选择评分运行" }));
  fireEvent.click(await screen.findByRole("button", { name: /数学 · 初中 · 简答题/ }));
  view.rerender(<DecisionLookup canRead tenantID="school-b" />);
  resolve({decision:detail()});
  await waitFor(()=>expect(screen.queryByText("需要人工处理")).toBeNull());
  fireEvent.click(screen.getByRole("button", { name: "选择评分运行" }));
  await screen.findAllByText("返回记录与当前学校不一致，请重新选择学校");
  expect(screen.queryByRole("button", { name:/数学 · 初中 · 简答题/ })).toBeNull();
});

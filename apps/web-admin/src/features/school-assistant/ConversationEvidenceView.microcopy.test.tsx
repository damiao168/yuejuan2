// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import { ConversationEvidenceView } from "./ConversationEvidenceView";
import type { ConversationEvidence } from "./conversationState";

vi.mock("antd", () => ({
  Button: ({ children }: { children: ReactNode }) => <button type="button">{children}</button>,
  Collapse: ({ items }: { items: { key: string; label: ReactNode; children: ReactNode }[] }) => <>{items.map(item => <details key={item.key}><summary>{item.label}</summary>{item.children}</details>)}</>
}));
afterEach(cleanup);
const user = { permissions: [] } as unknown as SessionUser;
const evidence: ConversationEvidence = {
  tools: [{ call_id: "one", tool_name: "exams", label: "考试资料", status: "running" }], sources: [], actions: []
};

it("查询完成后汇总成功过程，不再常驻逐条进度", () => {
  const view = render(<ConversationEvidenceView evidence={evidence} user={user} />);
  expect(screen.getByLabelText("业务查询进度")).toBeTruthy();
  view.rerender(<ConversationEvidenceView evidence={{ ...evidence, tools: [{ ...evidence.tools[0], status: "completed" }] }} user={user} />);
  expect(screen.queryByLabelText("业务查询进度")).toBeNull();
  expect(screen.getByText("已查询1项资料")).toBeTruthy();
  expect(screen.queryByRole("status")).toBeNull();
});

it("来源读取失败的影响保持可见，不因折叠来源详情而消失", () => {
  const sources = [{ id: "source", label: "成绩", status: "forbidden", scope_label: "本场考试", read_at: "2026-10-28T00:00:00Z" }] as ConversationEvidence["sources"];
  const view = render(<ConversationEvidenceView evidence={{ ...evidence, tools: [{ ...evidence.tools[0], status: "failed", message: "无权读取" }], sources }} user={user} />);
  const detail = view.container.querySelector("details")!;
  expect(detail.open).toBe(false);
  const warning = screen.getByRole("status");
  expect(warning.textContent).toContain("部分资料未能完整读取");
  expect(warning.closest("details")).toBeNull();
  expect(screen.getByLabelText("查询失败").textContent).toContain("无权读取");
});

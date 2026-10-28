// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import { QuestionBankHandoffs } from "./QuestionBankHandoffs";

const search = vi.hoisted(() => vi.fn());
vi.mock("../../api/questionBank", () => ({ questionBankApi: { searchQuestionBankItems: search } }));
vi.mock("antd", () => ({ Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => <button onClick={onClick}>{children}</button>, Space: ({ children }: { children: ReactNode }) => <div>{children}</div>, Pagination: () => null }));
vi.mock("../../components/PageState", () => ({ ErrorState: ({ message }: { message: string }) => <p role="alert">{message}</p> }));
let host: HTMLDivElement; let root: Root;
const user = { id: "reviewer", tenant: "school", permissions: ["question_bank:read", "question_bank:review", "question_bank:publish"] } as SessionUser;
beforeEach(() => {
  vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  search.mockResolvedValue({ total: 1, items: [{ item: { id: "item", bank_id: "bank", item_code: "Q1" }, version: { id: "version", stem: "待审核的题干" } }] });
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
it("待办自动读取当前职责范围并直达确切版本，发布待办独立查询", async () => {
  const navigate = vi.fn();
  await act(async () => root.render(<QuestionBankHandoffs user={user} onNavigate={navigate} />));
  expect(search).toHaveBeenLastCalledWith(expect.objectContaining({ query: { pending_action: "review", limit: 5, offset: 0 } }));
  await act(async () => [...host.querySelectorAll("button")].find(button => button.textContent === "待审核的题干")!.click());
  expect(navigate).toHaveBeenCalledWith("/question-bank?bank=bank&item=item&version=version");
  await act(async () => [...host.querySelectorAll("button")].find(button => button.textContent === "待我发布")!.click());
  expect(search).toHaveBeenLastCalledWith(expect.objectContaining({ query: { pending_action: "publish", limit: 5, offset: 0 } }));
});
it("只有读权限不查询工作待办；换账号后旧查询不能回填", async () => {
  const navigate = vi.fn();
  await act(async () => root.render(<QuestionBankHandoffs user={{ ...user, permissions: ["question_bank:read"] }} onNavigate={navigate} />));
  expect(search).not.toHaveBeenCalled();
  let complete!: (value: unknown) => void;
  search.mockImplementationOnce(() => new Promise(resolve => { complete = resolve; }));
  await act(async () => root.render(<QuestionBankHandoffs user={user} onNavigate={navigate} />));
  const signal = search.mock.calls[0][0].signal;
  await act(async () => root.render(<QuestionBankHandoffs user={{ ...user, id: "new-actor" }} onNavigate={navigate} />));
  await act(async () => complete({ total: 1, items: [{ item: { id: "old" }, version: { id: "old", stem: "旧账号题干" } }] }));
  expect(signal.aborted).toBe(true); expect(host.textContent).not.toContain("旧账号题干");
});
it("审核身份换成仅发布身份时自动切换待办，已加载的上一账号题目不残留", async () => {
  await act(async () => root.render(<QuestionBankHandoffs user={user} onNavigate={vi.fn()} />));
  search.mockResolvedValueOnce({ total: 1, items: [{ item: { id: "published-item", bank_id: "bank" }, version: { id: "to-publish", stem: "等待发布题目" } }] });
  await act(async () => root.render(<QuestionBankHandoffs user={{ ...user, id: "publisher", permissions: ["question_bank:read", "question_bank:publish"] }} onNavigate={vi.fn()} />));
  expect(search).toHaveBeenLastCalledWith(expect.objectContaining({ query: { pending_action: "publish", limit: 5, offset: 0 } }));
  expect(host.textContent).not.toContain("待审核的题干"); expect(host.textContent).toContain("等待发布题目");
});

// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PublishedQuestionAppeal } from "../api/questionAppeals";
import { QuestionAppealRegradeLaunch } from "./QuestionAppealRegradeLaunch";

const mocks = vi.hoisted(() => ({ preview: vi.fn(), create: vi.fn(), created: vi.fn(), working: vi.fn() }));
vi.mock("../api/scoreReleases", () => ({ previewRegrade: mocks.preview, createRegradeJob: mocks.create }));
vi.mock("antd", () => ({
  Alert: ({ message, description }: { message: string; description: string }) => <aside>{message}{description}</aside>,
  Button: ({ children, onClick, disabled }: { children: string; onClick: () => void; disabled?: boolean }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Select: ({ options, onChange }: { options: Array<{ label: string; value: string }>; onChange: (value: string) => void }) => <select onChange={(event) => onChange(event.target.value)}><option value="">选择阅卷员</option>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>
}));

describe("学生申请单份答卷复评创建", () => {
  let host: HTMLDivElement; let root: Root;
  const appeal = { id: "appeal", exam_id: "exam", question_id: "question", submission_id: "submission", source_release_id: "released", source_release_version: 2, question_no: "1" } as PublishedQuestionAppeal;
  beforeEach(() => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.preview.mockResolvedValue({ preview: { question_id: "question", source_release_id: "released", affected_count: 1 } });
    mocks.create.mockResolvedValue({ regrade: { job: { id: "new-job" } } });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  const render = async () => act(async () => root.render(<QuestionAppealRegradeLaunch appeal={appeal} reviewers={[{ id: "grader", username: "g", display_name: "重评老师", status: "active", roles: ["grader"] }]} response="已核对原卷，过程分需重新评分。" busy={false} onCreated={mocks.created} onWorkingChange={mocks.working} />));
  const click = async (label: string) => act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent === label)!.click());
  const choose = async () => act(async () => { const select = host.querySelector("select")!; select.value = "grader"; select.dispatchEvent(new Event("change", { bubbles: true })); });

  it("预览与创建限定同一发布单份答卷，失败重试沿用幂等键并公开后续发布步骤", async () => {
    await render(); await choose(); await click("预览本申请影响范围");
    expect(mocks.preview).toHaveBeenCalledWith("exam", "question", { source_release_id: "released", selector: { submission_ids: ["submission"] } });
    mocks.create.mockRejectedValueOnce(new Error("网络响应丢失"));
    await click("创建本题复评任务"); expect(host.textContent).toContain("网络响应丢失");
    await click("创建本题复评任务");
    expect(mocks.create).toHaveBeenCalledTimes(2);
    const first = mocks.create.mock.calls[0][2]; const second = mocks.create.mock.calls[1][2];
    expect(first).toEqual(second); expect(second).toMatchObject({ source_release_id: "released", selector: { submission_ids: ["submission"] }, strategy: "human_recheck", assignee_id: "grader" });
    expect(mocks.created).toHaveBeenCalledWith("new-job"); expect(mocks.working).toHaveBeenCalledWith(true); expect(mocks.working).toHaveBeenLastCalledWith(false);
    expect(host.textContent).toContain("批准、启动、复核候选、完成复评并发布新成绩");
  });

  it("影响范围包含多份答卷时阻止创建，不能把其他学生带入本申请", async () => {
    mocks.preview.mockResolvedValue({ preview: { question_id: "question", source_release_id: "released", affected_count: 3 } });
    await render(); await choose(); await click("预览本申请影响范围"); await click("创建本题复评任务");
    expect(mocks.create).not.toHaveBeenCalled(); expect(host.textContent).toContain("将影响 3 份答卷");
  });
});

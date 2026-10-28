// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PublishedQuestionAppeal } from "../api/questionAppeals";
import { QuestionAppealRegradeLaunch } from "./QuestionAppealRegradeLaunch";

const mocks = vi.hoisted(() => ({ preview: vi.fn(), create: vi.fn(), decide: vi.fn(), created: vi.fn(), linked: vi.fn(), working: vi.fn(),list:vi.fn(),get:vi.fn() }));
vi.mock("../api/scoreReleases", () => ({ previewRegrade: mocks.preview, createRegradeJob: mocks.create,listRegradeJobs:mocks.list,getRegradeJob:mocks.get }));
vi.mock("../api/questionAppeals", () => ({ decideQuestionAppeal: mocks.decide }));
vi.mock("antd", () => ({
  Alert: ({ message, description }: { message: string; description: string }) => <aside>{message}{description}</aside>,
  Button: ({ children, onClick, disabled }: { children: string; onClick: () => void; disabled?: boolean }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Select: ({ options, onChange }: { options: Array<{ label: string; value: string }>; onChange: (value: string) => void }) => <select onChange={(event) => onChange(event.target.value)}><option value="">选择阅卷员</option>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>
}));

describe("学生申请单份答卷复评创建", () => {
  let host: HTMLDivElement; let root: Root;
  const appeal = { id: "appeal", exam_id: "exam", question_id: "question", submission_id: "submission", source_release_id: "released", source_release_version: 2, question_no: "1", revision: 4 } as PublishedQuestionAppeal;
  beforeEach(() => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.preview.mockResolvedValue({ preview: { question_id: "question", source_release_id: "released", affected_count: 1 } });
    mocks.create.mockResolvedValue({ regrade: { job: { id: "new-job" } } });
    mocks.decide.mockResolvedValue({});
    mocks.list.mockResolvedValue({regrade_jobs:[]});
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  const render = async () => act(async () => root.render(<QuestionAppealRegradeLaunch appeal={appeal} reviewers={[{ id: "grader", username: "g", display_name: "重评老师", status: "active", roles: ["grader"] }]} response="已核对原卷，过程分需重新评分。" busy={false} onCreated={mocks.created} onLinked={mocks.linked} onWorkingChange={mocks.working} />));
  const click = async (label: string) => act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent === label)!.click());
  const choose = async () => act(async () => { const select = host.querySelector("select")!; select.value = "grader"; select.dispatchEvent(new Event("change", { bubbles: true })); });

  it("预览与创建限定同一发布单份答卷，失败重试沿用幂等键并公开后续发布步骤", async () => {
    await render(); await choose();
    expect(mocks.preview).toHaveBeenCalledWith("exam", "question", { source_release_id: "released", selector: { submission_ids: ["submission"] } });
    mocks.create.mockRejectedValueOnce(new Error("网络响应丢失"));
    await click("创建并关联本次复评"); expect(host.textContent).toContain("网络响应丢失");
    await click("创建并关联本次复评");
    expect(mocks.create).toHaveBeenCalledTimes(2);
    const first = mocks.create.mock.calls[0][2]; const second = mocks.create.mock.calls[1][2];
    expect(first).toEqual(second); expect(second).toMatchObject({ source_release_id: "released", selector: { submission_ids: ["submission"] }, strategy: "human_recheck", assignee_id: "grader" });
    expect(mocks.created).toHaveBeenCalledWith("new-job"); expect(mocks.working).toHaveBeenCalledWith(true); expect(mocks.working).toHaveBeenLastCalledWith(false);
    expect(mocks.decide).toHaveBeenCalledWith("appeal", { decision: "refer_regrade", public_response: "已核对原卷，过程分需重新评分。", regrade_job_id: "new-job", expected_revision: 4 });
    expect(mocks.linked).toHaveBeenCalledOnce();
    expect(host.textContent).toContain("批准、启动、复核候选、完成复评并发布新成绩");
  });

  it("影响范围包含多份答卷时阻止创建，不能把其他学生带入本申请", async () => {
    mocks.preview.mockResolvedValue({ preview: { question_id: "question", source_release_id: "released", affected_count: 3 } });
    await render(); await choose(); await click("创建并关联本次复评");
    expect(mocks.create).not.toHaveBeenCalled(); expect(host.textContent).toContain("将影响 3 份答卷");
  });

  it("创建成功但关联失败时，继续只关联已创建的同一任务", async () => {
    await render(); await choose();
    mocks.decide.mockRejectedValueOnce(new Error("关联暂时不可用"));
    await click("创建并关联本次复评");
    expect(mocks.create).toHaveBeenCalledOnce(); expect(mocks.linked).not.toHaveBeenCalled();
    await click("继续关联本次复评");
    expect(mocks.create).toHaveBeenCalledOnce(); expect(mocks.decide).toHaveBeenCalledTimes(2);
    expect(mocks.decide.mock.calls[1]).toEqual(mocks.decide.mock.calls[0]); expect(mocks.linked).toHaveBeenCalledOnce();
  });

  it("刷新后唯一已有同来源本人复评直接恢复关联，不能另建任务",async()=>{
    const job={id:"historical-job",status:"draft",created_by:"manager",exam_id:"exam",question_id:"question",source_release_id:"released",reason_code:"appeal_pattern",reason_text:"已核对原卷，过程分需重新评分。",strategy:"human_recheck",affected_count:1,selector:{submission_ids:["submission"]},created_at:"2026-10-28T00:00:00Z",severity_delta:0};
    mocks.list.mockResolvedValue({regrade_jobs:[job]});mocks.get.mockResolvedValue({regrade:{job,items:[{submission_id:"submission",assigned_to:"grader"}]}});
    await act(async()=>root.render(<QuestionAppealRegradeLaunch appeal={appeal} currentUserId="manager" reviewers={[{id:"grader",display_name:"阅卷教师",roles:["grader"]} as never]} response="已核对原卷，过程分需重新评分。" busy={false} onCreated={mocks.created} onLinked={mocks.linked} onWorkingChange={mocks.working}/>));
    expect(host.textContent).toContain("继续关联本次复评");
    await click("继续关联本次复评");
    expect(mocks.create).not.toHaveBeenCalled();expect(mocks.decide).toHaveBeenCalledWith("appeal",expect.objectContaining({regrade_job_id:"historical-job"}));
  });

  it("已有任务目录读取失败时阻止新建，避免把未知结果当没有任务",async()=>{
    mocks.list.mockRejectedValue(new Error("目录不可用"));
    await act(async()=>root.render(<QuestionAppealRegradeLaunch appeal={appeal} currentUserId="manager" reviewers={[{id:"grader",display_name:"阅卷教师",roles:["grader"]} as never]} response="已核对原卷，过程分需重新评分。" busy={false} onCreated={mocks.created} onLinked={mocks.linked} onWorkingChange={mocks.working}/>));
    await choose();await click("创建并关联本次复评");expect(mocks.create).not.toHaveBeenCalled();expect(host.textContent).toContain("目录不可用");
  });

  it("核对已有任务期间父组件刷新后使用最新回调，不重复读取或覆盖新输入",async()=>{
    const job={id:"historical-job",status:"draft",created_by:"manager",exam_id:"exam",question_id:"question",source_release_id:"released",reason_code:"appeal_pattern",reason_text:"原任务说明",strategy:"human_recheck",affected_count:1,selector:{submission_ids:["submission"]},created_at:"2026-10-28T00:00:00Z",severity_delta:0};
    let complete!:(value:unknown)=>void;
    mocks.list.mockResolvedValue({regrade_jobs:[job]});mocks.get.mockReturnValue(new Promise(resolve=>{complete=resolve;}));
    const latestCreated=vi.fn(),latestResponse=vi.fn(),oldResponse=vi.fn();
    const view=(created:(id:string)=>void,recovered:(response:string)=>void)=><QuestionAppealRegradeLaunch appeal={appeal} currentUserId="manager" reviewers={[{id:"grader",display_name:"阅卷教师",roles:["grader"]} as never]} response="" busy={false} onRecoveredResponse={recovered} onCreated={created} onWorkingChange={mocks.working}/>;
    await act(async()=>root.render(view(mocks.created,oldResponse)));
    await act(async()=>root.render(view(latestCreated,latestResponse)));
    await act(async()=>complete({regrade:{job,items:[{submission_id:"submission",assigned_to:"grader"}]}}));
    expect(latestCreated).toHaveBeenCalledWith("historical-job");expect(latestResponse).toHaveBeenCalledWith("原任务说明");
    expect(mocks.created).not.toHaveBeenCalled();expect(oldResponse).not.toHaveBeenCalled();expect(mocks.list).toHaveBeenCalledOnce();
  });
});

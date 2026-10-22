// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ExamReadinessPage } from "./ExamPreparationPage";
import { ApiClientError } from "../api/client";

const mocks = vi.hoisted(() => ({ exam: vi.fn(), readiness: vi.fn(), refresh: vi.fn(), confirm: vi.fn(), roster: vi.fn(), questions: vi.fn(), profile: vi.fn(), error: vi.fn(), success: vi.fn(), navigate: vi.fn(), changed: vi.fn(), modalConfirm: vi.fn(), modalCancel: vi.fn() }));
vi.mock("../api/exams", () => ({ getExam: mocks.exam, refreshExamCandidates: mocks.refresh, updateExam: vi.fn() }));
vi.mock("../api/configuration", () => ({ getExamReadiness: mocks.readiness, confirmExamReadiness: mocks.confirm }));
vi.mock("../api/scores", () => ({ listExamRoster: mocks.roster }));
vi.mock("../api/papers", () => ({ listQuestions: mocks.questions }));
vi.mock("../api/assessment", () => ({ getQuestionAssessmentProfile: mocks.profile }));
vi.mock("../api/org", () => ({ listClasses: vi.fn(), listGrades: vi.fn() }));
vi.mock("../components/features/assessment/ExamScoringPreparation", () => ({ ExamScoringPreparation: () => <p>逐题评分方式（单独覆盖配置与保存回归）</p> }));
vi.mock("../components/PageState", () => ({ LoadingState: ({ label }: { label: string }) => <p>{label}</p>, ErrorState: ({ message }: { message: string }) => <p>{message}</p> }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: { error: mocks.error, success: mocks.success } }) },
  Alert: ({ message, description, action }: { message: ReactNode; description: ReactNode; action?: ReactNode }) => <aside>{message}{description}{action}</aside>,
  Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick: () => void; disabled?: boolean; loading?: boolean }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>,
  Checkbox: () => null, Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Modal: ({ open, title, children, onOk, onCancel, okText, cancelText, okButtonProps }: { open: boolean; title: string; children: ReactNode; onOk: () => void; onCancel: () => void; okText: string; cancelText: string; okButtonProps?: { disabled?: boolean } }) => {
    mocks.modalConfirm.mockImplementation(onOk);
    mocks.modalCancel.mockImplementation(onCancel);
    return open ? <div role="dialog">{title}{children}<button disabled={okButtonProps?.disabled} onClick={onOk}>{okText}</button><button onClick={onCancel}>{cancelText}</button></div> : null;
  },
  Table: ({ dataSource, columns }: { dataSource: Array<Record<string, string>>; columns: Array<{ dataIndex: string; render?: (value: string) => ReactNode }> }) => <table><tbody>{dataSource.map((item, index) => <tr key={index}>{columns.map((column) => <td key={column.dataIndex}>{column.render ? column.render(item[column.dataIndex]) : item[column.dataIndex]}</td>)}</tr>)}</tbody></table>
}));

const readiness = { ready: true, confirmed: false, configuration_hash: "reviewed-hash", checks: [], advisories: [], scoring_preview: [{ question_id: "q1", question_no: "1", scoring_policy: { mode: "AI_ASSIST" }, source: "default", reason: "系统匹配" }] };
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done; }); return { promise, resolve }; }

describe("考试准备冻结复核", () => {
  let host: HTMLDivElement; let root: Root;
  beforeEach(() => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.exam.mockImplementation(async (id: string) => ({ exam: { id, name: id, status: "configured", class_ids: ["class-1"] } }));
    mocks.readiness.mockResolvedValue({ readiness }); mocks.refresh.mockResolvedValue({});
    mocks.roster.mockResolvedValue({ roster: { summary: { expected: 2 }, entries: [{ key: "a", student_id: "a", student_name: "学生甲", student_no: "1001", class_name: "一班" }, { key: "b", student_id: "b", student_name: "学生乙", student_no: "1002", class_name: "一班" }] } });
    mocks.questions.mockResolvedValue({ questions: [{ id: "q1", question_no: "1" }] });
    mocks.profile.mockResolvedValue({ assessment_profile: { scoring_policy: { mode: "AI_ASSIST" } } });
    mocks.confirm.mockResolvedValue({ readiness: { ...readiness, confirmed: true } });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  const render = async (examId = "exam-1", canManage = true) => { await act(async () => root.render(<ExamReadinessPage examId={examId} canManage={canManage} onNavigate={mocks.navigate} onExamChanged={mocks.changed} />)); };
  const click = async (label: string) => { await act(async () => { const button = [...host.querySelectorAll("button")].find((item) => item.textContent === label); expect(button).toBeDefined(); button!.click(); }); };

  it("顶部常驻确认按钮，未通过检查时显示待处理数量和实际任务入口", async () => {
    mocks.readiness.mockResolvedValue({ readiness: { ...readiness, ready: false, checks: [
      { code: "student_scope", label: "学生范围", passed: true, section: "students", message: "已配置" },
      { code: "paper_file", label: "试卷文件", passed: false, section: "paper", message: "缺少登记" },
      { code: "locked_template", label: "答题卡模板", passed: false, section: "template", message: "尚未锁定" },
    ] } });
    await render();
    const action = [...host.querySelectorAll<HTMLButtonElement>(".preparation-heading button")].find((button) => button.textContent === "确认准备完成");
    expect(action?.disabled).toBe(true);
    expect(host.querySelector(".readiness-heading-title")?.textContent).toContain("待处理 2 项");
    expect(host.querySelector(".readiness-action-hint")?.textContent).toContain("请先完成待处理项目，再重新检查");
    expect(host.querySelector(".preparation-heading")?.textContent).toContain("冻结学生名单、题目、答案、评分配置和答题区域");
    expect(host.textContent).not.toContain("还有 2 项准备检查未完成");
    const issues = host.querySelector(".exam-readiness-issues")!;
    const scoring = [...host.querySelectorAll("p")].find((item) => item.textContent?.includes("单独覆盖配置与保存回归"))!;
    expect(issues.compareDocumentPosition(scoring) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(host.textContent).toContain("缺少登记");
    expect(host.textContent).toContain("尚未锁定");
    await act(async () => host.querySelectorAll<HTMLButtonElement>(".exam-readiness-issues button")[1].click());
    expect(mocks.navigate).toHaveBeenCalledWith("/exams/exam-1/template");
    expect(mocks.refresh).not.toHaveBeenCalled();
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("ready 标记不能覆盖仍未通过的检查项目", async () => {
    mocks.readiness.mockResolvedValue({ readiness: { ...readiness, checks: [{ code: "question_scores", label: "小题分值", passed: false, section: "questions", message: "分值待确认" }] } });
    await render();
    const action = [...host.querySelectorAll("button")].find((button) => button.textContent === "确认准备完成");
    expect(action?.disabled).toBe(true);
    await click("确认准备完成");
    expect(mocks.refresh).not.toHaveBeenCalled();
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it.each(["ready", "collecting", "grading", "published", "archived"])("%s 阶段保留匹配当前配置的确认快照，显示已完成且不重新冻结", async (status) => {
    mocks.exam.mockResolvedValue({ exam: { id: "exam-1", name: "Exam", status, class_ids: ["class-1"] } });
    mocks.readiness.mockResolvedValue({ readiness: { ...readiness, confirmed: false, snapshot_id: "snapshot-1", confirmed_at: "2026-10-12T12:00:00Z" } });
    await render();
    expect(host.textContent).toContain("考试准备已完成");
    expect(host.textContent).toContain("匹配的确认记录");
    expect([...host.querySelectorAll("button")].some((button) => button.textContent === "确认准备完成")).toBe(false);
    expect(mocks.refresh).not.toHaveBeenCalled();
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it.each([{ snapshot_id: "snapshot-1" }, { confirmed_at: "2026-10-12T12:00:00Z" }])("不把不完整的快照信息 %j 当成完成记录", async (partial) => {
    mocks.readiness.mockResolvedValue({ readiness: { ...readiness, ...partial } });
    await render();
    expect(host.textContent).not.toContain("考试准备已完成");
    expect([...host.querySelectorAll("button")].find((button) => button.textContent === "确认准备完成")?.disabled).toBe(false);
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("只读账号可看到确认入口，但不能发起名单同步或冻结", async () => {
    await render("exam-1", false);
    expect([...host.querySelectorAll("button")].find((button) => button.textContent === "确认准备完成")?.disabled).toBe(true);
    await click("确认准备完成");
    expect(mocks.refresh).not.toHaveBeenCalled();
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("权限变化后旧确认窗口回调不能读取准备版本或提交冻结", async () => {
    await render(); await click("确认准备完成");
    const oldConfirm = mocks.modalConfirm.getMockImplementation()!;
    const reads = mocks.readiness.mock.calls.length;
    await render("exam-1", false);
    expect([...host.querySelectorAll("button")].find((button) => button.textContent === "确认冻结并完成准备")?.disabled).toBe(true);
    await act(async () => oldConfirm());
    expect(mocks.readiness).toHaveBeenCalledTimes(reads);
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("确认预读期间权限改变时不继续发出冻结写请求", async () => {
    await render(); await click("确认准备完成");
    const latest = deferred<unknown>();
    mocks.readiness.mockReturnValueOnce(latest.promise);
    await click("确认冻结并完成准备");
    await render("exam-1", false);
    await act(async () => latest.resolve({ readiness }));
    expect(mocks.confirm).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("考试已进入后续阶段时旧确认窗口不能重新冻结", async () => {
    await render(); await click("确认准备完成");
    const oldConfirm = mocks.modalConfirm.getMockImplementation()!;
    mocks.exam.mockResolvedValue({ exam: { id: "exam-1", name: "Exam", status: "grading", class_ids: ["class-1"] } });
    await click("重新检查");
    const reads = mocks.readiness.mock.calls.length;
    await act(async () => oldConfirm());
    expect(mocks.readiness).toHaveBeenCalledTimes(reads);
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("服务端确认成功后展示真实已完成记录并刷新工作区，不提供再次确认入口", async () => {
    const confirmed = { ...readiness, confirmed: true, snapshot_id: "snapshot-1", confirmed_at: "2026-10-12T12:00:00Z" };
    mocks.confirm.mockImplementation(async () => {
      mocks.exam.mockResolvedValue({ exam: { id: "exam-1", name: "Exam", status: "ready", class_ids: ["class-1"] } });
      mocks.readiness.mockResolvedValue({ readiness: confirmed });
      return { readiness: confirmed };
    });
    await render(); await click("确认准备完成"); await click("确认冻结并完成准备");
    expect(mocks.confirm).toHaveBeenCalledExactlyOnceWith("exam-1", "reviewed-hash");
    expect(mocks.changed).toHaveBeenCalledOnce();
    expect(host.textContent).toContain("考试准备已完成");
    expect([...host.querySelectorAll("button")].some((button) => button.textContent === "确认准备完成")).toBe(false);
    expect(mocks.success).toHaveBeenCalledWith("考试准备已确认完成");
  });

  it("确认写入成功后回读失败仍保留服务端回执，恢复只读取且不重复确认", async () => {
    const confirmed = { ...readiness, confirmed: true, snapshot_id: "snapshot-1", confirmed_at: "2026-10-12T12:00:00Z" };
    mocks.confirm.mockImplementation(async () => {
      mocks.exam.mockRejectedValueOnce(new Error("状态读取暂不可用")).mockResolvedValue({ exam: { id: "exam-1", name: "Exam", status: "ready", class_ids: ["class-1"] } });
      mocks.readiness.mockResolvedValue({ readiness: confirmed });
      return { readiness: confirmed };
    });
    await render(); await click("确认准备完成"); await click("确认冻结并完成准备");
    expect(mocks.confirm).toHaveBeenCalledOnce();
    expect(mocks.changed).toHaveBeenCalledOnce();
    expect(host.textContent).toContain("考试准备已完成");
    expect(host.textContent).toContain("状态刷新未完成");
    expect(mocks.success).not.toHaveBeenCalled();
    expect([...host.querySelectorAll("button")].some((button) => button.textContent === "确认准备完成")).toBe(false);
    await click("重新检查");
    expect(host.textContent).toContain("考试准备已完成");
    expect(host.textContent).not.toContain("状态刷新未完成");
    expect(mocks.confirm).toHaveBeenCalledOnce();
    expect(mocks.refresh).not.toHaveBeenCalled();
  });

  it.each(["ready", "collecting", "grading", "published", "archived"])("%s 阶段准备记录未确认时禁用确认入口，不允许重新冻结", async (status) => {
    mocks.exam.mockResolvedValue({ exam: { id: "exam-1", name: "exam-1", status, class_ids: ["class-1"] } });
    await render();
    expect([...host.querySelectorAll("button")].find((button) => button.textContent === "确认准备完成")?.disabled).toBe(true);
    expect(host.textContent).toContain("准备配置只读");
    expect(mocks.refresh).not.toHaveBeenCalled(); expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("有效默认方案可确认，窗口只说明冻结效果且不重复读取名单或逐题策略", async () => {
    mocks.profile.mockRejectedValue(new ApiClientError(404, "assessment_profile_not_found", "未找到逐题配置"));
    await render(); await click("确认准备完成");
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.refresh).not.toHaveBeenCalled();
    expect(mocks.roster).not.toHaveBeenCalled(); expect(mocks.questions).not.toHaveBeenCalled(); expect(mocks.profile).not.toHaveBeenCalled();
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain("确认后将冻结本场考试准备");
    expect(host.querySelector('[role="dialog"]')?.querySelector("table")).toBeNull(); expect(host.textContent).not.toContain("学生甲"); expect(host.textContent).toContain("不能直接修改");
    await click("确认冻结并完成准备"); expect(mocks.confirm).toHaveBeenCalledWith("exam-1", "reviewed-hash");
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it.each(["ready", "collecting"] as const)("已确认的 %s 阶段显示对应状态并进入本场导入页面", async (status) => {
    mocks.exam.mockResolvedValue({ exam: { id: "exam/1", name: "Exam", status, class_ids: ["class-1"] } });
    mocks.readiness.mockResolvedValue({ readiness: { ...readiness, confirmed: true } });
    await render("exam/1");
    expect(host.textContent).toContain(status === "ready" ? "准备已确认" : "考试已进入答卷导入阶段");
    expect(host.textContent).not.toContain("已满足准备确认条件");
    expect([...host.querySelectorAll("button")].some((button) => button.textContent === "确认准备完成")).toBe(false);
    await click("前往答卷导入");
    expect(mocks.navigate).toHaveBeenCalledWith("/exams/exam%2F1/capture");
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("真实评分阻断项不能被 ready 标记覆盖，也不能直接调用旧回调确认", async () => {
    await render(); await click("确认准备完成");
    const oldConfirm = mocks.modalConfirm.getMockImplementation()!;
    mocks.readiness.mockResolvedValue({ readiness: { ...readiness, scoring_preview: [{ ...readiness.scoring_preview[0], blocking_reason: "学段未知，请核对年级" }] } });
    await click("重新检查");
    const reads = mocks.readiness.mock.calls.length;
    expect([...host.querySelectorAll("button")].find((button) => button.textContent === "确认准备完成")?.disabled).toBe(true);
    await act(async () => oldConfirm());
    expect(mocks.readiness).toHaveBeenCalledTimes(reads); expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("打开确认窗口时发现新的评分阻断项，更新页面并阻止冻结", async () => {
    await render();
    mocks.readiness.mockResolvedValueOnce({ readiness: { ...readiness, ready: false, scoring_preview: [{ ...readiness.scoring_preview[0], blocking_reason: "学段存在冲突" }] } });
    await click("确认准备完成");
    expect(host.querySelector('[role="dialog"]')).toBeNull(); expect(host.textContent).toContain("准备状态已变化"); expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("复核后配置变化时阻止旧确认并保留已核对内容", async () => {
    await render(); await click("确认准备完成");
    mocks.readiness.mockResolvedValueOnce({ readiness: { ...readiness, configuration_hash: "changed-hash" } });
    await click("确认冻结并完成准备");
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(host.textContent).toContain("考试准备内容已变化");
    expect(host.querySelector('[role="dialog"]')).not.toBeNull(); expect(host.textContent).toContain("确认后将冻结本场考试准备");
  });

  it("冻结请求失败时保留窗口供重试", async () => {
    mocks.confirm.mockRejectedValueOnce(new Error("服务暂时不可用"));
    await render(); await click("确认准备完成"); await click("确认冻结并完成准备");
    expect(host.textContent).toContain("冻结未完成，可重新检查或重试"); expect(host.textContent).toContain("确认后将冻结本场考试准备");
    expect(host.textContent).not.toContain("暂时无法完成冻结核对");
    await click("确认冻结并完成准备"); expect(mocks.confirm).toHaveBeenCalledTimes(2);
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it("关闭后旧窗口确认回调不能冻结，重新核对后新窗口可确认", async () => {
    await render(); await click("确认准备完成");
    const oldConfirm = mocks.modalConfirm.getMockImplementation()!;
    await click("返回修改");
    await act(async () => { oldConfirm(); });
    expect(mocks.confirm).not.toHaveBeenCalled();
    await click("确认准备完成"); await click("确认冻结并完成准备");
    expect(mocks.confirm).toHaveBeenCalledTimes(1);
  });

  it("同一事件重复核对只读取一次准备状态", async () => {
    await render();
    const review = deferred<unknown>(); mocks.readiness.mockReturnValueOnce(review.promise);
    const reads = mocks.readiness.mock.calls.length;
    await act(async () => {
      const button = [...host.querySelectorAll("button")].find((item) => item.textContent === "确认准备完成")!;
      button.click(); button.click();
    });
    expect(mocks.readiness).toHaveBeenCalledTimes(reads + 1);
    await act(async () => review.resolve({ readiness }));
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
  });

  it("确认开始的同一事件中重复确认和旧关闭回调都不能绕过忙碌边界", async () => {
    await render(); await click("确认准备完成");
    const oldConfirm = mocks.modalConfirm.getMockImplementation()!;
    const oldCancel = mocks.modalCancel.getMockImplementation()!;
    const latest = deferred<unknown>(); mocks.readiness.mockReturnValueOnce(latest.promise);
    const readinessReads = mocks.readiness.mock.calls.length;
    await act(async () => { oldConfirm(); oldConfirm(); oldCancel(); });
    expect(mocks.readiness).toHaveBeenCalledTimes(readinessReads + 1);
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
    await act(async () => latest.resolve({ readiness }));
    expect(mocks.confirm).toHaveBeenCalledTimes(1);
    // 成功后旧窗口已失效；保留的旧回调也不能第二次冻结。
    await act(async () => { oldConfirm(); });
    expect(mocks.confirm).toHaveBeenCalledTimes(1);
  });

  it("打开窗口时更新为同次准备检查的版本，确认提交该版本", async () => {
    await render();
    mocks.readiness.mockResolvedValue({ readiness: { ...readiness, configuration_hash: "new-hash" } });
    await click("确认准备完成");
    expect(host.textContent).toContain("确认后将冻结本场考试准备");
    await click("确认冻结并完成准备"); expect(mocks.confirm).toHaveBeenCalledWith("exam-1", "new-hash");
  });

  it("读取准备期间切换考试，旧请求完成不能打开新考试的确认窗口", async () => {
    await render();
    const review = deferred<unknown>(); mocks.readiness.mockReturnValueOnce(review.promise);
    await click("确认准备完成"); await render("exam-2");
    await act(async () => review.resolve({ readiness }));
    expect(mocks.roster).not.toHaveBeenCalled(); expect(mocks.confirm).not.toHaveBeenCalled();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it("切换考试后回到同一考试，旧准备读取不能恢复已丢弃的窗口", async () => {
    await render();
    const review = deferred<unknown>(); mocks.readiness.mockReturnValueOnce(review.promise);
    await click("确认准备完成");
    await render("exam-2"); await render("exam-1");
    await act(async () => review.resolve({ readiness }));
    expect(host.querySelector('[role="dialog"]')).toBeNull(); expect(mocks.confirm).not.toHaveBeenCalled();
    await click("确认准备完成"); await click("确认冻结并完成准备");
    expect(mocks.confirm).toHaveBeenCalledWith("exam-1", "reviewed-hash");
  });
});

// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ExamReadinessPage } from "./ExamPreparationPage";
import { ApiClientError } from "../api/client";

const mocks = vi.hoisted(() => ({ exam: vi.fn(), readiness: vi.fn(), refresh: vi.fn(), confirm: vi.fn(), roster: vi.fn(), questions: vi.fn(), profile: vi.fn(), error: vi.fn(), success: vi.fn(), navigate: vi.fn(), modalConfirm: vi.fn(), modalCancel: vi.fn() }));
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

const readiness = { ready: true, confirmed: false, configuration_hash: "reviewed-hash", checks: [], advisories: [] };
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
  const render = async (examId = "exam-1") => { await act(async () => root.render(<ExamReadinessPage examId={examId} canManage onNavigate={mocks.navigate} />)); };
  const click = async (label: string) => { await act(async () => { const button = [...host.querySelectorAll("button")].find((item) => item.textContent === label); expect(button).toBeDefined(); button!.click(); }); };

  it.each(["ready", "collecting", "grading", "published", "archived"])("%s 阶段准备记录未确认也不显示重新冻结入口", async (status) => {
    mocks.exam.mockResolvedValue({ exam: { id: "exam-1", name: "exam-1", status, class_ids: ["class-1"] } });
    await render();
    expect(host.textContent).not.toContain("核对并冻结考试准备");
    expect(mocks.refresh).not.toHaveBeenCalled(); expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("先同步并展示真实人数、名单和实际逐题策略，二次确认前不冻结", async () => {
    await render(); await click("核对并冻结考试准备");
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.refresh).toHaveBeenCalledWith("exam-1");
    expect(host.textContent).toContain("将冻结 2 名学生"); expect(host.textContent).toContain("学生甲"); expect(host.textContent).toContain("学生乙");
    expect(host.textContent).toContain("AI 建议，教师逐题确认"); expect(host.textContent).toContain("不能直接修改");
    await click("确认冻结并完成准备"); expect(mocks.confirm).toHaveBeenCalledWith("exam-1", "reviewed-hash");
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it.each(["ready", "collecting"] as const)("已确认的 %s 阶段显示对应状态并进入本场导入页面", async (status) => {
    mocks.exam.mockResolvedValue({ exam: { id: "exam/1", name: "Exam", status, class_ids: ["class-1"] } });
    mocks.readiness.mockResolvedValue({ readiness: { ...readiness, confirmed: true } });
    await render("exam/1");
    expect(host.textContent).toContain(status === "ready" ? "准备已确认" : "考试已进入答卷导入阶段");
    expect(host.textContent).not.toContain("已满足准备确认条件");
    expect(host.textContent).not.toContain("核对并冻结考试准备");
    await click("前往答卷导入");
    expect(mocks.navigate).toHaveBeenCalledWith("/exams/exam%2F1/capture");
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("未保存逐题配置时禁用冻结，强制确认回调也不读取新版本或提交", async () => {
    mocks.profile.mockRejectedValueOnce(new ApiClientError(404, "assessment_profile_not_found", "未找到逐题配置"));
    await render(); await click("核对并冻结考试准备");
    expect(host.textContent).toContain("逐题评分方式尚未核实，暂不能冻结");
    expect([...host.querySelectorAll("button")].find((button) => button.textContent === "确认冻结并完成准备")?.disabled).toBe(true);
    const readinessReads = mocks.readiness.mock.calls.length;
    // 直接调用窗口回调，验证 handler 不依赖 DOM 的 disabled 属性来阻止冻结。
    await act(async () => { mocks.modalConfirm(); });
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.readiness).toHaveBeenCalledTimes(readinessReads);
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("先在小题与分值中保存每道题"));
    await click("前往题目配置");
    expect(mocks.navigate).toHaveBeenCalledWith("/exams/exam-1/questions");
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    await click("核对并冻结考试准备"); await click("确认冻结并完成准备");
    expect(mocks.confirm).toHaveBeenCalledWith("exam-1", "reviewed-hash");
  });

  it.each(["FUTURE_SCORING_MODE", "toString"])("无法识别策略 %s 时保留核对窗口并阻止冻结", async (mode) => {
    mocks.profile.mockResolvedValue({ assessment_profile: { scoring_policy: { mode } } });
    await render(); await click("核对并冻结考试准备");
    expect(host.textContent).toContain("逐题评分方式尚未核实，暂不能冻结");
    expect([...host.querySelectorAll("button")].find((button) => button.textContent === "确认冻结并完成准备")?.disabled).toBe(true);
    await act(async () => { mocks.modalConfirm(); });
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("复核后配置变化时阻止旧确认并保留已核对内容", async () => {
    await render(); await click("核对并冻结考试准备");
    mocks.readiness.mockResolvedValueOnce({ readiness: { ...readiness, configuration_hash: "changed-hash" } });
    await click("确认冻结并完成准备");
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(host.textContent).toContain("考试准备内容已变化");
    expect(host.querySelector('[role="dialog"]')).not.toBeNull(); expect(host.textContent).toContain("学生甲");
  });

  it("冻结请求失败时保留名单和策略供重试", async () => {
    mocks.confirm.mockRejectedValueOnce(new Error("服务暂时不可用"));
    await render(); await click("核对并冻结考试准备"); await click("确认冻结并完成准备");
    expect(host.textContent).toContain("冻结未完成，核对内容已保留"); expect(host.textContent).toContain("学生乙");
    expect(host.textContent).not.toContain("暂时无法完成冻结核对");
    await click("确认冻结并完成准备"); expect(mocks.confirm).toHaveBeenCalledTimes(2);
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it("关闭后旧窗口确认回调不能冻结，重新核对后新窗口可确认", async () => {
    await render(); await click("核对并冻结考试准备");
    const oldConfirm = mocks.modalConfirm.getMockImplementation()!;
    await click("返回修改");
    await act(async () => { oldConfirm(); });
    expect(mocks.confirm).not.toHaveBeenCalled();
    await click("核对并冻结考试准备"); await click("确认冻结并完成准备");
    expect(mocks.confirm).toHaveBeenCalledTimes(1);
  });

  it("同一事件重复核对只同步一次名单", async () => {
    const refresh = deferred<unknown>(); mocks.refresh.mockReturnValueOnce(refresh.promise);
    await render();
    await act(async () => {
      const button = [...host.querySelectorAll("button")].find((item) => item.textContent === "核对并冻结考试准备")!;
      button.click(); button.click();
    });
    expect(mocks.refresh).toHaveBeenCalledTimes(1);
    await act(async () => refresh.resolve({}));
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
  });

  it("确认开始的同一事件中重复确认和旧关闭回调都不能绕过忙碌边界", async () => {
    await render(); await click("核对并冻结考试准备");
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

  it("名单与策略读取期间版本变化时不展示混合内容，重新核对后才能冻结", async () => {
    await render();
    mocks.readiness.mockResolvedValueOnce({ readiness }).mockResolvedValueOnce({ readiness: { ...readiness, configuration_hash: "new-hash" } });
    await click("核对并冻结考试准备");
    expect(host.textContent).toContain("读取期间考试准备内容发生变化");
    expect(host.textContent).toContain("暂时无法完成冻结核对");
    expect(host.querySelector('[role="dialog"]')).toBeNull(); expect(mocks.confirm).not.toHaveBeenCalled();
    mocks.readiness.mockResolvedValue({ readiness: { ...readiness, configuration_hash: "new-hash" } });
    await click("核对并冻结考试准备");
    expect(host.textContent).toContain("学生甲");
    await click("确认冻结并完成准备"); expect(mocks.confirm).toHaveBeenCalledWith("exam-1", "new-hash");
  });

  it("同步名单期间切换考试，旧同步完成不能打开新考试的确认窗口", async () => {
    const refresh = deferred<unknown>(); mocks.refresh.mockReturnValueOnce(refresh.promise);
    await render(); await click("核对并冻结考试准备"); await render("exam-2");
    await act(async () => refresh.resolve({}));
    expect(mocks.roster).not.toHaveBeenCalled(); expect(mocks.confirm).not.toHaveBeenCalled();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it("切换考试后回到同一考试，旧策略读取不能恢复已丢弃的窗口", async () => {
    const profile = deferred<unknown>(); mocks.profile.mockReturnValueOnce(profile.promise);
    await render(); await click("核对并冻结考试准备");
    await render("exam-2"); await render("exam-1");
    await act(async () => profile.resolve({ assessment_profile: { scoring_policy: { mode: "AI_ASSIST" } } }));
    expect(host.querySelector('[role="dialog"]')).toBeNull(); expect(mocks.confirm).not.toHaveBeenCalled();
    await click("核对并冻结考试准备"); await click("确认冻结并完成准备");
    expect(mocks.confirm).toHaveBeenCalledWith("exam-1", "reviewed-hash");
  });
});

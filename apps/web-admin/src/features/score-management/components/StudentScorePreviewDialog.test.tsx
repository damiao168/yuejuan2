// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ScoreRelease, StudentScorePreviewResult } from "../../../api/scoreReleases";
import { StudentScorePreviewContent, StudentScorePreviewDialog } from "./StudentScorePreviewDialog";

const mocks = vi.hoisted(() => ({ detail: vi.fn(), preview: vi.fn() }));
vi.mock("../../../api/scoreReleases", () => ({ getScoreReleaseDetail: mocks.detail, getScoreReleaseStudentPreview: mocks.preview }));
vi.mock("../../../api/client", () => ({ getUserErrorMessage: (cause: unknown, fallback: string) => cause instanceof Error ? cause.message : fallback }));
vi.mock("../../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: string }) => <p>{children}</p> }));
vi.mock("../hooks/useScoreReleaseWorkflow", () => ({ visibleReleaseContent: () => ["逐题得分", "题目原文"] }));
vi.mock("antd", () => ({
  Alert: ({ message, description, action }: { message: ReactNode; description: ReactNode; action?: ReactNode }) => <aside>{message}{description}{action}</aside>,
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button onClick={onClick}>{children}</button>,
  Modal: ({ title, children, footer }: { title: ReactNode; children: ReactNode; footer: ReactNode }) => <div role="dialog">{title}{children}{footer}</div>,
  Select: ({ id, value, options, onChange }: { id: string; value: string; options: Array<{ label: string; value: string }>; onChange: (value: string) => void }) => <select id={id} value={value} onChange={(event) => onChange(event.target.value)}>{options.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Spin: ({ tip }: { tip: string }) => <p>{tip}</p>
}));

const release = { id: "release-2", version: 2, status: "draft", visibility_policy: { show_question_scores: true, show_question_stem: true } } as ScoreRelease;
const result = (score: number): StudentScorePreviewResult => ({ exam_id: "exam-1", release_id: "release-2", release_version: 2, total_score: score, max_score: 100, overall_total_score: score, overall_max_score: 100, score_rate: score / 100, appeal_window: { open: false } });
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done; }); return { promise, resolve }; }

describe("冻结成绩学生预览", () => {
  let host: HTMLDivElement; let root: Root;
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.detail.mockResolvedValue({ release, items: [{ student_id: "student-a", total_score: 99 }, { student_id: "student-b", total_score: 88 }], questions: [] });
    mocks.preview.mockResolvedValue({ result: result(73) });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  const render = async () => { await act(async () => root.render(<StudentScorePreviewDialog release={release} currentVersion={1} portalUrl="https://students.example.edu" onClose={() => undefined} />)); };

  it("候选来自指定冻结名单，分数来自真实投影，逐题隐藏时省略详情并注明预览边界", async () => {
    await render();
    expect(mocks.detail).toHaveBeenCalledWith("release-2", expect.any(AbortSignal));
    expect(mocks.preview).toHaveBeenCalledWith("release-2", "student-a", expect.any(AbortSignal));
    expect(host.textContent).toContain("73 / 100 分");
    expect(host.textContent).not.toContain("99 / 100 分");
    expect(host.textContent).toContain("此版本未公开逐题详情");
    expect(host.textContent).toContain("门户当前为 V1");
    expect(host.textContent).toContain("答卷图片、跨科汇总及实际申诉操作");
    expect(host.querySelector("a")?.href).toBe("https://students.example.edu/");
  });

  it("切换学生后迟到的旧请求不能覆盖当前学生成绩", async () => {
    const first = deferred<{ result: StudentScorePreviewResult }>();
    mocks.preview.mockReturnValueOnce(first.promise).mockResolvedValueOnce({ result: result(42) });
    await render();
    expect(host.textContent).toContain("正在读取此学生的冻结成绩");
    await act(async () => { const select = host.querySelector("select")!; select.value = "student-b"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(host.textContent).toContain("42 / 100 分");
    await act(async () => first.resolve({ result: result(97) }));
    expect(host.textContent).toContain("42 / 100 分");
    expect(host.textContent).not.toContain("97 / 100 分");
    expect((mocks.preview.mock.calls[0][2] as AbortSignal).aborted).toBe(true);
  });

  it("只显示服务端实际返回的公开题干、答案和评分要点", async () => {
    mocks.preview.mockResolvedValue({ result: { ...result(73), questions: [{ question_id: "q1", question_no: "1", score: 3, max_score: 5, stem: "已公开题干", correct_answer: "参考内容", rubric_summary: ["公开评分点"] }] } });
    await render();
    expect(host.textContent).toContain("已公开题干"); expect(host.textContent).toContain("参考内容"); expect(host.textContent).toContain("公开评分点");
    expect(host.textContent).not.toContain("本人答案"); expect(host.querySelector("img")).toBeNull();
  });

  it("开放复核的预览展示真实题目申请路径，缺少题目时明确显示矛盾阻断", async () => {
    await act(async () => root.render(<StudentScorePreviewContent result={{ ...result(73), appeal_window: { open: true }, questions: [{ question_id: "q1", question_no: "1", score: 3, max_score: 5 }] }} />));
    expect(host.textContent).toContain("申请本题成绩复核"); expect(host.textContent).toContain("学生在本题详情选择复核原因");
    await act(async () => root.render(<StudentScorePreviewContent result={{ ...result(73), appeal_window: { open: true } }} />));
    expect(host.textContent).toContain("学生当前没有可操作的复核申请入口");
  });

  it("预览失败提供重试，不用冻结列表中的总分替代学生视角", async () => {
    mocks.preview.mockRejectedValueOnce(new Error("无权限或数据读取失败")).mockResolvedValueOnce({ result: result(73) });
    await render();
    expect(host.textContent).toContain("无权限或数据读取失败"); expect(host.textContent).not.toContain("99 / 100 分");
    await act(async () => [...host.querySelectorAll("button")].find((item) => item.textContent === "重试")!.click());
    expect(host.textContent).toContain("73 / 100 分");
  });

  it("非首位学生预览失败后重试保留对象，不重新读取名单或切换到首位", async () => {
    mocks.preview.mockResolvedValueOnce({ result: result(73) }).mockRejectedValueOnce(new Error("第二位学生暂时读取失败")).mockResolvedValueOnce({ result: result(42) });
    await render();
    await act(async () => { const select = host.querySelector("select")!; select.value = "student-b"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(host.textContent).toContain("第二位学生暂时读取失败");
    await act(async () => [...host.querySelectorAll("button")].find((item) => item.textContent === "重试")!.click());
    expect(mocks.detail).toHaveBeenCalledTimes(1);
    expect(mocks.preview).toHaveBeenCalledTimes(3);
    expect(mocks.preview).toHaveBeenLastCalledWith("release-2", "student-b", expect.any(AbortSignal));
    expect(host.querySelector("select")!.value).toBe("student-b");
    expect(host.textContent).toContain("42 / 100 分");
  });

  it("冻结名单读取失败仍可单独重试并进入学生预览", async () => {
    mocks.detail.mockRejectedValueOnce(new Error("名单暂时读取失败")).mockResolvedValueOnce({ release, items: [{ student_id: "student-b" }], questions: [] });
    await render();
    expect(host.textContent).toContain("名单暂时读取失败");
    expect(mocks.preview).not.toHaveBeenCalled();
    await act(async () => [...host.querySelectorAll("button")].find((item) => item.textContent === "重试")!.click());
    expect(mocks.detail).toHaveBeenCalledTimes(2);
    expect(mocks.preview).toHaveBeenLastCalledWith("release-2", "student-b", expect.any(AbortSignal));
    expect(host.textContent).toContain("73 / 100 分");
  });

  it("公开统计保留零值，统计不可用时沿用原因提示且空题目数组不显示隐藏提示", async () => {
    const preview: StudentScorePreviewResult = {
      ...result(0), questions: [],
      reference: { scope: "exam", sample_size: 5, statistics_available: true, mean_score: 0, median_score: 0, rank: 0, percentile: 0, min_score: 0, max_score: 0, q1: 0, q3: 0 }
    };
    await act(async () => root.render(<StudentScorePreviewContent result={preview} />));
    expect(host.textContent).toContain("平均分 0 中位数 0 本版本考试排名 0 百分位 0 最低分 0 最高分 0 下四分位数 0 上四分位数 0");
    expect(host.textContent).not.toContain("此版本未公开逐题详情");
    for (const [reason, text] of [["small_cohort", "人数不足，暂不展示统计和排名"], ["unavailable", "统计数据暂不可用"]]) {
      await act(async () => root.render(<StudentScorePreviewContent result={{ ...preview, reference: { scope: "exam", sample_size: 1, statistics_available: false, unavailable_reason: reason, mean_score: 0 } }} />));
      expect(host.textContent).toContain(text);
      expect(host.textContent).not.toContain("平均分 0");
    }
  });

  it("重复评分要点保持原顺序，前面插入不同要点时原节点仍对应同一条文本", async () => {
    const preview = (items: string[]): StudentScorePreviewResult => ({
      ...result(73), questions: [{ question_id: "q1", question_no: "1", score: 3, max_score: 5, rubric_summary: items }]
    });
    await act(async () => root.render(<StudentScorePreviewContent result={preview(["重复", "其他", "重复"])} />));
    const previous = [...host.querySelectorAll("li")];
    expect(previous.map((item) => item.textContent)).toEqual(["重复", "其他", "重复"]);
    await act(async () => root.render(<StudentScorePreviewContent result={preview(["新增", "重复", "其他", "重复"])} />));
    const current = [...host.querySelectorAll("li")];
    expect(current.map((item) => item.textContent)).toEqual(["新增", "重复", "其他", "重复"]);
    expect(current.slice(1)).toEqual(previous);
    expect(current[1]).toBe(previous[0]);
    expect(current[2]).toBe(previous[1]);
    expect(current[3]).toBe(previous[2]);
  });

  it.each([
    { version: 2, text: "（门户当前版本）" },
    { version: undefined, text: "（门户当前未发布）" },
    { version: 1, text: "（门户当前为 V1）" }
  ])("门户版本提示沿用$text", async ({ version, text }) => {
    await act(async () => root.render(<StudentScorePreviewDialog release={release} currentVersion={version} onClose={() => undefined} />));
    expect(host.textContent).toContain(`正在预览 V2${text}`);
  });
});

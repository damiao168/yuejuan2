// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../../../api/exams";
import { presetVisibility } from "../hooks/useScoreReleaseWorkflow";
import { ScorePublishDialog } from "./ScorePublishDialog";

vi.mock("antd", () => {
  const Radio = Object.assign(({ children, value }: { children: ReactNode; value: string }) => <label data-radio={value}>{children}</label>, {
    Group: ({ children }: { children: ReactNode }) => <div>{children}</div>
  });
  return {
    Radio,
    Modal: ({ open, children, footer }: { open: boolean; children: ReactNode; footer: ReactNode }) => open ? <section>{children}{footer}</section> : null,
    Alert: ({ message, description }: { message: ReactNode; description: ReactNode }) => <div>{message}{description}</div>,
    Button: ({ children, onClick, disabled }: ComponentProps<"button">) => <button type="button" onClick={onClick} disabled={disabled}>{children}</button>,
    Checkbox: ({ children, checked, disabled, onChange }: { children: ReactNode; checked: boolean; disabled?: boolean; onChange: (event: { target: { checked: boolean } }) => void }) =>
      <label><input type="checkbox" checked={checked} disabled={disabled} onChange={(event) => onChange({ target: { checked: event.target.checked } })} />{children}</label>,
    Input: { TextArea: (props: ComponentProps<"textarea"> & { showCount?: boolean }) => { const { showCount: _ignored, ...rest } = props; return <textarea {...rest} />; } },
    InputNumber: ({ id, value, disabled, min, max, step, onChange, "aria-label": label }: { id: string; value: number | null; disabled: boolean; min: number; max: number; step: number; onChange: (value: number | null) => void; "aria-label": string }) => <input id={id} aria-label={label} type="number" value={value ?? ""} disabled={disabled} min={min} max={max} step={step} onChange={event => onChange(event.target.value === "" ? null : Number(event.target.value))} />
  };
});

type Props = ComponentProps<typeof ScorePublishDialog>;
const exam = { id: "exam", name: "期中考试", subject: "math", appeal_enabled: false } as unknown as Exam;

describe("一步发布弹窗", () => {
  let root: Root; let host: HTMLDivElement;
  const workflow = (overrides: Partial<Props["workflow"]> = {}): Props["workflow"] => ({
    publishOpen: true, closePublish: vi.fn(), republish: false, awaitingAuthentication: false, preset: "custom", choosePreset: vi.fn(),
    visibility: presetVisibility("basic", false), setVisibility: vi.fn(), rankReason: "", setRankReason: vi.fn(),
    highScoreTopN: 10, setHighScoreTopN: vi.fn(),
    correctionNote: "", setCorrectionNote: vi.fn(), submitPublish: vi.fn(), previewPublish: vi.fn(), ...overrides
  });
  const render = async (props: Partial<Props> = {}) => {
    await act(async () => root.render(<ScorePublishDialog workflow={workflow()} exam={exam} actioning={null} canWrite educationStage="junior"
      counts={{ expected: 3, received: 2, absent: 1, withScores: 2 }}
      absentees={[{ key: "s3", student_name: "缺考学生", student_no: "003", class_name: "一班", status: "absent", resolution_code: "absent", expected_page_count: 1, actual_page_count: 0 }]} {...props} />));
  };
  const box = (label: string) => [...host.querySelectorAll("label")].find(node => node.textContent === label)?.querySelector<HTMLInputElement>("input");
  beforeEach(() => { Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("写明发给多少人、多少人按缺考计，并可展开缺考名单", async () => {
    await render();
    expect(host.textContent).toContain("将向 2 名学生发布成绩；1 名学生没有匹配答卷，按缺考计。");
    expect(host.querySelector(".score-publish-absentees")?.textContent).toContain("缺考学生 · 003 · 一班");
    expect(host.textContent).toContain("学生将看到：总分");
  });

  it("需要近期验证时明确显示等待身份验证，不继续伪装成发布请求处理中", async () => {
    await render({ actioning: "release-publish", workflow: workflow({ awaitingAuthentication: true }) });
    expect(host.textContent).toContain("请在身份验证窗口输入当前账号密码");
    expect(host.textContent).toContain("等待身份验证");
  });

  // 义务教育学段考试结果不排名、不公布；排名项须先单独打开，不能和其它统计一起顺手勾上。
  it("义务教育学段默认隐藏排名项，高中直接可选", async () => {
    await render();
    expect(box("具体排名")).toBeUndefined();
    expect(box("公开排名或百分位")).toBeDefined();
    await act(async () => box("公开排名或百分位")!.click());
    expect(box("具体排名")).toBeDefined();
    await render({ educationStage: "senior" });
    expect(box("公开排名或百分位")).toBeUndefined();
    expect(box("具体排名")).toBeDefined();
  });

  it("关闭逐题得分时禁用所有依赖内容，群体统计仍可单独选择", async () => {
    await render({ workflow: workflow({ visibility: { ...presetVisibility("basic", false), show_question_scores: false, show_answers: true, show_feedback: true } }) });
    expect(box("教师公开反馈")?.disabled).toBe(true);
    expect(box("参考答案与本人答案")).toBeUndefined();
    expect(box("题目统计")?.disabled).toBe(true);
    expect(box("群体统计")?.disabled).toBe(false);
    expect(box("逐题得分")?.disabled).toBe(false);
  });

  it("公开答案不再作为可选开关，已公开逐题成绩仍准确摘要其答案", async () => {
    await render({ workflow: workflow({ visibility: { ...presetVisibility("basic", false), show_question_scores: true, show_answers: false } }) });
    expect(box("参考答案与本人答案")).toBeUndefined();
    expect(host.textContent).toContain("学生将看到：总分、逐题得分、参考答案与本人答案");
    expect(box("教师公开反馈")).toBeDefined();
  });

  it("已公开排名时要求填写原因，提示依据文件", async () => {
    await render({ workflow: workflow({ visibility: { ...presetVisibility("basic", false), show_exact_rank: true } }) });
    expect(host.textContent).toContain("教基厅函〔2021〕34号");
    expect(host.querySelector("#score-publish-rank-reason")).not.toBeNull();
  });

  it("优秀答卷设置独立于自定义选项，开启时只补齐必要的逐题得分", async () => {
    const actions = workflow({ preset: "basic" });
    await render({ workflow: actions });
    const input = host.querySelector<HTMLInputElement>("#score-publish-excellent-top-n")!;
    expect(input.value).toBe("10");
    expect(input.disabled).toBe(true);
    expect(host.textContent).toContain("截止名次同分全部纳入，实际份数可能更多");
    await act(async () => box("提供优秀答卷")!.click());
    expect(actions.choosePreset).toHaveBeenCalledWith("custom");
    const update = vi.mocked(actions.setVisibility).mock.calls[0][0];
    expect(typeof update === "function" ? update(actions.visibility) : update).toEqual({ ...actions.visibility, show_question_scores: true, show_high_score_paper: true });
  });

  it("名次范围可编辑，不会在自定义逐题选项里出现第二个优秀答卷开关", async () => {
    const actions = workflow({ visibility: { ...presetVisibility("question", false), show_high_score_paper: true }, highScoreTopN: 3 });
    await render({ workflow: actions });
    const input = host.querySelector<HTMLInputElement>("#score-publish-excellent-top-n")!;
    expect(input.value).toBe("3");
    expect(input.disabled).toBe(false);
    expect(box("匿名优秀答卷")).toBeUndefined();
    expect([...host.querySelectorAll("label")].filter(label => label.textContent === "提供优秀答卷")).toHaveLength(1);
  });
});

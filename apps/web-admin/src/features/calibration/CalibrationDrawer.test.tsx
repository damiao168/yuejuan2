// @vitest-environment jsdom
import { act, StrictMode, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CalibrationDrawer } from "./CalibrationDrawer";
import type { CalibrationPolicy, CalibrationSession, PutCalibrationPolicyRequest } from "../../api/calibration";
import type { CalibrationSchoolTemplates } from "./CalibrationSchoolTemplates";
import { ApiClientError } from "../../api/client";

const mocks = vi.hoisted(() => ({
  policy: vi.fn(), qualification: vi.fn(), start: vi.fn(), submit: vi.fn(), savePolicy: vi.fn(), qualified: vi.fn(),
  success: vi.fn(), error: vi.fn(), warning: vi.fn(),
  school: vi.fn(), formChanged: { current: undefined as undefined | (() => void) },
  form: { setFieldsValue: vi.fn(), setFieldValue: vi.fn(), validateFields: vi.fn(), getFieldsValue: vi.fn() }
}));
vi.mock("./CalibrationSchoolTemplates", () => ({ CalibrationSchoolTemplates: (props: Parameters<typeof CalibrationSchoolTemplates>[0]) => { mocks.school(props); return null; } }));
vi.mock("../../api/calibration", () => ({
  getCalibrationPolicy: mocks.policy, getGraderQualification: mocks.qualification,
  createCalibrationSession: mocks.start, submitCalibrationAttempt: mocks.submit, putCalibrationPolicy: mocks.savePolicy
}));
vi.mock("../../api/client", async importOriginal => ({ ...await importOriginal<typeof import("../../api/client")>(), apiClient: { url: (url: string) => url }, getUserErrorMessage: (_error: unknown, fallback: string) => fallback }));
vi.mock("../grading/SharedScoreControl", () => ({
  SharedScoreControl: ({ value, onChange, disabled }: { value: { score: number | null }; onChange: (value: { score: number; rubricSelections: Record<string, number> }) => void; disabled: boolean }) =>
    <input aria-label="校准样本分数" type="number" disabled={disabled} value={value.score ?? ""} onChange={(event) => onChange({ score: Number(event.target.value), rubricSelections: {} })} />
}));
vi.mock("antd", () => {
  const passthrough = ({ children }: { children?: ReactNode }) => <>{children}</>;
  return {
    App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error, warning: mocks.warning } }) },
    Drawer: ({ open, children, extra }: { open: boolean; children: ReactNode; extra: ReactNode }) => open ? <div>{extra}{children}</div> : null,
    Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick: () => void; disabled?: boolean; loading?: boolean }) => <button onClick={onClick} disabled={disabled || loading}>{children}</button>,
    Alert: ({ message, description }: { message: ReactNode; description: ReactNode }) => <div>{message}{description}</div>,
    Form: Object.assign(({ children, onValuesChange }: { children: ReactNode; onValuesChange?: () => void }) => { mocks.formChanged.current = onValuesChange; return <>{children}</>; }, { useForm: () => [mocks.form], Item: passthrough }),
    Input: () => null, Select: () => null, InputNumber: () => null, Space: passthrough, Tag: passthrough, Divider: passthrough,
    Descriptions: Object.assign(passthrough, { Item: passthrough }),
    Empty: Object.assign(({ description }: { description?: ReactNode }) => <div>{description}</div>, { PRESENTED_IMAGE_SIMPLE: null }),
    Progress: () => null,
    List: Object.assign(({ dataSource, renderItem, header }: { dataSource: unknown[]; renderItem: (item: unknown, index: number) => ReactNode; header?: ReactNode }) => <div>{header}{dataSource.map((item, index) => <div key={index}>{renderItem(item, index)}</div>)}</div>, { Item: passthrough })
  };
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
function policy(question = "A", revision = 4): CalibrationPolicy {
  return { id: question, exam_id: "exam", question_id: question, archetype_code: "extended_response", max_score: 10,
    minimum_samples: 3, maximum_mae: 1, minimum_exact_agreement: 0.6, minimum_within_one_agreement: 0.9,
    maximum_severe_rate: 0, severe_error_threshold: 3, qualification_validity_days: 30, revision, created_at: "", updated_at: "" };
}
function session(question = "A"): CalibrationSession {
  return { id: `session-${question}`, exam_id: "exam", question_id: question, grader_id: "grader", exam_question_snapshot_id: "snapshot",
    archetype_code: "extended_response", risk_tier: "R3", gold_set_hash: "hash", status: "in_progress", submitted_count: 0, started_at: "",
    samples: [{ gold_paper_id: `gold-${question}`, submission_id: `answer-${question}`, gold_version: 1,
      max_score: 10, rubric_snapshot: {}, answer_image_url: `/image-${question}` }] };
}

describe("校准异步结果与盲评边界", () => {
  let root: Root;
  let host: HTMLDivElement;
  let props: Parameters<typeof CalibrationDrawer>[0];
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.policy.mockImplementation((_exam: string, question: string) => Promise.resolve({ policy: policy(question) }));
    mocks.qualification.mockResolvedValue({ qualification: undefined });
    mocks.form.getFieldsValue.mockReturnValue({ expected_revision: 4 });
    mocks.formChanged.current = undefined;
    mocks.start.mockResolvedValue({ session: session() });
    host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host);
    props = { open: true, examId: "exam", questionId: "A", graderId: "grader", canManagePolicy: false, onClose: vi.fn(), onQualified: mocks.qualified };
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render(next: Partial<typeof props> = {}) {
    props = { ...props, ...next };
    await act(async () => root.render(<CalibrationDrawer {...props} />));
  }
  function button(text: string) {
    const target = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === text);
    expect(target, text).toBeDefined(); return target!;
  }
  async function click(text: string) { await act(async () => button(text).click()); }
  it("服务端冷却拒绝后显示真实可重考时间并禁止重复请求，到时自动恢复", async () => {
    vi.useFakeTimers({ toFake: ["Date", "setTimeout", "clearTimeout"] });
    try {
      mocks.start.mockRejectedValue(new ApiClientError(429, "calibration_retake_cooldown", "cooling", {}, 90));
      await render();
      await click("开始本题试评");
      expect(host.textContent).toContain("可重新参加时间");
      expect(host.textContent).toContain("上次试评完成24小时后可重考");
      expect(button("开始本题试评").disabled).toBe(true);
      await click("开始本题试评");
      expect(mocks.start).toHaveBeenCalledTimes(1);
      await act(async () => { vi.advanceTimersByTime(90_001); });
      expect(button("开始本题试评").disabled).toBe(false);
    } finally { vi.useRealTimers(); }
  });
  function schoolProps() { return mocks.school.mock.calls[mocks.school.mock.calls.length - 1]![0] as Parameters<typeof CalibrationSchoolTemplates>[0]; }
  it("新建本题标准继承已知作答类型和满分，没有同题依据时留空", async () => {
    mocks.policy.mockResolvedValue({ policy: undefined });
    await render({ canManagePolicy: true, questionDefaults: { questionId: "A", archetypeCode: "numeric_expression", maxScore: 7 } });
    expect(mocks.form.setFieldsValue).toHaveBeenLastCalledWith(expect.objectContaining({ archetype_code: "numeric_expression", max_score: 7 }));
    await render({ questionId: "B" });
    expect(mocks.form.setFieldsValue).toHaveBeenLastCalledWith(expect.objectContaining({ archetype_code: undefined, max_score: undefined }));
  });
  async function score(value: number) {
    const input = host.querySelector<HTMLInputElement>('[aria-label="校准样本分数"]')!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, String(value));
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  it.each([false, true])("未读到标准的说明按管理权限显示，不开放开始试评 canManagePolicy=%s", async (canManagePolicy) => {
    mocks.policy.mockResolvedValue({ policy: undefined });
    await render({ canManagePolicy });
    expect(button("开始本题试评").disabled).toBe(true);
    expect(host.textContent).toContain(canManagePolicy ? "暂未读到本题通过标准。请核对并保存标准，同时准备已批准的标准卷。" : "暂未读到本题通过标准，当前不能开始试评。请联系管理员核对。");
    expect(host.textContent).not.toContain("尚未设置本题通过标准");
    expect(host.textContent).not.toContain("展开上方设置");
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it("切换题目后迟到的旧资格与政策不能覆盖新题目", async () => {
    // A 的请求先开始，B 已显示后 A 才完成；不能恢复 A 的资格或表单版本。
    const oldPolicy = deferred<{ policy: CalibrationPolicy }>();
    const oldQualification = deferred<{ qualification: { status: string; valid_until: string } }>();
    mocks.policy.mockReturnValueOnce(oldPolicy.promise);
    mocks.qualification.mockReturnValueOnce(oldQualification.promise);
    await render(); await render({ questionId: "B" });
    expect(mocks.form.setFieldsValue).toHaveBeenLastCalledWith(expect.objectContaining({ expected_revision: 4 }));
    await act(async () => {
      oldPolicy.resolve({ policy: policy("A", 99) });
      oldQualification.resolve({ qualification: { status: "qualified", valid_until: "2099-01-01" } });
    });
    expect(host.textContent).not.toContain("试评已通过");
    expect(mocks.form.setFieldsValue).toHaveBeenLastCalledWith(expect.objectContaining({ expected_revision: 4 }));
  });

  it("旧的开始请求在关闭并重新打开后不能恢复旧样本", async () => {
    const pending = deferred<{ session: CalibrationSession }>(); mocks.start.mockReturnValueOnce(pending.promise);
    await render(); await click("开始本题试评"); await render({ open: false }); await render({ open: true, questionId: "B" });
    await act(async () => pending.resolve({ session: session("A") }));
    expect(host.querySelector('img[src="/image-A"]')).toBeNull();
    expect(button("开始本题试评").disabled).toBe(false);
  });

  it("提交前不展示参考分，提交失败保留样本与输入", async () => {
    mocks.submit.mockRejectedValueOnce(new Error("network"));
    await render(); await click("开始本题试评"); await score(5);
    expect(host.textContent).not.toContain("参考分 8");
    await click("提交本份试评");
    expect(mocks.submit).toHaveBeenCalledWith("session-A", { gold_paper_id: "gold-A", submitted_score: 5, rubric_selections: {} });
    expect(host.querySelector<HTMLInputElement>('[aria-label="校准样本分数"]')?.value).toBe("5");
    expect(host.querySelector('img[src="/image-A"]')).not.toBeNull();
    expect(mocks.qualified).not.toHaveBeenCalled();
  });

  it("切换题目后旧提交结果不能宣告新题目已具备资格", async () => {
    const pending = deferred<unknown>(); mocks.submit.mockReturnValueOnce(pending.promise);
    await render(); await click("开始本题试评"); await score(5); await click("提交本份试评"); await render({ questionId: "B" });
    await act(async () => pending.resolve({ session: { ...session(), status: "passed" },
      attempt: { submitted_score: 5, reference_score: 8, absolute_error: 3, criterion_differences: [] },
      qualification: { status: "qualified", valid_until: "2099-01-01" } }));
    expect(mocks.qualified).not.toHaveBeenCalled();
    expect(host.textContent).not.toContain("参考分 8");
    expect(host.textContent).not.toContain("试评已通过");
  });

  it("同一题目的并行刷新只接受最后发起的政策版本", async () => {
    const earlier = deferred<{ policy: CalibrationPolicy }>();
    const latest = deferred<{ policy: CalibrationPolicy }>();
    mocks.policy.mockReturnValueOnce(earlier.promise).mockReturnValueOnce(latest.promise);
    // 开发模式的 effect 重放也会产生同题目的重叠请求，后一请求完成后不能再被前一请求覆盖。
    await act(async () => root.render(<StrictMode><CalibrationDrawer {...props} /></StrictMode>));
    expect(mocks.policy).toHaveBeenCalledTimes(2);
    await act(async () => latest.resolve({ policy: policy("A", 6) }));
    await act(async () => earlier.resolve({ policy: policy("A", 5) }));
    expect(mocks.form.setFieldsValue).toHaveBeenLastCalledWith(expect.objectContaining({ expected_revision: 6 }));
  });

  it("保存通过标准保留expected_revision并采用返回的新版本", async () => {
    const values = { archetype_code: "extended_response", max_score: 10, minimum_samples: 3, expected_revision: 4 };
    mocks.form.validateFields.mockResolvedValue(values); mocks.savePolicy.mockResolvedValue({ policy: policy("A", 5) });
    await render({ canManagePolicy: true }); await click("保存通过标准");
    expect(mocks.savePolicy).toHaveBeenCalledWith("exam", "A", values);
    expect(mocks.form.setFieldValue).toHaveBeenCalledWith("expected_revision", 5);
  });

  it("学校标准带入保留来源，人工调整后清除来源身份并保留目标修订", async () => {
    await render({ canManagePolicy: true });
    const props = schoolProps();
    await act(async () => props.onApply({ ...policy(), expected_revision: 4, school_template_id: "template", school_template_revision: 2 }));
    expect(mocks.form.setFieldsValue).toHaveBeenLastCalledWith(expect.objectContaining({ expected_revision: 4, school_template_id: "template", school_template_revision: 2 }));
    expect(schoolProps().hasUnsavedChanges).toBe(true);
    await act(async () => mocks.formChanged.current?.());
    expect(mocks.form.setFieldsValue).toHaveBeenLastCalledWith({ school_template_id: undefined, school_template_revision: undefined });
  });

  it("带入未保存模板后刷新手动策略，保存只使用刷新后的阈值与来源", async () => {
    // Ant Design以合并方式更新字段；漏掉undefined清空会让隐藏模板身份继续为另一组阈值背书。
    let fields: Partial<PutCalibrationPolicyRequest> = {};
    mocks.form.setFieldsValue.mockImplementation((next: Partial<PutCalibrationPolicyRequest>) => { fields = { ...fields, ...next }; });
    mocks.form.getFieldsValue.mockImplementation(() => fields);
    mocks.form.validateFields.mockImplementation(async () => fields);
    mocks.savePolicy.mockResolvedValue({ policy: policy("A", 5) });
    await render({ canManagePolicy: true });
    await act(async () => schoolProps().onApply({ ...policy(), expected_revision: 4, maximum_mae: 0, school_template_id: "template", school_template_revision: 1 }));
    expect(fields.school_template_id).toBe("template");
    await click("刷新"); await click("保存通过标准");
    expect(mocks.savePolicy).toHaveBeenCalledWith("exam", "A", expect.objectContaining({ maximum_mae: 1, expected_revision: 4, school_template_id: undefined, school_template_revision: undefined }));
  });

  it("重新打开已采用学校模板的策略，原样保存保留正式来源", async () => {
    const saved = { ...policy(), school_template_id: "approved-template", school_template_revision: 1 };
    let fields: Partial<PutCalibrationPolicyRequest> = {};
    mocks.form.setFieldsValue.mockImplementation((next: Partial<PutCalibrationPolicyRequest>) => { fields = { ...fields, ...next }; });
    mocks.form.validateFields.mockImplementation(async () => fields);
    mocks.policy.mockResolvedValue({ policy: saved }); mocks.savePolicy.mockResolvedValue({ policy: { ...saved, revision: 5 } });
    await render({ canManagePolicy: true }); await click("保存通过标准");
    expect(mocks.savePolicy).toHaveBeenCalledWith("exam", "A", expect.objectContaining({ expected_revision: 4, school_template_id: "approved-template", school_template_revision: 1 }));
  });

  it("模板读取和管理期间不能同时保存本题或开始试评，八项规则默认收在按题调整", async () => {
    await render({ canManagePolicy: true });
    const props = schoolProps();
    const advanced = [...host.querySelectorAll("details")].find(item => item.querySelector("summary")?.textContent === "按题调整通过标准");
    expect(advanced?.open).toBe(false);
    await act(async () => props.onWorkingChange(true));
    expect(button("保存通过标准").disabled).toBe(true); expect(button("开始本题试评").disabled).toBe(true);
    await click("保存通过标准"); expect(mocks.savePolicy).not.toHaveBeenCalled();
    await act(async () => props.onWorkingChange(false));
    expect(button("保存通过标准").disabled).toBe(false);
  });

  it("同一份提交不并发发送，提交后只显示本人评分而不泄露参考答案", async () => {
    const pending = deferred<unknown>(); mocks.submit.mockReturnValueOnce(pending.promise);
    await render(); await click("开始本题试评"); await score(5);
    await act(async () => { button("提交本份试评").click(); button("提交本份试评").click(); });
    expect(mocks.submit).toHaveBeenCalledOnce(); expect(host.textContent).not.toContain("参考分 8");
    await act(async () => pending.resolve({ session: { ...session(), status: "passed", submitted_count: 1 },
      attempt: { submitted_score: 5, reference_score: 8, absolute_error: 3, criterion_differences: [] },
      qualification: { status: "qualified", valid_until: "2099-01-01" } }));
    expect(host.textContent).toContain("你的评分 5"); expect(host.textContent).not.toContain("参考分 8"); expect(host.textContent).not.toContain("相差 3 分"); expect(mocks.qualified).toHaveBeenCalledOnce();
  });

  it.each(["passed", "failed"] as const)("完成且有统计数据的 %s 校准显示结果，并只允许失败场景重做", async (status) => {
    mocks.submit.mockResolvedValueOnce({
      session: { ...session(), status, completed_at: new Date().toISOString(), submitted_count: 1, metrics: { sample_count: 1, criterion_sample_count: 1, mae: 0.5, exact_agreement: 1, within_one_agreement: 1, criterion_agreement: 1, severe_disagreement_rate: 0 } },
      attempt: { submitted_score: 5, reference_score: 5, absolute_error: 0, exact_match: true, severe_disagreement: false, criterion_differences: [] }
    });
    await render(); await click("开始本题试评"); await score(5);
    expect(host.textContent).not.toContain("参考分 5");
    await click("提交本份试评");
    expect(host.textContent).toContain("你的评分 5");
    expect(host.textContent).not.toContain("参考分 5");
    expect(host.textContent).toContain("0.50");
    expect(host.textContent).toContain(status === "passed" ? "试评通过，已达到本题阅卷要求" : "试评未通过，请核对整体一致性结果并按重考要求重新试评");
    const restart = [...host.querySelectorAll("button")].find((entry) => entry.textContent === "重新试评");
    expect(Boolean(restart)).toBe(status === "failed");
    if (restart) {
      expect(restart.disabled).toBe(true);
      expect(host.textContent).toContain("可重新参加时间");
    }
    expect(mocks.qualified).not.toHaveBeenCalled();
  });
});

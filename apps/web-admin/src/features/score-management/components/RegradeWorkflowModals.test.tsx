// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { RegradeWorkflowModals } from "./RegradeWorkflowModals";

// antd 只换成原生元素，用来核对按钮是否可达、是否禁用以及回调参数；不模拟 antd 自身的交互。
vi.mock("antd", () => {
  const List = Object.assign(({ dataSource, renderItem, locale }: { dataSource: unknown[]; renderItem: (value: unknown) => ReactNode; locale?: { emptyText: string } }) =>
    <div>{dataSource.length ? dataSource.map(renderItem) : locale?.emptyText}</div>, {
    Item: Object.assign(({ actions, children }: { actions: ReactNode[]; children: ReactNode }) => <article>{children}{actions}</article>, {
      Meta: ({ title, description }: { title: ReactNode; description: ReactNode }) => <div>{title}{description}</div>
    })
  });
  const Input = Object.assign(() => null, {
    TextArea: ({ value, onChange }: ComponentProps<"textarea">) => <textarea value={value} onChange={onChange} />
  });
  return {
    List, Input,
    Modal: ({ open, title, children, footer, okText, cancelText, okButtonProps, onOk, onCancel }: {
      open: boolean; title: string; children: ReactNode; footer?: ReactNode; okText?: string; cancelText?: string;
      okButtonProps?: { disabled?: boolean }; onOk?: () => void; onCancel?: () => void;
    }) => open ? <section aria-label={title}>{children}{footer ?? <footer>
      <button type="button" onClick={onCancel}>{cancelText}</button>
      <button type="button" disabled={okButtonProps?.disabled} onClick={onOk}>{okText}</button>
    </footer>}</section> : null,
    Alert: ({ message, description, action }: { message: ReactNode; description?: ReactNode; action?: ReactNode }) => <div role="alert">{message}{description}{action}</div>,
    Button: ({ children, onClick, disabled }: ComponentProps<"button">) => <button type="button" onClick={onClick} disabled={disabled}>{children}</button>,
    Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
    InputNumber: () => null,
    Select: ({ options, value, placeholder, onChange }: { options?: Array<{ value: string; label: string }>; value?: string; placeholder?: string; onChange?: (value: string) => void }) =>
      <select aria-label={placeholder} value={value ?? ""} onChange={event => onChange?.(event.target.value)}>
        <option value="" />{(options ?? []).map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
  };
});
vi.mock("./RegradeCandidateEvidence", () => ({ RegradeCandidateEvidence: () => <span>当前修订证据</span> }));

type Props = ComponentProps<typeof RegradeWorkflowModals>;
type Workflow = Props["regradeWorkflow"];

// 关闭的弹窗也会构造 JSX，默认值只补齐渲染需要读取的字段。
function workflow(overrides: Record<string, unknown>): Workflow {
  return {
    regradeReviewScores: {}, regradeReviewNotes: {}, reviewedEvidence: {}, regradeReviewConflicts: {},
    regradeReasonText: "", regradeAssigneeID: "", regradeQuestionOptions: [], regradeGraderOptions: [],
    ...overrides
  } as unknown as Workflow;
}

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

async function render(regradeWorkflow: Workflow, props: Partial<Props> = {}) {
  await act(async () => root.render(<RegradeWorkflowModals publishedReleaseExists actioning={null} regradeWorkflow={regradeWorkflow} {...props} />));
}
const decisionButtons = (scope: ParentNode = host) => [...scope.querySelectorAll<HTMLButtonElement>("article > button")];
const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find(value => value.textContent === label)!;

it("异常候选仍出现在管理复核界面，接受/驳回及处理说明均可达", async () => {
  const decide = vi.fn();
  const item = { id: "exception", status: "exception", revision: 8, review_note: "原异常说明", max_score: 5 };
  await render(workflow({
    regradeReviewOpen: true, regradeReview: { items: [item, { id: "resolved", status: "resolved" }] },
    regradeReviewNotes: { exception: "新处理说明" }, reviewedEvidence: { exception: 8 }, decideRegradeItem: decide
  }));
  expect(host.textContent).toContain("异常待处理");
  expect(host.textContent).toContain("原异常说明");
  expect(host.textContent).toContain("处理异常必填");
  expect(host.querySelector("textarea")?.value).toBe("新处理说明");
  expect(host.querySelectorAll("article")).toHaveLength(1);
  const buttons = decisionButtons();
  expect(buttons.map(value => value.textContent)).toEqual(["接受", "驳回"]);
  await act(async () => buttons[0].click());
  expect(decide).toHaveBeenCalledWith(item, "accept");
  await act(async () => buttons[1].click());
  expect(decide).toHaveBeenCalledWith(item, "reject");
});

it("未核对当前修订材料或存在修订冲突时，三个复核决定都不可提交", async () => {
  const item = { id: "item", status: "awaiting_review", revision: 3, max_score: 5 };
  const resolve = vi.fn();
  const base = { regradeReviewOpen: true, regradeReview: { items: [item] }, resolveReviewConflict: resolve };
  await render(workflow({ ...base, reviewedEvidence: { item: 2 } }));
  expect(decisionButtons().map(value => [value.textContent, value.disabled])).toEqual([["接受", true], ["驳回", true], ["标记异常", true]]);

  await render(workflow({ ...base, reviewedEvidence: { item: 3 }, regradeReviewConflicts: { item: 3 } }));
  expect(decisionButtons().every(value => value.disabled)).toBe(true);
  expect(host.textContent).toContain("候选已更新，本页修改已保留");
  await act(async () => button("保留本页修改").click());
  await act(async () => button("重置为新候选").click());
  expect(resolve.mock.calls).toEqual([["item", true], ["item", false]]);

  await render(workflow({ ...base, reviewedEvidence: { item: 3 } }));
  expect(decisionButtons().every(value => !value.disabled)).toBe(true);
  await render(workflow({ ...base, reviewedEvidence: { item: 3 } }), { actioning: "regrade-review-item" });
  expect(decisionButtons().every(value => value.disabled)).toBe(true);
});

it("发起复评须先预览并填写原因和阅卷员；换题或取消都清除旧预览", async () => {
  const actions = { closeRegradeModal: vi.fn(), clearRegradePreview: vi.fn(), setRegradeQuestionId: vi.fn(), createSelectedRegrade: vi.fn(), previewSelectedRegrade: vi.fn() };
  const base = {
    ...actions, regradeModalOpen: true, regradeQuestionId: "q1", regradeReasonCode: "quality_incident", regradeStrategy: "human_recheck",
    regradeQuestionOptions: [{ value: "q1", label: "1 · 满分 5" }, { value: "q2", label: "2 · 满分 10" }],
    regradeReasonText: "评分细则写错", regradeAssigneeID: "grader"
  };
  await render(workflow({ ...base, regradePreview: null }));
  expect(button("创建复评任务").disabled).toBe(true);
  expect(button("预览影响范围").disabled).toBe(false);
  await act(async () => button("预览影响范围").click());
  expect(actions.previewSelectedRegrade).toHaveBeenCalledTimes(1);

  const preview = { question_id: "q1", source_release_id: "release", source_release_version: 2, affected_count: 7 };
  await render(workflow({ ...base, regradePreview: preview }));
  expect(host.textContent).toContain("来源：正式成绩 V2 · 影响 7 份答卷");
  expect(host.textContent).toContain("须到成绩发布页发布更正");
  expect(button("创建复评任务").disabled).toBe(false);
  await act(async () => button("创建复评任务").click());
  expect(actions.createSelectedRegrade).toHaveBeenCalledTimes(1);
  for (const missing of [{ regradeReasonText: "  " }, { regradeAssigneeID: "" }]) {
    await render(workflow({ ...base, regradePreview: preview, ...missing }));
    expect(button("创建复评任务").disabled).toBe(true);
  }

  await render(workflow({ ...base, regradePreview: preview }));
  const question = host.querySelector<HTMLSelectElement>('select[aria-label="选择题目"]')!;
  await act(async () => { question.value = "q2"; question.dispatchEvent(new Event("change", { bubbles: true })); });
  expect(actions.setRegradeQuestionId).toHaveBeenCalledWith("q2");
  expect(actions.clearRegradePreview).toHaveBeenCalledTimes(1);
  await act(async () => button("取消").click());
  expect(actions.closeRegradeModal).toHaveBeenCalledTimes(1);
  expect(actions.clearRegradePreview).toHaveBeenCalledTimes(2);

  await render(workflow({ ...base, regradePreview: null }), { publishedReleaseExists: false });
  expect(button("预览影响范围").disabled).toBe(true);
});

// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewTask } from "../../../../api/review";
import type { AssignmentBatch } from "../assignmentPlan";
import { AssignmentPreview } from "./AssignmentPreview";

const controls = vi.hoisted(() => ({
  confirm: null as null | (() => Promise<void>), cancel: null as null | (() => void)
}));

vi.mock("antd", () => ({
  Modal: (props: {
    title: string; children: ReactNode; confirmLoading: boolean; closable: boolean; maskClosable: boolean; keyboard: boolean;
    okText: string; okButtonProps: { disabled: boolean }; cancelButtonProps: { disabled: boolean };
    onOk: () => Promise<void>; onCancel: () => void;
  }) => {
    controls.confirm = props.onOk;
    controls.cancel = props.onCancel;
    return <div role="dialog" aria-label={props.title} data-loading={props.confirmLoading} data-mask-closable={props.maskClosable} data-keyboard={props.keyboard}>
      {props.children}
      <button type="button" aria-label="关闭" disabled={!props.closable} onClick={props.onCancel}>×</button>
      <button type="button" disabled={props.okButtonProps.disabled} onClick={() => void props.onOk()}>{props.okText}</button>
      <button type="button" disabled={props.cancelButtonProps.disabled} onClick={props.onCancel}>取消</button>
    </div>;
  },
  Select: (props: {
    id: string; "aria-label": string; disabled: boolean; value: string[];
    options: Array<{ value: string; label: string }>; onChange: (value: string[]) => void;
  }) => <select id={props.id} multiple aria-label={props["aria-label"]} disabled={props.disabled} value={props.value}
    onChange={event => props.onChange(Array.from(event.currentTarget.selectedOptions, option => option.value))}>
    {props.options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
  </select>,
  Segmented: (props: {
    "aria-label": string; disabled: boolean; value: string; options: Array<{ label: string; value: string }>;
    onChange: (value: string) => void;
  }) => <div role="group" aria-label={props["aria-label"]}>{props.options.map(option => <button key={option.value} type="button" disabled={props.disabled}
    aria-pressed={props.value === option.value} onClick={() => props.onChange(option.value)}>{option.label}</button>)}</div>,
  InputNumber: (props: {
    "aria-label": string; disabled: boolean; value: number | null; min: number; max: number; step: number;
    onChange: (value: number | null) => void;
  }) => <input type="number" aria-label={props["aria-label"]} disabled={props.disabled} min={props.min} max={props.max} step={props.step} value={props.value ?? ""}
    onChange={event => props.onChange(event.target.value === "" ? null : Number(event.target.value))} />
}));

type Props = Parameters<typeof AssignmentPreview>[0];
function task(id: string, question: string): ReviewTask {
  return { id, exam_id: "exam", exam_name: "期中", question_id: `question-${question}`, question_no: question, subject: "math", status: "pending", revision: 3 } as ReviewTask;
}
function fixture(): Props {
  return {
    tasks: [task("12-b", "12"), task("2", "2"), task("12-a", "12"), task("10", "10")], defaultGrader: "a",
    options: [{ value: "a", label: "教师甲" }, { value: "b", label: "教师乙" }], unassignedCount: 4, busy: false,
    onCancel: vi.fn(), onConfirm: vi.fn(async () => false)
  };
}
function chooseTeachers(ids: string[]) {
  const select = screen.getByRole<HTMLSelectElement>("listbox", { name: "统一选择阅卷教师" });
  Array.from(select.options).forEach(option => { option.selected = ids.includes(option.value); });
  fireEvent.change(select);
}
function setCount(name: string, value: string) { fireEvent.change(screen.getByRole("spinbutton", { name: `${name}分配数量` }), { target: { value } }); }
function confirmButton() { return screen.getByRole<HTMLButtonElement>("button", { name: /确认分配/ }); }
const identities = (plan: AssignmentBatch[]) => plan.map(batch => [batch.graderId, batch.tasks.map(item => item.id)]);

describe("统一安排教师预览", () => {
  beforeEach(() => { Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); });
  afterEach(cleanup);

  it("只统一选一次教师，按自然题号展示范围，一位教师接收全部任务", async () => {
    const props = fixture();
    const { container } = render(<AssignmentPreview {...props} />);
    expect(screen.getByText("已选 3 道题 · 4 份答卷")).toBeTruthy();
    expect(Array.from(container.querySelectorAll(".grading-assignment-preview-question"), item => item.textContent)).toEqual(["第 2 题1 份", "第 10 题1 份", "第 12 题2 份"]);
    expect(screen.getAllByRole("listbox")).toHaveLength(1);
    expect(container.querySelector(".grading-assignment-preview-teacher")?.textContent).toBe("教师甲4 份");
    await act(async () => { fireEvent.click(confirmButton()); });
    const plan = (props.onConfirm as ReturnType<typeof vi.fn>).mock.calls[0][0] as AssignmentBatch[];
    expect(identities(plan)).toEqual([["a", ["2", "10", "12-b", "12-a"]]]);
    plan[0].tasks.forEach(item => expect(item).toBe(props.tasks.find(original => original.id === item.id)));
    expect(props.onCancel).not.toHaveBeenCalled();
  });

  it("未选教师不能确认，多教师跨题均分且预览每人的数量", async () => {
    const props = { ...fixture(), defaultGrader: "", tasks: [19, 14, 17, 15, 18, 16].map(no => task(String(no), String(no))) };
    const { container } = render(<AssignmentPreview {...props} />);
    expect(confirmButton().disabled).toBe(true);
    chooseTeachers(["a", "b"]);
    expect(Array.from(container.querySelectorAll(".grading-assignment-preview-teacher"), item => item.textContent)).toEqual(["教师甲3 份", "教师乙3 份"]);
    await act(async () => { fireEvent.click(confirmButton()); });
    expect(identities((props.onConfirm as ReturnType<typeof vi.fn>).mock.calls[0][0])).toEqual([["a", ["14", "16", "18"]], ["b", ["15", "17", "19"]]]);
  });

  it("自定义数量先填入当前均分数，调整后按指定份数提交完整计划", async () => {
    const props = fixture();
    render(<AssignmentPreview {...props} />);
    chooseTeachers(["a", "b"]);
    fireEvent.click(screen.getByRole("button", { name: "自定义数量" }));
    expect(screen.getByRole<HTMLInputElement>("spinbutton", { name: "教师甲分配数量" }).value).toBe("2");
    expect(screen.getByRole<HTMLInputElement>("spinbutton", { name: "教师乙分配数量" }).value).toBe("2");
    setCount("教师甲", "1"); setCount("教师乙", "3");
    expect(screen.getByText("已填写 4 / 4 份")).toBeTruthy();
    expect(confirmButton().disabled).toBe(false);
    await act(async () => { fireEvent.click(confirmButton()); });
    expect(identities((props.onConfirm as ReturnType<typeof vi.fn>).mock.calls[0][0])).toEqual([["a", ["2"]], ["b", ["10", "12-b", "12-a"]]]);
  });

  it.each([
    ["3", "合计必须等于待分配任务数"], ["5", "合计必须等于待分配任务数"],
    ["-1", "非负安全整数"], ["0.5", "非负安全整数"], ["", "请填写每位教师的分配数量"]
  ])("自定义数量为%s时阻止无效提交并显示原因", async (value, error) => {
    const props = fixture();
    render(<AssignmentPreview {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "自定义数量" }));
    setCount("教师甲", value);
    expect(screen.getByRole("alert").textContent).toContain(error);
    expect(confirmButton().disabled).toBe(true);
    await act(async () => { await controls.confirm!(); });
    expect(props.onConfirm).not.toHaveBeenCalled();
  });

  it("自定义模式移除教师后不分配给旧教师，剩余数量仍须覆盖全部任务", async () => {
    const props = fixture();
    render(<AssignmentPreview {...props} />);
    chooseTeachers(["a", "b"]);
    fireEvent.click(screen.getByRole("button", { name: "自定义数量" }));
    chooseTeachers(["b"]);
    expect(screen.queryByRole("spinbutton", { name: "教师甲分配数量" })).toBeNull();
    expect(confirmButton().disabled).toBe(true);
    setCount("教师乙", "4");
    await act(async () => { fireEvent.click(confirmButton()); });
    expect(identities((props.onConfirm as ReturnType<typeof vi.fn>).mock.calls[0][0])).toEqual([["b", ["2", "10", "12-b", "12-a"]]]);
  });

  it("父级忙碌时禁止确认、取消、关闭和所有配置修改", async () => {
    const props = fixture();
    const { rerender } = render(<AssignmentPreview {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "自定义数量" }));
    rerender(<AssignmentPreview {...props} busy />);
    for (const element of [...screen.getAllByRole<HTMLButtonElement>("button"), screen.getByRole<HTMLSelectElement>("listbox"), screen.getByRole<HTMLInputElement>("spinbutton")]) expect(element.disabled).toBe(true);
    expect(screen.getByRole("dialog").getAttribute("data-mask-closable")).toBe("false");
    expect(screen.getByRole("dialog").getAttribute("data-keyboard")).toBe("false");
    await act(async () => { controls.cancel!(); await controls.confirm!(); });
    expect(props.onCancel).not.toHaveBeenCalled(); expect(props.onConfirm).not.toHaveBeenCalled();
  });

  it("父级busy尚未回传时也拦住同帧重复提交与取消，成功后只关闭一次", async () => {
    let finish!: (success: boolean) => void;
    const props = { ...fixture(), onConfirm: vi.fn(() => new Promise<boolean>(resolve => { finish = resolve; })) };
    render(<AssignmentPreview {...props} />);
    let pending!: Promise<void>;
    // 同帧调用捕获的旧回调，模拟父级状态尚未重绘时的连续确认与取消。
    const confirm = controls.confirm!, cancel = controls.cancel!;
    act(() => { pending = confirm(); void confirm(); cancel(); });
    expect(props.onConfirm).toHaveBeenCalledOnce(); expect(props.onCancel).not.toHaveBeenCalled();
    expect(confirmButton().disabled).toBe(true);
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "取消" }).disabled).toBe(true);
    await act(async () => { finish(true); await pending; });
    expect(props.onCancel).toHaveBeenCalledOnce();
  });

  it("提交异常后保留预览并解锁重试与取消", async () => {
    const props = { ...fixture(), onConfirm: vi.fn(async () => { throw new Error("synthetic failure"); }) };
    render(<AssignmentPreview {...props} />);
    await act(async () => { fireEvent.click(confirmButton()); });
    expect(screen.getByRole("alert").textContent).toBe("分配失败，请稍后重试");
    expect(confirmButton().disabled).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(props.onCancel).toHaveBeenCalledOnce();
  });
});

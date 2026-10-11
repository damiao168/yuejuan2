// @vitest-environment jsdom
import { act, type ChangeEventHandler, type MouseEventHandler, type ReactNode, type TextareaHTMLAttributes } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import type { SchoolAssistantResult as AssistantResult, SchoolAssistantSource } from "../../api/schoolAssistant";
import { useSchoolAssistantContext } from "./SchoolAssistantProvider";
import { SchoolAssistantPanel } from "./SchoolAssistantPanel";

type AssistantValue = NonNullable<ReturnType<typeof useSchoolAssistantContext>>;
const mocks = vi.hoisted(() => ({ assistant: null as AssistantValue | null, listExams: vi.fn() }));
vi.mock("../../api/exams", () => ({ listAllExams: mocks.listExams }));
vi.mock("./SchoolAssistantProvider", () => ({ useSchoolAssistantContext: () => mocks.assistant }));
vi.mock("./SchoolAssistantResult", () => ({
  AssistantSources: ({ sources }: { sources: SchoolAssistantSource[] }) => <div data-sources>{sources.map((source) => source.label).join(",")}</div>,
  SchoolAssistantResult: ({ result }: { result: AssistantResult }) => <div data-result>{result.request_id}</div>
}));
vi.mock("antd", () => ({
  Alert: ({ message, description, action }: { message: ReactNode; description?: ReactNode; action?: ReactNode }) => <div data-alert>{message}{description}{action}</div>,
  Button: ({ children, onClick, disabled, htmlType = "button" }: { children: ReactNode; onClick?: MouseEventHandler<HTMLButtonElement>; disabled?: boolean; htmlType?: "button" | "submit" }) => <button type={htmlType} onClick={onClick} disabled={disabled}>{children}</button>,
  Input: { TextArea: ({ autoSize: _autoSize, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement> & { autoSize: unknown }) => <textarea {...props} /> },
  Radio: {
    Group: ({ children, onChange }: { children: ReactNode; onChange: ChangeEventHandler<HTMLDivElement> }) => <div onChange={onChange}>{children}</div>,
    Button: ({ children, value, disabled }: { children: ReactNode; value: string; disabled: boolean }) => <label><input type="radio" name="task" value={value} disabled={disabled} />{children}</label>
  },
  Select: () => <select aria-label="选择学科考试" />,
  Spin: () => <span data-spin />,
  Tooltip: ({ children }: { children: ReactNode }) => <span>{children}</span>
}));

const result: AssistantResult = { request_id: "result-1", intent: "exam_progress", status: "completed", scope: { class_ids: [], data_basis: "exam_live", scope_label: "考试" }, facts: [], blocks: [], sources: [], actions: [], limitations: [] };

describe("学校助手展示拆分及表单语义", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.listExams.mockReset().mockResolvedValue({ exams: [] });
    mocks.assistant = {
      user: { permissions: ["exam:manage"] } as SessionUser,
      context: { exam_id: "exam", exam_name: "测试考试" }, setContext: vi.fn(),
      query: { status: "idle", sources: [], progress: "", error: "" },
      capabilities: { mode: "school_business", model_required: false, intents: [
        { intent: "exam_progress", label: "进度", available: true, required_permission: "exam:manage" },
        { intent: "publish_diagnosis", label: "发布", available: true, required_permission: "exam:manage" }
      ] },
      capabilitiesLoading: false, capabilitiesError: "", refreshCapabilities: vi.fn(), run: vi.fn().mockResolvedValue(undefined), cancel: vi.fn(), retry: vi.fn(), open: vi.fn(), navigate: vi.fn()
    };
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render() { await act(async () => root.render(<SchoolAssistantPanel compact />)); }
  async function click(label: string) {
    const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label)!;
    await act(async () => button.click());
  }

  it("用组标题标记任务选项，并沿用空问题的预设任务提交", async () => {
    await render();
    const group = host.querySelector("fieldset.school-assistant-task")!;
    expect(group.querySelector("legend")?.textContent).toBe("查询任务");
    expect(group.querySelectorAll('input[type="radio"]')).toHaveLength(2);
    await click("发送查询");
    expect(mocks.assistant!.run).toHaveBeenLastCalledWith("exam_progress", "查看考试进度");
    await act(async () => group.querySelector<HTMLInputElement>('input[value="publish_diagnosis"]')!.click());
    await click("发送查询");
    expect(mocks.assistant!.run).toHaveBeenLastCalledWith("publish_diagnosis", "为什么不能发布");
    expect(mocks.listExams).not.toHaveBeenCalled();
  });

  it("保留查询进行中、取消、失败和完成的展示与操作", async () => {
    mocks.assistant!.query = { status: "running", sources: [], progress: "正在核实", error: "" };
    await render();
    expect(host.querySelector("output.school-assistant-progress")?.textContent).toBe("正在核实");
    expect(host.querySelector(".school-assistant-answer")?.getAttribute("aria-live")).toBe("polite");
    expect(host.querySelector<HTMLTextAreaElement>("textarea")!.disabled).toBe(true);
    await click("停止查询");
    expect(mocks.assistant!.cancel).toHaveBeenCalledOnce();
    mocks.assistant!.query = { ...mocks.assistant!.query, status: "cancelled" };
    await render();
    expect(host.querySelector("output")).toBeNull();
    expect(host.textContent).toContain("已停止查询");
    mocks.assistant!.query = { ...mocks.assistant!.query, status: "error", error: "读取失败" };
    await render();
    expect(host.textContent).toContain("读取失败");
    await click("重新查询");
    mocks.assistant!.query = { ...mocks.assistant!.query, status: "completed", result };
    await render();
    expect(host.querySelector("[data-result]")?.textContent).toBe("result-1");
    expect(host.querySelector("[data-sources]")).toBeNull();
    await click("重新核实");
    expect(mocks.assistant!.retry).toHaveBeenCalledTimes(2);
  });

  it("任务不可用时禁用查询，缺少授权上下文时只显示提示", async () => {
    mocks.assistant!.capabilities!.intents[0].available = false;
    await render();
    expect([...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === "发送查询")!.disabled).toBe(true);
    mocks.assistant = null;
    await render();
    expect(host.textContent).toBe("学校业务助手仅对学校管理员开放");
    expect(host.querySelector("form")).toBeNull();
  });
});

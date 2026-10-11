// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../api/businessCommand";
import { BackmarkQueue } from "./BackmarkQueue";
import { RegradeQueue } from "./RegradeQueue";

const mocks = vi.hoisted(() => ({
  confirm: vi.fn(), warning: vi.fn(), error: vi.fn(), success: vi.fn(), guard: vi.fn(),
  createImageURL: vi.fn(), revokeImageURL: vi.fn(),
  regrade: { list: vi.fn(), claim: vi.fn(), context: vi.fn(), image: vi.fn(), submit: vi.fn() },
  backmark: { list: vi.fn(), claim: vi.fn(), context: vi.fn(), image: vi.fn(), submit: vi.fn() }
}));

vi.mock("antd", () => ({
  App: { useApp: () => ({ message: mocks, modal: mocks }) },
  Button: ({ children, onClick, disabled }: any) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Drawer: ({ open, children }: any) => open ? <div>{children}</div> : null,
  Alert: ({ message, description }: any) => <div>{message}{description}</div>,
  Empty: ({ description }: any) => <p>{description}</p>,
  Input: { TextArea: ({ value, onChange, disabled }: any) => <textarea disabled={disabled} value={value} onChange={onChange} /> },
  List: Object.assign(({ dataSource, renderItem }: any) => <div>{dataSource.map(renderItem)}</div>, {
    Item: Object.assign(({ children, actions }: any) => <div>{children}{actions}</div>, {
      Meta: ({ title }: any) => <p>{title}</p>
    })
  }),
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Spin: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Tag: ({ children }: { children: ReactNode }) => <span>{children}</span>
}));
vi.mock("../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: mocks.guard }));
vi.mock("../features/grading/SharedScoreControl", () => ({
  SharedScoreControl: ({ value, onChange, disabled }: any) => <>
    <span>分数 {value.score ?? "空"}</span>
    <button disabled={disabled} onClick={() => onChange({ score: 4, rubricSelections: { p: 4 } })}>给4分</button>
  </>
}));
vi.mock("../api/regrade", () => ({
  listMyRegradeItems: mocks.regrade.list, claimRegradeItem: mocks.regrade.claim,
  getRegradeItemContext: mocks.regrade.context, downloadRegradeSegmentImage: mocks.regrade.image,
  recordRegradeCandidate: mocks.regrade.submit
}));
vi.mock("../api/backmark", () => ({
  listMyBackmarkItems: mocks.backmark.list, claimBackmarkItem: mocks.backmark.claim,
  getBackmarkItemContext: mocks.backmark.context, downloadBackmarkSegmentImage: mocks.backmark.image,
  submitBackmarkItem: mocks.backmark.submit
}));

for (const kind of ["backmark", "regrade"] as const) describe(`${kind} answer evidence`, () => {
  let root: Root;
  let container: HTMLDivElement;
  let originalCreateImageURL: PropertyDescriptor | undefined;
  let originalRevokeImageURL: PropertyDescriptor | undefined;
  const api = mocks[kind];
  const label = kind === "backmark" ? "回标" : "重评";
  const scoringTitle = kind === "backmark" ? "独立评分" : "新版细则评分";

  const contextResponse = (answer = { raw_answer: "原始回答", ocr_text: "OCR 回答" }) => {
    const context = {
      expected_revision: 2,
      frozen_rubric: { version: "frozen-2", points: [{ id: "p", score: 5, description: "依据完整步骤给分" }], deductions: [{ description: "单位缺失扣一分" }] },
      question: { question_no: "1", question_type: "short_answer", score: 5, stem: "题干", options: ["A. 选项内容"], answer_key: { value: "参考答案", accepted_forms: ["等价答案"] }, solution: { steps: ["完整解题步骤"] } },
      answer
    };
    return kind === "backmark" ? { backmark_context: context } : { regrade_context: context };
  };

  beforeEach(() => {
    vi.clearAllMocks();
    setBusinessCommandScope("tenant", "teacher");
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const item = { id: "item", max_score: 5, created_at: "2026-10-05T00:00:00Z", status: "assigned" };
    api.list.mockResolvedValue(kind === "backmark" ? { backmark_items: [item], has_more: false } : { regrade_items: [item] });
    api.claim.mockResolvedValue(kind === "backmark" ? { backmark_item: item } : { regrade_item: item });
    api.context.mockResolvedValue(contextResponse());
    api.image.mockResolvedValue({ blob: new Blob(["answer crop"], { type: "image/png" }) });
    api.submit.mockResolvedValue({});
    mocks.createImageURL.mockReturnValue("blob:review-crop");
    originalCreateImageURL = Object.getOwnPropertyDescriptor(URL, "createObjectURL");
    originalRevokeImageURL = Object.getOwnPropertyDescriptor(URL, "revokeObjectURL");
    Object.defineProperties(URL, {
      createObjectURL: { configurable: true, writable: true, value: mocks.createImageURL },
      revokeObjectURL: { configurable: true, writable: true, value: mocks.revokeImageURL }
    });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    if (originalCreateImageURL) Object.defineProperty(URL, "createObjectURL", originalCreateImageURL);
    else Reflect.deleteProperty(URL, "createObjectURL");
    if (originalRevokeImageURL) Object.defineProperty(URL, "revokeObjectURL", originalRevokeImageURL);
    else Reflect.deleteProperty(URL, "revokeObjectURL");
  });

  const renderQueue = async (canWork = true) => {
    await act(async () => root.render(kind === "backmark" ? <BackmarkQueue canWork={canWork} /> : <RegradeQueue canWork={canWork} />));
  };
  const click = async (text: string) => {
    const button = [...container.querySelectorAll("button")].find((item) => item.textContent === text);
    if (!button) throw new Error(`missing ${text}`);
    await act(async () => button.click());
  };
  const openItem = async () => {
    await renderQueue();
    await click(`${label}任务`);
    await click(`开始${label}`);
  };

  it("keeps evidence hidden without work permission and renders the current crop before scoring", async () => {
    await renderQueue(false);
    expect(container.textContent).toBe("");
    expect(api.list).not.toHaveBeenCalled();
    await openItem();
    expect(api.claim).toHaveBeenCalledWith("item");
    expect(api.context).toHaveBeenCalledWith("item");
    expect(api.image).toHaveBeenCalledWith("item");
    const materials = container.querySelector('section[aria-label="本任务题目与完整评分依据"]');
    expect(materials?.textContent).toContain("第 1 题 · 满分 5");
    for (const text of ["题干", "A. 选项内容", "参考答案", "等价答案", "完整解题步骤", "冻结细则 · frozen-2", "依据完整步骤给分", "单位缺失扣一分"]) expect(materials?.textContent).toContain(text);
    const evidence = materials?.parentElement;
    expect(evidence?.querySelector("h3")?.textContent).toBe("1 · short_answer");
    expect(evidence?.textContent).toContain("原始回答");
    expect(evidence?.textContent).not.toContain("OCR 回答");
    expect([...container.querySelectorAll("section")].find(section => section.querySelector("h3")?.textContent === scoringTitle)).toBeDefined();
    const image = evidence?.querySelector("img");
    expect(image?.getAttribute("src")).toBe("blob:review-crop");
    expect(image?.getAttribute("alt")).toBe("当前答题区域");
    expect(mocks.createImageURL).toHaveBeenCalledOnce();
    expect(mocks.revokeImageURL).not.toHaveBeenCalled();
    await click("给4分");
    expect(container.textContent).toContain("分数 4");
  });

  it("retains OCR evidence and scoring when the current crop cannot be downloaded", async () => {
    api.image.mockRejectedValue(new Error("image unavailable"));
    api.context.mockResolvedValue(contextResponse({ raw_answer: "", ocr_text: "OCR 回答" }));
    await openItem();
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("答题图片暂不可预览");
    expect(container.textContent).toContain("可依据下方文字内容独立评分。");
    expect(container.textContent).toContain("OCR 回答");
    expect(mocks.createImageURL).not.toHaveBeenCalled();
    expect(mocks.error).not.toHaveBeenCalled();
    await click("给4分");
    expect(container.textContent).toContain("分数 4");
  });

  it("releases a late crop from a previous login without reopening its scoring view", async () => {
    let finishImage!: (value: { blob: Blob }) => void;
    api.image.mockImplementation(() => new Promise((resolve) => { finishImage = resolve; }));
    await openItem();
    expect(api.image).toHaveBeenCalledOnce();
    // 领取和上下文已经完成，图片仍在下载；换登录后迟到图片只能释放，不能恢复旧任务。
    await act(async () => { setBusinessCommandScope("tenant", "another-teacher"); });
    await act(async () => { finishImage({ blob: new Blob(["late crop"], { type: "image/png" }) }); });
    expect(mocks.createImageURL).toHaveBeenCalledOnce();
    expect(mocks.revokeImageURL).toHaveBeenCalledOnce();
    expect(mocks.revokeImageURL).toHaveBeenCalledWith("blob:review-crop");
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("section")).toBeNull();
    expect(container.textContent).not.toContain("分数 空");
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("does not reopen a previous login's scoring view when its late crop fails", async () => {
    let failImage!: (error: Error) => void;
    api.image.mockImplementation(() => new Promise((_resolve, reject) => { failImage = reject; }));
    await openItem();
    expect(api.image).toHaveBeenCalledOnce();
    // 下载失败没有临时 URL，旧会话仍须 return；不能把返回错误地放进有 URL 的内层条件。
    await act(async () => { setBusinessCommandScope("tenant", "another-teacher"); });
    await act(async () => { failImage(new Error("late image unavailable")); });
    expect(mocks.createImageURL).not.toHaveBeenCalled();
    expect(mocks.revokeImageURL).not.toHaveBeenCalled();
    expect(container.querySelector("section")).toBeNull();
    expect(container.textContent).not.toContain("分数 空");
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.error).not.toHaveBeenCalled();
  });
});

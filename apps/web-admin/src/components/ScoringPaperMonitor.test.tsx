// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ScoringRun, ScoringRunItem } from "../api/review";
import { ScoringPaperMonitor } from "./ScoringPaperMonitor";

const mocks = vi.hoisted(() => ({ download: vi.fn(), createURL: vi.fn(), revokeURL: vi.fn() }));
vi.mock("../api/files", () => ({ downloadFileBlob: mocks.download }));
vi.mock("antd", async () => {
  const { createElement } = await import("react");
  const Empty = Object.assign(({ description }: { description: string }) => createElement("p", null, description), { PRESENTED_IMAGE_SIMPLE: "simple" });
  return {
    Alert: ({ message }: { message: string }) => createElement("p", { role: "alert" }, message),
    Button: ({ onClick, children }: { onClick: () => void; children: React.ReactNode }) => createElement("button", { type: "button", onClick }, children),
    Empty,
    Progress: ({ percent }: { percent: number }) => createElement("span", { "data-progress": percent }),
    Spin: () => createElement("span", null, "loading"),
    Tag: ({ color, children }: { color: string; children: React.ReactNode }) => createElement("span", { "data-tone": color }, children),
  };
});

const OriginalURL = globalThis.URL;
function scoringItem(overrides: Partial<ScoringRunItem> = {}): ScoringRunItem {
  return {
    answer_segment_id: "segment-1", submission_page_id: "page-1",
    page_no: 1, page_file_asset_id: "", question_id: "question-1", question_no: "Q1", question_type: "short_answer",
    anonymous_code: "A", normalized_bbox: { x: 0.1, y: 0.2, width: 0.5, height: 0.1 }, state: "queued",
    ...overrides,
    submission_id: overrides.submission_id ?? `submission-${overrides.anonymous_code ?? "A"}`,
  };
}

describe("scoring paper monitor presentation and image lifecycle", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.download.mockReset().mockResolvedValue({ blob: new Blob(["page"]) });
    mocks.createURL.mockReset().mockReturnValue("blob:test-page");
    mocks.revokeURL.mockReset();
    vi.stubGlobal("URL", class extends OriginalURL {
      static createObjectURL = mocks.createURL;
      static revokeObjectURL = mocks.revokeURL;
    });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
  });

  async function render(items: ScoringRunItem[], status?: string, onRefresh = vi.fn()) {
    const run = status ? { status, review_count: 2 } as ScoringRun : undefined;
    await act(async () => root.render(<ScoringPaperMonitor items={items} run={run} onRefresh={onRefresh} />));
    return onRefresh;
  }

  it.each([
    ["needs_review", "本批答卷有待处理或确认项"],
    ["completed", "本批答卷已处理完成"],
    ["running", "正在逐卷识别与评分"],
    [undefined, "正在逐卷识别与评分"],
  ])("preserves the header for run status %s", async (status, title) => {
    await render([scoringItem()], status);
    expect(host.querySelector("h3")?.textContent).toBe(title);
  });

  it.each([
    [{ score: 0, max_score: 0 }, "Q1 0/0"],
    [{ score: 1.25, max_score: 2.26, grade_source: "ai_suggestion" }, "Q1 ≈1.3/2.3"],
    [{ score: 2, max_score: undefined }, "Q1 2"],
  ])("preserves score and maximum formatting for %j", async (score, text) => {
    await render([scoringItem({ ...score, state: "confirmed", page_file_asset_id: "asset-1" })]);
    expect(host.querySelector(".scoring-score-mark")?.textContent).toBe(text);
    expect(host.querySelector(".scoring-score-mark")?.getAttribute("style")).toContain("left: 60%");
  });

  it("prioritizes failure over completion and keeps other candidates processing", async () => {
    await render([
      scoringItem({ anonymous_code: "C", state: "queued" }),
      scoringItem({ anonymous_code: "B", state: "confirmed", score: 0 }),
      scoringItem({ anonymous_code: "A", state: "confirmed", answer_segment_id: "a-confirmed" }),
      scoringItem({ anonymous_code: "A", state: "failed", answer_segment_id: "a-failed" }),
    ]);
    const buttons = [...host.querySelectorAll(".scoring-candidate-item")];
    expect(buttons.map((button) => button.querySelector("strong")?.textContent)).toEqual(["A", "B", "C"]);
    expect(buttons[0].querySelector("svg.danger")).not.toBeNull();
    expect(buttons[1].querySelector("svg.success")).not.toBeNull();
    expect(buttons[2].querySelector("svg.processing")).not.toBeNull();
    expect(host.querySelector("[data-progress]")?.getAttribute("data-progress")).toBe("0");
  });

  it("keeps one submission together even when legacy segment fallback codes differ", async () => {
    await render([
      scoringItem({ anonymous_code: "old-segment-1", submission_id: "one-paper", state: "review", reason_code: "scoring_rule_missing" }),
      scoringItem({ anonymous_code: "old-segment-2", submission_id: "one-paper", answer_segment_id: "segment-2", state: "review" }),
    ], "needs_review");
    expect(host.querySelectorAll(".scoring-candidate-item")).toHaveLength(1);
    expect(host.textContent).toContain("0/2 已识别");
    expect(host.textContent).toContain("本题尚无生效评分规则");
    expect(host.querySelector("[data-progress]")?.getAttribute("data-progress")).toBe("0");
  });

  it("counts actual blank observations and zero-score suggestions separately from manual routing", async () => {
    await render([
      scoringItem({ recognition_completed: true, recognition_decision: "blank", state: "review", score: 0, max_score: 5, grade_source: "ai_suggestion" }),
      scoringItem({ answer_segment_id: "s2", state: "review", recognition_completed: false }),
    ]);
    expect(host.querySelector(".scoring-monitor-progress")?.textContent).toContain("1/ 2 题已识别 · 1 题已生成分数或建议");
    expect(host.querySelector("[data-progress]")?.getAttribute("data-progress")).toBe("50");
  });

  it("preserves failed, review and processing activity tones", async () => {
    await render([
      scoringItem({ state: "failed", question_no: "Q1", reason_code: "ocr_failed" }),
      scoringItem({ state: "review", question_no: "Q2", answer_segment_id: "s2" }),
      scoringItem({ state: "processing", question_no: "Q3", answer_segment_id: "s3" }),
      scoringItem({ state: "confirmed", question_no: "Q4", answer_segment_id: "s4" }),
    ]);
    expect([...host.querySelectorAll(".scoring-activity-item [data-tone]")].map((node) => node.getAttribute("data-tone"))).toEqual(["error", "warning", "processing"]);
    expect(host.querySelector(".scoring-activity-list")?.textContent).toContain("卷面识别失败");
    expect(host.querySelectorAll(".scoring-activity-item")).toHaveLength(3);
  });

  it("shows the empty state and preserves a single refresh action", async () => {
    const onRefresh = await render([]);
    expect(host.textContent).toContain("评分启动后，这里会逐卷显示处理进度与红笔得分");
    expect(mocks.download).not.toHaveBeenCalled();
    await render([scoringItem()], undefined, onRefresh);
    const refresh = [...host.querySelectorAll("button")].find((button) => button.textContent === "刷新")!;
    await act(async () => refresh.click());
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it("distinguishes partial image failure from a fully failed download", async () => {
    mocks.download.mockImplementation((asset: string) => asset === "bad" ? Promise.reject(new Error("download failed")) : Promise.resolve({ blob: new Blob(["page"]) }));
    await render([
      scoringItem({ page_file_asset_id: "good", state: "confirmed" }),
      scoringItem({ page_file_asset_id: "bad", page_no: 2, answer_segment_id: "s2" }),
      scoringItem({ anonymous_code: "B", page_file_asset_id: "bad", answer_segment_id: "s3" }),
    ]);
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("1 页影像暂时无法加载，其余页面已正常显示。");
    expect(host.querySelectorAll("img")).toHaveLength(1);
    await act(async () => host.querySelectorAll<HTMLButtonElement>(".scoring-candidate-item")[1].click());
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("整卷影像暂时无法加载，请刷新后重试。");
    expect(host.querySelectorAll("img")).toHaveLength(0);
    expect(mocks.revokeURL).toHaveBeenCalledWith("blob:test-page");
  });

  it("ignores an old page download after switching candidates", async () => {
    // 先切卷，再让旧请求返回；旧文件不能建立 object URL，也不能进入新卷影像。
    let resolveOld!: (value: { blob: Blob }) => void;
    const oldDownload = new Promise<{ blob: Blob }>((resolve) => { resolveOld = resolve; });
    mocks.download.mockImplementation((asset: string) => asset === "old" ? oldDownload : Promise.resolve({ blob: new Blob(["new"]) }));
    await render([
      scoringItem({ anonymous_code: "A", page_file_asset_id: "old" }),
      scoringItem({ anonymous_code: "B", page_file_asset_id: "new", answer_segment_id: "s2" }),
    ]);
    await act(async () => host.querySelectorAll<HTMLButtonElement>(".scoring-candidate-item")[1].click());
    await act(async () => resolveOld({ blob: new Blob(["old"]) }));
    expect(mocks.createURL).toHaveBeenCalledTimes(1);
    expect(host.querySelector("img")?.getAttribute("alt")).toBe("B 第 1 页");
  });
});

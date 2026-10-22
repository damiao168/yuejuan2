// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CapturePage, ImageQualityIssue, ImageQualityRun } from "../../api/capture";
import { ImageQualityWorkspace } from "./ImageQualityWorkspace";

const mocks = vi.hoisted(() => ({
  runs: vi.fn(), rerun: vi.fn(), download: vi.fn(), refresh: vi.fn(), override: vi.fn(),
  message: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("../../api/capture", () => ({ listPageQualityRuns: mocks.runs, runImageQualityCheck: mocks.rerun }));
vi.mock("../../api/files", () => ({ downloadFileBlob: mocks.download }));
vi.mock("../../api/client", () => ({ getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback }));

// 只转交展示参数和回调；加载顺序、请求代次和权限条件由真实工作区组件执行。
vi.mock("antd", () => {
  type Options = { label: string; value: string; disabled?: boolean }[];
  const Choice = ({ options, value, onChange }: { options: Options; value: string; onChange: (value: string) => void }) =>
    <select aria-label={value === "source" || value === "normalized" ? "预览模式" : "页面筛选"} value={value} onChange={(event) => onChange(event.target.value)}>{options.map((item) => <option key={item.value} value={item.value} disabled={item.disabled}>{item.label}</option>)}</select>;
  return {
    App: { useApp: () => ({ message: mocks.message }) }, Segmented: Choice,
    Alert: ({ message, action }: { message: ReactNode; action?: ReactNode }) => <div role="alert">{message}{action}</div>,
    Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean }) => <button type="button" disabled={disabled || loading} onClick={onClick}>{children}</button>,
    Empty: Object.assign(({ description }: { description: string }) => <p>{description}</p>, { PRESENTED_IMAGE_SIMPLE: "simple" }),
    Skeleton: Object.assign(() => <p data-loading="runs">记录加载中</p>, { Image: () => <p data-loading="preview">预览加载中</p> }),
    Progress: ({ percent, strokeColor }: { percent: number; strokeColor?: string }) => <meter min={0} max={100} value={percent} data-color={strokeColor} />,
    Select: ({ options, value, onChange }: { options: Options; value: string; onChange: (value: string) => void }) => <select aria-label="检测记录" value={value} onChange={(event) => onChange(event.target.value)}>{options.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select>,
    Space: ({ children, className }: { children: ReactNode; className?: string }) => <div className={className}>{children}</div>,
    Switch: ({ checked, disabled, onChange }: { checked: boolean; disabled: boolean; onChange: (checked: boolean) => void }) => <input type="checkbox" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} />,
    Tag: ({ children, color }: { children: ReactNode; color: string }) => <span data-tone={color}>{children}</span>,
  };
});

function page(id = "p1", overrides: Partial<CapturePage> = {}): CapturePage {
  return {
    id, capture_batch_id: "batch", capture_file_id: "file", source_index: 1, submission_id: `s-${id}`,
    submission_page_id: `sp-${id}`, assigned_page_no: 1, sequence_no: 1, rotation_degrees: 0,
    decoded_file_asset_id: "", status: "normalized", revision: 1, page_identity: { quality_status: "review" },
    match_candidates: [], ...overrides,
  };
}
function run(id = "r1", report: Record<string, unknown> = {}, issues: ImageQualityIssue[] = []): ImageQualityRun {
  return {
    id, submission_id: "s-p1", submission_page_id: "sp-p1", page_no: 1, source_file_asset_id: "source",
    processing_status: "completed", quality_status: "passed", profile_name: id, profile_version: "v1",
    metric_schema_version: "v1", report_schema_version: "v1", quality_report: { quality_score: 78, decision: "PASS", ...report },
    quality_issues: issues, normalization_transform: {}, attempt_no: 1, created_at: "2026-10-01T01:02:03Z",
  };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

describe("图像质量工作区展示职责回归", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("URL", { createObjectURL: vi.fn((blob: Blob) => `blob:${blob.size}`), revokeObjectURL: vi.fn() });
    mocks.runs.mockResolvedValue({ runs: [run()] }); mocks.rerun.mockResolvedValue({ runs: [run()] }); mocks.refresh.mockResolvedValue(undefined);
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); });
  async function render(pages = [page()], canManage = true) {
    await act(async () => root.render(<ImageQualityWorkspace pages={pages} canManage={canManage} onRefresh={mocks.refresh} onOverride={mocks.override} />));
  }
  function button(label: string) {
    const found = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.includes(label));
    expect(found).toBeDefined(); return found!;
  }
  async function click(label: string) { await act(async () => button(label).click()); }

  it("新页下载失败清除旧图，重试并实际显示当前原图后才可放行", async () => {
    const first = page("p1", { decoded_file_asset_id: "source-1" });
    const second = page("p2", { sequence_no: 2, decoded_file_asset_id: "source-2" });
    mocks.download.mockResolvedValueOnce({ blob: new Blob(["first"]) }).mockRejectedValueOnce(new Error("当前页下载失败"));
    await render([first, second]);
    await act(async () => host.querySelector("img")!.dispatchEvent(new Event("load")));
    expect(button("人工放行").disabled).toBe(false);
    await click("扫描序号 2");
    expect(host.querySelector("img")).toBeNull();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("当前页下载失败");
    expect(button("人工放行").disabled).toBe(true);
    await click("人工放行"); expect(mocks.override).not.toHaveBeenCalled();
    mocks.download.mockResolvedValueOnce({ blob: new Blob(["second"]) });
    await click("重新加载当前页");
    expect(mocks.download).toHaveBeenLastCalledWith("source-2");
    expect(button("人工放行").disabled).toBe(true);
    await act(async () => host.querySelector("img")!.dispatchEvent(new Event("load")));
    await click("人工放行"); expect(mocks.override).toHaveBeenCalledWith(second);
  });

  it("当前原图无法显示时撤销放行证据", async () => {
    mocks.download.mockResolvedValue({ blob: new Blob(["image"]) });
    await render([page("p1", { decoded_file_asset_id: "source" })]);
    await act(async () => host.querySelector("img")!.dispatchEvent(new Event("error")));
    expect(host.querySelector("img")).toBeNull();
    expect(button("人工放行").disabled).toBe(true);
  });

  it("排除未拆页和已删除页面，保留空工作区", async () => {
    await render([page("pending", { submission_page_id: undefined }), page("deleted", { status: "deleted" })]);
    expect(host.textContent).toContain("拆页完成后可在这里查看图像质检结果"); expect(mocks.runs).not.toHaveBeenCalled();
  });

  it.each([[49, "#cf1322"], [50, "#d48806"], [77, "#d48806"], [78, "#1677ff"]])("评分 %s 保留颜色边界和无管理权限的动作", async (score, color) => {
    mocks.runs.mockResolvedValue({ runs: [run("r1", { quality_score: score })] }); await render([page()], false);
    expect(host.querySelector("[data-color]")?.getAttribute("data-color")).toBe(color);
    expect(button("重检本份答卷").disabled).toBe(true); expect(button("人工放行").disabled).toBe(true);
    expect(host.textContent).toContain("旧版检测记录未包含七项明细");
  });

  it("加载和错误优先于旧记录，重试空结果仍显示未检测", async () => {
    await render([page(), page("p2", { sequence_no: 2 })]);
    const next = deferred<{ runs: ImageQualityRun[] }>(); mocks.runs.mockReturnValueOnce(next.promise);
    await click("扫描序号 2");
    expect(host.querySelector('[data-loading="runs"]')).not.toBeNull();
    expect(host.querySelector(".quality-run-select")).toBeNull();
    await act(async () => next.reject(new Error("质检记录失败")));
    expect(host.querySelector(".quality-inline-error")?.textContent).toBe("质检记录失败重试");
    mocks.runs.mockResolvedValueOnce({ runs: [] }); await click("重试");
    expect(host.textContent).toContain("该页面尚无真实质检记录");
    expect(mocks.runs).toHaveBeenLastCalledWith("sp-p2");
  });

  it("切页后迟到的记录和图片不覆盖新页面", async () => {
    const oldRuns = deferred<{ runs: ImageQualityRun[] }>(); const oldPreview = deferred<{ blob: Blob }>();
    const newPreview = deferred<{ blob: Blob }>();
    mocks.runs.mockImplementation((id: string) => id === "sp-p1" ? oldRuns.promise : Promise.resolve({ runs: [run("new")] }));
    mocks.download.mockImplementation((id: string) => id === "source-1" ? oldPreview.promise : newPreview.promise);
    await render([page("p1", { decoded_file_asset_id: "source-1" }), page("p2", { sequence_no: 2, decoded_file_asset_id: "source-2" })]);
    await click("扫描序号 2");
    // 先完成新页记录，再独立完成其图片；旧页两类响应最后返回。
    await act(async () => newPreview.resolve({ blob: new Blob(["new"]) }));
    await act(async () => { oldRuns.resolve({ runs: [run("old")] }); oldPreview.resolve({ blob: new Blob(["old-preview"]) }); });
    expect(host.textContent).toContain("算法 new/v1"); expect(host.textContent).not.toContain("算法 old/v1");
    expect(host.querySelector("img")?.getAttribute("src")).toBe("blob:3");
    expect(URL.createObjectURL).toHaveBeenCalledTimes(1);
  });

  it("原图显示热区，标准化预览保留开关限制和历史选择", async () => {
    const current = { ...run("current", { focus: { map: [{ row: 0, column: 1, x: 0.1, y: 0.2, width: 0.3, height: 0.4, focus_score: 0.72, status: "warning" }] } }), normalized_file_asset_id: "normalized" };
    mocks.runs.mockResolvedValue({ runs: [current, run("history")] }); mocks.download.mockResolvedValue({ blob: new Blob(["image"]) });
    await render([page("p1", { decoded_file_asset_id: "source" })]);
    expect(host.querySelector(".focus-map-patch")?.getAttribute("title")).toBe("清晰度 72");
    expect(host.querySelector<HTMLElement>(".focus-map-patch")?.style.left).toBe("10%");
    const preview = host.querySelector<HTMLSelectElement>('[aria-label="预览模式"]')!;
    await act(async () => { preview.value = "normalized"; preview.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(mocks.download).toHaveBeenLastCalledWith("normalized"); expect(host.querySelector(".focus-map-overlay")).toBeNull();
    expect(host.querySelector<HTMLInputElement>('input[type="checkbox"]')?.disabled).toBe(true);
    const history = host.querySelector<HTMLSelectElement>('[aria-label="检测记录"]')!;
    expect(history.options[0].textContent).toContain("当前 · "); expect(history.options[1].textContent).toContain("历史 · ");
    await act(async () => { history.value = "history"; history.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(host.textContent).toContain("算法 history/v1");
  });

  it("检测明细、门禁和观测值保留数值语义，结构化观测值显示内容", async () => {
    // NaN/Infinity 用组件输入模拟既有非有限数展示；JSON 接口不会传这两类数值。
    const values = [undefined, null, false, 0, "原值", 1.23456, Number.NaN, Number.POSITIVE_INFINITY, { x: 1 }, [0, { x: 1 }]];
    const issues = values.map((observed) => ({ code: "low_sharpness", severity: "warning", action: "review", observed }));
    mocks.runs.mockResolvedValue({ runs: [run("r1", {
      dimensions: { completeness: { score: 99.5, status: "failed" } },
      hard_gates: [{ code: "page_decode", status: "passed" }, null], metrics: { bad_focus_patch_ratio: 0, effective_short_edge_px: 123.6 },
    }, issues)] }); await render();
    expect(host.querySelectorAll(".quality-dimension-row")).toHaveLength(7);
    expect(host.textContent).toContain("文件可解码通过"); expect(host.textContent).toContain("gate-1待模板阶段评估");
    expect(host.textContent).toContain("局部坏块 0%"); expect(host.textContent).toContain("有效短边 124 px");
    expect([...host.querySelectorAll(".quality-issue-list small")].map((item) => item.textContent)).toEqual([
      "请查看原图和检测详情", "实测 null", "实测 false", "实测 0", "实测 原值", "实测 1.235", "实测 NaN", "实测 Infinity", '实测 {"x":1}', '实测 [0,{"x":1}]',
    ]);
  });

  it("先显示失败原因和阈值，文件内第1页不冒充真实答题卡页码", async () => {
    mocks.runs.mockResolvedValue({ runs: [run("r1", {}, [{ code: "low_effective_resolution", severity: "failed", action: "recapture", observed: 900, threshold: 1200 }])] });
    await render([page(), page("p2", { sequence_no: 2, assigned_page_no: 2, page_identity: { quality_status: "passed", page_no_source: "visual_template_match", matched_page_no: 2 } })]);
    const rows = [...host.querySelectorAll(".image-quality-page-row")];
    expect(rows[0].textContent).toContain("扫描页 1");
    expect(rows[0].textContent).toContain("答题卡页码待匹配");
    expect(rows[1].textContent).toContain("答题卡第 2 页");
    const issues = host.querySelector(".quality-issue-list")!;
    expect(issues.textContent).toContain("实测 900；规则阈值 1200；请重新扫描或上传更清晰的原图");
    expect(issues.compareDocumentPosition(host.querySelector(".quality-dimensions")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("重检成功后加载所选页和刷新批次，人工放行接收原页面", async () => {
    const selected = page("p1", { decoded_file_asset_id: "source" });
    mocks.download.mockResolvedValue({ blob: new Blob(["source"]) });
    await render([selected]);
    expect(button("人工放行").disabled).toBe(true);
    await act(async () => host.querySelector("img")!.dispatchEvent(new Event("load")));
    await click("人工放行"); expect(mocks.override).toHaveBeenCalledWith(selected);
    await click("重检本份答卷"); expect(mocks.rerun).toHaveBeenCalledWith("s-p1");
    expect(mocks.runs).toHaveBeenCalledTimes(2); expect(mocks.runs).toHaveBeenLastCalledWith("sp-p1");
    expect(mocks.refresh).toHaveBeenCalledTimes(1); expect(mocks.message.success).toHaveBeenCalledWith("已提交 1 页重新检测；原质检记录继续保留");
  });
});

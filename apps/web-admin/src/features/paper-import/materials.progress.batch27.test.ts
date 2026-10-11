import { describe, expect, it } from "vitest";
import type { PaperImportJob, PaperImportSource } from "../../api/papers";
import { paperImportProgress, paperImportTeacherProgress } from "./materials";

type RuntimeProgress = NonNullable<PaperImportJob["runtime_progress"]>;
type Progress = ReturnType<typeof paperImportProgress>;

function job(patch: Partial<PaperImportJob> = {}): PaperImportJob {
  return {
    id: "import-27", exam_id: "exam-27", exam_paper_id: "", paper_file_asset_id: "", answer_file_asset_id: "",
    status: "processing", generation: 1, run_id: "run-27", source_revision: "sources-27", subject: "mathematics",
    sources: [], question_candidates: [], answer_candidates: [], solution_candidates: [], rubric_candidates: [],
    structured_issues: [], questions: [], issues: [], created_at: "2026-10-06T10:00:00Z", ...patch
  };
}

function source(processing_status: PaperImportSource["processing_status"]): PaperImportSource {
  return { id: processing_status, document_index: 0, file_asset_id: "file-27", role_hint: "auto", detected_role: "unknown", role_confidence: 0, processing_status };
}

function runtime(patch: Partial<RuntimeProgress> = {}): RuntimeProgress {
  return { task_type: "ocr", task_status: "running", updated_at: "2026-10-06T10:00:03Z", ...patch };
}

describe("试卷导入进度的状态与 Worker 展示边界", () => {
  it("排队尚无阶段投影时保留服务端任务更新时间，不推造计数或比例", () => {
    const progress = paperImportTeacherProgress(job({ sources: [source("pending")], updated_at: "2026-10-06T10:00:07Z" }));
    expect(progress).toMatchObject({ updatedAt: "2026-10-06T10:00:07Z", label: "等待识别", percent: undefined });
    expect(progress.counter).toBeUndefined(); expect(progress.startedAt).toBeUndefined();
    expect(progress.detail).toContain("无需重复上传");
  });
  it("已有阶段更新时间时不被任务记录的其他更新时间替代", () => {
    const progress = paperImportTeacherProgress(job({ updated_at: "2026-10-06T10:00:07Z", runtime_progress: runtime() }));
    expect(progress.updatedAt).toBe("2026-10-06T10:00:03Z");
  });
  it("历史任务没有更新时间时保持未知，不用创建时间补作最新进度", () => {
    expect(paperImportTeacherProgress(job()).updatedAt).toBeUndefined();
  });
  it("teacher progress retains measured counts and timestamps without exposing worker internals", () => {
    const progress = paperImportTeacherProgress(job({runtime_progress:runtime({stage:"paper_parse",unit:"parse_chunk",completed:2,total:5,message:"Provider endpoint secret",model:"internal-model",cold_start:true,started_at:"start",progress_changed_at:"changed"})}));
    expect(progress).toMatchObject({percent:40,label:"整理题目与答案",counter:"2/5 项内容",startedAt:"start",changedAt:"changed",updatedAt:"2026-10-06T10:00:03Z"});
    expect(progress.detail).toBe("系统正在处理考试资料，完成后请逐题核对。");
  });
  it.each(["queued","leased"])("teacher %s progress explains waiting without suggesting another upload", task_status => {
    expect(paperImportTeacherProgress(job({runtime_progress:runtime({task_status})})).detail).toContain("无需重复上传");
  });
  it("teacher progress retains the final review state even if runtime is stale", () => {
    expect(paperImportTeacherProgress(job({status:"review_required",runtime_progress:runtime({phase:"model_loading"})}))).toMatchObject({percent:100,label:"等待人工核对"});
  });
  const terminalCases: { name: string; patch: Partial<PaperImportJob>; expected: Progress }[] = [
    { name: "已导入优先于残留取消码", patch: { status: "applied", error_code: "paper_import_cancelled" }, expected: { percent: 100, label: "导入完成", detail: "题目与评分资料已写入考试" } },
    { name: "取消码优先于失败状态", patch: { status: "failed", error_code: "paper_import_cancelled" }, expected: { percent: undefined, label: "已停止识别", detail: "识别任务已手动停止，可重新识别" } },
    { name: "未发现考试内容优先于普通复核", patch: { status: "review_required", structured_issues: [{ code: "NO_EXAM_CONTENT_DETECTED", severity: "error", certainty: "confirmed", message: "无考试内容", source_refs: [] }] }, expected: { percent: 100, label: "未识别到考试内容", detail: "请检查是否上传了无关图片或错误文件" } },
    { name: "普通复核不继续显示 Worker", patch: { status: "review_required" }, expected: { percent: 100, label: "等待人工核对", detail: "自动识别完成，请核对识别结果" } },
    { name: "识别失败显示首条原因", patch: { status: "failed", issues: ["首条失败原因", "不得显示的后续原因"] }, expected: { percent: undefined, label: "识别失败", detail: "首条失败原因" } }
  ];

  // 终态任务仍可能带上一阶段计数；这些数据不能把结果重新展示成处理中。
  it.each(terminalCases)("$name，不被残留 runtime 或失败来源覆盖", ({ patch, expected }) => {
    expect(paperImportProgress(job({ ...patch, sources: [source("failed")], runtime_progress: runtime({ completed: 2, total: 5 }) }))).toEqual(expected);
  });

  const sourceCases: { name: string; sources: PaperImportSource[]; expected: Progress }[] = [
    { name: "没有来源", sources: [], expected: { percent: undefined, label: "准备资料", detail: "正在登记上传文件" } },
    { name: "存在失败来源", sources: [source("pending"), source("failed")], expected: { percent: undefined, label: "文字识别失败", detail: "请检查源文件后重试" } },
    { name: "其他来源等待领取", sources: [source("pending"), source("processed")], expected: { percent: undefined, label: "等待 Worker", detail: "任务已提交，等待 Worker 领取；暂无可计算的完成比例" } }
  ];
  it.each(sourceCases)("没有 runtime 时按来源降级：$name", ({ sources, expected }) => {
    expect(paperImportProgress(job({ sources }))).toEqual(expected);
  });

  it.each([{ name: "没有来源", sources: [] }, { name: "失败来源", sources: [source("failed")] }])("有效 runtime 优先于来源降级：$name", ({ sources }) => {
    expect(paperImportProgress(job({ sources, runtime_progress: runtime({ completed: 1, total: 2, unit: "page" }) }))).toEqual({
      percent: 50, label: "文字识别", detail: "Worker 正在处理", counter: "1/2 页",
      startedAt: undefined, changedAt: undefined, updatedAt: "2026-10-06T10:00:03Z"
    });
  });

  const counterCases: { name: string; patch: Partial<RuntimeProgress>; percent: number | undefined; counter: string | undefined }[] = [
    { name: "负完成数归零", patch: { completed: -2, total: 4 }, percent: 0, counter: "0/4 项" },
    { name: "超出总数只封顶百分比", patch: { completed: 4, total: 3 }, percent: 100, counter: "4/3 项" },
    { name: "比例向下取整", patch: { completed: 2, total: 3 }, percent: 66, counter: "2/3 项" },
    { name: "总量为零不推算", patch: { completed: 7, total: 0 }, percent: undefined, counter: undefined },
    { name: "负总量归零后不推算", patch: { completed: 7, total: -1 }, percent: undefined, counter: undefined },
    { name: "缺少完成数从零计数", patch: { total: 4 }, percent: 0, counter: "0/4 项" },
    { name: "加载模型只保留计数", patch: { completed: 4, total: 4, phase: "model_loading" }, percent: undefined, counter: "4/4 项" }
  ];
  it.each(counterCases)("保留计数边界：$name", ({ patch, percent, counter }) => {
    expect(paperImportProgress(job({ runtime_progress: runtime(patch) }))).toEqual({
      percent, label: "文字识别", detail: "Worker 正在处理", counter,
      startedAt: undefined, changedAt: undefined, updatedAt: "2026-10-06T10:00:03Z"
    });
  });

  const labelCases: { name: string; patch: Partial<RuntimeProgress>; label: string; counter: string }[] = [
    { name: "未知阶段和显式空单位", patch: { stage: "future_stage", unit: "" }, label: "考试资料识别", counter: "1/2 " },
    { name: "空阶段回退 task_type，未知单位保留原文", patch: { stage: "", task_type: "paper_parse", unit: "future_unit" }, label: "题目结构解析", counter: "1/2 future_unit" },
    { name: "未知 task_type 与缺失单位", patch: { task_type: "future_task" }, label: "考试资料识别", counter: "1/2 项" }
  ];
  it.each(labelCases)("保留开放字段的降级：$name", ({ patch, label, counter }) => {
    expect(paperImportProgress(job({ runtime_progress: runtime({ completed: 1, total: 2, ...patch }) }))).toMatchObject({ label, counter });
  });

  it.each(["queued", "leased"])("%s 优先显示队列文案，不使用 Worker message", (task_status) => {
    expect(paperImportProgress(job({ runtime_progress: runtime({ task_status, message: "旧运行提示" }) }))).toMatchObject({ detail: "任务已进入处理队列" });
  });

  it("组合提示保持原顺序，三个时间字段分别透传", () => {
    expect(paperImportProgress(job({ runtime_progress: runtime({
      stage: "paper_parse", completed: 2, total: 5, unit: "parse_chunk", message: "解析进行中",
      batch_no: 2, batch_total: 4, model: "synthetic-model", cold_start: true,
      runtime_plan_source: "compatibility_default", parse_route: "compact_model",
      started_at: "2026-10-06T10:00:00Z", progress_changed_at: "2026-10-06T10:00:02Z"
    }) }))).toEqual({
      percent: 40, label: "题目结构解析", counter: "2/5 个解析块",
      detail: "解析进行中 · 批次 2/4 · 模型 synthetic-model · 正在加载本地缓存，不会重新下载模型 · 未标定兼容模式 · 仅歧义解析块调用大模型",
      startedAt: "2026-10-06T10:00:00Z", changedAt: "2026-10-06T10:00:02Z", updatedAt: "2026-10-06T10:00:03Z"
    });
  });

  it.each(["anchored", "unrelated_guard"])("%s 仅追加未调用大模型的说明", (parse_route) => {
    expect(paperImportProgress(job({ runtime_progress: runtime({ parse_route }) }))).toMatchObject({ detail: "Worker 正在处理 · 本阶段未调用大模型" });
  });

  it("缺少配对批次或可选标记时不追加提示", () => {
    expect(paperImportProgress(job({ runtime_progress: runtime({ batch_no: 2, model: "", cold_start: false, runtime_plan_source: "calibrated", parse_route: "future_route", message: "" }) }))).toMatchObject({ detail: "Worker 正在处理" });
  });
});

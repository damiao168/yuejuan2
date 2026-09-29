import type { CapturePage, ImageQualityRun } from "../../api/capture";

export type QualityPageFilter = "all" | "attention" | "passed" | "pending";

export interface QualityPageCounts {
  all: number;
  attention: number;
  passed: number;
  pending: number;
}

export interface QualityDimensionRow {
  key: string;
  label: string;
  score: number;
  status: string;
}

const dimensionOrder = [
  ["completeness", "页面完整性"],
  ["effective_resolution", "有效分辨率"],
  ["sharpness", "局部清晰度"],
  ["geometry", "几何质量"],
  ["illumination_contrast", "光照与对比度"],
  ["occlusion_reflection", "遮挡与反光"],
  ["noise_compression", "噪声与压缩"],
] as const;

export function qualityStatusOf(page: CapturePage): string {
  const status = page.page_identity.quality_status;
  return typeof status === "string" && status ? status : "unchecked";
}

export function qualityPageCounts(pages: CapturePage[]): QualityPageCounts {
  return pages.reduce<QualityPageCounts>(
    (counts, page) => {
      const status = qualityStatusOf(page);
      counts.all += 1;
      if (status === "passed") counts.passed += 1;
      else if (status === "review" || status === "failed") counts.attention += 1;
      else counts.pending += 1;
      return counts;
    },
    { all: 0, attention: 0, passed: 0, pending: 0 },
  );
}

export function filterQualityPages(pages: CapturePage[], filter: QualityPageFilter): CapturePage[] {
  if (filter === "all") return pages;
  return pages.filter((page) => {
    const status = qualityStatusOf(page);
    if (filter === "attention") return status === "review" || status === "failed";
    if (filter === "passed") return status === "passed";
    return !["passed", "review", "failed"].includes(status);
  });
}

export function buildQualityDimensionRows(run?: ImageQualityRun): QualityDimensionRow[] {
  const rawDimensions = run?.quality_report.dimensions;
  if (!isRecord(rawDimensions)) return [];
  const dimensions = rawDimensions;
  return dimensionOrder.map(([key, label]) => {
    const raw = dimensions[key];
    const dimension = isRecord(raw) ? raw : {};
    return {
      key,
      label,
      score: numeric(dimension.score),
      status: typeof dimension.status === "string" ? dimension.status : "unchecked",
    };
  });
}

export function qualityDecision(run?: ImageQualityRun): string {
  // 报告决定优先；旧格式只能在处理完成后回退到质量状态，不能把处理中当作通过。
  const decision = run?.quality_report.decision;
  if (typeof decision === "string") return decision;
  if (run?.processing_status !== "completed") return "PENDING";
  if (run.quality_status === "passed") return "PASS";
  if (run.quality_status === "failed") return "REJECT";
  return run.quality_status === "review" ? "LOW_QUALITY" : "PENDING";
}

export function qualityScore(run?: ImageQualityRun): number {
  return numeric(run?.quality_report.quality_score);
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function numeric(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

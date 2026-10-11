export interface GoldRubricPoint {
  id: string;
  description: string;
  score: number;
  // 冻结的评分细则快照早于仅供编辑器使用的标记；校准和
  // 提名控件仍可安全地共用同一个评分组件。
  required: boolean;
}

export function rubricPointsFromSnapshot(snapshot: Record<string, unknown>): GoldRubricPoint[] {
  const raw = Array.isArray(snapshot.points) ? snapshot.points : [];
  return raw.flatMap((value, index) => {
    if (!value || typeof value !== "object") return [];
    const point = value as Record<string, unknown>;
    const score = Number(point.score);
    if (!Number.isFinite(score)) return [];
    return [{
      id: typeof point.id === "string" && point.id ? point.id : `point-${index + 1}`,
      description: typeof point.description === "string" && point.description ? point.description : `采分点 ${index + 1}`,
      score,
      required: point.required === true
    }];
  });
}

export function buildRubricEvidence(selections: Record<string, number>, points: GoldRubricPoint[]) {
  return points.reduce<Record<string, number>>((result, point) => {
    const score = selections[point.id];
    if (typeof score === "number" && Number.isFinite(score)) result[point.id] = score;
    return result;
  }, {});
}

const gapLabels: Record<string, string> = {
  missing_zero_score: "缺少 0 分标准卷",
  missing_middle_score: "缺少中间分标准卷",
  missing_full_score: "缺少满分标准卷",
  missing_typical_error: "缺少典型错误证据",
  insufficient_trait_boundaries: "写作维度边界覆盖不足"
};

export function goldCoverageGapLabel(code: string) {
  if (code.startsWith("missing_pattern:")) return `缺少典型模式：${code.slice("missing_pattern:".length)}`;
  return gapLabels[code] ?? code;
}

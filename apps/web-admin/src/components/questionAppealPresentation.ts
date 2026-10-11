export interface QuestionAppealRubricPoint {
  key: string;
  label: string;
  score?: number;
}

export const questionAppealStatusLabels: Record<string, string> = {
  submitted: "待处理", under_review: "处理中", rejected: "已答复", upheld_pending_regrade: "已转复评", resolved: "已完成"
};
export const questionAppealReasonLabels: Record<string, string> = {
  recognition_error: "识别有误", missing_step_credit: "过程分遗漏", rubric_disagreement: "评分要点异议",
  calculation_error: "分数计算有误", annotation_issue: "批注或反馈问题", other: "其他"
};

export function formatQuestionAppealScore(value: number) {
  return Number.isInteger(value) ? String(value) : value.toFixed(1);
}

export function questionAppealStatusColor(status: string): string {
  if (status === "submitted") return "orange";
  if (status === "resolved") return "green";
  return "blue";
}

export function questionAppealReleaseSource(source: string): string {
  if (source === "initial") return "首次发布";
  if (source === "regrade") return "重评发布";
  return "其他来源";
}

export function questionAppealReleaseStatus(status: string): string {
  if (status === "published") return "已发布";
  if (status === "superseded") return "已被替代";
  return "未知状态";
}

function rubricPointKey(value: unknown, index: number): string {
  // 外部快照的对象或函数不能作为文本标识；合法标量仍按原值转换。
  switch (typeof value) {
    case "string":
    case "number":
    case "boolean":
    case "bigint":
    case "symbol":
      return String(value);
    default:
      return String(index);
  }
}

function rubricPointScore(item: Readonly<Record<string, unknown>>): number | undefined {
  if (typeof item.score === "number") return item.score;
  if (typeof item.points === "number") return item.points;
  return undefined;
}

export function questionAppealRubricPoints(snapshot: Readonly<Record<string, unknown>> | undefined): QuestionAppealRubricPoint[] {
  const points = snapshot?.points;
  if (!Array.isArray(points)) return [];
  // 非对象点被跳过，但回退编号仍取快照原索引；标签只判空，保留原文本中的空白。
  return points.flatMap((point, index) => {
    if (!point || typeof point !== "object") return [];
    const item = point as Record<string, unknown>;
    const label = [item.code, item.description, item.name].find((value): value is string => typeof value === "string" && Boolean(value.trim())) ?? `评分点 ${index + 1}`;
    return [{ key: rubricPointKey(item.id ?? item.code ?? index, index), label, score: rubricPointScore(item) }];
  });
}

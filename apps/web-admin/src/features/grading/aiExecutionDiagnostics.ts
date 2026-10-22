const failureLabels: Record<string, string> = {
  output_incomplete: "评分建议未完成",
  output_truncated: "输出达到上限",
  finish_reason_missing: "缺少完成标识",
  finish_reason_unknown: "完成状态无法识别",
  provider_refused: "模型未提供评分建议",
  content_filtered: "评分请求被拦截",
  unsupported_generation: "返回了不支持的内容类型",
  response_envelope_invalid: "响应结构异常",
  envelope_invalid: "响应结构异常",
  schema_invalid: "评分格式校验失败",
  evidence_invalid: "评分证据校验失败",
  capability_unavailable: "模型能力暂不可用"
};

const usageLabels: Record<string, string> = {
  reported: "已报告",
  partial: "部分报告，仍有未知用量",
  unavailable: "未报告（未知）",
  invalid: "报告无效（未知）"
};

export interface AIFailureSummaryRow {
  key: string;
  label: string;
  count: number;
  attempts: string;
  requests: string;
  usage: string;
}

function count(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

/** 诊断仅展示固定标签和计数；未知字段不作为错误原文透传，也不能把缺失用量解释为零。 */
export function aiFailureSummaryRows(value: unknown): AIFailureSummaryRow[] {
  if (!Array.isArray(value)) return [];
  return value.slice(0, 32).flatMap((item: unknown, index) => {
    if (!item || typeof item !== "object" || Array.isArray(item)) return [];
    const source = item as Record<string, unknown>;
    const failed = count(source.count);
    if (!failed) return [];
    const attempts = count(source.attempts);
    const requests = count(source.provider_request_count);
    const label = typeof source.failure_kind === "string" ? failureLabels[source.failure_kind] : undefined;
    const usage = typeof source.usage_status === "string" ? usageLabels[source.usage_status] : undefined;
    return [{
      key: `failure-${index}`,
      label: label ?? "其他评分异常",
      count: failed,
      attempts: attempts === undefined ? "尝试次数未知" : `尝试 ${attempts} 次`,
      requests: requests === undefined ? "上游调用次数未知" : `上游调用 ${requests} 次`,
      usage: usage ?? usageLabels.unavailable
    }];
  });
}

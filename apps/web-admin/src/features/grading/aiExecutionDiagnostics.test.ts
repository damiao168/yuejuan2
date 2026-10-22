import { describe, expect, it } from "vitest";
import { aiFailureSummaryRows } from "./aiExecutionDiagnostics";

describe("AI failure diagnostic projection", () => {
  it.each(["response_envelope_invalid", "envelope_invalid"])("uses a fixed label for %s across diagnostic contract versions", (failureKind) => {
    expect(aiFailureSummaryRows([{ failure_kind: failureKind, count: 1 }])[0].label).toBe("响应结构异常");
  });

  it("keeps missing usage and request counts unknown while preserving a reported zero", () => {
    const rows = aiFailureSummaryRows([
      { failure_kind: "output_truncated", count: 2, attempts: 2, usage_status: "partial" },
      { failure_kind: "capability_unavailable", count: 1, attempts: 0, provider_request_count: 0 }
    ]);
    expect(rows[0]).toMatchObject({ label: "输出达到上限", count: 2, attempts: "尝试 2 次", requests: "上游调用次数未知", usage: "部分报告，仍有未知用量" });
    expect(rows[1]).toMatchObject({ attempts: "尝试 0 次", requests: "上游调用 0 次", usage: "未报告（未知）" });
  });

  it("never displays arbitrary error text, identifiers or partial model content", () => {
    const rows = aiFailureSummaryRows([{ failure_kind: "secret provider error", count: 1, usage_status: "secret usage", request_id: "private-id", content: "{ partial json" }]);
    expect(rows).toEqual([{ key: "failure-0", label: "其他评分异常", count: 1, attempts: "尝试次数未知", requests: "上游调用次数未知", usage: "未报告（未知）" }]);
    expect(JSON.stringify(rows)).not.toMatch(/secret|private|partial json/);
  });

  it("rejects malformed counters and does not invent failure facts", () => {
    expect(aiFailureSummaryRows(undefined)).toEqual([]);
    expect(aiFailureSummaryRows([null, {}, { count: -1 }, { count: 0 }, { count: 1.2 }])).toEqual([]);
    expect(aiFailureSummaryRows([{ count: 1, attempts: Infinity, provider_request_count: -1, usage_status: "invalid" }])[0]).toMatchObject({ attempts: "尝试次数未知", requests: "上游调用次数未知", usage: "报告无效（未知）" });
  });
});

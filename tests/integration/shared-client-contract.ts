import assert from "node:assert/strict";
import { EduGradeApi } from "@edugrade/sdk";
import { ApiClient } from "../../apps/web-admin/src/api/client";
import { DesktopApiClient } from "../../apps/desktop-client/src/api/client";

// 在 Node 中比较两端共享契约和错误映射，不覆盖浏览器 Cookie、CORS 或桌面原生能力。
export async function run(baseUrl: string) {
  const clients = [new ApiClient({ baseUrl }), new DesktopApiClient({ baseUrl })];
  const results = [];
  for (const transport of clients) {
    const api = new EduGradeApi(transport);
    const response = await api.getExamProcessingSummary({ path: { examId: "exam-1" } });
    assert.equal(response.summary.total_pages, 1);
    assert.equal(response.summary.exam_id, "exam-1");
    const error = await transport.request("/api/v1/processing/exceptions?status=invalid").catch((failure: unknown) => failure);
    assert.ok(error instanceof Error);
    assert.equal((error as { requestId?: string }).requestId, "contract-request");
    assert.equal((error as { traceId?: string }).traceId, "contract-trace");
    assert.equal((error as { status?: number }).status, 400);
    results.push({ summary: response.summary, code: (error as { code?: string }).code });
  }
  assert.deepEqual(results[0], results[1]);
  return { status: "passed", consumers: ["web-admin", "desktop-client"], sharedGeneratedSDK: true, results };
}

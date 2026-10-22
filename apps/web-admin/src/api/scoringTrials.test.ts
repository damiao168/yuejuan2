import { afterEach, describe, expect, it, vi } from "vitest";
import { createScoringAITrial, decideScoringAITrial, enqueueScoringAITrial, getScoringRunAITrials, type CreateScoringAITrialInput } from "./scoringTrials";

afterEach(() => { vi.unstubAllGlobals(); });

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

const input: CreateScoringAITrialInput = {
  command_id: "stable-trial-command",
  question_id: "question-1",
  samples: [{ segment_id: "segment-1", human_score: 0, roles: ["representative", "boundary", "incomplete_evidence"] }]
};

describe("AI 试改服务端资源 API", () => {
  it("scopes recovery to the encoded scoring run and forwards request cancellation", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse({ trials: [{ id: "trial-1", sample_batch_id: "batch-1" }] }));
    vi.stubGlobal("fetch", fetchMock);
    const controller = new AbortController();
    await expect(getScoringRunAITrials("run/1", controller.signal)).resolves.toMatchObject({ trials: [{ id: "trial-1", sample_batch_id: "batch-1" }] });
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/scoring-runs/run%2F1/ai-trials");
    expect(fetchMock.mock.calls[0][1]?.signal).toBe(controller.signal);
  });

  it("reuses the same business command and independent human scores after a lost create response", async () => {
    const fetchMock = vi.fn<typeof fetch>()
      .mockRejectedValueOnce(new TypeError("response lost"))
      .mockResolvedValueOnce(jsonResponse({ trial: { id: "trial-1", sample_batch_id: "sample-batch" } }, 201));
    vi.stubGlobal("fetch", fetchMock);
    await expect(createScoringAITrial("run-1", input)).rejects.toThrow("response lost");
    await expect(createScoringAITrial("run-1", input)).resolves.toMatchObject({ trial: { sample_batch_id: "sample-batch" } });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const requests = fetchMock.mock.calls.map(([url, init]) => ({
      url,
      method: init?.method,
      key: new Headers(init?.headers).get("Idempotency-Key"),
      body: JSON.parse(String(init?.body))
    }));
    expect(requests[0]).toEqual(requests[1]);
    expect(requests[0]).toEqual({ url: "/api/v1/scoring-runs/run-1/ai-trials", method: "POST", key: input.command_id, body: input });
  });

  it("recovers only the server-linked trial batch without sending a client-chosen batch or segment list", async () => {
    const result = { batch: { id: "server-trial-batch" }, tasks: [], enqueue_result: { accepted_count: 5 } };
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(result));
    vi.stubGlobal("fetch", fetchMock);
    await expect(enqueueScoringAITrial("run/1", "trial/1")).resolves.toEqual(result);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/scoring-runs/run%2F1/ai-trials/trial%2F1/enqueue");
    expect(fetchMock.mock.calls[0][1]?.method).toBe("POST");
    expect(fetchMock.mock.calls[0][1]?.body).toBeUndefined();
  });

  it("passes the reviewed revision and reason to expansion and surfaces a stale decision without retrying", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse({ error: { code: "trial_revision_conflict" }, conflict_revision: 8 }, 409));
    vi.stubGlobal("fetch", fetchMock);
    const decision = { decision: "expand", reason: "已逐份核对人工基准与证据", expected_revision: 7 } as const;
    await expect(decideScoringAITrial("run-1", "trial-1", decision)).rejects.toMatchObject({ status: 409, code: "trial_revision_conflict", conflictRevision: 8 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/scoring-runs/run-1/ai-trials/trial-1/decision");
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toEqual(decision);
  });
});

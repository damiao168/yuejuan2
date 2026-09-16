import { afterEach, expect, it, vi } from "vitest";
import { emptyBackmarkBatchPage } from "../../../../tests/e2e/fixtures/backmarkContractFixtures";
import { listBackmarkBatches } from "./backmark";

afterEach(() => vi.unstubAllGlobals());

it("uses the generated backmark route and the same typed response as the E2E mock", async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(emptyBackmarkBatchPage)));
  vi.stubGlobal("fetch", fetch);
  await expect(listBackmarkBatches("exam-1", undefined, { limit: 50, cursor: "next" })).resolves.toEqual(emptyBackmarkBatchPage);
  const url = new URL(fetch.mock.calls[0][0], "http://local");
  expect(url.pathname).toBe("/api/v1/backmark-batches");
  expect(Object.fromEntries(url.searchParams)).toEqual({ exam_id: "exam-1", limit: "50", cursor: "next" });
});

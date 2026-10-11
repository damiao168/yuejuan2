// @vitest-environment jsdom
import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../api/businessCommand";
import type { ReviewTaskContext } from "../api/review";
import { createReviewTaskContextScope, reviewTaskContextKeys, reviewTaskContextQueryOptions } from "./reviewTaskContext";

const request = vi.hoisted(() => vi.fn());
vi.mock("../api/review", () => ({ getReviewTaskContext: request }));

function context(): ReviewTaskContext {
  return { task: { id: "task-A" }, draft: null, answer_artifact: { segment_image_url: "/segment", original_image_url: "/original" } } as ReviewTaskContext;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

let clients: QueryClient[];
function client() {
  const value = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(value);
  return value;
}

beforeEach(() => {
  clients = [];
  vi.clearAllMocks();
  setBusinessCommandScope("tenant-A", "teacher-A");
  request.mockResolvedValue({ context: context() });
});
afterEach(() => { clients.forEach((value) => value.clear()); });

describe("review task context session/cache ownership", () => {
  it("deduplicates within one provider and keeps independent provider caches separate", async () => {
    const first = client();
    const second = client();
    const scope = createReviewTaskContextScope("exam-A", false);
    const options = reviewTaskContextQueryOptions(scope, "task-A");
    await Promise.all([first.fetchQuery(options), first.fetchQuery(options)]);
    expect(request).toHaveBeenCalledTimes(1);
    expect(second.getQueryData(options.queryKey)).toBeUndefined();
    await second.fetchQuery(options);
    expect(request).toHaveBeenCalledTimes(2);
    expect(request.mock.calls[0][1]).toBeInstanceOf(AbortSignal);
  });

  it("does not retain original image addresses in teacher-experience context data", async () => {
    const value = client();
    const teacher = reviewTaskContextQueryOptions(createReviewTaskContextScope("exam-A", false), "task-A");
    const admin = reviewTaskContextQueryOptions(createReviewTaskContextScope("exam-A", true), "task-A");
    await value.fetchQuery(teacher);
    await value.fetchQuery(admin);
    expect(value.getQueryData(teacher.queryKey)?.answer_artifact.original_image_url).toBeUndefined();
    expect(value.getQueryData(admin.queryKey)?.answer_artifact.original_image_url).toBe("/original");
  });

  it("rejects an old account response and keeps the new account's task data", async () => {
    const value = client();
    const old = createReviewTaskContextScope("exam-A", false);
    const response = deferred<{ context: ReviewTaskContext }>();
    request.mockReturnValueOnce(response.promise);
    const pending = value.fetchQuery(reviewTaskContextQueryOptions(old, "task-A"));
    const outcome = pending.catch((error) => error);
    setBusinessCommandScope("tenant-B", "teacher-B");
    const current = createReviewTaskContextScope("exam-A", false);
    const currentKey = reviewTaskContextKeys.detail(current, "task-A");
    const currentData = context();
    value.setQueryData(currentKey, currentData);
    response.resolve({ context: context() });
    expect(await outcome).toMatchObject({ code: "session_scope_changed" });
    expect(value.getQueryData(currentKey)).toBe(currentData);
    expect(value.getQueryData(reviewTaskContextKeys.detail(old, "task-A"))).toBeUndefined();
  });

  it("does not reuse a context cache after the same user signs in again", async () => {
    const value = client();
    const first = createReviewTaskContextScope("exam-A", false);
    await value.fetchQuery(reviewTaskContextQueryOptions(first, "task-A"));
    setBusinessCommandScope("", "");
    setBusinessCommandScope("tenant-A", "teacher-A");
    const next = createReviewTaskContextScope("exam-A", false);
    expect(reviewTaskContextKeys.detail(next, "task-A")).not.toEqual(reviewTaskContextKeys.detail(first, "task-A"));
    await value.fetchQuery(reviewTaskContextQueryOptions(next, "task-A"));
    expect(request).toHaveBeenCalledTimes(2);
  });

  it("never starts a query captured before logout", async () => {
    const scope = createReviewTaskContextScope();
    setBusinessCommandScope("", "");
    await expect(client().fetchQuery(reviewTaskContextQueryOptions(scope, "task-A"))).rejects.toMatchObject({ code: "session_scope_changed" });
    expect(request).not.toHaveBeenCalled();
  });
});

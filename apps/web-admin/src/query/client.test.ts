import { describe, expect, it } from "vitest";
import { ApiClientError } from "../api/client";
import { createAppQueryClient, shouldRetryQuery } from "./client";
import { examWorkspaceKeys, invalidateExamWorkspace } from "./examWorkspace";

describe("query defaults", () => {
  it("does not retry client errors and bounds transient retries", () => {
    expect(shouldRetryQuery(0, new ApiClientError(403, "forbidden", "Forbidden"))).toBe(false);
    expect(shouldRetryQuery(0, new ApiClientError(503, "unavailable", "Unavailable"))).toBe(true);
    expect(shouldRetryQuery(2, new TypeError("Network error"))).toBe(false);
  });

  it("invalidates only the changed exam workspace projection", async () => {
    const queryClient = createAppQueryClient();
    queryClient.setQueryData(examWorkspaceKeys.detail("exam-1"), { id: "exam-1" });
    queryClient.setQueryData(examWorkspaceKeys.detail("exam-2"), { id: "exam-2" });

    await invalidateExamWorkspace(queryClient, "exam-1");

    expect(queryClient.getQueryState(examWorkspaceKeys.detail("exam-1"))?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(examWorkspaceKeys.detail("exam-2"))?.isInvalidated).toBe(false);
    queryClient.clear();
  });

  it("workspace cache separates authorization scopes while exam invalidation reaches both", async () => {
    const client = createAppQueryClient();
    client.setQueryData(examWorkspaceKeys.detail("exam", "before"), { secret: "foreign" });
    expect(client.getQueryData(examWorkspaceKeys.detail("exam", "after"))).toBeUndefined();
    client.setQueryData(examWorkspaceKeys.detail("exam", "after"), { safe: "local" });
    await invalidateExamWorkspace(client, "exam");
    for (const scope of ["before", "after"]) expect(client.getQueryState(examWorkspaceKeys.detail("exam", scope))?.isInvalidated).toBe(true);
    client.clear();
  });
});

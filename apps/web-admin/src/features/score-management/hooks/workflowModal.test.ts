import { describe, expect, it } from "vitest";
import { closedWorkflowModal, workflowModalReducer } from "./workflowModal";

describe("score workflow modal lifecycle", () => {
  it("opens, closes, and starts a clean lifecycle when reopened", () => {
    const opened = workflowModalReducer(closedWorkflowModal, { type: "open" });
    expect(opened.status).toBe("open");
    const closed = workflowModalReducer(opened, { type: "close" });
    expect(closed).toEqual({ status: "closed" });
    expect(workflowModalReducer(closed, { type: "open" })).toEqual({ status: "open" });
  });
});

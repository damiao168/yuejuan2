import { describe, expect, it } from "vitest";
import { LatestRequestController } from "./latestRequest";

describe("latest request controller", () => {
  it("accepts an initial load", () => {
    const controller = new LatestRequestController();
    const request = controller.begin("exam-a");
    expect(request.key).toBe("exam-a");
    expect(request.isCurrent()).toBe(true);
  });

  it("rejects a stale response when switching exams", () => {
    const controller = new LatestRequestController();
    const previousExam = controller.begin("exam-a");
    const currentExam = controller.begin("exam-b");
    expect(previousExam.isCurrent()).toBe(false);
    expect(currentExam.isCurrent()).toBe(true);
  });

  it("invalidates an unmounted request and accepts a retry", () => {
    const controller = new LatestRequestController();
    const failedAttempt = controller.begin("exam-a");
    controller.invalidate();
    const retry = controller.begin("exam-a");
    expect(failedAttempt.isCurrent()).toBe(false);
    expect(retry.isCurrent()).toBe(true);
  });
});

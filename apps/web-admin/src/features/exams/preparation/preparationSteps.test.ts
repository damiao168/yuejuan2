import { describe, expect, it } from "vitest";
import type { ExamReadiness } from "../../../api/configuration";
import { preparationNavigationSteps } from "./preparationSteps";

const confirmedAt = "2026-10-12T01:00:00Z";

function confirmation(overrides: Partial<ExamReadiness> = {}) {
  const readiness: ExamReadiness = {
    ready: true, confirmed: false, configuration_hash: "current-config", import_snapshot_available: false,
    checks: [], ...overrides
  };
  return preparationNavigationSteps(readiness).find((step) => step.key === "confirmation")!;
}

describe("准备确认导航", () => {
  it("后续阶段配置变化后保留此前准备确认事实，并提示重新核对当前配置", () => {
    const step = confirmation({
      ready: false, configuration_hash: "changed-config",
      preparation_snapshot_id: "historical-snapshot", preparation_confirmed_at: confirmedAt
    });
    expect(step.state).toBe("complete");
    expect(step.detail).toContain(`准备已于 ${new Date(confirmedAt).toLocaleString("zh-CN", { hour12: false })} 确认`);
    expect(step.detail).toContain("此前确认记录");
    expect(step.detail).toContain("当前配置请重新核对");
    expect(step.detail).not.toContain("准备已核对并确认");
  });

  it.each([
    {},
    { preparation_snapshot_id: "historical-snapshot" },
    { preparation_confirmed_at: confirmedAt }
  ])("配置尚未确认且没有完整历史记录时仍待确认：%j", (history) => {
    expect(confirmation(history)).toMatchObject({ state: "incomplete", detail: "检查已通过，仍需核对并确认准备" });
  });

  it("匹配当前配置的确认快照仍可展示完成，但不声称当前已确认", () => {
    const step = confirmation({ snapshot_id: "current-snapshot", confirmed_at: confirmedAt });
    expect(step.state).toBe("complete");
    expect(step.detail).toContain("此前确认记录");
  });

  it("当前准备已确认时按当前确认时间展示", () => {
    const step = confirmation({ confirmed: true, confirmed_at: confirmedAt });
    expect(step.state).toBe("complete");
    expect(step.detail).toContain("准备已于");
    expect(step.detail).not.toContain("此前确认记录");
  });
});

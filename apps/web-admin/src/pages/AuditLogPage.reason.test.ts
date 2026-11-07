import { describe, expect, it } from "vitest";
import { auditReasonText } from "./AuditLogPage";

describe("操作审计说明", () => {
  it("旧记录的英文系统说明显示为中文，原值不改", () => {
    expect(auditReasonText("score.finalized", "finalize exam grades")).toBe("汇总考试成绩");
    expect(auditReasonText("score.report_exported", "export internal non-official exam score report")).toBe("导出整场考试内部成绩报表（非正式版本）");
  });

  it("说明只是重复动作代码或为空时不显示，其他说明仍脱敏", () => {
    expect(auditReasonText("score_release.published", "score_release.published")).toBe("-");
    expect(auditReasonText("score.confirmed", "  ")).toBe("-");
    expect(auditReasonText("score.confirmed", "已核对，本场成绩无误")).toBe("已核对，本场成绩无误");
    expect(auditReasonText("user.updated", "password=secret")).not.toContain("secret");
  });
});

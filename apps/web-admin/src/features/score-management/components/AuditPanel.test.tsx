// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { AuditLog } from "../../../api/audit";
import { AuditPanel } from "./AuditPanel";

describe("成绩导出操作记录", () => {
  it("区分正式发布快照与两种内部核验导出", () => {
    const actions = ["score.exported", "score.report_exported", "score_release.formal_exported"];
    const logs = actions.map((action, index) => ({ id: String(index), tenant_id: "synthetic", action, target_type: "exam", created_at: "2026-10-31T00:00:00Z" })) as AuditLog[];
    const host = document.createElement("div");
    host.innerHTML = renderToStaticMarkup(<AuditPanel canRead logs={logs} />);
    expect([...host.querySelectorAll("strong")].map(node => node.textContent)).toEqual(["内部原始数据导出", "内部核验表导出", "正式成绩表导出"]);
    expect(host.textContent).not.toContain("成绩操作");
  });
});

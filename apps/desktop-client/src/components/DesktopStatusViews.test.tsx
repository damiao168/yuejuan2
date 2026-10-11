import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { SyncQueueItem } from "../types";
import { QueueList, ScanQueueRecovery, ScanQueueTable } from "./DesktopStatusViews";

function queueItem(status: SyncQueueItem["status"]): SyncQueueItem {
  return { id: "scan-1", title: "答卷", kind: "scan_upload", status, progress: 0, detail: "待同步", updatedAt: "2026-10-03T00:00:00Z" };
}

describe("desktop queue status rendering", () => {
  it.each([
    ["not_configured", "未配置/待接入"],
    ["failed", "上传失败"],
    ["succeeded", "已完成"],
    ["uploading", "上传中"],
    ["pending", "等待上传"],
    ["conflict", "需要处理"]
  ] as const)("shows the actual %s queue state", (status, label) => {
    expect(renderToStaticMarkup(<QueueList items={[queueItem(status)]} />)).toContain(label);
  });

  it("keeps all local quality outcomes visible in the scan table", () => {
    const item = queueItem("failed");
    item.qualityChecks = [
      { key: "pass", label: "完整性", status: "passed", detail: "通过" },
      { key: "warn", label: "清晰度", status: "warning", detail: "待核对" },
      { key: "fail", label: "页码", status: "failed", detail: "缺失" },
      { key: "unset", label: "模板", status: "not_configured", detail: "未设置" }
    ];
    const html = renderToStaticMarkup(<ScanQueueTable items={[item]} onRetry={() => undefined} />);
    expect(html).toContain("完整性: 通过");
    expect(html).toContain("清晰度: 请关注");
    expect(html).toContain("页码: 失败");
    expect(html).toContain("模板: 未配置/待接入");
  });

  it("shows batch names and creation time while keeping internal identifiers in folded diagnostics", () => {
    const item = { ...queueItem("pending"), examId: "exam-internal", captureBatchId: "batch-internal", examName: "英语月考", captureBatchName: "教务处上午扫描", captureBatchCreatedAt: "2026-10-10T01:00:00Z" };
    for (const html of [renderToStaticMarkup(<QueueList items={[item]} />), renderToStaticMarkup(<ScanQueueTable items={[item]} onRetry={() => undefined} />)]) {
      expect(html).toContain("英语月考"); expect(html).toContain("教务处上午扫描"); expect(html).toContain("批次创建");
      expect(html).toMatch(/<details><summary>内部关联信息<\/summary>.*exam-internal.*batch-internal.*<\/details>/);
    }
  });

  it("does not call an unreadable local queue empty and provides an actual same-page retry", () => {
    const html = renderToStaticMarkup(<ScanQueueRecovery status="error" error="磁盘读取失败" onRetry={() => undefined} onDiagnostics={() => undefined} />);
    expect(html).toContain("本机上传队列读取失败"); expect(html).toContain("重新读取本机队列"); expect(html).toContain("打开诊断");
    expect(renderToStaticMarkup(<QueueList items={[]} emptyText={false} />)).toBe("");
    expect(renderToStaticMarkup(<QueueList items={[queueItem("pending")]} emptyText={false} />)).toContain("等待上传");
  });
});

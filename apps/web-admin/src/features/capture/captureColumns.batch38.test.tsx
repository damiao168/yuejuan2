import type { ReactElement, ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { ColumnType } from "antd/es/table";
import { describe, expect, it, vi } from "vitest";
import type { CaptureFile, CapturePage } from "../../api/capture";
import { captureFileColumns, capturePageColumns } from "./captureColumns";

function file(overrides: Partial<CaptureFile> = {}): CaptureFile {
  return {
    id: "file-38", capture_batch_id: "batch-38", file_asset_id: "asset-38",
    original_name: "答卷.pdf", content_type: "application/pdf", sha256: "hash-38",
    byte_size: 100, page_count: 1, status: "ready", created_at: "2026-10-08T00:00:00Z",
    ...overrides,
  };
}

function page(overrides: Partial<CapturePage> = {}): CapturePage {
  return {
    id: "page-38", capture_batch_id: "batch-38", capture_file_id: "file-38",
    source_index: 1, sequence_no: 1, rotation_degrees: 0, decoded_file_asset_id: "decoded-38",
    status: "ready", revision: 4, page_identity: {}, match_candidates: [], ...overrides,
  };
}

function fileIssue(record: CaptureFile): ReactNode {
  const column = captureFileColumns().find((item) => item.title === "问题") as ColumnType<CaptureFile>;
  return column.render!(undefined, record, 0) as ReactNode;
}

function templateCell(record: CapturePage): ReactNode {
  const columns = capturePageColumns({
    batchCanManage: true,
    showPage: vi.fn().mockResolvedValue(undefined),
    rotatePage: vi.fn().mockResolvedValue(undefined),
    changePageLifecycle: vi.fn().mockResolvedValue(undefined),
    setQualityOverrideReason: vi.fn(),
    setQualityOverridePage: vi.fn(),
  });
  const column = columns.find((item) => item.title === "模板判断") as ColumnType<CapturePage>;
  return column.render!(undefined, record, 0) as ReactNode;
}

describe("采集列错误与模板候选展示", () => {
  it.each([
    ["duplicate", "pdf_decode_failed", "PDF 无法解析，请重新导出后再导入", "pdf_decode_failed"],
    ["failed", "unsupported_type", "文件格式不支持，请转为 PDF 或图片", "unsupported_type"],
    ["duplicate", "future_error", "处理失败，请删除后重新导入", "future_error"],
    ["duplicate", "", "内容与批次内文件重复", undefined],
    ["failed", "", "-", undefined],
    ["ready", undefined, "-", undefined],
  ] as const)("%s 文件的错误码 %s 保留原提示优先级", (status, errorCode, text, title) => {
    const cell = fileIssue(file({ status, error_code: errorCode }));
    expect(renderToStaticMarkup(cell)).toContain(text);
    if (typeof cell === "string") {
      expect(title).toBeUndefined();
      expect(cell).toBe(text);
      return;
    }
    expect((cell as ReactElement<{ title: string }>).props.title).toBe(title);
  });

  it("比较中先显示处理状态，无候选时保留短横线", () => {
    expect(templateCell(page({ status: "page_matching", match_candidates: [{ template_name: "旧候选" }] }))).toBe("正在比较已锁定模板…");
    expect(templateCell(page())).toBe("-");
  });

  it.each([
    ["needs_review", "需确认：候选模板 · 43%"],
    ["ready", "已识别：候选模板 · 43%"],
  ] as const)("%s 的前三个候选保留名称回退、零和负版本及 NaN 转换", (status, text) => {
    const cell = templateCell(page({ status, match_candidates: [
      { template_name: "", template_id: "备选ID", version_no: 0, score: "0.425" },
      { template_name: "负版本", version_no: -2, score: "0.876" },
      { template_id: "", version_no: "非法版本", score: "非法分数" },
      { template_name: "第四候选", version_no: 99, score: 1 },
    ] })) as ReactElement<{ title: string }>;
    expect(renderToStaticMarkup(cell)).toContain(text);
    expect(cell.props.title).toBe("备选ID · 43%；负版本 v-2 · 88%；候选模板 · NaN%");
  });

  it("非零版本保留主标签和候选详情中的版本后缀", () => {
    const cell = templateCell(page({ match_candidates: [{ template_name: "模板甲", version_no: "3", score: "0.994" }] })) as ReactElement<{ title: string }>;
    expect(renderToStaticMarkup(cell)).toContain("已识别：模板甲 v3 · 99%");
    expect(cell.props.title).toBe("模板甲 v3 · 99%");
  });
});

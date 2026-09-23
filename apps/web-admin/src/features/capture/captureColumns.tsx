import { Button, Space, Tooltip, type TableColumnsType } from "antd";
import { FolderOpen, RotateCw, Trash2, Undo2 } from "lucide-react";
import type { CaptureFile, CapturePage } from "../../api/capture";
import { StatusTag } from "../../components/StatusTag";
import { statusLabels, statusTone } from "./capturePresentation";

export const errorLabels: Record<string, string> = {
  pdf_decode_failed: "PDF 无法解析，请重新导出后再导入",
  unsupported_type: "文件格式不支持，请转为 PDF 或图片",
};

const contentTypeLabels: Record<string, string> = {
  "application/pdf": "PDF",
  "image/jpeg": "JPG 图片",
  "image/png": "PNG 图片",
  "image/tiff": "TIFF 图片",
};

export function captureFileColumns(): TableColumnsType<CaptureFile> {
  return [
    { title: "文件", dataIndex: "original_name", ellipsis: true },
    {
      title: "类型",
      dataIndex: "content_type",
      width: 150,
      render: (value: string) => (
        <span title={value}>
          {contentTypeLabels[value] ??
            (value ? value.split("/").pop()!.toUpperCase() : "-")}
        </span>
      ),
    },
    { title: "页数", dataIndex: "page_count", width: 72 },
    {
      title: "状态",
      width: 110,
      render: (_, item) => (
        <StatusTag tone={statusTone(item.status)}>
          {statusLabels[item.status] ?? "未知状态"}
        </StatusTag>
      ),
    },
    {
      title: "问题",
      width: 200,
      render: (_, item) =>
        item.error_code ? (
          <Tooltip title={item.error_code}>
            <span>
              {errorLabels[item.error_code] ?? "处理失败，请删除后重新导入"}
            </span>
          </Tooltip>
        ) : item.status === "duplicate" ? (
          "内容与批次内文件重复"
        ) : (
          "-"
        ),
    },
  ];
}

export function capturePageColumns({ batchCanManage, showPage, rotatePage, changePageLifecycle, setQualityOverrideReason, setQualityOverridePage }: {
  batchCanManage: boolean;
  showPage: (page: CapturePage) => Promise<void>;
  rotatePage: (page: CapturePage) => Promise<void>;
  changePageLifecycle: (page: CapturePage) => Promise<void>;
  setQualityOverrideReason: (reason: string) => void;
  setQualityOverridePage: (page: CapturePage) => void;
}): TableColumnsType<CapturePage> {
  return [
    { title: "扫描顺序", dataIndex: "sequence_no", width: 90 },
    { title: "原文件页码", dataIndex: "source_index", width: 100 },
    { title: "答卷页码", dataIndex: "assigned_page_no", width: 90 },
    {
      title: "旋转",
      dataIndex: "rotation_degrees",
      width: 72,
      render: (value: number) => `${value}°`,
    },
    {
      title: "状态",
      width: 110,
      render: (_, item) => (
        <StatusTag tone={statusTone(item.status)}>
          {statusLabels[item.status] ?? "未知状态"}
        </StatusTag>
      ),
    },
    {
      title: "模板判断",
      width: 220,
      render: (_, item) => {
        if (item.status === "page_matching") return "正在比较已锁定模板…";
        const candidates = item.match_candidates ?? [];
        if (!candidates.length) return "-";
        const top = candidates[0];
        const name = String(top.template_name || "候选模板");
        const version = Number(top.version_no || 0);
        const score = Math.round(Number(top.score || 0) * 100);
        const detailText = candidates.slice(0, 3).map((candidate) => {
          const candidateName = String(candidate.template_name || candidate.template_id || "候选模板");
          const candidateVersion = Number(candidate.version_no || 0);
          return `${candidateName}${candidateVersion ? ` v${candidateVersion}` : ""} · ${Math.round(Number(candidate.score || 0) * 100)}%`;
        }).join("；");
        return <Tooltip title={detailText}><span>{item.status === "needs_review" ? "需确认：" : "已识别："}{name}{version ? ` v${version}` : ""} · {score}%</span></Tooltip>;
      },
    },
    {
      title: "操作",
      width: 280,
      render: (_, item) => (
        <Space>
          <Button
            size="small"
            icon={<FolderOpen size={14} />}
            onClick={() => void showPage(item)}
            aria-label="查看页面"
          />
          <Button
            size="small"
            icon={<RotateCw size={14} />}
            onClick={() => void rotatePage(item)}
            disabled={!batchCanManage || item.status === "deleted"}
            aria-label="顺时针旋转"
          />
          <Button
            size="small"
            danger={item.status !== "deleted"}
            icon={
              item.status === "deleted" ? (
                <Undo2 size={14} />
              ) : (
                <Trash2 size={14} />
              )
            }
            onClick={() => void changePageLifecycle(item)}
            disabled={!batchCanManage}
            aria-label={item.status === "deleted" ? "恢复页面" : "删除页面"}
          />
          {["review", "failed"].includes(String(item.page_identity.quality_status ?? "")) &&
          item.status !== "deleted" ? (
            <Button
              size="small"
              type="primary"
              disabled={!batchCanManage || !item.submission_page_id}
              onClick={() => {
                setQualityOverrideReason("");
                setQualityOverridePage(item);
              }}
            >
              质量放行
            </Button>
          ) : null}
        </Space>
      ),
    },
  ];
}

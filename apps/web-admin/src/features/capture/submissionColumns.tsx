import { Button, Dropdown, Space, Tooltip, Upload, type TableColumnsType } from "antd";
import { Eye, FileSearch, Image as ImageIcon, MoreHorizontal, RotateCcw } from "lucide-react";
import { getSafeUserText } from "../../api/client";
import type { OcrTask, Submission, SubmissionPage } from "../../api/submissions";
import { StatusTag } from "../../components/StatusTag";
import type { SubmissionView } from "./captureQueries";
import { latestTask, summaryString, processingState, statusTone, derivedIssues, issueTone, pageStatusTone, pageStatusLabels, ocrTone, ocrLabel, formatTime } from "./submissionPresentation";

interface SubmissionColumnOptions {
  studentName: (submission: Submission) => string;
  openPages: (row: SubmissionView) => Promise<void>;
  openOcrDrawer: (row: SubmissionView) => Promise<void>;
}

interface PageColumnOptions {
  previewLoading: boolean;
  previewPage: (page: SubmissionPage) => Promise<void>;
  replacePage: (page: SubmissionPage, file: File) => Promise<void>;
  canWrite: boolean;
  actioning: string | null;
}

export function captureSubmissionColumns({ studentName, openPages, openOcrDrawer }: SubmissionColumnOptions): TableColumnsType<SubmissionView> {
  return [
    {
      title: (
        <Tooltip title="上传时取自文件名，关联学生后显示准考证号">
          <span>答题卡编号</span>
        </Tooltip>
      ),
      fixed: "left",
      width: 210,
      render: (_, row) => (
        <div className="capture-identity-cell">
          <strong>{row.submission.candidate_no || "暂未生成"}</strong>
        </div>
      )
    },
    { title: "学生姓名", width: 130, render: (_, row) => studentName(row.submission) },
    {
      title: "页数",
      width: 92,
      render: (_, row) => `${row.pages.length || row.submission.actual_page_count}/${row.submission.expected_page_count || "未设"}`
    },
    {
      title: "当前状态",
      width: 190,
      render: (_, row) => {
        const task = latestTask(row.ocrTasks);
        const taskStatus = task?.status ?? summaryString(row, "latest_ocr_status");
        const state = processingState(row);
        if (state === "failed") return <StatusTag tone="danger">{taskStatus === "failed" ? "文字识别失败" : "图片质量未通过"}</StatusTag>;
        if (state === "completed") return <StatusTag tone="success">处理完成</StatusTag>;
        if (state === "processing") return <StatusTag tone="processing">{taskStatus === "processing" ? "正在识别" : "识别排队中"}</StatusTag>;
        if (taskStatus === "completed") return <StatusTag tone="warning">题目区域尚未形成</StatusTag>;
        return <StatusTag tone={statusTone(row.submission.status)}>待核对</StatusTag>;
      }
    },
    {
      title: "问题说明",
      width: 300,
      render: (_, row) => {
        const issues = derivedIssues(row);
        return issues.length > 0 ? (
          <Space wrap size={[0, 4]}>
            {issues.slice(0, 3).map((issue, index) => (
              <StatusTag key={`${issue.code}-${index}`} tone={issueTone(issue.code)}>
                {getSafeUserText(issue.message, "处理检查未通过")}
              </StatusTag>
            ))}
            {issues.length > 3 ? <StatusTag tone="neutral">{`+${issues.length - 3}`}</StatusTag> : null}
          </Space>
        ) : (
          <span className="muted-text">暂无问题</span>
        );
      }
    },
    {
      title: "查看",
      fixed: "right",
      width: 220,
      render: (_, row) => {
        // 旧答卷仅供材料核对；状态推进统一留在批次流水线，不能由旧页面拼接质检、OCR和直接切题接口。
        return (
          <Space className="table-actions" size={6}>
            <Button
              type="primary"
              onClick={() => void openPages(row)}
            >
              查看答题卡
            </Button>
            <Dropdown
              menu={{
                items: [
                  { key: "pages", label: "查看原始页面", icon: <ImageIcon size={14} />, onClick: () => openPages(row) },
                  { key: "ocr", label: "查看识别详情", icon: <FileSearch size={14} />, onClick: () => void openOcrDrawer(row) }
                ]
              }}
              trigger={["click"]}
            >
              <Button aria-label="更多操作" icon={<MoreHorizontal size={16} />} />
            </Dropdown>
          </Space>
        );
      }
    }
  ];
}

export function capturePageColumns({ previewLoading, previewPage, replacePage, canWrite, actioning }: PageColumnOptions): TableColumnsType<SubmissionPage> {
  return [
    { title: "页码", dataIndex: "page_no", width: 72 },
    { title: "状态", dataIndex: "status", width: 110, render: (value: string) => <StatusTag tone={pageStatusTone(value)}>{pageStatusLabels[value] ?? "未知状态"}</StatusTag> },
    {
      title: "质量问题",
      width: 180,
      render: (_, page) =>
        page.quality_issues.length > 0 ? (
          <Space wrap>
            {page.quality_issues.map((issue, index) => (
              <StatusTag key={`${issue.code}-${index}`} tone={issueTone(issue.code)}>
                {getSafeUserText(issue.message, "图片质量检查未通过")}
              </StatusTag>
            ))}
          </Space>
        ) : (
          <StatusTag tone="success">无</StatusTag>
        )
    },
    {
      title: "操作",
      width: 190,
      render: (_, page) => (
        <Space>
          <Button size="small" icon={<Eye size={14} />} loading={previewLoading} onClick={() => void previewPage(page)}>
            查看
          </Button>
          <Upload
            showUploadList={false}
            beforeUpload={(file) => {
              void replacePage(page, file);
              return false;
            }}
          >
            <Button size="small" icon={<RotateCcw size={14} />} disabled={!canWrite || Boolean(actioning)}>
              重传
            </Button>
          </Upload>
        </Space>
      )
    }
  ];
}

export function captureOcrTaskColumns(): TableColumnsType<OcrTask> {
  return [
    {
      title: "任务",
      dataIndex: "id",
      width: 130,
      render: (value: string, _task, index) => (
        <Tooltip title={value}>
          <span>识别任务 {index + 1}</span>
        </Tooltip>
      )
    },
    { title: "状态", width: 110, render: (_, task) => <StatusTag tone={ocrTone(task)}>{ocrLabel([task])}</StatusTag> },
    { title: "结果数", dataIndex: "result_count", width: 90 },
    { title: "创建时间", dataIndex: "created_at", width: 170, render: formatTime }
  ];
}

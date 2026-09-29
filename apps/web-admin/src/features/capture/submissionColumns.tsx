import { Button, Dropdown, Space, Tooltip, Upload, type TableColumnsType } from "antd";
import { Eye, FileSearch, Image as ImageIcon, MoreHorizontal, RotateCcw } from "lucide-react";
import { getSafeUserText } from "../../api/client";
import { runImageQualityCheck } from "../../api/capture";
import { createOcrTask, generateAnswerSegments, runQualityCheck, updateSubmissionStatus, type OcrTask, type Submission, type SubmissionPage } from "../../api/submissions";
import { StatusTag } from "../../components/StatusTag";
import type { SubmissionView } from "./captureQueries";
import { latestTask, summaryString, summaryNumber, processingState, statusTone, derivedIssues, issueTone, pageStatusTone, pageStatusLabels, ocrTone, ocrLabel, formatTime } from "./submissionPresentation";

interface SubmissionColumnOptions {
  studentName: (submission: Submission) => string;
  canWrite: boolean;
  actioning: string | null;
  runAction: (key: string, action: () => Promise<void>, successText: string, reload?: () => Promise<void>) => Promise<void>;
  refreshSingle: (submissionId: string) => Promise<void>;
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

export function captureSubmissionColumns({ studentName, canWrite, actioning, runAction, refreshSingle, openPages, openOcrDrawer }: SubmissionColumnOptions): TableColumnsType<SubmissionView> {
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
        if (taskStatus === "completed") return <StatusTag tone="warning">等待生成题目区域</StatusTag>;
        return <StatusTag tone={statusTone(row.submission.status)}>等待自动处理</StatusTag>;
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
      title: "下一步",
      fixed: "right",
      width: 220,
      render: (_, row) => {
        const id = row.submission.id;
        const task = latestTask(row.ocrTasks);
        const taskStatus = task?.status ?? summaryString(row, "latest_ocr_status");
        const segmentCount = row.segments.length || summaryNumber(row, "segment_count");
        const qualityPending = row.submission.quality_status !== "passed";
        const needsReady = !qualityPending && row.submission.status !== "ready_for_ocr";
        const ocrFailed = taskStatus === "failed";
        const needsOcr = !qualityPending && !needsReady && (!taskStatus || ocrFailed);
        const needsSegments = taskStatus === "completed" && segmentCount === 0;

        const runNext = () => {
          if (qualityPending) {
            return runAction(`quality-${id}`, async () => {
              // 先确认页面完整，再排图像质量任务；完整性失败时不能继续下游识别。
              const integrity = await runQualityCheck(id);
              if (!integrity.result.valid) {
                throw new Error("答卷完整性未通过，请先补齐或更正页面");
              }
              await runImageQualityCheck(id);
            }, "图像质量检测已进入队列", () => refreshSingle(id));
          }
          if (needsReady) {
            return runAction(`ready-${id}`, async () => {
              await updateSubmissionStatus(id, "ready_for_ocr", row.submission.revision);
            }, "答题卡已转入待识别，可点击『开始识别』继续", () => refreshSingle(id));
          }
          if (needsOcr) {
            return runAction(`ocr-${id}`, async () => {
              await createOcrTask(id);
            }, "文字识别已开始", () => refreshSingle(id));
          }
          if (needsSegments) {
            return runAction(`segment-${id}`, async () => {
              await generateAnswerSegments(id);
            }, "题目区域已生成", () => refreshSingle(id));
          }
          void openPages(row);
          return Promise.resolve();
        };

        const primaryLabel = qualityPending
          ? "检查图片质量"
          : needsReady
            ? "转入待识别"
            : needsOcr
              ? ocrFailed ? "重试识别" : "开始识别"
              : needsSegments
                ? "生成题目区域"
                : "查看答题卡";
        return (
          <Space className="table-actions" size={6}>
            <Button
              type="primary"
              disabled={!canWrite && primaryLabel !== "查看答题卡"}
              loading={Boolean(actioning?.endsWith(id))}
              onClick={() => void runNext()}
            >
              {primaryLabel}
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

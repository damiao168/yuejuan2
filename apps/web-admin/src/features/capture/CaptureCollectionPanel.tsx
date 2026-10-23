import { useEffect, useRef, useState } from "react";
import { Alert, App, Button, Empty, Input, Modal, Segmented, Space, type TableColumnsType } from "antd";
import { completeCaptureCollection, excludeCaptureFile, type CaptureCollectionEntry, type CaptureCollectionIssue, type CaptureCollectionSummary, type CompleteCaptureCollectionInput } from "../../api/captureCollection";
import { cancelCaptureBatch, processCaptureBatch } from "../../api/capture";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { setExamAttendance } from "../../api/scores";
import { ResponsiveTable } from "../../components/ResponsiveTable";
import { StatusTag } from "../../components/StatusTag";

const entryLabels: Record<CaptureCollectionEntry["status"], string> = {
  missing: "未收卷", processing: "正在处理", needs_attention: "需要处理", ready: "导入成功", absent: "已记缺考",
};
const issueLabels: Record<string, string> = {
  missing: "尚未收到答卷", missing_submission: "尚未收到答卷", missing_pages: "答卷缺页", incomplete_pages: "答卷缺页",
  duplicate_submission: "同一学生有重复答卷", student_unidentified: "尚未匹配学生", student_not_in_roster: "学生不在本场名册",
  absent_has_submission: "缺考学生仍有答卷，请核对", quality_failed: "图像质量需要处理", quality_review: "图像质量需要核对",
  registration_failed: "版面对齐失败", registration_review: "版面对齐需要核对", segments_missing: "题目切分尚未完成",
  grouping_incomplete: "答卷尚未组齐，请核对文件和页序", template_missing: "尚未确认本场答题卡模板", page_needs_review: "页面质量或版面对齐需要处理",
  file_failed: "文件处理失败", file_processing: "文件正在处理", task_processing: "页面正在处理", upload_group_incomplete: "本次选择的答卷尚未组齐", upload_incomplete: "本次上传未完成",
};

export interface CaptureCollectionTarget { batchId: string; submissionId?: string; tab: string }

export function CaptureCollectionPanel({ examId, actorKey, summary, loading, error, canManage, localBusy, onRefresh, onLocate, onReplace, onCompleted, onMaterialChanged }: Readonly<{
  examId: string;
  actorKey: string;
  summary?: CaptureCollectionSummary;
  loading: boolean;
  error?: string;
  canManage: boolean;
  localBusy: boolean;
  onRefresh: () => Promise<void>;
  onLocate: (target: CaptureCollectionTarget) => void;
  onReplace: (issue: CaptureCollectionIssue, reason: string) => Promise<void>;
  onCompleted: (summary: CaptureCollectionSummary) => Promise<void>;
  onMaterialChanged: (batchId: string) => Promise<void>;
}>) {
  const { message, modal } = App.useApp();
  const [filter, setFilter] = useState("pending");
  const [acting, setActing] = useState(false);
  const [editor, setEditor] = useState<{ entry: CaptureCollectionEntry; status: "expected" | "absent" } | { issue: CaptureCollectionIssue; replace?: boolean; cancelBatch?: boolean }>();
  const [reason, setReason] = useState("");
  const busy = useRef(false);
  const active = useRef(true);
  const dialog = useRef<{ destroy: () => void } | null>(null);
  const context = useRef({ examId, summary, canManage, localBusy });
  context.current = { examId, summary, canManage, localBusy };
  const commandKey = `capture-collection-complete:v1:${actorKey}:${examId}`;

  // biome-ignore lint/correctness/useExhaustiveDependencies: 换考试也必须销毁旧场次的结束确认框，不能只在卸载时清理。
  useEffect(() => {
    active.current = true;
    return () => { active.current = false; dialog.current?.destroy(); };
  }, [examId]);
  useEffect(() => {
    // 回读已确认本场关闭后，丢失响应的旧命令也已恢复；下一次补传必须使用新命令。
    if (summary?.closed && !summary.can_complete) {
      try { localStorage.removeItem(commandKey); } catch { /* 当前结果仍可查看，下次提交再报告存储错误。 */ }
    }
  }, [commandKey, summary?.closed, summary?.can_complete]);

  const pendingEntries = summary?.entries.filter(entry => entry.status !== "ready" && entry.status !== "absent") ?? [];
  const entries = filter === "all" ? summary?.entries ?? [] : pendingEntries;
  const blocked = !summary || loading || Boolean(error) || !canManage || localBusy || acting || !summary.can_complete;
  const finishReasons = !summary ? [error || "正在读取导入核对信息"]
    : error ? [error]
    : localBusy ? ["本页仍有上传或处理操作，请等待完成"]
    : !canManage ? ["当前为只读状态，无法结束导入"]
    : summary.closed && !summary.can_complete ? ["本场导入已结束；需要补传时请创建补传批次"]
    : summary.blockers.length ? summary.blockers
    : loading ? ["正在刷新导入核对信息"]
    : summary.can_complete ? [] : ["仍有答卷或材料需要处理，请检查下方列表"];

  function finishCollection() {
    if (blocked || !summary || busy.current) return;
    const revision = summary.revision;
    dialog.current = modal.confirm({
      title: "结束本场导入",
      content: `应收 ${summary.required_count} 人，已成功导入 ${summary.ready_count} 人，缺考 ${summary.absent_count} 人。结束时系统会再次核对全部学生与材料，并封存已处理批次。${summary.exam_status === "collecting" ? "考试将进入阅卷阶段。" : "本次补传不会改变当前阅卷阶段。"}`,
      okText: "确认结束导入", cancelText: "返回核对",
      onOk: async () => {
        const current = context.current;
        if (!active.current || current.examId !== examId || !current.canManage || current.localBusy || busy.current) return;
        busy.current = true;
        setActing(true);
        try {
          const stored = localStorage.getItem(commandKey);
          // 网络中断后沿用原修订号和命令键；新一轮核对不能悄悄改变已发出的结束操作。
          const command: CompleteCaptureCollectionInput = stored ? JSON.parse(stored) : { revision, idempotency_key: crypto.randomUUID() };
          if (!stored && (current.summary?.revision !== revision || !current.summary.can_complete)) {
            message.info("导入情况已变化，请按最新核对结果再次结束导入");
            await onRefresh();
            return;
          }
          if (!Number.isInteger(command.revision) || !command.idempotency_key) throw new Error("结束导入恢复记录无效，请刷新核对后重试");
          localStorage.setItem(commandKey, JSON.stringify(command));
          const result = await completeCaptureCollection(examId, command);
          localStorage.removeItem(commandKey);
          if (active.current && context.current.examId === examId) {
            await onCompleted(result.summary);
            message.success(result.summary.exam_status === "grading" && summary.exam_status === "collecting" ? "本场导入已结束，已进入阅卷阶段" : "本次导入已结束");
          }
        } catch (cause) {
          if (cause instanceof ApiClientError && cause.status >= 400 && cause.status < 500 && ![408, 429].includes(cause.status)) localStorage.removeItem(commandKey);
          // 提交可能已生效或被其他人更新；失败后也重读事实，避免继续使用旧人数和旧修订号。
          if (active.current && context.current.examId === examId) {
            await onRefresh();
            message.error(getUserErrorMessage(cause, "暂时无法确认结束结果，请刷新后重试"));
          }
        } finally {
          busy.current = false;
          if (active.current && context.current.examId === examId) setActing(false);
        }
      },
    });
  }

  async function saveResolution() {
    if (!editor || !canManage || localBusy || busy.current || !reason.trim()) return;
    const target = editor;
    busy.current = true;
    setActing(true);
    try {
      if ("entry" in target) {
        await setExamAttendance(examId, target.entry.student_id, target.status, reason.trim());
      } else if (target.replace) {
        await onReplace(target.issue, reason.trim());
      } else if (target.cancelBatch) {
        await cancelCaptureBatch(target.issue.batch_id, reason.trim());
      } else if (target.issue.file_id) {
        await excludeCaptureFile(target.issue.file_id, reason.trim());
      }
      if (!active.current || context.current.examId !== examId) return;
      setEditor(undefined);
      setReason("");
      if ("issue" in target && target.replace) return;
      await onRefresh();
      if ("issue" in target) await onMaterialChanged(target.issue.batch_id);
      message.success("entry" in target ? target.status === "absent" ? "已标记缺考并重新核对" : "已恢复应考并重新核对" : target.cancelBatch ? "该批次已取消，未完成上传不再计入" : "本次选择的材料已不计入，历史记录已保留");
    } catch (cause) {
      if (active.current && context.current.examId === examId) message.error(getUserErrorMessage(cause, "处理失败，请重新核对后重试"));
    } finally {
      busy.current = false;
      if (active.current && context.current.examId === examId) setActing(false);
    }
  }

  async function retryFile(issue: CaptureCollectionIssue) {
    if (!canManage || localBusy || busy.current) return;
    busy.current = true;
    setActing(true);
    try {
      await processCaptureBatch(issue.batch_id);
      if (!active.current || context.current.examId !== examId) return;
      await Promise.all([onRefresh(), onMaterialChanged(issue.batch_id)]);
      message.success("待处理文件已重新排队");
    } catch (cause) {
      if (active.current && context.current.examId === examId) message.error(getUserErrorMessage(cause, "文件重试失败，请查看材料详情"));
    } finally {
      busy.current = false;
      if (active.current && context.current.examId === examId) setActing(false);
    }
  }

  const columns: TableColumnsType<CaptureCollectionEntry> = [
    { title: "学生", key: "student", render: (_, entry) => <div><strong>{entry.student_name || entry.student_no}</strong><div className="muted">{[entry.class_name, entry.student_no].filter(Boolean).join(" · ")}</div></div> },
    { title: "导入情况", key: "status", render: (_, entry) => <div><StatusTag tone={entry.status === "ready" ? "success" : entry.status === "processing" ? "processing" : entry.status === "absent" ? "neutral" : "warning"}>{entryLabels[entry.status]}</StatusTag>{entry.status !== "ready" && entry.status !== "processing" ? <div className="muted">{entry.status === "absent" ? entry.reason || "已核实缺考" : issueLabels[entry.code] || entry.reason || (entry.status === "missing" ? "请补传答卷，或核实后标记缺考" : "请查看答卷处理具体问题")}</div> : null}</div> },
    { title: "页数", key: "pages", width: 100, render: (_, entry) => entry.status === "absent" ? "—" : `${entry.actual_pages} / ${entry.expected_pages}` },
    { title: "操作", key: "action", render: (_, entry) => <Space wrap>
      {entry.batch_id ? <Button size="small" disabled={localBusy} onClick={() => onLocate({ batchId: entry.batch_id!, submissionId: entry.submission_id, tab: entry.code.includes("quality") ? "image-quality" : entry.code === "page_needs_review" ? "issues" : ["grouping_incomplete", "missing_pages"].includes(entry.code) ? "files" : entry.code.includes("registration") || entry.code.includes("segment") || entry.code === "template_missing" ? "processing" : "matching" })}>{entry.status === "ready" ? "查看答卷" : "查看处理"}</Button> : null}
      {canManage && (entry.status === "missing" || entry.status === "absent") ? <Button size="small" disabled={acting || localBusy} onClick={() => { setEditor({ entry, status: entry.status === "absent" ? "expected" : "absent" }); setReason(""); }}>{entry.status === "absent" ? "恢复应考" : "标记缺考"}</Button> : null}
    </Space> },
  ];

  return <section className="capture-collection" aria-label="本场学生答卷核对">
    <div className="capture-collection-heading">
      <div><h2>学生答卷核对</h2><p>{summary ? `名册 ${summary.roster_count} 人 · 缺考 ${summary.absent_count} 人 · 每人 ${summary.pages_per_student || "待确认"} 页` : "按本场已确认名册核对学生答卷"}</p></div>
      <Space wrap><Button loading={loading} onClick={() => void onRefresh()}>刷新核对</Button><Button type="primary" disabled={blocked} loading={acting} onClick={finishCollection}>结束本场导入</Button></Space>
    </div>
    <div className="capture-collection-stats" aria-label="学生导入统计">
      <div><span>应收</span><strong>{summary?.required_count ?? "—"}</strong></div>
      <div><span>成功导入</span><strong>{summary?.ready_count ?? "—"}</strong></div>
      <div><span>待完成</span><strong>{summary?.pending_count ?? "—"}</strong></div>
    </div>
    {finishReasons.length ? <Alert type={summary?.closed && !error ? "success" : "info"} showIcon message={finishReasons.join("；")} /> : <p className="muted">应收学生答卷已齐全，正常处理通过的页面无需逐页人工确认。结束时由系统再次核对。</p>}
    {summary ? <>
      <div className="capture-collection-filter"><Segmented aria-label="学生核对范围" value={filter} onChange={value => setFilter(String(value))} options={[{ value: "pending", label: `待完成 (${pendingEntries.length})` }, { value: "all", label: "全部学生" }]} /></div>
      <ResponsiveTable className="dense-data-table" rowKey="student_id" columns={columns} dataSource={entries} pagination={{ pageSize: 10 }} size="small" locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={filter === "pending" ? "学生待办已处理完毕，请继续核对材料问题" : "本场暂无学生名册"} /> }} />
      {summary.issues.length ? <div className="capture-collection-materials"><h3>材料问题 <small>{summary.issues.length} 项，不计入学生人数</small></h3>{summary.issues.map(issue => <div className="capture-collection-material" key={`${issue.kind}:${issue.id}`}>
        <div><strong>{issue.name}</strong><span>{issueLabels[issue.code] || (issue.kind === "file" ? "文件需要处理" : issue.kind === "upload" ? "上传尚未完成" : issue.kind === "batch" ? "批次需要处理" : "答卷尚未唯一匹配到应考学生")}</span></div>
        <Space wrap><Button size="small" disabled={localBusy} onClick={() => onLocate({ batchId: issue.batch_id, submissionId: issue.submission_id, tab: issue.code === "task_processing" ? "processing" : issue.kind === "submission" ? "matching" : "files" })}>查看材料</Button>
          {canManage && issue.file_id && ["file_failed", "upload_group_incomplete"].includes(issue.code) ? <>{issue.code === "file_failed" ? <Button size="small" disabled={acting || localBusy} onClick={() => void retryFile(issue)}>重试原文件</Button> : null}<Button size="small" disabled={acting || localBusy} onClick={() => { setEditor({ issue, replace: true }); setReason(""); }}>上传替代答卷</Button><Button size="small" disabled={acting || localBusy} onClick={() => { setEditor({ issue }); setReason(""); }}>不计入</Button></> : null}
          {canManage && issue.code === "upload_incomplete" && !issue.file_id ? <Button size="small" disabled={acting || localBusy} onClick={() => { setEditor({ issue, cancelBatch: true }); setReason(""); }}>取消该批次</Button> : null}
        </Space>
      </div>)}</div> : null}
    </> : null}
    <Modal title={editor && "entry" in editor ? editor.status === "absent" ? `标记缺考：${editor.entry.student_name}` : `恢复应考：${editor.entry.student_name}` : editor && "issue" in editor && editor.replace ? "上传替代答卷" : editor && "issue" in editor && editor.cancelBatch ? "取消未完成上传所在批次" : "本次选择的材料不计入"} open={Boolean(editor)} okText={editor && "issue" in editor && editor.replace ? "继续选择完整答卷" : "确认并重新核对"} cancelText="取消" confirmLoading={acting} okButtonProps={{ disabled: !reason.trim() || !canManage || localBusy }} onCancel={() => { if (!acting) setEditor(undefined); }} onOk={() => void saveResolution()}>
      <p>{editor && "issue" in editor ? editor.cancelBatch ? "取消后，该批次的全部材料和未完成上传不再计入本场核对。请确认没有需保留的材料；对应学生仍需补传或核实缺考。" : `${editor.replace ? "请选择每名学生页数，并一次上传本次选择所对应的全部完整答卷。新文件登记成功后，" : "确认后，"}本次选择的全部文件及其答卷将退出有效输入，历史记录保留。${editor.replace ? "新答卷仍须处理成功后才算导入完成。" : "学生缺卷仍需补传或核实缺考。"}已产生评分的材料无法在这里移除。` : "请先核实学生情况并填写原因，系统会保留本次名册调整记录。"}</p>
      <Input.TextArea aria-label="处理原因" rows={3} maxLength={300} value={reason} onChange={event => setReason(event.target.value)} placeholder="填写本次处理原因" />
    </Modal>
  </section>;
}

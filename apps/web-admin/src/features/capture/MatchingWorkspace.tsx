import { useEffect, useRef, useState } from "react";
import { Alert, App, Button, Empty, InputNumber, Select, Space } from "antd";
import { Check, Combine, FileQuestion, FolderOpen, ScanLine, Scissors, UserCheck } from "lucide-react";
import type { CapturePage, MatchingQueue } from "../../api/capture";
import { StatusTag } from "../../components/StatusTag";
import { statusLabels, statusTone } from "./capturePresentation";
import { getUserErrorMessage } from "../../api/client";

function identityStatusPresentation(identityStatus: string): {
  tone: "success" | "danger" | "warning";
  label: string;
} {
  if (identityStatus === "matched") return { tone: "success", label: "已匹配" };
  if (identityStatus === "unknown") return { tone: "danger", label: "未知" };
  return { tone: "warning", label: "待确认" };
}

function assignedPageNo(page: CapturePage) {
  return Number.isInteger(page.assigned_page_no) && page.assigned_page_no! > 0 ? page.assigned_page_no : undefined;
}

function hasConfirmedPageNo(page: CapturePage) {
  // 上传分组也会先分配页码；只有已处理完成或具备页码识别/人工确认依据时，才移除设置入口。
  return Boolean(assignedPageNo(page) && (["ready", "completed"].includes(page.status)
    || page.page_identity?.page_no_source === "visual_template_match"
    || ["verified", "manual_override"].includes(String(page.page_identity?.barcode_status))));
}

function orderedPages(pages: CapturePage[]) {
  // assigned_page_no 是答题卡内页码；扫描顺序只用于未分配或重复页码的稳定排序，不能作为正确页码展示。
  return [...pages].sort((left, right) => (assignedPageNo(left) ?? Number.POSITIVE_INFINITY) - (assignedPageNo(right) ?? Number.POSITIVE_INFINITY) || left.sequence_no - right.sequence_no);
}

function MatchingPageRow({ page, canManage, actioning, onPreview, onConfirmPage, onSplit }: Readonly<{
  page: CapturePage;
  canManage: boolean;
  actioning: boolean;
  onPreview: (page: CapturePage) => Promise<void>;
  onConfirmPage: (page: CapturePage, pageNo: number) => Promise<void>;
  onSplit?: () => void;
}>) {
  const pageNo = hasConfirmedPageNo(page) ? assignedPageNo(page) : undefined;
  const [draft, setDraft] = useState<number | null>(assignedPageNo(page) ?? null);
  const [editing, setEditing] = useState(false);
  const validDraft = draft !== null && Number.isInteger(draft) && draft >= 1;
  function confirmPage() {
    if (!canManage || actioning || !validDraft || draft === null) return;
    void onConfirmPage(page, draft);
  }
  return <div className="matching-page-row" data-page-id={page.id}>
    <div className="matching-page-identity">
      <strong>{pageNo ? `第 ${pageNo} 页` : "页码待确认"}</strong>
      <StatusTag tone={statusTone(page.status)}>{statusLabels[page.status] ?? "未知状态"}</StatusTag>
    </div>
    <Space wrap>
      <Button icon={<FolderOpen size={15} />} onClick={() => void onPreview(page)}>查看图片</Button>
      {pageNo && !editing ? <Button disabled={!canManage || actioning} onClick={() => setEditing(true)}>调整页序</Button> : null}
      {onSplit ? <Button icon={<Scissors size={14} />} aria-label="拆分为新答卷" disabled={!canManage || actioning} onClick={onSplit} /> : null}
    </Space>
    {!pageNo || editing ? <div className="matching-page-confirmation">
      <InputNumber min={1} value={draft} onChange={value => setDraft(value === null ? null : Number(value))}
        onPressEnter={confirmPage} disabled={!canManage || actioning} aria-label="设置答卷页码" placeholder="填写页码" />
      <Button disabled={!canManage || actioning || !validDraft} onClick={confirmPage}>确认页码</Button>
      {editing ? <Button disabled={actioning} onClick={() => { setEditing(false); setDraft(assignedPageNo(page) ?? null); }}>取消调整</Button> : null}
    </div> : null}
  </div>;
}

export function MatchingWorkspace({
  queue,
  initialSubmissionId = "",
  canManage,
  readOnlyReason,
  onReopen,
  actioning,
  onPreview,
  onConfirmStudent,
  onUnknown,
  onConfirmPage,
  onSplit,
  onMerge,
}: Readonly<{
  queue: MatchingQueue;
  initialSubmissionId?: string;
  canManage: boolean;
  readOnlyReason?: string;
  onReopen?: () => void;
  actioning: boolean;
  onPreview: (page: CapturePage) => Promise<void>;
  onConfirmStudent: (
    submissionId: string,
    studentId: string,
    revision: number,
  ) => Promise<void>;
  onUnknown: (submissionId: string, revision: number) => Promise<void>;
  onConfirmPage: (page: CapturePage, pageNo: number) => Promise<void>;
  onSplit?: (submissionId: string, pageId: string) => Promise<void>;
  onMerge?: (targetId: string, sourceId: string) => Promise<void>;
}>) {
  const { modal } = App.useApp();
  const [selectedId, setSelectedId] = useState(initialSubmissionId || queue.submissions[0]?.id || "");
  const [candidateId, setCandidateId] = useState("");
  const [mergeTarget, setMergeTarget] = useState("");
  const [mergeFeedback, setMergeFeedback] = useState<{ success: boolean; text: string }>();
  useEffect(() => { if (initialSubmissionId && queue.submissions.some(item => item.id === initialSubmissionId)) setSelectedId(initialSubmissionId); }, [initialSubmissionId, queue.submissions]);
  // 编号属于答卷对象，不属于过滤后的位置；刷新和合并移除答卷时保留其余对象的编号。
  const submissionNumbers = useRef(new Map<string, number>());
  for (const item of queue.submissions) {
    if (!submissionNumbers.current.has(item.id)) {
      submissionNumbers.current.set(item.id, submissionNumbers.current.size + 1);
    }
  }
  function submissionLabel(item: MatchingQueue["submissions"][number]) {
    const student = queue.candidates.find((candidate) => candidate.id === item.student_id);
    let identity = "尚未确认学生";
    if (student) {
      identity = `${student.name} · ${student.student_no}`;
    } else if (item.candidate_no) {
      identity = `识别学号 ${item.candidate_no}（待确认）`;
    }
    return `答卷 ${submissionNumbers.current.get(item.id)} · ${identity} · ${item.pages.length} 页`;
  }
  const selected =
    queue.submissions.find((item) => item.id === selectedId) ??
    queue.submissions[0];
  // 切换答卷时重置候选，即使两份答卷的学生 ID 相同，也不能沿用上一次手动选择。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 答卷 ID 是候选选择的身份键；两份答卷学生相同也须丢弃上次合并目标。
  useEffect(() => {
    setCandidateId(selected?.student_id ?? "");
    setMergeTarget("");
  }, [selected?.id, selected?.student_id]);
  const target = queue.submissions.find((item) => item.id === mergeTarget && item.id !== selected?.id);
  function confirmMerge() {
    if (!selected || !target || !onMerge || !canManage || actioning) return;
    const source = selected;
    const destination = target;
    modal.confirm({
      title: "确认合并答卷",
      content: <div><p>来源：{submissionLabel(source)}</p><p>目标：{submissionLabel(destination)}</p><p>来源的全部页面将移入目标答卷，来源答卷将从列表移除。请先核对两份答卷的学生和页面。</p></div>,
      okText: "确认合并到目标答卷",
      cancelText: "继续核对",
      onOk: async () => {
        try {
          await onMerge(destination.id, source.id);
          setMergeFeedback({ success: true, text: `已将${submissionLabel(source)}合并到${submissionLabel(destination)}` });
          setSelectedId(destination.id);
          setMergeTarget("");
        } catch (error) {
          setMergeFeedback({ success: false, text: getUserErrorMessage(error, "合并失败，请核对后重试") });
        }
      },
    });
  }
  // 名册中已被其他已匹配答卷占用的学生不再作为候选；当前答卷的学生仍可保留。
  const used = new Set(
    queue.submissions
      .filter(
        (item) =>
          item.id !== selected?.id && item.identity_status === "matched",
      )
      .map((item) => item.student_id),
  );
  const candidates = queue.candidates.filter(
    (item) => !used.has(item.id) || item.id === selected?.student_id,
  );
  if (!selected) return <Empty description="暂无待匹配答卷" />;
  return (<>
    {!canManage ? <Alert type="info" showIcon message={readOnlyReason || "当前仅可查看答卷，暂不能修改学生匹配和页码。"}
      action={onReopen ? <Button disabled={actioning} onClick={onReopen}>重开批次后匹配</Button> : undefined} /> : null}
    <div className="matching-workspace">
      <aside className="matching-submissions">
        <div className="capture-pane-title">
          <strong>答卷</strong>
          <span>{queue.submissions.length}</span>
        </div>
        {queue.submissions.map((item) => {
          const identity = identityStatusPresentation(item.identity_status);
          return (
            <button
              type="button"
              key={item.id}
              className={
                item.id === selected.id
                  ? "capture-batch-row active"
                  : "capture-batch-row"
              }
              onClick={() => {
                setSelectedId(item.id);
                setCandidateId(item.student_id ?? "");
              }}
            >
              <span>
                <strong>答卷 {submissionNumbers.current.get(item.id)}</strong>
                <small>{submissionLabel(item).split(" · ").slice(1).join(" · ")}</small>
              </span>
              <StatusTag tone={identity.tone}>{identity.label}</StatusTag>
            </button>
          );
        })}
      </aside>
      <section className="matching-evidence">
        <h3>答卷页面</h3>
        {orderedPages(selected.pages).map(page => <MatchingPageRow key={`${selected.id}:${page.id}:${page.revision}:${page.assigned_page_no ?? "unset"}`}
          page={page} canManage={canManage} actioning={actioning} onPreview={onPreview} onConfirmPage={onConfirmPage}
          onSplit={selected.pages.length > 1 && selected.pages[0].id !== page.id && onSplit ? () => void onSplit(selected.id, page.id) : undefined} />)}
        <div className="matching-evidence-note">
          <ScanLine size={17} />
          <span>
            系统识别到的姓名、学号或条码仅供参考，最终以您在名册中确认的学生为准。
          </span>
        </div>
      </section>
      <section className="matching-candidates">
        <h3>本次考试名册</h3>
        <Select
          showSearch
          value={candidateId || undefined}
          onChange={setCandidateId}
          placeholder="按姓名或学号查找"
          optionFilterProp="label"
          disabled={!canManage || actioning}
          options={candidates.map((item) => ({
            value: item.id,
            label: `${item.name} · ${item.student_no} · ${item.class_name}`,
          }))}
        />
        <Space wrap>
          <Button
            type="primary"
            icon={<UserCheck size={16} />}
            loading={actioning}
            disabled={!canManage || actioning || !candidateId}
            onClick={() =>
              void onConfirmStudent(
                selected.id,
                candidateId,
                selected.identity_revision,
              )
            }
          >
            确认学生
          </Button>
          <Button
            danger
            icon={<FileQuestion size={16} />}
            loading={actioning}
            disabled={!canManage || actioning}
            onClick={() =>
              void onUnknown(selected.id, selected.identity_revision)
            }
          >
            标记未知
          </Button>
        </Space>
        <p className="muted-text">确认匹配后，名册会自动显示该生已交卷；未匹配到答卷的学生自动显示缺考。</p>
        {queue.submissions.length > 1 && onMerge ? (
          <Space direction="vertical">
            <Select
              value={mergeTarget || undefined}
              onChange={setMergeTarget}
              placeholder="合并到其他答卷"
              disabled={!canManage || actioning}
              options={queue.submissions
                .filter((item) => item.id !== selected.id)
                .map((item) => ({
                  value: item.id,
                  label: submissionLabel(item),
                }))}
            />
            {target ? <div aria-label="合并目标核对"><p>目标：{submissionLabel(target)}</p>{orderedPages(target.pages).map((page) => <Button key={page.id} onClick={() => void onPreview(page)}>{assignedPageNo(page) ? `查看目标第 ${assignedPageNo(page)} 页` : "查看目标待确认页面"}</Button>)}</div> : null}
            <Button
              icon={<Combine size={15} />}
              disabled={!canManage || actioning || !target}
              onClick={confirmMerge}
            >
              合并
            </Button>
          </Space>
        ) : null}
        {mergeFeedback ? <Alert type={mergeFeedback.success ? "success" : "error"} showIcon message={mergeFeedback.text} /> : null}
        {selected.identity_status === "matched" ? (
          <div className="matching-confirmed">
            <Check size={17} />
            {canManage ? "已确认，仍可重新选择并更正" : "学生身份已确认，当前仅可查看"}
          </div>
        ) : null}
      </section>
    </div></>
  );
}

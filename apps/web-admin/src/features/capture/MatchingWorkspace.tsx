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

export function MatchingWorkspace({
  queue,
  initialSubmissionId = "",
  canManage,
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
  useEffect(() => { if (initialSubmissionId && queue.submissions.some(item => item.id === initialSubmissionId)) setSelectedId(initialSubmissionId); }, [initialSubmissionId, queue.batch_id]);
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
  return (
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
        {selected.pages.map((page, index) => (
          <div className="matching-page-row" key={page.id}>
            <Button
              icon={<FolderOpen size={15} />}
              onClick={() => void onPreview(page)}
            >
              第 {page.sequence_no} 页
            </Button>
            <span className="muted-text">页码</span>
            <InputNumber
              key={`${page.id}-${page.assigned_page_no ?? "unset"}`}
              min={1}
              defaultValue={page.assigned_page_no ?? undefined}
              onBlur={(event) => {
                const next = Number(event.target.value);
                if (
                  Number.isInteger(next) &&
                  next >= 1 &&
                  next !== page.assigned_page_no
                )
                  void onConfirmPage(page, next);
              }}
              onPressEnter={(event) => {
                const next = Number(event.currentTarget.value);
                if (
                  Number.isInteger(next) &&
                  next >= 1 &&
                  next !== page.assigned_page_no
                )
                  void onConfirmPage(page, next);
              }}
              disabled={!canManage || actioning}
              aria-label="确认答卷页码"
            />
            <Space>
              <StatusTag tone={statusTone(page.status)}>
                {statusLabels[page.status] ?? "未知状态"}
              </StatusTag>
              {selected.pages.length > 1 && index > 0 && onSplit ? (
                <Button
                  icon={<Scissors size={14} />}
                  aria-label="拆分为新答卷"
                  onClick={() => void onSplit(selected.id, page.id)}
                />
              ) : null}
            </Space>
          </div>
        ))}
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
            disabled={!canManage || !candidateId}
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
            disabled={!canManage}
            onClick={() =>
              void onUnknown(selected.id, selected.identity_revision)
            }
          >
            标记未知
          </Button>
        </Space>
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
            {target ? <div aria-label="合并目标核对"><p>目标：{submissionLabel(target)}</p>{target.pages.map((page) => <Button key={page.id} onClick={() => void onPreview(page)}>查看目标第 {page.assigned_page_no ?? page.sequence_no} 页</Button>)}</div> : null}
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
            已确认，仍可重新选择并更正
          </div>
        ) : null}
      </section>
    </div>
  );
}

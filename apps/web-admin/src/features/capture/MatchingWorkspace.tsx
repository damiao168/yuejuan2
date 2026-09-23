import { useEffect, useState } from "react";
import { Button, Empty, InputNumber, Select, Space } from "antd";
import { Check, Combine, FileQuestion, FolderOpen, ScanLine, Scissors, UserCheck } from "lucide-react";
import type { CapturePage, MatchingQueue } from "../../api/capture";
import { StatusTag } from "../../components/StatusTag";
import { statusLabels, statusTone } from "./capturePresentation";

export function MatchingWorkspace({
  queue,
  canManage,
  actioning,
  onPreview,
  onConfirmStudent,
  onUnknown,
  onConfirmPage,
  onSplit,
  onMerge,
}: {
  queue: MatchingQueue;
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
}) {
  const [selectedId, setSelectedId] = useState(queue.submissions[0]?.id ?? "");
  const [candidateId, setCandidateId] = useState("");
  const [mergeTarget, setMergeTarget] = useState("");
  const selected =
    queue.submissions.find((item) => item.id === selectedId) ??
    queue.submissions[0];
  useEffect(() => {
    setCandidateId(selected?.student_id ?? "");
  }, [selected?.id, selected?.student_id]);
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
        {queue.submissions.map((item, index) => (
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
              <strong>答卷 {index + 1}</strong>
              <small>{item.pages.length} 页</small>
            </span>
            <StatusTag
              tone={
                item.identity_status === "matched"
                  ? "success"
                  : item.identity_status === "unknown"
                    ? "danger"
                    : "warning"
              }
            >
              {item.identity_status === "matched"
                ? "已匹配"
                : item.identity_status === "unknown"
                  ? "未知"
                  : "待确认"}
            </StatusTag>
          </button>
        ))}
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
          <Space.Compact>
            <Select
              value={mergeTarget || undefined}
              onChange={setMergeTarget}
              placeholder="合并到其他答卷"
              options={queue.submissions
                .filter((item) => item.id !== selected.id)
                .map((item, index) => ({
                  value: item.id,
                  label: `答卷 ${index + 1}`,
                }))}
            />
            <Button
              icon={<Combine size={15} />}
              disabled={!mergeTarget}
              onClick={() => void onMerge(mergeTarget, selected.id)}
            >
              合并
            </Button>
          </Space.Compact>
        ) : null}
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


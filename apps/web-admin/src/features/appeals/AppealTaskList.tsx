import type { Appeal } from "../../api/appeals";
import type { Exam } from "../../api/exams";
import type { Student } from "../../api/org";
import { EmptyState, LoadingState } from "../../components/PageState";
import { StatusTag } from "../../components/StatusTag";
import type { ProductExperience } from "../../router/experience";
import { assignmentProgress, formatTime, readableReason, statusLabels, statusTone } from "./appealPresentation";

type Props = Readonly<{
  loading: boolean;
  appeals: Appeal[];
  selectedId: string;
  mode: ProductExperience;
  exams: Record<string, Exam>;
  students: Record<string, Student>;
  onSelect: (id: string) => void;
}>;

export function AppealTaskList({ loading, appeals, selectedId, mode, exams, students, onSelect }: Props) {
  if (loading) return <LoadingState label="正在读取复核申请" />;
  if (!appeals.length) return <EmptyState title="暂无复核申请" description="当前筛选条件下没有待处理记录。" />;
  return <ul className="appeal-task-list" aria-label="复核申请">
    {appeals.map((appeal) => <li key={appeal.id}>
      <button type="button" className={`appeal-task-item${appeal.id === selectedId ? " active" : ""}`} aria-current={appeal.id === selectedId ? "true" : undefined} onClick={() => onSelect(appeal.id)}>
        <span className="appeal-task-first">
          <strong>{exams[appeal.exam_id]?.name ?? appeal.exam_name ?? "未命名考试"}</strong>
          <StatusTag tone={statusTone(appeal.status)}>{statusLabels[appeal.status] ?? "处理中"}</StatusTag>
        </span>
        <span>{mode === "teacher" ? appeal.anonymous_code || "匿名答卷" : students[appeal.student_id]?.name || "学生信息未匹配"} · {appeal.question_no || "整卷"}</span>
        <span className="appeal-task-reason">{readableReason(appeal.reason)}</span>
        <small>{assignmentProgress(appeal)} · {formatTime(appeal.created_at)}</small>
      </button>
    </li>)}
  </ul>;
}

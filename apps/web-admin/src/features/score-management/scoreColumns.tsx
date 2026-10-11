import { Button, Space, type TableColumnsType } from "antd";
import { UserCheck, UserX } from "lucide-react";
import type { RosterEntry, SubmissionGrade } from "../../api/scores";
import { StatusTag } from "../../components/StatusTag";
import type { ScoreIdentityMaps } from "./hooks/useScoreWorkspaceData";
import {
  formatScore, rosterResolutionLabels, rosterStatusLabels, rosterStatusTone,
  sourceLabels, statusLabels, statusTone
} from "./scorePresentation";

type RosterColumnOptions = Readonly<{
  canWrite: boolean;
  publishedOrLocked: boolean;
  openAttendanceEditor: (entry: RosterEntry, status: "expected" | "absent") => void;
  openSubmission?: (entry: RosterEntry) => void;
  canOpenSubmission?: (entry: RosterEntry) => boolean;
}>;

function AttendanceAction({ entry, options }: Readonly<{ entry: RosterEntry; options: RosterColumnOptions }>) {
  const canOpen = Boolean(options.openSubmission) && options.canOpenSubmission?.(entry) !== false;
  if (entry.status !== "graded" && entry.status !== "absent" && entry.resolution_code !== "missing_submission" && canOpen) return <Button size="small" onClick={() => options.openSubmission?.(entry)}>{entry.resolution_code === "grading_incomplete" ? "查看阅卷任务" : "定位答卷处理"}</Button>;
  // 答卷临时行没有可调整的名册身份；锁定或门禁不可写时也不能进入调整表单。
  if (!options.canWrite || options.publishedOrLocked || !entry.student_id || entry.key.startsWith("submission:")) return entry.resolution_code === "missing_submission" && canOpen ? <Button size="small" onClick={() => options.openSubmission?.(entry)}>核查漏扫与匹配</Button> : "-";
  if (entry.status === "absent") {
    return <Button size="small" icon={<UserCheck size={14} />} onClick={() => options.openAttendanceEditor(entry, "expected")}>恢复应考</Button>;
  }
  if (entry.resolution_code === "missing_submission") {
    return <Space wrap><Button size="small" danger icon={<UserX size={14} />} onClick={() => options.openAttendanceEditor(entry, "absent")}>标记缺考</Button>{canOpen ? <Button size="small" onClick={() => options.openSubmission?.(entry)}>核查漏扫与匹配</Button> : null}</Space>;
  }
  return <span className="muted">请先处理答卷</span>;
}

export function createRosterColumns(options: RosterColumnOptions): TableColumnsType<RosterEntry> {
  return [
    {
      title: "学生", key: "student", width: 180,
      render: (_value, record) => record.student_name ? (
        <div className="score-roster-student"><strong>{record.student_name}</strong><span>{record.student_no || "无学号"}</span></div>
      ) : <span className="muted">身份待确认</span>
    },
    { title: "班级", dataIndex: "class_name", width: 130, render: (value?: string) => value || "-" },
    { title: "答卷号", dataIndex: "candidate_no", width: 150, render: (value?: string) => value || "-" },
    {
      title: "对账状态", dataIndex: "status", width: 150,
      render: (value: string) => <StatusTag tone={rosterStatusTone(value)}>{rosterStatusLabels[value] ?? "未知状态"}</StatusTag>
    },
    {
      title: "核对说明", dataIndex: "resolution_code",
      render: (value: string, record) => (
        <div className="score-roster-resolution">
          <span>{rosterResolutionLabels[value] ?? "未知处理方式"}</span>
          {record.expected_page_count > 0 ? <small>{`页数 ${record.actual_page_count}/${record.expected_page_count}`}</small> : null}
          {record.attendance_reason ? <small>{`处置原因：${record.attendance_reason}`}</small> : null}
        </div>
      )
    },
    { title: "操作", key: "actions", width: 130, render: (_value, entry) => <AttendanceAction entry={entry} options={options} /> }
  ];
}

export function createScoreColumns({ canReadStudentNames, identities }: Readonly<{
  canReadStudentNames: boolean;
  identities: ScoreIdentityMaps;
}>): TableColumnsType<SubmissionGrade> {
  return [
    { title: "匿名码", dataIndex: "anonymous_code", width: 150 },
    {
      title: "学生姓名", dataIndex: "student_id", width: 140,
      render: (value?: string) => {
        if (!canReadStudentNames) return <span className="muted">权限受限</span>;
        return value ? identities.students[value]?.name ?? "未匹配" : "未关联";
      }
    },
    {
      title: "班级", dataIndex: "student_id", width: 140,
      render: (value?: string) => {
        if (!canReadStudentNames) return <span className="muted">权限受限</span>;
        const student = value ? identities.students[value] : undefined;
        return student ? identities.classes[student.class_id]?.name ?? "未匹配" : "未关联";
      }
    },
    {
      title: "总分", dataIndex: "total_score", width: 120,
      render: (_value: number, record) => <strong>{formatScore(record.total_score)} / {formatScore(record.max_score)}</strong>
    },
    {
      title: "各题分", dataIndex: "items",
      render: (_value: unknown, record) => (
        <div className="score-item-strip">
          {(record.items ?? []).length > 0 ? (record.items ?? []).map((item) => (
            <span key={item.id} title={`${sourceLabels[item.source] ?? "其他来源"} · ${statusLabels[item.status] ?? "未知状态"}`}>
              {item.question_no}: {formatScore(item.score)}/{formatScore(item.max_score)}
            </span>
          )) : <span>无题目分</span>}
        </div>
      )
    },
    {
      title: "状态", dataIndex: "status", width: 120,
      render: (value: string) => <span title={value}><StatusTag tone={statusTone(value)}>{statusLabels[value] ?? "未知状态"}</StatusTag></span>
    },
    {
      title: "锁定", dataIndex: "locked", width: 88,
      render: (value: boolean) => <StatusTag tone={value ? "success" : "neutral"}>{value ? "已锁定" : "未锁定"}</StatusTag>
    }
  ];
}

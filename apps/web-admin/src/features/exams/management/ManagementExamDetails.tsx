import { Alert, Button, Descriptions, Table, type TableColumnsType } from "antd";
import type { Exam, ManagementExam } from "../../../api/exams";
import { StatusTag } from "../../../components/StatusTag";
import { examSubjectLabel, examStatusLabels, examStatusTone } from "../../../constants/examStatus";
import { examTypeOptions } from "../../../constants/examCatalog";
import { formatExamTime, isManagementExamArchived } from "./managementPresentation";

function subjectPreparationLabel(exam: ManagementExam, subject: Exam) {
  // 历史归档缺少原阶段，进度事实不可用时也不能根据学科状态推断准备完成。
  if (subject.status === "archived") return "历史归档，原阶段未知";
  if (exam.progress.facts_available === false) return "准备状态暂不可用";
  if (["draft", "configured"].includes(subject.status)) return "尚未确认准备完成";
  if (["ready", "collecting", "grading", "reviewing", "finalized", "published"].includes(subject.status)) return "已进入准备完成或后续阶段";
  return "暂无法获取";
}

function subjectActionLabel(exam: ManagementExam, subject: Exam) {
  if (isManagementExamArchived(exam) || subject.status === "archived") return "查看学科";
  if (["draft", "configured"].includes(subject.status)) return "准备配置";
  if (["ready","preparation_confirmed","collecting","processing"].includes(subject.status)) return "导入与核对答题卡";
  if (subject.status === "grading") return "继续阅卷";
  if (subject.status === "reviewing") return "处理人工复核";
  if (subject.status === "finalized") return "发布成绩";
  if (subject.status === "published") return "查看成绩";
  return "查看学科";
}

export function ManagementSubjectTable({ exam, classNames, onOpenSubject }: Readonly<{
  exam: ManagementExam;
  classNames?: Map<string, string>;
  onOpenSubject?: (exam: Exam) => void;
}>) {
  const columns: TableColumnsType<Exam> = [
    { title: "学科", dataIndex: "subject", render: examSubjectLabel },
    { title: "满分", dataIndex: "total_score", render: (value: number) => `${value} 分` },
    { title: "参考班级", render: (_, subject) => classNames ? subject.class_ids.map((id) => classNames.get(id) ?? id).join("、") || "未设置" : `${subject.class_ids.length} 个班级` },
    { title: "当前阶段", render: (_, subject) => <StatusTag tone={examStatusTone(subject.status)}>{examStatusLabels[subject.status] ?? "暂无法获取进度"}</StatusTag> },
    { title: "准备情况", render: (_, subject) => subjectPreparationLabel(exam, subject) }
  ];
  if (onOpenSubject) columns.push({ title: "操作", render: (_, subject) => <Button size="small" onClick={() => onOpenSubject(subject)}>{subjectActionLabel(exam, subject)}</Button> });
  return <Table<Exam> rowKey="id" dataSource={exam.exams} columns={columns} pagination={false} size="small" scroll={{ x: 680 }} locale={{ emptyText: "暂无已授权学科" }} />;
}

export function ManagementExamDetails({ exam, schoolName, gradeName, classNames, creatorName, onOpenSubject }: Readonly<{
  exam: ManagementExam;
  schoolName?: string;
  gradeName?: string;
  classNames?: Map<string, string>;
  creatorName?: string;
  onOpenSubject?: (exam: Exam) => void;
}>) {
  return <div className="detail-stack">
    {!exam.scope_complete ? <Alert type="info" showIcon message="仅显示你获授权的学科，整场编辑和归档需要完整考试管理权限。" /> : null}
    {isManagementExamArchived(exam) ? <Alert type="info" showIcon message="考试已归档，业务内容只读；已发布成绩按原权限访问。" /> : null}
    {exam.legacy_archived ? <Alert type="warning" showIcon message="包含历史归档记录，原业务阶段缺少可靠记录，暂不支持恢复。" /> : null}
    <Descriptions bordered column={1} size="small">
      <Descriptions.Item label="考试名称">{exam.name}</Descriptions.Item>
      <Descriptions.Item label="考试类型">{examTypeOptions.find((option) => option.value === exam.exam_type)?.label ?? "其他类型"}</Descriptions.Item>
      <Descriptions.Item label="学校">{schoolName || "本校"}</Descriptions.Item>
      <Descriptions.Item label="年级">{gradeName || "未关联年级"}</Descriptions.Item>
      <Descriptions.Item label="参考班级">{exam.class_ids.length} 个班级</Descriptions.Item>
      <Descriptions.Item label="学科数量">{exam.exams.length} 科{!exam.scope_complete ? "（已授权范围）" : ""}</Descriptions.Item>
      <Descriptions.Item label="考试进度">{exam.progress.label}</Descriptions.Item>
      <Descriptions.Item label="创建人">{creatorName || "本校管理员"}</Descriptions.Item>
      <Descriptions.Item label="创建时间">{formatExamTime(exam.created_at)}</Descriptions.Item>
      {exam.archived_at ? <Descriptions.Item label="归档时间">{formatExamTime(exam.archived_at)}</Descriptions.Item> : null}
      {exam.archive_reason ? <Descriptions.Item label="归档说明">{exam.archive_reason}</Descriptions.Item> : null}
    </Descriptions>
    <h3>{exam.scope_complete ? "全部学科" : "已授权学科"}</h3>
    <ManagementSubjectTable exam={exam} classNames={classNames} onOpenSubject={onOpenSubject} />
  </div>;
}

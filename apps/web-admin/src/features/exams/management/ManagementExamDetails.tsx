import { Alert, Descriptions, Table, type TableColumnsType } from "antd";
import type { Exam, ManagementExam } from "../../../api/exams";
import { StatusTag } from "../../../components/StatusTag";
import { examSubjectLabel, examStatusLabels, examStatusTone } from "../../../constants/examStatus";
import { examTypeOptions } from "../../../constants/examCatalog";
import { formatExamTime, isManagementExamArchived } from "./managementPresentation";

export function ManagementSubjectTable({ exam, classNames, onOpenSubject }: Readonly<{
  exam: ManagementExam;
  classNames?: Map<string, string>;
  onOpenSubject?: (exam: Exam) => void;
}>) {
  const columns: TableColumnsType<Exam> = [
    { title: "学科", dataIndex: "subject", render: (value: string, subject) => onOpenSubject
      ? <button type="button" className="exam-name-link" onClick={(event) => { event.stopPropagation(); onOpenSubject(subject); }}>{examSubjectLabel(value)}</button>
      : examSubjectLabel(value) },
    { title: "满分", dataIndex: "total_score", render: (value: number) => `${value} 分` },
    { title: "参考班级", render: (_, subject) => classNames ? subject.class_ids.map((id) => classNames.get(id) ?? id).join("、") || "未设置" : `${subject.class_ids.length} 个班级` },
    { title: "当前阶段", render: (_, subject) => <StatusTag tone={examStatusTone(subject.status)}>{examStatusLabels[subject.status] ?? "暂无法获取进度"}</StatusTag> }
  ];
  // 整行点击方便鼠标操作；名称仍用原生按钮，键盘可以独立聚焦并进入同一学科。
  return <Table<Exam> rowKey="id" dataSource={exam.exams} columns={columns} pagination={false} size="small" scroll={{ x: 520 }}
    rowClassName={onOpenSubject ? "exam-navigable-row" : undefined} onRow={onOpenSubject ? (subject) => ({ onClick: () => onOpenSubject(subject) }) : undefined}
    locale={{ emptyText: "暂无已授权学科" }} />;
}

export function ManagementExamDetails({ exam, gradeName, classNames, creatorName, onOpenSubject, layout = "stacked" }: Readonly<{
  exam: ManagementExam;
  gradeName?: string;
  classNames?: Map<string, string>;
  creatorName?: string;
  onOpenSubject?: (exam: Exam) => void;
  // 详情抽屉沿用上下布局；整场考试页面单独选择左右布局。
  layout?: "stacked" | "columns";
}>) {
  const information = <Descriptions bordered column={1} size="small">
      <Descriptions.Item label="考试名称">{exam.name}</Descriptions.Item>
      <Descriptions.Item label="考试类型">{examTypeOptions.find((option) => option.value === exam.exam_type)?.label ?? "其他类型"}</Descriptions.Item>
      <Descriptions.Item label="年级">{gradeName || "未关联年级"}</Descriptions.Item>
      <Descriptions.Item label="参考班级">{exam.class_ids.length} 个班级</Descriptions.Item>
      <Descriptions.Item label="学科数量">{exam.exams.length} 科{!exam.scope_complete ? "（已授权范围）" : ""}</Descriptions.Item>
      <Descriptions.Item label="考试进度">{exam.progress.label}</Descriptions.Item>
      <Descriptions.Item label="创建人">{creatorName || "本校管理员"}</Descriptions.Item>
      <Descriptions.Item label="创建时间">{formatExamTime(exam.created_at)}</Descriptions.Item>
      {exam.archived_at ? <Descriptions.Item label="归档时间">{formatExamTime(exam.archived_at)}</Descriptions.Item> : null}
      {exam.archive_reason ? <Descriptions.Item label="归档说明">{exam.archive_reason}</Descriptions.Item> : null}
    </Descriptions>;
  const subjects = <>
    <h3>{exam.scope_complete ? "全部学科" : "已授权学科"}</h3>
    <ManagementSubjectTable exam={exam} classNames={classNames} onOpenSubject={onOpenSubject} />
  </>;

  return <div className="detail-stack">
    {!exam.scope_complete ? <Alert type="info" showIcon message="仅显示你获授权的学科，整场编辑和归档需要完整考试管理权限。" /> : null}
    {isManagementExamArchived(exam) ? <Alert type="info" showIcon message="考试已归档，业务内容只读；已发布成绩按原权限访问。" /> : null}
    {exam.legacy_archived ? <Alert type="warning" showIcon message="包含历史归档记录，原业务阶段缺少可靠记录，暂不支持恢复。" /> : null}
    {layout === "columns" ? <div className="management-exam-overview-grid">
      <section className="management-exam-subjects" aria-label="考试学科">{subjects}</section>
      <aside className="management-exam-information" aria-label="考试信息"><h3>考试信息</h3>{information}</aside>
    </div> : <>{information}{subjects}</>}
  </div>;
}

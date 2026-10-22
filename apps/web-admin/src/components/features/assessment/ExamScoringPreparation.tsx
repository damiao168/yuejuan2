import { useState } from "react";
import { Alert, Button, Table } from "antd";
import { getSafeUserText } from "../../../api/client";
import type { ReadinessScoringPreview } from "../../../api/configuration";
import type { Exam } from "../../../api/exams";
import { examSubjectLabel } from "../../../constants/examStatus";
import { preparationScoringModeLabel, preparationScoringSourceLabels, preparationScoringSummary } from "./assessmentPreparation";

export function ExamScoringPreparation({ exam, preview, canManage, onNavigate }: Readonly<{
  exam: Exam;
  preview?: ReadinessScoringPreview[];
  canManage: boolean;
  onNavigate: (path: string) => void;
}>) {
  const [expanded, setExpanded] = useState(false);
  const rows = preview ?? [];
  const issues = rows.filter((row) => Boolean(row.blocking_reason));
  // 学段等考试级问题可能影响整卷，只给一个修复入口；题目自身的问题仍逐题保留。
  const examIssues = [...new Map(issues.filter((row) => row.blocking_section === "exam").map((row) => [row.blocking_reason, row])).values()];
  const questionIssues = issues.filter((row) => row.blocking_section !== "exam");
  const adjusted = rows.filter((row) => row.source === "saved").length;
  const editable = canManage && ["draft", "configured"].includes(exam.status);
  const questionsPath = `/exams/${encodeURIComponent(exam.id)}/questions`;

  // 准备检查已经解析默认、单题设置和冻结快照；这里展示同一份结果，不把没有单独保存误报为缺失。
  return <section className="workspace-section scoring-preparation">
    <div className="section-head"><div className="scoring-preparation-title"><h2>评分方案</h2>
      {preview ? <span className="scoring-verification-count" role="status">{issues.length ? `待处理 ${examIssues.length + questionIssues.length} 项` : `已匹配 ${rows.length} 题`}{adjusted ? ` · 沿用单题设置 ${adjusted} 题` : ""}</span> : null}
    </div><Button aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>{expanded ? "收起评分明细" : editable ? "查看 / 调整" : "查看评分明细"}</Button></div>
    {preview ? <div className="scoring-preparation-feedback">{preparationScoringSummary(rows) || (rows.length ? "请处理下列评分方案问题。" : "添加题目后，系统会匹配评分方案。")}</div>
      : <Alert type="warning" showIcon message="评分方案摘要暂未读取" description="请点击页面上方“重新检查”重试。" />}
    <p className="readiness-state-note">系统匹配评分流程，已有单题设置优先保留。本卷答案与评分细则在“小题与分值”中维护。</p>
    {issues.length ? <ul className="exam-setup-inline-issues exam-readiness-issues" aria-label="待处理评分方案">{examIssues.map((row) => <li key={`exam:${row.blocking_reason}`}>
      <div><strong>考试信息</strong><span>{getSafeUserText(row.blocking_reason, "考试信息需要核对")}</span></div>
      <Button type="link" onClick={() => onNavigate("/exams")}>核对考试信息</Button>
    </li>)}{questionIssues.map((row) => <li key={row.question_id}>
      <div><strong>第 {row.question_no} 题</strong><span>{getSafeUserText(row.blocking_reason, "评分方案需要核对")}</span></div>
      <Button type="link" onClick={() => onNavigate(questionsPath)}>去处理</Button>
    </li>)}</ul> : null}
    {expanded ? <>
      <Table<ReadinessScoringPreview> rowKey="question_id" dataSource={rows} size="small" pagination={{ pageSize: 10 }} columns={[
        { title: "题号", render: (_, row) => `第 ${row.question_no} 题` },
        { title: "评分方式", render: (_, row) => row.blocking_reason ? "待处理" : preparationScoringModeLabel(row.processing_mode ?? row.scoring_policy.mode) },
        { title: "方案来源", render: (_, row) => preparationScoringSourceLabels[row.source] },
        { title: "采用模板", render: (_, row) => row.profile.id ? `${row.profile.education_stage === "senior" ? "高中" : row.profile.education_stage === "junior" ? "初中" : ""}${examSubjectLabel(row.profile.subject_code)} · 第 ${row.profile.version} 版` : "待匹配" },
        { title: "说明", render: (_, row) => getSafeUserText(row.blocking_reason || row.reason, "") }
      ]} />
      <Button onClick={() => onNavigate(questionsPath)}>{editable ? "前往题目页逐题调整" : "前往题目页查看"}</Button>
    </> : null}
  </section>;
}

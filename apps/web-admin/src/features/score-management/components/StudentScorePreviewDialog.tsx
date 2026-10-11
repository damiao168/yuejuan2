import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Modal, Select, Spin } from "antd";
import {
  getScoreReleaseDetail, getScoreReleaseStudentPreview,
  type ScoreRelease, type ScoreReleaseDetail, type StudentScorePreviewResult
} from "../../../api/scoreReleases";
import { getUserErrorMessage } from "../../../api/client";
import { MathMarkdown } from "../../../components/MathMarkdown";
import { visibleReleaseContent } from "../hooks/useScoreReleaseWorkflow";
import "./student-score-preview.css";

function StudentScorePreviewStatistics({ reference }: Readonly<{ reference: NonNullable<StudentScorePreviewResult["reference"]> }>) {
  if (!reference.statistics_available) {
    return <p>{reference.unavailable_reason === "small_cohort" ? "人数不足，暂不展示统计和排名" : "统计数据暂不可用"}</p>;
  }
  // 公开统计中的 0 是有效值，只有服务端未返回的字段才省略。
  return <p>
    {reference.mean_score !== undefined ? `平均分 ${reference.mean_score} ` : ""}
    {reference.median_score !== undefined ? `中位数 ${reference.median_score} ` : ""}
    {reference.rank !== undefined ? `本版本考试排名 ${reference.rank} ` : ""}
    {reference.percentile !== undefined ? `百分位 ${reference.percentile} ` : ""}
    {reference.min_score !== undefined ? `最低分 ${reference.min_score} ` : ""}
    {reference.max_score !== undefined ? `最高分 ${reference.max_score} ` : ""}
    {reference.q1 !== undefined ? `下四分位数 ${reference.q1} ` : ""}
    {reference.q3 !== undefined ? `上四分位数 ${reference.q3}` : ""}
  </p>;
}

function StudentScorePreviewRubric({ items }: Readonly<{ items: readonly string[] }>) {
  // 公开量规没有条目 ID；文本和同文出现次数组成 key，重复要点仍逐条展示。
  const occurrences = new Map<string, number>();
  return <ul>{items.map((item) => {
    const occurrence = (occurrences.get(item) ?? 0) + 1;
    occurrences.set(item, occurrence);
    return <li key={JSON.stringify([item, occurrence])}><MathMarkdown>{item}</MathMarkdown></li>;
  })}</ul>;
}

function studentPreviewCurrentVersionText(releaseVersion: number, currentVersion?: number) {
  if (currentVersion === releaseVersion) return "（门户当前版本）";
  if (currentVersion === undefined) return "（门户当前未发布）";
  return `（门户当前为 V${currentVersion}）`;
}

export function StudentScorePreviewContent({ result }: Readonly<{ result: StudentScorePreviewResult }>) {
  const reference = result.reference;
  return <div className="student-score-preview-content">
    <h3>冻结成绩 V{result.release_version} · {result.total_score} / {result.max_score} 分</h3>
    {reference ? <section aria-label="公开成绩统计">
      <h4>本版本考试统计 · {reference.sample_size} 人</h4>
      <StudentScorePreviewStatistics reference={reference} />
    </section> : null}
    {result.questions ? result.questions.map((question) => <section key={question.question_id} className="student-score-preview-question">
      <h4>第 {question.question_no} 题 · {question.score} / {question.max_score} 分</h4>
      {result.appeal_window.open ? <p><strong>申请本题成绩复核</strong> · 正式发布后，学生在本题详情选择复核原因、填写说明，再确认提交。</p> : null}
      {question.stem ? <><strong>题目原文</strong><MathMarkdown>{question.stem}</MathMarkdown></> : null}
      {question.correct_answer ? <><strong>参考答案</strong><MathMarkdown>{question.correct_answer}</MathMarkdown></> : null}
      {question.actual_answer ? <><strong>本人答案</strong><MathMarkdown>{question.actual_answer}</MathMarkdown></> : null}
      {question.feedback ? <><strong>教师公开反馈</strong><MathMarkdown>{question.feedback}</MathMarkdown></> : null}
      {question.rubric_summary?.length ? <><strong>评分要点</strong><StudentScorePreviewRubric items={question.rubric_summary} /></> : null}
      {question.knowledge_points?.length ? <p>知识点：{question.knowledge_points.join("、")}</p> : null}
      {question.cohort ? <p>题目统计：{question.cohort.sample_size} 人 · 平均得分率 {(question.cohort.mean_score_rate * 100).toFixed(1)}% · 满分率 {(question.cohort.full_score_rate * 100).toFixed(1)}% · 零分率 {(question.cohort.zero_score_rate * 100).toFixed(1)}%</p> : null}
    </section>) : <p>此版本未公开逐题详情。</p>}
    <p>申诉窗口：{result.appeal_window.open ? "开放" : "未开放"}{result.appeal_window.closes_at ? ` · 截止 ${new Date(result.appeal_window.closes_at).toLocaleString("zh-CN")}` : ""}</p>
    {result.appeal_window.open && !result.questions?.length ? <Alert type="error" showIcon message="学生当前没有可操作的复核申请入口" description="本版本未提供逐题成绩。请重新创建公开逐题得分的草稿后再发布。" /> : null}
  </div>;
}

export function StudentScorePreviewDialog({ release, currentVersion, portalUrl, studentLabels = {}, onClose }: Readonly<{
  release: ScoreRelease;
  currentVersion?: number;
  portalUrl?: string;
  studentLabels?: Readonly<Record<string, string>>;
  onClose: () => void;
}>) {
  const [detail, setDetail] = useState<ScoreReleaseDetail | null>(null);
  const [studentId, setStudentId] = useState("");
  const [result, setResult] = useState<StudentScorePreviewResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [error, setError] = useState("");
  // 名单与成绩分别重试；某位学生的预览失败不能重置为冻结名单的首位学生。
  const [detailRetry, setDetailRetry] = useState(0);
  const [previewRetry, setPreviewRetry] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setLoading(true); setError(""); setDetail(null); setStudentId(""); setResult(null);
    void getScoreReleaseDetail(release.id, controller.signal).then((response) => {
      if (!active) return;
      setDetail(response);
      setStudentId(response.items.find((item) => item.student_id)?.student_id ?? "");
    }).catch((cause) => {
      if (active) setError(getUserErrorMessage(cause, "冻结版本读取失败，请重试"));
    }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; controller.abort(); };
  }, [release.id, detailRetry]);

  useEffect(() => {
    if (!studentId) return;
    const controller = new AbortController();
    let active = true;
    // 切换学生或版本时清空旧结果；取消请求仍可能返回，active 防止把旧学生成绩显示给新选择。
    setResult(null); setError(""); setPreviewLoading(true);
    void getScoreReleaseStudentPreview(release.id, studentId, controller.signal).then((response) => {
      if (active) setResult(response.result);
    }).catch((cause) => {
      if (active) setError(getUserErrorMessage(cause, "学生预览读取失败，请重试"));
    }).finally(() => { if (active) setPreviewLoading(false); });
    return () => { active = false; controller.abort(); };
  }, [release.id, studentId, previewRetry]);

  // 候选来自所选版本的不可变快照，不能用当前成绩表推断该旧版本有哪些学生或分数。
  const studentOptions = useMemo(() => [...new Map((detail?.items ?? []).filter((item) => item.student_id).map((item) => [item.student_id!, { value: item.student_id!, label: studentLabels[item.student_id!] ?? `学生 ${item.student_id}` }])).values()], [detail, studentLabels]);
  const visibleItems = visibleReleaseContent(release.visibility_policy);
  const currentVersionText = studentPreviewCurrentVersionText(release.version, currentVersion);
  const renderPreview = () => {
    if (previewLoading) return <Spin tip="正在读取此学生的冻结成绩"><div style={{ minHeight: 70 }} /></Spin>;
    if (result) return <StudentScorePreviewContent result={result} />;
    return null;
  };

  return <Modal open title={`学生视角预览 · V${release.version}`} width={900} onCancel={onClose} footer={<Button onClick={onClose}>关闭预览</Button>}>
    <Alert showIcon type="info" message={`正在预览 V${release.version}${currentVersionText}`} description="预览冻结成绩和公开内容；答卷图片、跨科汇总及实际申诉操作请在正式发布后由学生端核验。此处考试统计不代表学生端班级或年级排名。预览不会切换门户生效版本。" />
    <p>公开策略：{visibleItems.join("、") || "仅总分"}</p>
    <p>目标入口：学生门户{portalUrl ? <> · <a href={portalUrl} target="_blank" rel="noreferrer">{portalUrl}</a></> : " · 尚未配置入口链接，请使用学校发放的学生门户地址"}</p>
    {loading ? <Spin tip="正在读取冻结学生名单"><div style={{ minHeight: 70 }} /></Spin> : <div className="student-score-preview-student-selector">
      <label htmlFor="score-preview-student">选择冻结名单中的学生</label>
      <Select id="score-preview-student" showSearch optionFilterProp="label" placeholder="选择学生" value={studentId || undefined} options={studentOptions} onChange={setStudentId} />
      <small>可识别的姓名和学号仅用于查找；下方成绩与公开内容取自所选冻结版本。</small>
      {!studentOptions.length && !error ? <p>此版本没有已关联学生的成绩，无法投影学生视角。</p> : null}
    </div>}
    {error ? <Alert showIcon type="error" message="预览读取失败" description={error} action={<Button onClick={() => detail ? setPreviewRetry((value) => value + 1) : setDetailRetry((value) => value + 1)}>重试</Button>} /> : null}
    {renderPreview()}
  </Modal>;
}

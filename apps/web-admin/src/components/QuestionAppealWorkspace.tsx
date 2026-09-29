import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Button, Drawer, Empty, Input, List, Select, Space, Tag, Typography } from "antd";
import { Eye, FileCheck2, RotateCcw } from "lucide-react";
import {
  decideQuestionAppeal,
  getQuestionAppealAnswerImage,
  getQuestionAppealContext,
  listQuestionAppeals,
  startQuestionAppealReview,
  type PublishedQuestionAppeal,
  type PublishedQuestionAppealContext
} from "../api/questionAppeals";
import { getUserErrorMessage } from "../api/client";
import { listManagedUsers, type ManagedUser } from "../api/users";

const statusLabels: Record<string, string> = {
  submitted: "待处理", under_review: "处理中", rejected: "已答复", upheld_pending_regrade: "已转复评", resolved: "已完成"
};
const reasonLabels: Record<string, string> = {
  recognition_error: "识别有误", missing_step_credit: "过程分遗漏", rubric_disagreement: "评分要点异议",
  calculation_error: "分数计算有误", annotation_issue: "批注或反馈问题", other: "其他"
};

function messageFor(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function formatScore(value: number) {
  return Number.isInteger(value) ? String(value) : value.toFixed(1);
}

function rubricPoints(snapshot: Record<string, unknown> | undefined): Array<{ key: string; label: string; score?: number }> {
  const points = snapshot?.points;
  if (!Array.isArray(points)) return [];
  return points.flatMap((point, index) => {
    if (!point || typeof point !== "object") return [];
    const item = point as Record<string, unknown>;
    const label = [item.code, item.description, item.name].find((value): value is string => typeof value === "string" && Boolean(value.trim())) ?? `评分点 ${index + 1}`;
    return [{ key: String(item.id ?? item.code ?? index), label, score: typeof item.score === "number" ? item.score : typeof item.points === "number" ? item.points : undefined }];
  });
}

export function QuestionAppealWorkspace({ examId = "", canManage, canWork, onChanged }: { examId?: string; canManage: boolean; canWork: boolean; onChanged?: () => void }) {
  const [appeals, setAppeals] = useState<PublishedQuestionAppeal[]>([]);
  const [reviewers, setReviewers] = useState<ManagedUser[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [selectedId, setSelectedId] = useState("");
  const [context, setContext] = useState<PublishedQuestionAppealContext | null>(null);
  const [contextError, setContextError] = useState("");
  const [actioning, setActioning] = useState(false);
  const [assignedTo, setAssignedTo] = useState("");
  const [regradeJobID, setRegradeJobID] = useState("");
  const [notice, setNotice] = useState("");
  const [contextRevision, setContextRevision] = useState(0);
  const [answerImageURL, setAnswerImageURL] = useState("");

  const refresh = useCallback(async () => {
    if (!canManage && !canWork) { setAppeals([]); return; }
    setLoading(true); setError("");
    try {
      const response = await listQuestionAppeals(examId);
      setAppeals(response.appeals);
      setSelectedId((current) => response.appeals.some((appeal) => appeal.id === current) ? current : response.appeals[0]?.id ?? "");
      if (canManage) {
        const users = await listManagedUsers({ limit: 100 });
        setReviewers(users.users.filter((user) => user.status === "active" && user.roles.some((role) => ["teacher", "grader", "arbitrator"].includes(role))));
      }
    } catch (failure) { setError(messageFor(failure)); }
    finally { setLoading(false); }
  }, [canManage, canWork, examId]);

  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => {
    if (!selectedId) { setContext(null); return; }
    let active = true;
    setContext(null); setContextError("");
    void getQuestionAppealContext(selectedId).then((response) => { if (active) { setContext(response.context); setAssignedTo(response.context.appeal.assigned_to ?? ""); } }).catch((failure) => { if (active) setContextError(messageFor(failure)); });
    return () => { active = false; };
  }, [selectedId, contextRevision]);
  useEffect(() => {
    if (!context?.answer_image_ready) { setAnswerImageURL(""); return; }
    let active = true;
    let objectURL = "";
    void getQuestionAppealAnswerImage(context.appeal.id).then((asset) => {
      objectURL = URL.createObjectURL(asset.blob);
      if (active) setAnswerImageURL(objectURL);
    }).catch(() => { if (active) setAnswerImageURL(""); });
    return () => { active = false; if (objectURL) URL.revokeObjectURL(objectURL); };
  }, [context?.appeal.id, context?.answer_image_ready]);

  const selected = useMemo(() => appeals.find((appeal) => appeal.id === selectedId), [appeals, selectedId]);
  const points = rubricPoints(context?.rubric_snapshot);
  const refreshSelected = async () => {
    await refresh(); setContextRevision((value) => value + 1); onChanged?.();
  };
  const assign = async () => {
    if (!selected || !assignedTo) return;
    setActioning(true);
    try {
      await startQuestionAppealReview(selected.id, { assigned_to: assignedTo, expected_revision: selected.revision });
      setNotice("已分派给阅卷教师处理。"); await refreshSelected();
    } catch (failure) { setContextError(messageFor(failure)); }
    finally { setActioning(false); }
  };
  const decide = async (decision: "reject" | "refer_regrade") => {
    if (!context) return;
    if (decision === "refer_regrade" && !regradeJobID.trim()) { setContextError("请先在题目级复评中创建该题的任务，再填入任务编号完成关联。"); return; }
    setActioning(true);
    try {
      await decideQuestionAppeal(context.appeal.id, {
        decision,
        public_response: decision === "reject" ? "已复核本题答卷、评分要点和发布版本依据，当前成绩保持不变。" : "已受理，正在按题目级复评流程处理。",
        ...(decision === "refer_regrade" ? { regrade_job_id: regradeJobID.trim() } : {}),
        expected_revision: context.appeal.revision
      });
      setNotice(decision === "reject" ? "已答复学生，原成绩保持不变。" : "已关联受治理的题目级复评任务。"); await refreshSelected();
    } catch (failure) { setContextError(messageFor(failure)); }
    finally { setActioning(false); }
  };

  if (!canManage && !canWork) return null;
  return <section className="question-appeal-workspace" aria-labelledby="question-appeal-title">
    <header className="question-appeal-heading"><div><h2 id="question-appeal-title">题目申诉</h2><p>只处理已发布版本中的题目异议；需要更正时进入题目级复评，再生成新的成绩版本。</p></div><Button icon={<RotateCcw size={15} />} loading={loading} onClick={() => void refresh()}>刷新</Button></header>
    {error ? <Alert type="error" showIcon message="题目申诉暂时无法加载" description={error} /> : null}
    {!loading && appeals.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前考试没有题目申诉" /> : null}
    {appeals.length > 0 ? <List size="small" className="question-appeal-list" dataSource={appeals} renderItem={(appeal) => <List.Item className={appeal.id === selectedId ? "is-selected" : ""} actions={[<Button key="open" size="small" onClick={() => setSelectedId(appeal.id)}>查看</Button>]}><div><strong>{appeal.question_no} · {reasonLabels[appeal.reason_code] ?? "其他原因"}</strong><span>原分 {formatScore(appeal.source_score)} / {formatScore(appeal.source_max_score)} · 发布 V{appeal.source_release_version}</span></div><Tag color={appeal.status === "submitted" ? "orange" : appeal.status === "resolved" ? "green" : "blue"}>{statusLabels[appeal.status] ?? "处理中"}</Tag></List.Item>} /> : null}
    <Drawer width={760} open={Boolean(selectedId)} title={selected ? `${selected.question_no} 题目申诉` : "题目申诉"} onClose={() => setSelectedId("")}>
      {contextError ? <Alert type="error" showIcon message="无法处理此申诉" description={contextError} /> : null}
      {!context && !contextError ? <Typography.Text type="secondary">正在加载发布版本依据…</Typography.Text> : null}
      {context ? <div className="question-appeal-context">
        <section className="question-appeal-facts"><div><span>学生说明</span><strong>{context.appeal.reason}</strong></div><div><span>原评分来源</span><strong>{context.source_type || "已冻结评分事实"}</strong></div><div><span>发布版本</span><strong>V{context.appeal.source_release_version}</strong></div></section>
        {context.appeal.selected_region ? <Alert type="info" showIcon message="学生已圈选争议区域" description="红框只用于定位答题位置，不会修改原始答题图或成绩。" /> : null}
        {context.answer_image_ready && answerImageURL ? <section><h3><Eye size={16} /> 答题图</h3><div className="question-appeal-image"><img src={answerImageURL} alt={`${context.appeal.question_no} 答题区域`} />{context.appeal.selected_region ? <RegionOverlay region={context.appeal.selected_region} /> : null}</div></section> : <Alert type="warning" showIcon message="答题图暂不可用" description="申诉与发布版本依据仍可查看；请从阅卷证据链确认图像状态。" />}
        <section><h3><FileCheck2 size={16} /> 冻结评分要点</h3>{points.length ? <List size="small" dataSource={points} renderItem={(point) => <List.Item><span>{point.label}</span>{point.score !== undefined ? <Tag>{formatScore(point.score)} 分</Tag> : null}</List.Item>} /> : <Typography.Text type="secondary">本题没有可展示的结构化评分要点。</Typography.Text>}</section>
        <section><h3>版本历史</h3><List size="small" dataSource={context.release_history} renderItem={(version) => <List.Item><span>V{version.version} · {version.source === "initial" ? "首次发布" : version.source === "regrade" ? "重评发布" : "其他来源"}</span><Tag color={version.status === "published" ? "green" : "default"}>{version.status === "published" ? "已发布" : version.status === "superseded" ? "已被替代" : "未知状态"}</Tag></List.Item>} /></section>
        {notice ? <Alert type="success" showIcon message={notice} /> : null}
        {context.appeal.status === "submitted" && canManage ? <section className="question-appeal-actions"><h3>分派处理</h3><Space wrap><Select className="question-appeal-reviewer" placeholder="选择阅卷教师" value={assignedTo || undefined} options={reviewers.map((user) => ({ value: user.id, label: user.display_name || user.username }))} onChange={setAssignedTo} /><Button type="primary" loading={actioning} disabled={!assignedTo} onClick={() => void assign()}>开始复核</Button></Space></section> : null}
        {context.appeal.status === "under_review" ? <section className="question-appeal-actions"><h3>处理结论</h3><Space wrap><Button loading={actioning} onClick={() => void decide("reject")}>答复并维持原成绩</Button>{canManage ? <><Input className="question-appeal-regrade-id" placeholder="已创建的复评任务编号" value={regradeJobID} onChange={(event) => setRegradeJobID(event.target.value)} /><Button type="primary" loading={actioning} onClick={() => void decide("refer_regrade")}>关联题目级复评</Button></> : null}</Space><Typography.Paragraph type="secondary">关联后不会直接改分；复评完成后仍需生成并发布新的成绩版本。</Typography.Paragraph></section> : null}
      </div> : null}
    </Drawer>
  </section>;
}

function RegionOverlay({ region }: { region: Record<string, unknown> }) {
  // 只接受标准答题图的归一化框；其他坐标系不能直接按图片百分比定位。
  const values = [region.x, region.y, region.width, region.height];
  if (region.coordinate_space !== "canonical_image_normalized" || values.some((value) => typeof value !== "number")) return null;
  const [x, y, width, height] = values as number[];
  return <span className="question-appeal-region" style={{ left: `${x * 100}%`, top: `${y * 100}%`, width: `${width * 100}%`, height: `${height * 100}%` }} />;
}

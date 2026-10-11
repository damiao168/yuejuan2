import { Alert, Button, Empty, Input, List, Select, Space, Tag, Typography } from "antd";
import { Eye, FileCheck2, RotateCcw } from "lucide-react";
import type { PublishedQuestionAppeal, PublishedQuestionAppealContext, QuestionAppealReleaseVersion } from "../api/questionAppeals";
import type { ManagedUser } from "../api/users";
import { examSubjectLabel } from "../constants/examStatus";
import {
  formatQuestionAppealScore,
  questionAppealReasonLabels,
  questionAppealReleaseSource,
  questionAppealReleaseStatus,
  questionAppealStatusColor,
  questionAppealStatusLabels,
  type QuestionAppealRubricPoint
} from "./questionAppealPresentation";

// 展示组件固定在模块顶层，输入重渲染时保持组件身份；状态和异步操作仍归工作台。
function questionAppealSubjectLabel(subject?: string) {
  const value = subject?.trim();
  if (!value) return "";
  // 任务摘要可能直接提供中文学科；代码值才通过统一名称表转换。
  if (/^[\u4e00-\u9fa5\s/]+$/.test(value)) return value;
  return examSubjectLabel(value);
}

function questionAppealQueueStatusLabel(appeal: PublishedQuestionAppeal) {
  if (appeal.status !== "under_review") return questionAppealStatusLabels[appeal.status] ?? "处理中";
  // 教师移交意见后状态仍是 under_review，队列需显示下一步等待管理员处理。
  if (appeal.review_opinion) return "待学校管理员处理意见";
  return "待独立教师意见";
}

export function questionAppealBusinessSummary(appeal: PublishedQuestionAppeal, examLabels: Readonly<Record<string, string>> = {}) {
  const name = appeal.exam_name?.trim() || examLabels[appeal.exam_id]?.trim() || "考试名称暂未读取";
  const subjectLabel = questionAppealSubjectLabel(appeal.exam_subject);
  return [name, subjectLabel, `第 ${appeal.question_no} 题`].filter(Boolean).join(" · ");
}

export function QuestionAppealSummary({ appeals, loading, actioning, error, identityError, examLabels = {}, onRefresh }: Readonly<{
  appeals: PublishedQuestionAppeal[]; loading: boolean; actioning: boolean; error: string; identityError: string; onRefresh: () => void;
  examLabels?: Readonly<Record<string, string>>;
}>) {
  return <>
    <header className="question-appeal-heading"><div><h2 id="question-appeal-title">学生题目复核申请</h2><p>共 {appeals.length} 条 · 等待分派 {appeals.filter((appeal) => appeal.status === "submitted").length} 条 · 意见已移交待管理员处理 {appeals.filter((appeal) => appeal.status === "under_review" && appeal.review_opinion).length} 条 · 重评处理中 {appeals.filter((appeal) => appeal.status === "upheld_pending_regrade").length} 条。需要更正时完成题目级复评，再发布新成绩并公布复核结果。</p></div><Button icon={<RotateCcw size={15} />} loading={loading} disabled={actioning} onClick={onRefresh}>刷新</Button></header>
    {error ? <Alert type="error" showIcon message="题目申诉暂时无法加载" description={error} /> : null}
    {!loading && appeals.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前考试没有题目申诉" /> : null}
    {identityError ? <Alert type="warning" showIcon message="学生姓名暂时无法读取，请按申请标识核对" description={identityError} /> : null}
    {!loading && appeals.some((appeal) => !appeal.exam_name?.trim() && !examLabels[appeal.exam_id]?.trim()) ? <Alert type="warning" showIcon message="部分复核任务的考试名称暂未读取" description="复核材料仍可查看。请重新读取任务摘要以确认考试；无需开通整场考试管理权限。" action={<Button disabled={actioning} onClick={onRefresh}>重新读取任务摘要</Button>} /> : null}
  </>;
}

export function QuestionAppealIdentityHeader({ appeal, examLabels, studentLabels, personalScope, onReloadSummary, summaryLoading = false }: Readonly<{
  appeal: PublishedQuestionAppeal; examLabels: Readonly<Record<string, string>>; studentLabels: Readonly<Record<string, string>>; personalScope: boolean;
  onReloadSummary?: () => void; summaryLoading?: boolean;
}>) {
  // 个人任务视角不展示学生标识；考试摘要随获授权任务提供，不依赖教师访问整场考试目录。
  return <div><p><strong>{questionAppealBusinessSummary(appeal, examLabels)}</strong>{!personalScope && appeal.student_id ? " · " + (studentLabels[appeal.student_id] ?? `学生记录 ${appeal.student_id}`) : ""}<br />原公布第 {appeal.source_release_version} 版 · 原分 {appeal.source_score} / {appeal.source_max_score}</p>
    {!appeal.exam_name?.trim() && !examLabels[appeal.exam_id]?.trim() && onReloadSummary ? <Alert type="warning" showIcon message="考试名称暂未读取" description="重新读取考试名称和学科会保留当前复核意见、分值建议及说明。" action={<Button loading={summaryLoading} onClick={onReloadSummary}>重新读取本任务摘要</Button>} /> : null}
    <details><summary>记录信息</summary><p>内部申请编号：{appeal.id}</p></details>
  </div>;
}

export function QuestionAppealFeedback({ notice, reviewersError, publicResponse }: Readonly<{
  notice: string; reviewersError: string; publicResponse: string | undefined;
}>) {
  return <>
    {notice ? <Alert type="success" showIcon message={notice} /> : null}
    {reviewersError ? <Alert type="warning" showIcon message="可分派教师暂时无法读取" description={`${reviewersError}；请由具备人员管理权限的学校管理员分派。申请记录与已分派的复核工作仍可查看。`} /> : null}
    {publicResponse ? <Alert type="info" showIcon message="已向学生公开的回复" description={publicResponse} /> : null}
  </>;
}

export function QuestionAppealResolution({ options, resolutionID, publicResponse, actioning, onResolutionChange, onPublicResponseChange, onResolve }: Readonly<{
  options: Array<{ value: string; label: string }>; resolutionID: string; publicResponse: string; actioning: boolean;
  onResolutionChange: (value: string) => void; onPublicResponseChange: (value: string) => void; onResolve: () => void;
}>) {
  return <section className="question-appeal-actions"><h3>公布已发布的复核结果</h3><p>先完成关联题目复评并正式发布新成绩，再选择结果版本。服务端会核对已完成的复评与本学生、本题的成绩事实。</p><Select aria-label="选择复核结果成绩版本" value={resolutionID || undefined} options={options} placeholder="选择后续已正式发布的成绩版本" onChange={onResolutionChange} disabled={actioning} /><label htmlFor="question-appeal-final-response">给学生的结果说明（必填）</label><Input.TextArea id="question-appeal-final-response" rows={4} maxLength={2000} value={publicResponse} onChange={(event) => onPublicResponseChange(event.target.value)} disabled={actioning} /><Button type="primary" disabled={!resolutionID || !publicResponse.trim()} loading={actioning} onClick={onResolve}>公布复核结果</Button>{!options.length ? <Alert type="info" showIcon message="尚无可选择的后续正式成绩版本" description="请先在考试成绩工作台完成复评并发布新成绩，再刷新此申请。" /> : null}</section>;
}

export function QuestionAppealQueue({ appeals, selectedId, onSelect, examLabels = {}, studentLabels = {}, personalScope = false }: Readonly<{ appeals: PublishedQuestionAppeal[]; selectedId: string; onSelect: (id: string) => void; examLabels?: Readonly<Record<string, string>>; studentLabels?: Readonly<Record<string, string>>; personalScope?: boolean }>) {
  if (appeals.length === 0) return null;
  return <List size="small" className="question-appeal-list" dataSource={appeals} renderItem={(appeal) => <List.Item className={appeal.id === selectedId ? "is-selected" : ""} actions={[<Button key="open" size="small" onClick={() => onSelect(appeal.id)}>查看第 {appeal.question_no} 题复核材料</Button>]}>
    <div><strong>{appeal.question_no} · {questionAppealReasonLabels[appeal.reason_code] ?? "其他原因"}</strong><span>{questionAppealBusinessSummary(appeal, examLabels)}{!personalScope && appeal.student_id ? " · " + (studentLabels[appeal.student_id] ?? `学生记录 ${appeal.student_id}`) : ""}</span><span>原分 {formatQuestionAppealScore(appeal.source_score)} / {formatQuestionAppealScore(appeal.source_max_score)} · 原公布第 {appeal.source_release_version} 版</span></div>
    <Tag color={questionAppealStatusColor(appeal.status)}>{questionAppealQueueStatusLabel(appeal)}</Tag>
  </List.Item>} />;
}

function RegionOverlay({ region }: Readonly<{ region: Record<string, unknown> }>) {
  // 只接受标准答题图的归一化框；其他坐标系不能直接按图片百分比定位。
  const values = [region.x, region.y, region.width, region.height];
  if (region.coordinate_space !== "canonical_image_normalized" || values.some((value) => typeof value !== "number")) return null;
  const [x, y, width, height] = values as number[];
  return <span className="question-appeal-region" style={{ left: `${x * 100}%`, top: `${y * 100}%`, width: `${width * 100}%`, height: `${height * 100}%` }} />;
}

function FrozenRubric({ points }: Readonly<{ points: QuestionAppealRubricPoint[] }>) {
  return <section><h3><FileCheck2 size={16} /> 冻结评分要点</h3>{points.length ? <List size="small" dataSource={points} renderItem={(point) => <List.Item><span>{point.label}</span>{point.score !== undefined ? <Tag>{formatQuestionAppealScore(point.score)} 分</Tag> : null}</List.Item>} /> : <Typography.Text type="secondary">本题没有可展示的结构化评分要点。</Typography.Text>}</section>;
}

function ReleaseHistory({ versions }: Readonly<{ versions: QuestionAppealReleaseVersion[] }>) {
  return <section><h3>版本历史</h3><List size="small" dataSource={versions} renderItem={(version) => <List.Item>
    <span>V{version.version} · {questionAppealReleaseSource(version.source)}</span>
    <Tag color={version.status === "published" ? "green" : "default"}>{questionAppealReleaseStatus(version.status)}</Tag>
  </List.Item>} /></section>;
}

export function QuestionAppealEvidence({ context, answerImageURL, points }: Readonly<{ context: PublishedQuestionAppealContext; answerImageURL: string; points: QuestionAppealRubricPoint[] }>) {
  return <>
    <section className="question-appeal-facts"><div><span>学生说明</span><strong>{context.appeal.reason}</strong></div><div><span>原评分来源</span><strong>{context.source_type || "已冻结评分事实"}</strong></div><div><span>发布版本</span><strong>V{context.appeal.source_release_version}</strong></div></section>
    {context.appeal.selected_region ? <Alert type="info" showIcon message="学生已圈选争议区域" description="红框只用于定位答题位置，不会修改原始答题图或成绩。" /> : null}
    {context.answer_image_ready && answerImageURL ? <section><h3><Eye size={16} /> 答题图</h3><div className="question-appeal-image"><img src={answerImageURL} alt={`${context.appeal.question_no} 答题区域`} />{context.appeal.selected_region ? <RegionOverlay region={context.appeal.selected_region} /> : null}</div></section> : <Alert type="warning" showIcon message="答题图暂不可用" description="申诉与发布版本依据仍可查看；请从阅卷证据链确认图像状态。" />}
    <FrozenRubric points={points} />
    <ReleaseHistory versions={context.release_history} />
  </>;
}

interface AppealActionsProps {
  status: string;
  canManage: boolean;
  assignedTo: string;
  reviewers: ManagedUser[];
  actioning: boolean;
  regradeJobID: string;
  publicResponse: string;
  canDecide: boolean;
  canManageScores: boolean;
  regradeOptions: Array<{ value: string; label: string }>;
  regradeError: string;
  onPublicResponseChange: (value: string) => void;
  onReloadRegrades: () => void;
  onAssignedToChange: (id: string) => void;
  onRegradeJobIDChange: (id: string) => void;
  onAssign: () => void;
  onDecide: (decision: "reject" | "refer_regrade") => void;
}

export function QuestionAppealActions(props: Readonly<AppealActionsProps>) {
  // 教师意见由独立面板移交；分派和最终公开答复仅显示给管理端。
  if (props.status === "submitted" && props.canManage) return <section className="question-appeal-actions"><h3>分派处理</h3><p>请选择未参与本题原批阅的独立教师。服务端会检查人员工作权限与原评记录。</p><Space wrap>
    <Select className="question-appeal-reviewer" placeholder="选择阅卷教师" value={props.assignedTo || undefined} options={props.reviewers.map((user) => ({ value: user.id, label: user.display_name || user.username }))} onChange={props.onAssignedToChange} />
    <Button type="primary" loading={props.actioning} disabled={!props.assignedTo} onClick={props.onAssign}>开始复核</Button>
  </Space></section>;
  if (props.status !== "under_review" || !props.canDecide) return null;
  return <section className="question-appeal-actions"><h3>处理结论</h3><Space wrap>
    <label htmlFor="question-appeal-public-response">给学生的复核回复（必填）</label>
    <Input.TextArea id="question-appeal-public-response" rows={4} maxLength={2000} showCount value={props.publicResponse} placeholder="写明已核对的答卷证据、评分依据及处理结论；提交后学生可见。" onChange={(event) => props.onPublicResponseChange(event.target.value)} disabled={props.actioning} />
    <Button loading={props.actioning} disabled={!props.publicResponse.trim()} onClick={() => { props.onDecide("reject"); }}>答复并维持原成绩</Button>
    {props.canManage && props.canManageScores ? <>
      <Select aria-label="选择本题复评任务" className="question-appeal-regrade-id" placeholder="选择同题同发布版本的复评任务" value={props.regradeJobID || undefined} options={props.regradeOptions} onChange={props.onRegradeJobIDChange} disabled={props.actioning} />
      <Button onClick={props.onReloadRegrades} disabled={props.actioning}>刷新复评任务</Button>
      <Button type="primary" loading={props.actioning} disabled={!props.publicResponse.trim() || !props.regradeJobID} onClick={() => { props.onDecide("refer_regrade"); }}>关联题目级复评</Button>
    </> : null}
  </Space>{props.regradeError ? <Alert type="warning" showIcon message={props.regradeError} /> : null}<Typography.Paragraph type="secondary">关联后不会直接改分；复评完成后仍需生成并发布新的成绩版本。学校成绩管理员负责后续发布；给学生的回复应说明当前阶段。</Typography.Paragraph></section>;
}

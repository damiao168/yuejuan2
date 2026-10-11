import { Alert, Button, Collapse, Descriptions, Empty, Select, Space, Statistic, Table, Tag } from "antd";
import { RefreshCw, ShieldAlert, ShieldCheck } from "lucide-react";
import type { QualityDashboard } from "../api/qualityDashboard";
import type { BackmarkBatch, BackmarkSummary } from "../api/backmark";
import type { RegradePreview, ScoreRelease } from "../api/scoreReleases";
import type { WorkflowActor } from "../api/workflowActors";
import { LoadingState } from "../components/PageState";

type Gate = QualityDashboard["gate"];
type QualityQuestion = QualityDashboard["questions"][number];

function gateLabel(gate: Gate) {
  if (gate === "blocked") return "不可发布";
  if (gate === "ready") return "可继续";
  return "需要关注";
}

function gateColor(gate: Gate): "error" | "success" | "warning" {
  if (gate === "blocked") return "error";
  if (gate === "ready") return "success";
  return "warning";
}

function gateDescription(gate: Gate) {
  if (gate === "blocked") return "存在必须先处置的质量问题，不能发布成绩。";
  if (gate === "ready") return "当前没有阻断性质量事件。";
  return "当前没有阻断项，但仍有待补齐的质量证据或处理项。";
}

function percentage(value?: number) {
  return value === undefined ? "样本不足" : `${Math.round(value * 100)}%`;
}

function batchStatusLabel(status: BackmarkBatch["status"]) {
  if (status === "completed") return "已完成";
  if (status === "cancelled") return "已取消";
  if (status === "ready_for_confirmation") return "待确认";
  return "处理中";
}

export function workflowActorLabel(user?: WorkflowActor) {
  if (!user) return "接手教师待核对";
  return user.username?.trim() ? `${user.display_name || user.username}（账号：${user.username}）` : `${user.display_name || "教师"}（账号待核对）`;
}

export function workflowActorOptions(users: WorkflowActor[]) {
  return users.map((user) => ({ value: user.id, label: workflowActorLabel(user), disabled: !user.username?.trim() }));
}

function batchTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "创建时间待核对" : date.toLocaleString("zh-CN", { hour12: false });
}

function dispositionLabel(value: BackmarkBatch["policy"]["disposition"]) {
  return { confirm: "记录差异，待人工确认", arbitrate: "达到分差阈值进入仲裁", regrade: "进入题目重评流程" }[value] || "差异去向待核对";
}

function batchRecord(batch: BackmarkBatch) {
  return <Collapse items={[{ key: "record", label: "内部记录信息", children: <Descriptions size="small" column={1} items={[
    { key: "batch", label: "批次内部编号", children: batch.id }, { key: "question", label: "题目内部编号", children: batch.question_id }
  ]} /> }]} />;
}

function renderBackmarkItemRecord(item: BackmarkSummary["items"][number]) {
  return <Collapse items={[{ key: "record", label: "内部记录信息", children: <span>回标项内部编号：{item.id}</span> }]} />;
}

function scoreDifference(value?: number) {
  if (value === undefined) return "-";
  return `${value > 0 ? "+" : ""}${value}`;
}

function QualityQuestionDetails({ row, canManage, onRecompute, onHandleFinding }: Readonly<{
  row: QualityQuestion; canManage: boolean; onRecompute: (questionId: string) => void;
  onHandleFinding?: (code: string, questionId: string) => void;
}>) {
  return <Collapse items={[
    {
      key: "evidence", label: "质量证据",
      children: <><Descriptions size="small" column={{ xs: 1, sm: 2, lg: 3 }} items={[
        { key: "calibration", label: "标定", children: <>{row.calibration.configured ? `完成 ${row.calibration.completed}，通过 ${row.calibration.passed}` : "未配置"}{canManage && onHandleFinding ? <Button size="small" onClick={() => onHandleFinding("calibration_not_configured", row.question.id)}>处理标定</Button> : null}</> },
        { key: "human", label: "双评一致", children: `${percentage(row.human_human_agreement.within_rule_agreement.rate)} · ${row.human_human_agreement.sample_size} 份` },
        { key: "groups", label: "分组复核", children: <>{`${row.answer_groups.group_count} 组，待抽检 ${row.answer_groups.open_sample_groups} 组`}{canManage && onHandleFinding ? <Button size="small" onClick={() => onHandleFinding("answer_group_sampling_pending",row.question.id)}>复核分组样本</Button> : null}</> },
        { key: "backmark", label: "回标", children: `开放批次 ${row.backmark.open_batches}，待处理 ${row.backmark.pending_items}` },
        { key: "bands", label: "标准卷分档", children: <>{row.gold.score_bands.map((item) => `${item.band} ${item.count}`).join("，") || "暂无"}{canManage && onHandleFinding ? <Button size="small" onClick={() => onHandleFinding("gold_coverage_incomplete",row.question.id)}>核对本题标准卷</Button> : null}</> }
      ]} />{canManage ? <Button size="small" onClick={() => onRecompute(row.question.id)}>重新计算本题质量</Button> : null}</>
    },
    {
      key: "findings", label: `待处理事项（${row.findings.length}）`,
      children: row.findings.length ? <Table className="dense-data-table" size="small" pagination={false} rowKey={(item) => `${item.code}-${item.incident_ref ?? item.question_id}`} dataSource={row.findings} columns={[
        { title: "状态", dataIndex: "status", render: (value: Gate) => <Tag color={gateColor(value)}>{gateLabel(value)}</Tag> },
        { title: "原因", dataIndex: "reason" },
        { title: "负责人", dataIndex: "owner" },
        { title: "处理路径", dataIndex: "resolution", render: (value: string, finding) => <>{value}{canManage && onHandleFinding ? <Button size="small" onClick={() => onHandleFinding(finding.code, finding.question_id)}>处理本题事项</Button> : null}</> }
      ]} /> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有待处理事项" />
    }
  ]} />;
}

// Table 回调只转交数据，展开内容使用顶层组件，刷新时不更换组件类型。
function renderQuestionDetails(row: QualityQuestion, canManage: boolean, onRecompute: (questionId: string) => void, onHandleFinding?: (code: string, questionId: string) => void) {
  return <QualityQuestionDetails row={row} canManage={canManage} onRecompute={onRecompute} onHandleFinding={onHandleFinding} />;
}

export function QualityDashboardOverview({ data, error, canManage, onSeed, onIncidents, onBackmark, onRefresh, onRecompute, onHandleFinding }: Readonly<{
  data: QualityDashboard; error?: string; canManage: boolean;
  onSeed: () => void; onIncidents: () => void; onBackmark: () => void; onRefresh: () => void; onRecompute: (questionId: string) => void;
  onHandleFinding?: (code: string, questionId: string) => void;
}>) {
  return <>
    <div className="page-heading compact-heading">
      <div><span className="eyebrow">评分质量</span><h1>质量总览</h1><p>基于标准卷、阅卷员标定、盲测、双评和回标的考试级质量证据。</p></div>
      <Space>
        <span className="muted-text">更新于 {new Date(data.generated_at).toLocaleString("zh-CN", { hour12: false })}</span>
        {canManage ? <Button onClick={onSeed}>盲测策略</Button> : null}
        {canManage ? <Button onClick={onIncidents}>质量事件</Button> : null}
        {canManage ? <Button onClick={onBackmark}>创建回标</Button> : null}
        <Button icon={<RefreshCw size={16} />} onClick={onRefresh}>刷新</Button>
      </Space>
    </div>
    <Alert showIcon type={gateColor(data.gate)} icon={data.gate === "ready" ? <ShieldCheck size={19} /> : <ShieldAlert size={19} />} message={`发布可行性：${gateLabel(data.gate)}`} description={gateDescription(data.gate)} />
    <div className="quality-dashboard-summary">
      <Statistic title="阻断项" value={data.blocking.length} valueStyle={{ color: data.blocking.length ? "#cf1322" : undefined }} />
      <Statistic title="需关注" value={data.warnings.length} valueStyle={{ color: data.warnings.length ? "#d48806" : undefined }} />
      <Statistic title="题目数" value={data.questions.length} />
      <Statistic title="可继续题目" value={data.questions.filter((item) => item.gate === "ready").length} valueStyle={{ color: "#389e0d" }} />
    </div>
    {error ? <Alert type="warning" showIcon message={error} /> : null}
    <div className="content-card">
      <div className="section-header"><div><h2>每题质量状态</h2><p>展开题目可查看数据依据、负责人和处理路径。</p></div></div>
      <Table size="middle" rowKey={(row) => row.question.id} pagination={false} dataSource={data.questions} columns={[
        { title: "题号", dataIndex: ["question", "question_no"], width: 92 },
        { title: "题型", dataIndex: ["question", "archetype_code"], render: (value: string) => value || "未配置" },
        { title: "风险", dataIndex: ["question", "risk_tier"], render: (value: string) => value ? <Tag>{value}</Tag> : "未配置" },
        { title: "质量状态", dataIndex: "gate", render: (value: Gate) => <Tag color={gateColor(value)}>{gateLabel(value)}</Tag> },
        { title: "标准卷", dataIndex: "gold", render: (value: QualityQuestion["gold"]) => value.ready ? `已覆盖 ${value.active_approved} 份` : "待补充" },
        { title: "盲测一致", dataIndex: "seed", render: (value: QualityQuestion["seed"]) => `${percentage(value.exact_agreement.rate)} · ${value.sample_size} 份` },
        { title: "未处理事件", dataIndex: "drift", render: (value: QualityQuestion["drift"]) => value.open_critical + value.open_warnings },
        { title: "待回标", dataIndex: "backmark", render: (value: QualityQuestion["backmark"]) => value.pending_items }
      ]} expandable={{ expandedRowRender: (row) => renderQuestionDetails(row, canManage, onRecompute, onHandleFinding) }} />
    </div>
  </>;
}

function renderBatchFooter(loading: boolean, onLoadMore: () => void) {
  return <Button block loading={loading} onClick={onLoadMore}>加载更多批次</Button>;
}

export function BackmarkBatchHistory({ batches, questionLabels, nextCursor, loading, onLoadMore, onSummary }: Readonly<{
  questionLabels: Record<string, string>;
  batches: BackmarkBatch[]; nextCursor: string; loading: boolean; onLoadMore: () => void; onSummary: (batch: BackmarkBatch) => void;
}>) {
  return <>
    <div className="section-header backmark-batch-history"><div><h3>已有回标批次</h3></div></div>
    <Table className="dense-data-table" size="small" rowKey="id" loading={loading} pagination={false} dataSource={batches} footer={nextCursor ? () => renderBatchFooter(loading, onLoadMore) : undefined} columns={[
      { title: "题目", dataIndex: "question_id", render: (value: string) => questionLabels[value] || "题号待核对" },
      { title: "影响答卷", dataIndex: "affected_count", render: (value: number) => `${value} 份` },
      { title: "状态", dataIndex: "status", render: (value: BackmarkBatch["status"]) => <Tag>{batchStatusLabel(value)}</Tag> },
      { title: "创建时间", dataIndex: "created_at", render: batchTime },
      { title: "差异去向", key: "policy", render: (_, batch: BackmarkBatch) => dispositionLabel(batch.policy.disposition) },
      { title: "结果", key: "summary", render: (_, batch: BackmarkBatch) => <Button size="small" type="link" onClick={() => onSummary(batch)}>查看差异</Button> }
    ]} expandable={{ expandedRowRender: batchRecord }} locale={{ emptyText: "当前考试没有回标批次" }} />
  </>;
}

type BackmarkSummaryProps = Readonly<{
  summary?: BackmarkSummary; nextCursor: string; canManage: boolean; loading: boolean;
  questionLabel: string; error?: string; releasesError?: string; reviewersError?: string; onRetry: () => void;
  releases: ScoreRelease[]; reviewers: WorkflowActor[]; releaseID: string; assignee: string; preview?: RegradePreview;
  onLoadMore: () => void; onReleaseChange: (value: string) => void; onAssigneeChange: (value: string) => void;
  onPreview: () => void; onCreate: () => void;
}>;

function BackmarkRegradeOptions({ summary, questionLabel, error, releases, reviewers, releasesError, reviewersError, releaseID, assignee, preview, loading, onRetry, onReleaseChange, onAssigneeChange, onPreview, onCreate }: BackmarkSummaryProps & Readonly<{ summary: BackmarkSummary }>) {
  if (error) return <Alert type="warning" showIcon message="请先重新读取当前差异，再发起复评" />;
  if (summary.batch.status !== "ready_for_confirmation") return <Alert type="warning" showIcon message="回标批次尚未完成" description="请等待所有回标项结束，再创建稳定的复评范围。" />;
  if (releasesError || reviewersError) return <Alert type="warning" showIcon message="回标差异已读取，复评交接资料尚未齐全" description={[releasesError, reviewersError].filter(Boolean).join("；")} action={<Button loading={loading} onClick={onRetry}>重新读取交接资料</Button>} />;
  if (!releases.length) return <Alert type="warning" showIcon message="当前考试没有已发布的成绩版本" description="回标差异可继续查看；必须先有已发布版本才能发起正式题目复评。" />;
  return <>
    <Space direction="vertical" style={{ width: "100%" }}>
      <Select value={releaseID || undefined} disabled={loading} placeholder="选择已发布成绩版本" options={releases.map((release) => ({ value: release.id, label: `V${release.version} · ${release.reason || "正式成绩"}` }))} onChange={onReleaseChange} />
      <Select value={assignee || undefined} disabled={loading} placeholder="选择复评阅卷员" options={workflowActorOptions(reviewers)} onChange={onAssigneeChange} />
      <Button loading={loading} disabled={!releaseID || !reviewers.some((user) => user.id === assignee && user.username?.trim())} onClick={onPreview}>预览正式复评范围</Button>
    </Space>
    {preview ? <Alert className="backmark-preview" type="info" showIcon message={`将冻结 ${preview.affected_count} 份已发布答卷`} description={`${questionLabel}；接手教师：${workflowActorLabel(reviewers.find((user) => user.id === assignee)!)}；来源成绩版本 V${preview.source_release_version}。确认后仍需审批、复评、差异复核和新版本发布。`} action={<Button type="primary" size="small" loading={loading} onClick={onCreate}>创建复评任务</Button>} /> : null}
  </>;
}

function BackmarkRegradeHandoff(props: BackmarkSummaryProps & Readonly<{ summary: BackmarkSummary }>) {
  // 是否开放复评按批次总数判断，当前分页没有严重项也不能隐藏入口。
  const hasRegradeCandidates = (props.summary.regrade_required_count ?? 0) > 0;
  if (!props.canManage || !hasRegradeCandidates) return null;
  return <>
    <div className="section-header backmark-batch-history"><div><h3>转入题目复评</h3><p>系统将从已发布成绩版本重新冻结本批次对应的学生范围；不会直接采用回标候选分。</p></div></div>
    <BackmarkRegradeOptions {...props} />
  </>;
}

// 这里只显示批次和复评条件；选择值、预览失效与所有请求仍由页面持有。
export function BackmarkSummaryContent(props: BackmarkSummaryProps) {
  const { summary, questionLabel, error, nextCursor, loading, onLoadMore, onRetry } = props;
  if (!summary) {
    if (error) return <Alert type="error" showIcon message="回标差异读取失败" description={error} action={<Button loading={loading} onClick={onRetry}>重新读取回标差异</Button>} />;
    if (loading) return <LoadingState label="正在加载回标差异" />;
    return <Empty description="请选择回标批次后查看差异" />;
  }
  const hasRegradeCandidates = (summary.regrade_required_count ?? 0) > 0;
  return <>
    <Alert type="info" showIcon message="回标只记录候选差异，不会修改当前成绩" description="只有标记为“需重评”的已完成项目，才能由质量管理员明确转入题目复评。新任务仍需审批、人工复评并创建新的成绩发布版本。" />
    <Descriptions size="small" bordered column={2} items={[
      { key: "batch", label: "回标题目", children: questionLabel },
      { key: "time", label: "创建时间", children: batchTime(summary.batch.created_at) },
      { key: "status", label: "批次状态", children: <Tag>{batchStatusLabel(summary.batch.status)}</Tag> },
      { key: "count", label: "受影响答卷", children: `${summary.batch.affected_count} 份` },
      { key: "regrade", label: "需重评", children: `${summary.regrade_required_count ?? 0} 份` },
      { key: "policy", label: "差异去向", children: dispositionLabel(summary.batch.policy.disposition) }
    ]} />
    {batchRecord(summary.batch)}
    {error ? <Alert type="warning" showIcon message="刷新失败，暂时保留上次读取的差异" description={error} action={<Button loading={loading} onClick={onRetry}>重新读取回标差异</Button>} /> : null}
    <div className="section-header backmark-batch-history"><div><h3>分差分布</h3></div></div>
    {summary.diff_histogram.length ? <Table className="dense-data-table" size="small" rowKey={(item) => `${item.delta}`} pagination={false} dataSource={summary.diff_histogram} columns={[
      { title: "新旧分差", dataIndex: "delta", render: (value: number) => `${value > 0 ? "+" : ""}${value} 分` },
      { title: "答卷数", dataIndex: "count", render: (value: number) => `${value} 份` }
    ]} /> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚未有完成的回标评分" />}
    <div className="section-header backmark-batch-history"><div><h3>严重变更清单</h3><p>仅列出需要进入题目复评的回标结果。</p></div></div>
    <Table className="dense-data-table" size="small" rowKey="id" pagination={false} dataSource={summary.items.filter((item) => item.status === "regrade_required")} columns={[
      { title: "答卷", key: "item", render: (_, __, index) => `本页第 ${index + 1} 份` },
      { title: "原分", dataIndex: "original_score" },
      { title: "新评分", dataIndex: "new_score", render: (value?: number) => value ?? "待完成" },
      { title: "分差", dataIndex: "diff", render: (value?: number) => scoreDifference(value) },
      { title: "状态", dataIndex: "status", render: () => <Tag color="error">需重评</Tag> }
    ]} expandable={{ expandedRowRender: renderBackmarkItemRecord }} locale={{ emptyText: hasRegradeCandidates ? "当前页暂无需重评项，请加载更多结果" : "没有需要题目复评的严重差异" }} />
    {nextCursor ? <Button block loading={loading} onClick={onLoadMore}>加载更多回标结果</Button> : null}
    <BackmarkRegradeHandoff {...props} summary={summary} />
  </>;
}

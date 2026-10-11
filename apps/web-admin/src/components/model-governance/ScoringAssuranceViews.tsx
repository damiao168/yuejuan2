import type { ReactNode } from "react";
import { Alert, Button, Card, Descriptions, Divider, Form, Input, InputNumber, List, Select, Space, Spin, Tag, Typography, type FormInstance } from "antd";
import { CheckCircle2, ClipboardCheck, RefreshCw, SlidersHorizontal } from "lucide-react";
import type {
  AddModelCalibrationEvidenceRequest, AIEligibilityPolicy, AIHumanDisagreement, AIHumanDisagreementTaxonomy,
  GradingEvaluationQualitySummary, GradingEvaluationResponseDifficulty, GradingEvaluationRun,
  GradingEvaluationSliceMetric, ModelCalibration, ModelCalibrationEvidence
} from "../../api/scoringAssurance";
import { assuranceDisagreementStatusLabel, assuranceErrorSourceOptions } from "./scoringAssurancePresentation";
import { eligibilityStages, eligibilityRisks, eligibilityScopeLabel, type EligibilitySelection } from "./eligibilityScope";

function percentage(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

function optionalPercentage(value?: number) {
  return value === undefined ? "未采集" : percentage(value);
}

function shortID(value: string) {
  return value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;
}

function eligibilityPolicySummary(policy: AIEligibilityPolicy | undefined, policyError: string | undefined, selectionComplete: boolean) {
  if (policy) return <Descriptions size="small" column={{ xs: 1, sm: 2, lg: 4 }} items={[
    { key: "version", label: "策略版本", children: `V${policy.version}` },
    { key: "quality", label: "最低质量", children: `OCR ${percentage(policy.min_ocr_quality)} · 解析 ${percentage(policy.min_parser_quality)}` },
    { key: "evaluation", label: "评测门槛", children: `${policy.min_eval_n} 条 · 严重误差 ≤ ${percentage(policy.max_severe_error_rate)}` },
    { key: "modes", label: "允许模式", children: policy.allowed_modes.join(" · ") }
  ]} />;
  // 没有策略且读取失败时不显示空状态；已有策略仍优先展示。
  if (policyError) return null;
  return <Typography.Text type="secondary">{selectionComplete ? "正在读取当前范围策略。" : "请补齐学段、学科、具体题型和风险级别；完整选择前不会发起查询。"}</Typography.Text>;
}

// 展示组件固定在模块顶层，重渲染保留控件身份；状态、表单实例和请求仍归工作台。
export function EligibilityPolicyPanel({
  axis, onAxisChange, policy, policyLoading, policyError, canManageEligibility, subjectOptions, archetypeOptions,
  selectionComplete, selectionLocked, canEditPolicy,
  loadPolicy, openPolicyEditor, children
}: Readonly<{
  axis: EligibilitySelection; onAxisChange: (axis: EligibilitySelection) => void; policy?: AIEligibilityPolicy;
  selectionComplete: boolean; selectionLocked: boolean; canEditPolicy: boolean;
  policyLoading: boolean; policyError?: string; canManageEligibility: boolean;
  subjectOptions: Array<{ value: string; label: string }>; archetypeOptions: Array<{ value: string; label: string }>;
  loadPolicy: () => Promise<void>; openPolicyEditor: () => void; children: ReactNode;
}>) {
  return <Card size="small" title={<Space><SlidersHorizontal size={17} />正式评分使用检查</Space>} extra={<Space><Button icon={<RefreshCw size={15} />} disabled={!selectionComplete || selectionLocked} loading={policyLoading} onClick={() => void loadPolicy()}>{policyError ? "重新读取当前范围" : "查询当前范围"}</Button>{canManageEligibility ? <Button type="primary" disabled={!selectionComplete || !canEditPolicy || policyLoading || selectionLocked} onClick={openPolicyEditor}>维护当前范围策略</Button> : null}</Space>}>
        <p>当前检查范围：{eligibilityScopeLabel(axis)}</p>
        <p>选择具体题型及题目评分配置中的实际风险级别后，系统才会查询此范围；通配绑定不会自动获得准入。R3 长作答不能配置为 AI 快速确认或规则自动。</p>
        <Space wrap className="model-assurance-axis">
          <Select aria-label="学科" placeholder="选择学科" disabled={selectionLocked} value={axis.subject_code} options={subjectOptions} onChange={(subject_code) => onAxisChange({ ...axis, subject_code })} />
          <Select aria-label="学段" placeholder="选择学段" disabled={selectionLocked} value={axis.education_stage} options={eligibilityStages} onChange={(education_stage) => onAxisChange({ ...axis, education_stage })} />
          <Select aria-label="题型" placeholder="选择具体题型" disabled={selectionLocked} value={axis.archetype_code} options={archetypeOptions} onChange={(archetype_code) => onAxisChange({ ...axis, archetype_code })} />
          <Select aria-label="风险级别" placeholder="选择实际风险级别" disabled={selectionLocked} value={axis.risk_tier} options={eligibilityRisks} onChange={(risk_tier) => onAxisChange({ ...axis, risk_tier })} />
        </Space>
        <Divider />
        <Spin spinning={policyLoading}>
          {policyError ? <Alert type={canEditPolicy ? "info" : "error"} showIcon message={policyError} description={canEditPolicy ? "可为当前范围配置策略；仍需满足评测、批准及使用策略等条件。" : "当前范围尚未读取成功，请重新读取；不会将失败当作未配置策略。"} /> : null}
          {eligibilityPolicySummary(policy, policyError, selectionComplete)}
        </Spin>
        {children}
      </Card>;
}

export function EvaluationDetail({
  selectedEvaluation, actioning, qualitySummary, sliceMetrics, responseDifficulty, onOpenObservation,
  transitionEvaluation, onCreateCalibration
}: Readonly<{
  selectedEvaluation?: GradingEvaluationRun; actioning: boolean; qualitySummary?: GradingEvaluationQualitySummary;
  sliceMetrics: GradingEvaluationSliceMetric[]; responseDifficulty: GradingEvaluationResponseDifficulty[];
  onOpenObservation: () => void; transitionEvaluation: (kind: "complete" | "invalidate") => Promise<void>; onCreateCalibration: () => void;
}>) {
  if (!selectedEvaluation) return null;
  return <section className="model-assurance-detail">
          <Divider orientation="left">{selectedEvaluation.display_name}</Divider>
          <Space wrap>
            {selectedEvaluation.status === "draft" ? <><Button onClick={onOpenObservation}>录入对齐观察</Button><Button type="primary" loading={actioning} disabled={selectedEvaluation.observation_count === 0} onClick={() => void transitionEvaluation("complete")}>冻结评测</Button></> : null}
            {selectedEvaluation.status !== "invalidated" ? <Button danger loading={actioning} onClick={() => void transitionEvaluation("invalidate")}>标记失效</Button> : null}
            <Button onClick={onCreateCalibration} disabled={selectedEvaluation.status !== "completed"}>基于评测创建校准</Button>
          </Space>
          <EvaluationMetrics qualitySummary={qualitySummary} sliceMetrics={sliceMetrics} responseDifficulty={responseDifficulty} />
        </section>;
}

function EvaluationMetrics({ qualitySummary, sliceMetrics, responseDifficulty }: Readonly<{
  qualitySummary?: GradingEvaluationQualitySummary; sliceMetrics: GradingEvaluationSliceMetric[];
  responseDifficulty: GradingEvaluationResponseDifficulty[];
}>) {
  return <div className="model-assurance-metrics">
            <section><h4>全链路质量</h4>{qualitySummary ? <><Descriptions size="small" column={1} items={[
              { key: "page", label: "页面匹配", children: `${optionalPercentage(qualitySummary.page_match_accuracy.rate)} · n=${qualitySummary.page_match_accuracy.observed_count}` },
              { key: "crop", label: "平均 Crop IoU", children: `${optionalPercentage(qualitySummary.mean_crop_iou.mean)} · n=${qualitySummary.mean_crop_iou.observed_count}` },
              { key: "cer", label: "平均转录 CER", children: `${optionalPercentage(qualitySummary.mean_transcription_cer.mean)} · n=${qualitySummary.mean_transcription_cer.observed_count}` },
              { key: "formula", label: "公式精确率", children: `${optionalPercentage(qualitySummary.formula_exact_rate.rate)} · n=${qualitySummary.formula_exact_rate.observed_count}` },
              { key: "rubric", label: "Rubric 评分点一致", children: `${optionalPercentage(qualitySummary.mean_rubric_criterion_agreement.mean)} · n=${qualitySummary.mean_rubric_criterion_agreement.observed_count}` },
              { key: "routing", label: "错误转人工召回", children: optionalPercentage(qualitySummary.risky_error_routing_recall) }
            ]} />{qualitySummary.error_attribution.length ? <List size="small" dataSource={qualitySummary.error_attribution} renderItem={(item) => <List.Item><span>{assuranceErrorSourceOptions.find((option) => option.value === item.source)?.label ?? "其他来源"}</span><Space size={4}><Tag>n={item.count}</Tag><Tag color={item.human_routing_recall < 1 ? "error" : "success"}>转人工 {percentage(item.human_routing_recall)}</Tag></Space></List.Item>} /> : <Typography.Text type="secondary">尚未发现或归因错误。</Typography.Text>}</> : <Typography.Text type="secondary">录入观察后显示图像、裁切、转录、公式、Rubric 与路由指标。</Typography.Text>}</section>
            <section><h4>切片指标</h4>{sliceMetrics.length ? <List size="small" dataSource={sliceMetrics.slice(0, 12)} renderItem={(item) => <List.Item><span>{item.dimension}: {item.value}</span><Space size={4}><Tag>n={item.metrics.sample_count}</Tag><Tag color={item.metrics.severe_error_rate > 0 ? "error" : "success"}>严重误差 {percentage(item.metrics.severe_error_rate)}</Tag></Space></List.Item>} /> : <Typography.Text type="secondary">完成评测后生成按题型、分段和 OCR 质量的切片。</Typography.Text>}</section>
            <section><h4>困难答卷</h4>{responseDifficulty.length ? <List size="small" dataSource={responseDifficulty.slice(0, 8)} renderItem={(item) => <List.Item><span>{item.difficulty_band} · {shortID(item.response_key)}</span><Tag color={item.severe_error ? "error" : "default"}>归一化误差 {percentage(item.normalized_error)}</Tag></List.Item>} /> : <Typography.Text type="secondary">完成评测后显示经验性难例，不代表学生能力。</Typography.Text>}</section>
          </div>;
}

export function CalibrationDetail({
  selectedCalibration, calibrationEvidence, actioning, evidenceForm, transitionCalibration, addCalibrationEvidence
}: Readonly<{
  selectedCalibration?: ModelCalibration; calibrationEvidence: ModelCalibrationEvidence[]; actioning: boolean;
  evidenceForm: FormInstance<AddModelCalibrationEvidenceRequest>;
  transitionCalibration: (kind: "complete" | "approve" | "invalidate") => Promise<void>; addCalibrationEvidence: () => Promise<void>;
}>) {
  if (!selectedCalibration) return null;
  // 只有草稿显示证据表单；完成、批准和失效状态沿用原有操作入口。
  return <section className="model-assurance-detail">
          <Space wrap><Typography.Text>方法：{selectedCalibration.method}</Typography.Text><Typography.Text>证据：{calibrationEvidence.length} 条</Typography.Text><Typography.Text>工件：{selectedCalibration.artifact_sha256 ? shortID(selectedCalibration.artifact_sha256) : "尚未生成"}</Typography.Text></Space>
          <Space wrap style={{ marginTop: 12 }}>
            {selectedCalibration.status === "draft" ? <><Button onClick={() => evidenceForm.resetFields()} disabled={actioning}>添加置信度证据</Button><Button type="primary" loading={actioning} disabled={calibrationEvidence.length === 0} onClick={() => void transitionCalibration("complete")}>完成校准</Button></> : null}
            {selectedCalibration.status === "completed" ? <Button type="primary" icon={<CheckCircle2 size={15} />} loading={actioning} onClick={() => void transitionCalibration("approve")}>批准校准</Button> : null}
            {selectedCalibration.status !== "invalidated" ? <Button danger loading={actioning} onClick={() => void transitionCalibration("invalidate")}>标记失效</Button> : null}
          </Space>
          {selectedCalibration.status === "draft" ? <Form form={evidenceForm} layout="inline" className="model-assurance-inline-form" onFinish={() => void addCalibrationEvidence()}>
            <Form.Item name="response_key" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}><Input placeholder="脱敏答卷键" /></Form.Item>
            <Form.Item name="raw_confidence" rules={[{ required: true }]}><InputNumber min={0} max={1} step={0.01} placeholder="模型置信度" /></Form.Item>
            <Button htmlType="submit" loading={actioning}>加入证据</Button>
          </Form> : null}
        </section>;
}

export function DisagreementQueue({
  disagreements, severeCount, disagreementLoading, disagreementError, canManageDisagreements, taxonomyOptions,
  loadDisagreements, onClassify, onRoute
}: Readonly<{
  disagreements: AIHumanDisagreement[]; severeCount: number; disagreementLoading: boolean; disagreementError?: string;
  canManageDisagreements: boolean; taxonomyOptions: Array<{ value: AIHumanDisagreementTaxonomy; label: string }>;
  loadDisagreements: () => Promise<void>; onClassify: (item: AIHumanDisagreement) => void; onRoute: (item: AIHumanDisagreement) => void;
}>) {
  return <Card size="small" title={<Space><ClipboardCheck size={17} />人机分歧队列</Space>} extra={<Space><Tag color={severeCount > 0 ? "error" : "success"}>待审阅严重分歧 {severeCount}</Tag><Button icon={<RefreshCw size={15} />} loading={disagreementLoading} onClick={() => void loadDisagreements()}>刷新</Button></Space>}>
        {disagreementError ? <Alert showIcon type="error" message="加载人机分歧失败" description={disagreementError} action={<Button onClick={() => void loadDisagreements()}>重试</Button>} /> : null}
        <List
          loading={disagreementLoading}
          dataSource={disagreements}
          locale={{ emptyText: "暂无需要处理的人机分歧" }}
          renderItem={(item) => <List.Item actions={canManageDisagreements ? [<Button key="classify" size="small" onClick={() => onClassify(item)}>分类</Button>, <Button key="route" size="small" onClick={() => onRoute(item)}>关联人工任务</Button>] : undefined}>
            <List.Item.Meta title={<Space><strong>题目 {shortID(item.question_id)}</strong><Tag color={item.severity === "severe" ? "error" : "warning"}>{item.severity === "severe" ? "严重" : "需关注"}</Tag><Tag>{assuranceDisagreementStatusLabel(item.status)}</Tag></Space>} description={<Space wrap><span>分差 {item.delta.toFixed(2)} / 满分 {item.max_score}</span><span>风险 {item.risk_tier}</span><span>{item.taxonomy ? taxonomyOptions.find((option) => option.value === item.taxonomy)?.label : "尚未分类"}</span></Space>} />
          </List.Item>}
        />
      </Card>;
}

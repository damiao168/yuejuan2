import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Descriptions, Divider, Drawer, Empty, Form, InputNumber, List, Progress, Select, Space, Tag } from "antd";
import { apiClient, getUserErrorMessage } from "../../api/client";
import {
  createCalibrationSession,
  getCalibrationPolicy,
  getGraderQualification,
  putCalibrationPolicy,
  submitCalibrationAttempt,
  type CalibrationAttempt,
  type CalibrationPolicy,
  type CalibrationSession,
  type GraderQualification,
  type PutCalibrationPolicyRequest
} from "../../api/calibration";
import { SharedScoreControl, type SharedScoreValue } from "../grading/SharedScoreControl";
import { rubricPointsFromSnapshot } from "../gold-papers/goldPaperPresentation";
import { TaskScoringReference } from "../../components/TaskScoringReference";
import { calibrationThresholdChecks, formatCriterionValue, formatMetric } from "./calibrationPresentation";
import "./calibration.css";

const emptyScore: SharedScoreValue = { score: null, rubricSelections: {} };
const archetypeOptions = [
  { value: "selected_response", label: "选择作答" },
  { value: "exact_text", label: "精确文本" },
  { value: "numeric_expression", label: "数值与表达式" },
  { value: "structured_steps", label: "分步解答" },
  { value: "short_constructed", label: "简短构造回答" },
  { value: "extended_response", label: "开放论述 / 写作" },
  { value: "diagram_graph", label: "作图与图像" },
  { value: "table_experiment", label: "表格与实验" }
];
// 界面用百分数填写，表单和接口仍保存 0～1 的比例；显示换算不能改变原有通过标准。
const rateFieldProps = {
  getValueProps: (value?: number | null) => ({ value: value == null ? value : Number((value * 100).toFixed(6)) }),
  normalize: (value?: number | null) => value == null ? value : value / 100
};

function renderCalibrationQualification(qualification?: GraderQualification) {
  return <Alert
    showIcon
    type={qualification?.status === "qualified" ? "success" : "warning"}
    message={qualification?.status === "qualified" ? "本题试评已通过，已具备阅卷资格" : "高风险题需先通过试评，再领取正式任务"}
    description={qualification?.status === "qualified" ? `本题资格有效至 ${new Date(qualification.valid_until).toLocaleDateString()}；标准卷变化后需重新确认资格。` : "独立给标准卷打分，系统会与参考分比较。每份提交后才显示参考分和相差分数。"}
  />;
}

function renderCalibrationResult(session: CalibrationSession | undefined, complete: boolean, checks: ReturnType<typeof calibrationThresholdChecks>, acting: boolean, start: () => Promise<void>) {
  if (!complete || !session?.metrics) return null;
  return <>
    <Divider />
    <Alert type={session.status === "passed" ? "success" : "error"} showIcon message={session.status === "passed" ? "试评通过，已达到本题阅卷要求" : "试评未通过，请对照参考结果后重新试评"} />
    <Descriptions bordered size="small" column={2}>
      {checks.map((check) => <Descriptions.Item key={check.key} label={check.label}><Tag color={check.passed ? "green" : "red"}>{formatMetric(check.key, check.value)}</Tag></Descriptions.Item>)}
    </Descriptions>
    {session.status !== "passed" ? <Button onClick={() => void start()} loading={acting}>重新试评</Button> : null}
  </>;
}

function attemptSeverityColor(attempt: CalibrationAttempt) {
  if (attempt.severe_disagreement) return "red";
  if (attempt.exact_match) return "green";
  return "orange";
}

export function CalibrationDrawer(props: Parameters<typeof ScopedCalibrationDrawer>[0]) {
  // 校准样本和资格属于本次打开的题目、阅卷员与权限；切换后不能继承上一作用域的盲评状态。
  const scope = JSON.stringify([props.open, props.examId, props.questionId, props.graderId ?? "", props.canManagePolicy]);
  return <ScopedCalibrationDrawer key={scope} {...props} />;
}

function ScopedCalibrationDrawer({
  open,
  examId,
  questionId,
  graderId,
  canManagePolicy,
  onClose,
  onQualified
}: Readonly<{
  open: boolean;
  examId: string;
  questionId: string;
  graderId?: string;
  canManagePolicy: boolean;
  onClose: () => void;
  onQualified?: () => void;
}>) {
  const { message } = App.useApp();
  const [policyForm] = Form.useForm<PutCalibrationPolicyRequest>();
  const [loading, setLoading] = useState(false);
  const [acting, setActing] = useState(false);
  const [policy, setPolicy] = useState<CalibrationPolicy>();
  const [qualification, setQualification] = useState<GraderQualification>();
  const [session, setSession] = useState<CalibrationSession>();
  const [score, setScore] = useState<SharedScoreValue>(emptyScore);
  const [attempts, setAttempts] = useState<CalibrationAttempt[]>([]);
  const mountedRef = useRef(true);
  const loadRequestRef = useRef(0);
  const actionRequestRef = useRef(0);
  const actionPendingRef = useRef(false);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      loadRequestRef.current += 1;
      actionRequestRef.current += 1;
    };
  }, []);

  const currentSample = session?.samples[session.submitted_count];
  const rubricPoints = useMemo(
    () => currentSample ? rubricPointsFromSnapshot(currentSample.rubric_snapshot) : [],
    [currentSample]
  );

  const load = useCallback(async () => {
    if (!open || !examId || !questionId) return;
    const requestId = ++loadRequestRef.current;
    setLoading(true);
    try {
      const [policyResult, qualificationResult] = await Promise.allSettled([
        getCalibrationPolicy(examId, questionId),
        getGraderQualification(examId, questionId, graderId)
      ]);
      // 刷新只接受本作用域最新请求；已关闭或切换题目的结果不能改资格和表单版本。
      if (!mountedRef.current || requestId !== loadRequestRef.current) return;
      const nextPolicy = policyResult.status === "fulfilled" ? policyResult.value.policy : undefined;
      const nextQualification = qualificationResult.status === "fulfilled" ? qualificationResult.value.qualification : undefined;
      setPolicy(nextPolicy);
      setQualification(nextQualification);
      if (nextPolicy) {
        policyForm.setFieldsValue({
          archetype_code: nextPolicy.archetype_code,
          max_score: nextPolicy.max_score,
          minimum_samples: nextPolicy.minimum_samples,
          maximum_mae: nextPolicy.maximum_mae,
          minimum_exact_agreement: nextPolicy.minimum_exact_agreement,
          minimum_within_one_agreement: nextPolicy.minimum_within_one_agreement,
          minimum_criterion_agreement: nextPolicy.minimum_criterion_agreement,
          maximum_severe_rate: nextPolicy.maximum_severe_rate,
          severe_error_threshold: nextPolicy.severe_error_threshold,
          qualification_validity_days: nextPolicy.qualification_validity_days,
          expected_revision: nextPolicy.revision
        });
      } else {
        policyForm.setFieldsValue({
          archetype_code: "extended_response", max_score: 10, minimum_samples: 3, maximum_mae: 1,
          minimum_exact_agreement: 0.6, minimum_within_one_agreement: 0.9, minimum_criterion_agreement: 0.8,
          maximum_severe_rate: 0, severe_error_threshold: 3, qualification_validity_days: 30, expected_revision: 0
        });
      }
    } finally {
      if (mountedRef.current && requestId === loadRequestRef.current) setLoading(false);
    }
  }, [examId, graderId, open, policyForm, questionId]);

  useEffect(() => {
    if (!open) return;
    setSession(undefined);
    setAttempts([]);
    setScore(emptyScore);
    void load();
  }, [load, open]);

  const savePolicy = async () => {
    if (!open || !canManagePolicy || actionPendingRef.current) return;
    const requestId = ++actionRequestRef.current;
    actionPendingRef.current = true;
    setActing(true);
    try {
      const values = await policyForm.validateFields();
      if (!mountedRef.current || requestId !== actionRequestRef.current) return;
      const result = await putCalibrationPolicy(examId, questionId, values);
      if (!mountedRef.current || requestId !== actionRequestRef.current) return;
      setPolicy(result.policy);
      policyForm.setFieldValue("expected_revision", result.policy.revision);
      message.success("试评通过标准已保存");
    } catch (error) {
      if (mountedRef.current && requestId === actionRequestRef.current) message.error(getUserErrorMessage(error, "试评通过标准保存失败"));
    } finally {
      if (mountedRef.current && requestId === actionRequestRef.current) {
        actionPendingRef.current = false;
        setActing(false);
      }
    }
  };

  const start = async () => {
    if (!open || !policy || actionPendingRef.current) return;
    const requestId = ++actionRequestRef.current;
    actionPendingRef.current = true;
    setActing(true);
    try {
      const result = await createCalibrationSession(examId, questionId);
      if (!mountedRef.current || requestId !== actionRequestRef.current) return;
      setSession(result.session);
      setAttempts([]);
      setScore(emptyScore);
    } catch (error) {
      if (mountedRef.current && requestId === actionRequestRef.current) message.error(getUserErrorMessage(error, "无法开始试评，请检查通过标准与已批准的标准卷是否齐备"));
    } finally {
      if (mountedRef.current && requestId === actionRequestRef.current) {
        actionPendingRef.current = false;
        setActing(false);
      }
    }
  };

  const submit = async () => {
    if (!open || actionPendingRef.current) return;
    if (!session || !currentSample || score.score === null) {
      message.warning("请先填写本份样本得分");
      return;
    }
    const requestId = ++actionRequestRef.current;
    actionPendingRef.current = true;
    setActing(true);
    try {
      const result = await submitCalibrationAttempt(session.id, {
        gold_paper_id: currentSample.gold_paper_id,
        submitted_score: score.score,
        rubric_selections: score.rubricSelections
      });
      // 远端提交可能已成功；旧作用域不展示其参考结果，也不能通知新题目已通过。
      if (!mountedRef.current || requestId !== actionRequestRef.current) return;
      // 偏差只使用提交后返回的结果；当前样本的参考分不在作答前展示。
      setAttempts((current) => [...current, result.attempt]);
      setSession(result.session);
      setQualification(result.qualification ?? qualification);
      setScore(emptyScore);
      if (result.qualification?.status === "qualified") onQualified?.();
    } catch (error) {
      if (mountedRef.current && requestId === actionRequestRef.current) message.error(getUserErrorMessage(error, "本份试评提交失败，请保留评分后重试"));
    } finally {
      if (mountedRef.current && requestId === actionRequestRef.current) {
        actionPendingRef.current = false;
        setActing(false);
      }
    }
  };

  const complete = Boolean(session && session.status !== "in_progress");
  const checks = session?.metrics ? calibrationThresholdChecks(session.metrics, policy) : [];

  return (
    <Drawer title="阅卷前试评" className="grader-trial-drawer" width="min(860px, 100vw)" open={open} onClose={onClose} extra={<Button loading={loading} onClick={() => void load()}>刷新</Button>}>
      <Space direction="vertical" size={16} style={{ width: "100%" }}>
        <div className="grader-trial-intro"><h3>先试评，统一本题的给分尺度</h3><p>用已批准的标准卷练习评分，检查你的给分与参考标准是否一致。</p><ol><li>管理员准备标准卷和通过标准</li><li>阅卷老师独立评分</li><li>提交后查看分差，全部完成后判定是否达标</li></ol></div>
        {renderCalibrationQualification(qualification)}

        {canManagePolicy ? (
          <details className="grader-trial-settings">
            <summary>设置本题试评通过标准<span>管理员设置</span></summary>
            <p className="muted">各项要求需同时达标。题型和满分须与标准卷一致；请先在“标准卷”中准备足量且已批准的样本。</p>
            <Form form={policyForm} layout="vertical" style={{ marginTop: 12 }}>
              <div className="grader-trial-fields">
                <Form.Item name="archetype_code" label="作答类型" rules={[{ required: true }]}><Select options={archetypeOptions} /></Form.Item>
                <Form.Item name="max_score" label="本题满分" rules={[{ required: true }]}><InputNumber min={0.5} addonAfter="分" /></Form.Item>
                <Form.Item name="minimum_samples" label="至少试评几份" extra="标准卷数量不能少于此值" rules={[{ required: true }]}><InputNumber min={1} precision={0} addonAfter="份" /></Form.Item>
                <Form.Item name="maximum_mae" label="平均分差上限" extra="每份与参考分相差的绝对值，再取平均" rules={[{ required: true }]}><InputNumber min={0} step={0.1} addonAfter="分" /></Form.Item>
                <Form.Item name="minimum_exact_agreement" label="分数完全相同的最低比例" extra="例如 60%：至少六成样本与参考分相同" {...rateFieldProps}><InputNumber min={0} max={100} step={5} addonAfter="%" /></Form.Item>
                <Form.Item name="minimum_within_one_agreement" label="分差不超过 1 分的最低比例" {...rateFieldProps}><InputNumber min={0} max={100} step={5} addonAfter="%" /></Form.Item>
                <Form.Item name="minimum_criterion_agreement" label="采分点判断相同的最低比例" extra="设置后须有可比较的采分点数据" {...rateFieldProps}><InputNumber min={0} max={100} step={5} addonAfter="%" /></Form.Item>
                <Form.Item name="maximum_severe_rate" label="严重分差的最高比例" extra="0% 表示不允许出现严重分差" {...rateFieldProps}><InputNumber min={0} max={100} step={5} addonAfter="%" /></Form.Item>
                <Form.Item name="severe_error_threshold" label="相差几分算严重分差" extra="达到此分差即计为严重" ><InputNumber min={0.1} step={0.5} addonAfter="分" /></Form.Item>
                <Form.Item name="qualification_validity_days" label="通过后资格有效期"><InputNumber min={1} precision={0} addonAfter="天" /></Form.Item>
                <Form.Item name="expected_revision" hidden><InputNumber /></Form.Item>
              </div>
              <Button type="primary" loading={acting} onClick={() => void savePolicy()}>保存通过标准</Button>
            </Form>
          </details>
        ) : null}

        {!session ? (
          <div>
            <Button type="primary" loading={acting} disabled={!policy} onClick={() => void start()}>开始本题试评</Button>
            {!policy ? <p className="muted">尚未设置本题通过标准。请管理员展开上方设置并保存，同时在“标准卷”中准备已批准的样本。</p> : <p className="muted">本题至少需 {policy.minimum_samples} 份标准卷；开始后请完成系统提供的全部样本。</p>}
          </div>
        ) : (
          <>
            <Progress percent={Math.round((session.submitted_count / Math.max(1, session.samples.length)) * 100)} format={() => `${session.submitted_count} / ${session.samples.length}`} />
            {!complete && currentSample ? (
              <section>
                <h3>试评样本 {session.submitted_count + 1}</h3>
                <TaskScoringReference question={currentSample.question} rubric={currentSample.rubric_snapshot} />
                {currentSample.answer_image_url
                  ? <img src={apiClient.url(currentSample.answer_image_url)} alt="试评样本答题原图" style={{ display: "block", maxWidth: "100%", maxHeight: 430, margin: "12px auto", objectFit: "contain" }} />
                  : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={`答题图暂不可预览 · 匿名样本 ${currentSample.submission_id.slice(0, 8)}`} />}
                <SharedScoreControl value={score} maxScore={currentSample.max_score} rubricPoints={rubricPoints} onChange={setScore} disabled={acting} />
                <Button type="primary" loading={acting} disabled={score.score === null} onClick={() => void submit()}>提交本份并查看分差</Button>
              </section>
            ) : null}
          </>
        )}

        {attempts.length ? (
          <List
            header="试评记录 · 提交后显示参考结果"
            dataSource={attempts}
            renderItem={(attempt, index) => (
              <List.Item>
                <div style={{ width: "100%" }}>
                  <Space wrap><strong>样本 {index + 1}</strong><span>你的评分 {attempt.submitted_score} / 参考分 {attempt.reference_score}</span><Tag color={attemptSeverityColor(attempt)}>相差 {attempt.absolute_error} 分</Tag></Space>
                  {attempt.criterion_differences.length ? (
                    <List size="small" dataSource={attempt.criterion_differences} renderItem={(difference) => <List.Item>{difference.criterion}：提交 {formatCriterionValue(difference.submitted)}，参考 {formatCriterionValue(difference.expected)}</List.Item>} />
                  ) : <p className="muted">采分点与参考一致</p>}
                </div>
              </List.Item>
            )}
          />
        ) : null}

        {renderCalibrationResult(session, complete, checks, acting, start)}
      </Space>
    </Drawer>
  );
}

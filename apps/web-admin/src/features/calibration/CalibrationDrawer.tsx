import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Descriptions, Divider, Drawer, Empty, Form, Input, InputNumber, List, Progress, Space, Tag } from "antd";
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

const emptyScore: SharedScoreValue = { score: null, rubricSelections: {} };

function renderCalibrationQualification(qualification?: GraderQualification) {
  return <Alert
    showIcon
    type={qualification?.status === "qualified" ? "success" : "warning"}
    message={qualification?.status === "qualified" ? "当前题目已具备阅卷资格" : "高风险题领取前需完成校准"}
    description={qualification?.status === "qualified" ? `资格有效至 ${new Date(qualification.valid_until).toLocaleDateString()}` : "参考分不会提前显示；每份提交后才显示该份偏差。"}
  />;
}

function renderCalibrationResult(session: CalibrationSession | undefined, complete: boolean, checks: ReturnType<typeof calibrationThresholdChecks>, acting: boolean, start: () => Promise<void>) {
  if (!complete || !session?.metrics) return null;
  return <>
    <Divider />
    <Alert type={session.status === "passed" ? "success" : "error"} showIcon message={session.status === "passed" ? "校准通过，可以领取本题正式阅卷任务" : "校准未通过，请查看偏差后重新开始"} />
    <Descriptions bordered size="small" column={2}>
      {checks.map((check) => <Descriptions.Item key={check.key} label={check.label}><Tag color={check.passed ? "green" : "red"}>{formatMetric(check.key, check.value)}</Tag></Descriptions.Item>)}
    </Descriptions>
    {session.status !== "passed" ? <Button onClick={() => void start()} loading={acting}>重新校准</Button> : null}
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
      message.success("校准阈值已保存");
    } catch (error) {
      if (mountedRef.current && requestId === actionRequestRef.current) message.error(getUserErrorMessage(error, "校准阈值保存失败"));
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
      if (mountedRef.current && requestId === actionRequestRef.current) message.error(getUserErrorMessage(error, "无法开始校准"));
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
      if (mountedRef.current && requestId === actionRequestRef.current) message.error(getUserErrorMessage(error, "本份校准样本提交失败"));
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
    <Drawer title="阅卷员校准" width={860} open={open} onClose={onClose} extra={<Button loading={loading} onClick={() => void load()}>刷新</Button>}>
      <Space direction="vertical" size={16} style={{ width: "100%" }}>
        {renderCalibrationQualification(qualification)}

        {canManagePolicy ? (
          <details>
            <summary>题目级校准阈值（管理员）</summary>
            <Form form={policyForm} layout="vertical" style={{ marginTop: 12 }}>
              <Space wrap align="start">
                <Form.Item name="archetype_code" label="题型" rules={[{ required: true }]}><Input style={{ width: 180 }} /></Form.Item>
                <Form.Item name="max_score" label="满分" rules={[{ required: true }]}><InputNumber min={0.5} /></Form.Item>
                <Form.Item name="minimum_samples" label="最少样本" rules={[{ required: true }]}><InputNumber min={1} precision={0} /></Form.Item>
                <Form.Item name="maximum_mae" label="最大 MAE" rules={[{ required: true }]}><InputNumber min={0} step={0.1} /></Form.Item>
                <Form.Item name="minimum_exact_agreement" label="最低完全一致率"><InputNumber min={0} max={1} step={0.05} /></Form.Item>
                <Form.Item name="minimum_within_one_agreement" label="最低误差≤1一致率"><InputNumber min={0} max={1} step={0.05} /></Form.Item>
                <Form.Item name="minimum_criterion_agreement" label="最低采分点一致率"><InputNumber min={0} max={1} step={0.05} /></Form.Item>
                <Form.Item name="maximum_severe_rate" label="最大严重偏差率"><InputNumber min={0} max={1} step={0.05} /></Form.Item>
                <Form.Item name="severe_error_threshold" label="严重偏差分差"><InputNumber min={0.1} step={0.5} /></Form.Item>
                <Form.Item name="qualification_validity_days" label="资格有效天数"><InputNumber min={1} precision={0} /></Form.Item>
                <Form.Item name="expected_revision" hidden><InputNumber /></Form.Item>
              </Space>
              <Button type="primary" loading={acting} onClick={() => void savePolicy()}>保存阈值</Button>
            </Form>
          </details>
        ) : null}

        {!session ? (
          <div>
            <Button type="primary" loading={acting} disabled={!policy} onClick={() => void start()}>开始本题校准</Button>
            {!policy ? <p className="muted">管理员需先配置本题阈值，并准备已批准的标准卷。</p> : null}
          </div>
        ) : (
          <>
            <Progress percent={Math.round((session.submitted_count / Math.max(1, session.samples.length)) * 100)} format={() => `${session.submitted_count} / ${session.samples.length}`} />
            {!complete && currentSample ? (
              <section>
                <h3>样本 {session.submitted_count + 1}</h3>
                <TaskScoringReference question={currentSample.question} rubric={currentSample.rubric_snapshot} />
                {currentSample.answer_image_url
                  ? <img src={apiClient.url(currentSample.answer_image_url)} alt="校准答题区域" style={{ display: "block", maxWidth: "100%", maxHeight: 430, margin: "12px auto", objectFit: "contain" }} />
                  : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={`答题图暂不可预览 · 匿名样本 ${currentSample.submission_id.slice(0, 8)}`} />}
                <SharedScoreControl value={score} maxScore={currentSample.max_score} rubricPoints={rubricPoints} onChange={setScore} disabled={acting} />
                <Button type="primary" loading={acting} disabled={score.score === null} onClick={() => void submit()}>提交本份并查看偏差</Button>
              </section>
            ) : null}
          </>
        )}

        {attempts.length ? (
          <List
            header="已提交样本偏差（参考结果只在提交后显示）"
            dataSource={attempts}
            renderItem={(attempt, index) => (
              <List.Item>
                <div style={{ width: "100%" }}>
                  <Space wrap><strong>样本 {index + 1}</strong><span>提交 {attempt.submitted_score} / 参考 {attempt.reference_score}</span><Tag color={attemptSeverityColor(attempt)}>绝对误差 {attempt.absolute_error}</Tag></Space>
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

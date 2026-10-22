import { useEffect, useRef, useState, type ReactNode } from "react";
import { Button, Drawer } from "antd";
import { getUserErrorMessage } from "../../api/client";
import { getExamReadiness, type ExamReadiness, type ReadinessCheck } from "../../api/configuration";
import { preparationNavigationSteps } from "../exams/preparation/preparationSteps";

type CheckState = { owner: number; busy: boolean; readiness?: ExamReadiness; error?: string };
type QuestionStep = ReturnType<typeof preparationNavigationSteps>[number] | undefined;

function FailedQuestionChecks({ step, failedChecks, onOpenCheck }: Readonly<{
  step: QuestionStep; failedChecks: ReadinessCheck[]; onOpenCheck?: (check: ReadinessCheck) => void;
}>) {
  return <>
    {step?.state === "unknown" && <p>检查结果不完整，请重新检查；当前结果不能确认本步完成。</p>}
    {failedChecks.length ? <ul className="paper-check-issues">{failedChecks.map(item => <li key={item.code}>
      <span><strong>{item.label}</strong>：{item.message}</span>
      {onOpenCheck && <Button type="link" size="small" onClick={() => onOpenCheck(item)}>去处理</Button>}
    </li>)}</ul> : <p>{step?.detail}</p>}
  </>;
}

function questionCheckDisplay(canManage: boolean, disabled: boolean, disabledReason: string | undefined, state: CheckState, step: QuestionStep,
  failedChecks: ReadinessCheck[], onOpenCheck?: (check: ReadinessCheck) => void): { title: string; type: "info" | "success" | "warning" | "error"; description: ReactNode } {
  const idle = { title: "待检查", type: "info" as const, description: "检查已保存的题目数量、总分、每题分值、答案和评分配置。" };
  if (!canManage) return { ...idle, description: "当前考试或账号只允许查看，暂时无法检查确认小题与分值。" };
  if (disabled) return { ...idle, description: disabledReason || "请先保存当前修改，再检查确认。" };
  if (state.busy) return { ...idle, title: "正在检查小题与分值", description: "正在核对已保存的正式配置。" };
  if (state.error) return { title: "小题与分值检查失败", type: "error", description: state.error };
  if (!state.readiness) return idle;
  if (step?.state === "complete") return { title: "小题与分值检查已通过", type: "success", description: "本步正式配置检查已通过；请继续设置答题卡并确认考试准备。" };
  return { title: "小题与分值尚未通过检查", type: "warning", description: <FailedQuestionChecks step={step} failedChecks={failedChecks} onOpenCheck={onOpenCheck} /> };
}

/** 此处只检查已经保存的正式配置；题目与分值通过不代表答题卡或整场准备也已确认。 */
export function PaperQuestionConfirmation({ examId, configurationKey, canManage, disabled, disabledReason, onChecked, onConfirmed, heading, children, onOpenCheck, compact = false, actions }: Readonly<{
  examId: string;
  configurationKey: string;
  canManage: boolean;
  disabled: boolean;
  disabledReason?: string;
  onChecked?: () => void;
  onConfirmed?: () => void;
  heading?: ReactNode;
  children?: ReactNode;
  onOpenCheck?: (check: ReadinessCheck) => void;
  compact?: boolean;
  actions?: ReactNode;
}>) {
  const key = JSON.stringify([examId, configurationKey, canManage, disabled]);
  const scope = useRef({ key, version: 0 });
  if (scope.current.key !== key) scope.current = { key, version: scope.current.version + 1 };
  const version = scope.current.version;
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  const operation = useRef<number | null>(null);
  const [storedState, setStoredState] = useState<CheckState>({ owner: version, busy: false });
  const [checkOpen, setCheckOpen] = useState(false);
  const state = storedState.owner === version ? storedState : { owner: version, busy: false };
  const step = preparationNavigationSteps(state.readiness).find((item) => item.key === "questions");
  const failedChecks = (state.readiness?.checks ?? []).filter((check) => check.section === "questions" && !check.passed);
  const owns = () => mounted.current && scope.current.version === version;
  const patch = (next: Partial<CheckState>) => {
    if (!owns()) return;
    setStoredState((current) => ({ ...(current.owner === version ? current : { owner: version, busy: false }), ...next }));
  };

  async function check() {
    if (!examId || !canManage || disabled || !owns() || operation.current === version) return;
    // 同步锁阻止连续点击；配置、权限或未保存状态变化后，旧请求不能恢复之前的通过提示。
    operation.current = version;
    patch({ busy: true, readiness: undefined, error: undefined });
    try {
      const { readiness } = await getExamReadiness(examId);
      if (!owns()) return;
      patch({ readiness });
      // 失败检查也刷新上方导航，避免远端配置已失效而导航仍显示上次的绿色。
      onChecked?.();
      // 用与上方五步导航相同的检查集合；缺失检查不能据此完成，也不替用户发布评分配置。
      if (owns() && preparationNavigationSteps(readiness).find((item) => item.key === "questions")?.state === "complete") onConfirmed?.();
    } catch (error) {
      patch({ readiness: undefined, error: getUserErrorMessage(error, "暂时无法获取检查结果，请重试") });
    } finally {
      if (operation.current === version) operation.current = null;
      patch({ busy: false });
    }
  }

  const { title, type, description } = questionCheckDisplay(canManage, disabled, disabledReason, state, step, failedChecks, onOpenCheck ? check => {
    if (compact) setCheckOpen(false);
    onOpenCheck(check);
  } : undefined);
  const needsExplanation = !canManage || disabled || Boolean(state.error) || Boolean(state.readiness && step?.state !== "complete");
  return <section className={`paper-question-confirmation${compact ? " is-compact" : ""}`} aria-label="小题与分值检查" data-tone={type}>
    <div className="paper-step-toolbar">
      <div className="paper-step-heading">{!compact && (heading ?? <h2>小题与分值</h2>)}{actions}</div>
      <div className="paper-step-check-action">{!compact && <span className="paper-check-status" data-tone={type} role="status">{title}</span>}
        <Button type="primary" disabled={!examId || !canManage || disabled || state.busy} loading={state.busy}
          onClick={() => { if (compact) setCheckOpen(true); void check(); }}>{compact ? "检查题目配置" : "检查并确认小题与分值"}</Button></div>
    </div>
    {children}
    {!compact && needsExplanation && <div className="paper-check-explanation" role={state.error ? "alert" : undefined}>{description}</div>}
    {compact && <Drawer title="题目配置检查" width="min(560px, 100vw)" open={checkOpen} onClose={() => setCheckOpen(false)}>
      <h3 role="status">{title}</h3><div className="paper-check-explanation" role={state.error ? "alert" : undefined}>{description}</div>
      <Button disabled={!examId || !canManage || disabled || state.busy} loading={state.busy} onClick={() => void check()}>重新检查</Button>
    </Drawer>}
  </section>;
}

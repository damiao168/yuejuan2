import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Input, Select, Space } from "antd";
import {
  approveCalibrationSchoolTemplate,
  listCalibrationSchoolTemplates,
  withdrawCalibrationSchoolTemplate,
  type CalibrationPolicy,
  type CalibrationSchoolTemplateDirectory,
  type PutCalibrationPolicyRequest
} from "../../api/calibration";
import { getBusinessCommandSession } from "../../api/businessCommand";
import { getUserErrorMessage } from "../../api/client";
import { assessmentArchetypeLabels } from "../../constants/assessmentLabels";
import { calibrationPolicyValues } from "./CalibrationPolicyReuse";

interface Props {
  examId: string;
  questionId: string;
  disabled: boolean;
  policy?: CalibrationPolicy;
  hasUnsavedChanges: boolean;
  readCurrent: () => Partial<PutCalibrationPolicyRequest>;
  onKnownQuestion: (facts: { archetype_code?: string; max_score?: number }) => void;
  onApply: (values: PutCalibrationPolicyRequest) => void;
  onWorkingChange: (working: boolean) => void;
}

export function CalibrationSchoolTemplates(props: Readonly<Props>) {
  // 目录与管理动作都属于同一题目；切题后不能继续恢复或批准旧题目的模板。
  return <ScopedSchoolTemplates key={`${props.examId}:${props.questionId}`} {...props} />;
}

function ScopedSchoolTemplates(props: Readonly<Props>) {
  const { examId, questionId } = props;
  const [directory, setDirectory] = useState<CalibrationSchoolTemplateDirectory>();
  const [selected, setSelected] = useState("");
  const [name, setName] = useState("");
  const [reason, setReason] = useState("");
  const [phase, setPhase] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [blocked, setBlocked] = useState(false);
  const request = useRef(0), working = useRef(false), blockedRef = useRef(false);
  const live = useRef(props); live.current = props;
  const current = (generation: number, session: ReturnType<typeof getBusinessCommandSession>) =>
    generation === request.current && session === getBusinessCommandSession() && !blockedRef.current;

  const load = useCallback(async () => {
    if (working.current || blockedRef.current) return;
    const generation = ++request.current, session = getBusinessCommandSession();
    working.current = true; live.current.onWorkingChange(true); setPhase("load"); setError("");
    try {
      const result = await listCalibrationSchoolTemplates(examId, questionId);
      if (generation !== request.current || session !== getBusinessCommandSession() || blockedRef.current) return;
      setDirectory(result);
      // 等待目录时表单可能已有新输入，只补仍未知的题目事实，不重填阈值或目标修订。
      const target = live.current.readCurrent();
      const facts: { archetype_code?: string; max_score?: number } = {};
      if (!target.archetype_code && result.question.archetype_code) facts.archetype_code = result.question.archetype_code;
      if (!(Number(target.max_score) > 0) && result.question.max_score > 0) facts.max_score = result.question.max_score;
      if (facts.archetype_code || facts.max_score) live.current.onKnownQuestion(facts);
    } catch (cause) {
      if (generation === request.current && session === getBusinessCommandSession()) setError(getUserErrorMessage(cause, "学校试评标准读取失败，请重试"));
    } finally {
      if (generation === request.current) { working.current = false; live.current.onWorkingChange(false); setPhase(""); }
    }
  }, [examId, questionId]);

  useEffect(() => {
    void load();
    return () => { request.current++; working.current = false; live.current.onWorkingChange(false); };
  }, [load]);
  useEffect(() => {
    const invalidate = () => {
      request.current++; working.current = false; live.current.onWorkingChange(false); blockedRef.current = true;
      setBlocked(true); setDirectory(undefined); setSelected(""); setPhase(""); setNotice("");
      setError("登录状态已变化，请重新打开本题后处理学校标准");
    };
    window.addEventListener("business-command-scope-changed", invalidate);
    return () => window.removeEventListener("business-command-scope-changed", invalidate);
  }, []);

  async function apply() {
    if (working.current || props.disabled || blockedRef.current) return;
    const chosen = directory?.templates.find(item => item.id === selected);
    if (!chosen) return;
    const generation = ++request.current, session = getBusinessCommandSession();
    const snapshot = JSON.stringify(live.current.readCurrent());
    working.current = true; live.current.onWorkingChange(true); setPhase("apply"); setError(""); setNotice("");
    try {
      const fresh = await listCalibrationSchoolTemplates(examId, questionId);
      if (!current(generation, session)) return;
      setDirectory(fresh);
      const source = fresh.templates.find(item => item.id === chosen.id && item.revision === chosen.revision && item.status === "approved");
      if (!source) throw new Error("这版学校标准已撤回或更新，请重新选择");
      const target = live.current.readCurrent();
      if (JSON.stringify(target) !== snapshot) throw new Error("读取期间本题设置已有修改，已保留输入，请重新带入");
      if (source.policy.archetype_code !== target.archetype_code || source.policy.max_score !== target.max_score) throw new Error("本题类型或满分已改变，请先核对题目资料");
      // 来源版本随表单保存，服务端会在正式保存事务内重验批准状态；阅卷员资格不随模板复制。
      live.current.onApply({ ...calibrationPolicyValues(source.policy, target.expected_revision ?? 0), school_template_id: source.id, school_template_revision: source.revision });
      setNotice(`已带入“${source.name}”第 ${source.revision} 版，请保存本题。`);
    } catch (cause) {
      if (current(generation, session)) setError(getUserErrorMessage(cause, "学校标准未带入，当前输入已保留"));
    } finally {
      if (generation === request.current) { working.current = false; live.current.onWorkingChange(false); setPhase(""); }
    }
  }

  async function approve() {
    const source = live.current.policy;
    if (working.current || props.disabled || blockedRef.current || !directory?.capabilities.can_approve || !source || live.current.hasUnsavedChanges || !name.trim()) return;
    const generation = ++request.current, session = getBusinessCommandSession();
    working.current = true; live.current.onWorkingChange(true); setPhase("approve"); setError(""); setNotice("");
    try {
      const result = await approveCalibrationSchoolTemplate(examId, questionId, { name: name.trim(), source_policy_revision: source.revision });
      if (!current(generation, session)) return;
      if (result.template.status !== "approved") throw new Error("同名的这版标准已撤回，请更换名称或先保存新版标准");
      const fresh = await listCalibrationSchoolTemplates(examId, questionId);
      if (!current(generation, session)) return;
      setDirectory(fresh); setSelected(result.template.id); setNotice("本题已保存标准已批准为学校模板。");
    } catch (cause) {
      if (current(generation, session)) setError(getUserErrorMessage(cause, "批准结果尚未确认，请刷新学校标准后核对"));
    } finally {
      if (generation === request.current) { working.current = false; live.current.onWorkingChange(false); setPhase(""); }
    }
  }

  async function withdraw() {
    const source = directory?.templates.find(item => item.id === selected);
    if (working.current || props.disabled || blockedRef.current || !directory?.capabilities.can_withdraw || !source || !reason.trim()) return;
    const generation = ++request.current, session = getBusinessCommandSession();
    working.current = true; live.current.onWorkingChange(true); setPhase("withdraw"); setError(""); setNotice("");
    try {
      await withdrawCalibrationSchoolTemplate(examId, questionId, source.id, { expected_revision: source.revision, reason: reason.trim() });
      if (!current(generation, session)) return;
      const fresh = await listCalibrationSchoolTemplates(examId, questionId);
      if (!current(generation, session)) return;
      setDirectory(fresh); setSelected(""); setReason(""); setNotice("学校模板已撤回，不能继续用于新的题目设置。");
    } catch (cause) {
      if (current(generation, session)) setError(getUserErrorMessage(cause, "撤回结果尚未确认，请刷新学校标准后核对"));
    } finally {
      if (generation === request.current) { working.current = false; live.current.onWorkingChange(false); setPhase(""); }
    }
  }

  const busy = props.disabled || Boolean(phase) || blocked;
  const chosen = directory?.templates.find(item => item.id === selected);
  const mayManage = directory?.capabilities.can_approve || directory?.capabilities.can_withdraw;
  return <section aria-label="学校试评标准" className="grader-trial-school-standard">
    <Space wrap>
      <Select aria-label="学校已批准试评标准" placeholder={directory?.templates.length ? "选择学校已批准标准" : "暂无匹配的学校标准"} value={selected || undefined}
        options={(directory?.templates ?? []).map(item => ({ value: item.id, label: `${item.name} · 第 ${item.revision} 版` }))} onChange={setSelected} disabled={busy} style={{ minWidth: 250 }} />
      <Button onClick={() => void apply()} disabled={busy || !selected} loading={phase === "apply"}>带入学校标准</Button>
      <Button onClick={() => void load()} disabled={busy} loading={phase === "load"}>刷新学校标准</Button>
    </Space>
    {chosen ? <details><summary>查看这版标准</summary><p>{assessmentArchetypeLabels[chosen.policy.archetype_code] || "本题作答类型"} · {chosen.policy.max_score} 分 · 至少 {chosen.policy.minimum_samples} 份样本</p>
      <p>批准于 {new Date(chosen.approved_at).toLocaleString("zh-CN")} · 来源标准修订 {chosen.source_policy_revision}</p>
      <p>平均分差不超过 {chosen.policy.maximum_mae} 分；同分比例至少 {Math.round(chosen.policy.minimum_exact_agreement * 100)}%；分差不超过 1 分的比例至少 {Math.round(chosen.policy.minimum_within_one_agreement * 100)}%。</p>
      <p>{chosen.policy.minimum_criterion_agreement === undefined ? "不单独要求采分点一致率" : `采分点一致率至少 ${Math.round(chosen.policy.minimum_criterion_agreement * 100)}%`}；严重分差比例最多 {Math.round(chosen.policy.maximum_severe_rate * 100)}%，相差达到 {chosen.policy.severe_error_threshold} 分计入；资格有效期 {chosen.policy.qualification_validity_days} 天。</p>
    </details> : null}
    {mayManage ? <details><summary>管理学校标准</summary>
      {directory?.capabilities.can_approve ? <><p>将本题已保存的标准批准为本校同学科模板。未保存的修改不会进入模板。</p><Space wrap>
        <Input aria-label="学校试评模板名称" placeholder="模板名称" maxLength={80} value={name} onChange={event => setName(event.target.value)} disabled={busy} />
        <Button onClick={() => void approve()} disabled={busy || !props.policy || props.hasUnsavedChanges || !name.trim()} loading={phase === "approve"}>批准已保存标准</Button>
      </Space>{props.hasUnsavedChanges ? <p>请先保存本题修改，再批准学校模板。</p> : null}</> : null}
      {directory?.capabilities.can_withdraw && chosen ? <Space wrap>
        <Input aria-label="撤回学校标准原因" placeholder="撤回原因" maxLength={500} value={reason} onChange={event => setReason(event.target.value)} disabled={busy} />
        <Button danger onClick={() => void withdraw()} disabled={busy || !reason.trim()} loading={phase === "withdraw"}>撤回所选模板</Button>
      </Space> : null}
    </details> : null}
    {error ? <Alert type="warning" showIcon message={error} /> : null}
    {notice ? <p role="status">{notice}</p> : null}
  </section>;
}

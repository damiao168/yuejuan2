import { useEffect, useRef, useState } from "react";
import { Alert, Button, Select, Space } from "antd";
import { listQuestions } from "../../api/papers";
import { getCalibrationPolicy, type CalibrationPolicy, type PutCalibrationPolicyRequest } from "../../api/calibration";
import { getUserErrorMessage } from "../../api/client";
import { getBusinessCommandSession } from "../../api/businessCommand";
import { assessmentArchetypeLabels } from "../../constants/assessmentLabels";

export function calibrationPolicyValues(policy: CalibrationPolicy, expectedRevision: number): PutCalibrationPolicyRequest {
  return { archetype_code: policy.archetype_code, max_score: policy.max_score, minimum_samples: policy.minimum_samples,
    maximum_mae: policy.maximum_mae, minimum_exact_agreement: policy.minimum_exact_agreement,
    minimum_within_one_agreement: policy.minimum_within_one_agreement, minimum_criterion_agreement: policy.minimum_criterion_agreement,
    maximum_severe_rate: policy.maximum_severe_rate, severe_error_threshold: policy.severe_error_threshold,
    qualification_validity_days: policy.qualification_validity_days, expected_revision: expectedRevision };
}

export function CalibrationPolicyReuse({ examId, questionId, disabled, readCurrent, onApply, onKnownQuestion }: Readonly<{
  examId: string; questionId: string; disabled: boolean;
  readCurrent: () => { archetype_code?: string; max_score?: number; expected_revision?: number };
  onApply: (values: PutCalibrationPolicyRequest) => void;
  onKnownQuestion: (values: { archetype_code?: string; max_score?: number }) => void;
}>) {
  const [items, setItems] = useState<{ label: string; policy: CalibrationPolicy }[]>([]);
  const [selected, setSelected] = useState(""); const [loading, setLoading] = useState(false); const [error, setError] = useState("");
  const request = useRef(0); useEffect(() => () => { request.current++; }, []);
  async function load() {
    if (disabled || loading) return;
    const generation = ++request.current, session = getBusinessCommandSession();
    let target = readCurrent();
    const current = () => generation === request.current && session === getBusinessCommandSession();
    setLoading(true); setError(""); setItems([]); setSelected("");
    try {
      const questions = await listQuestions(examId);
      if (!current()) return;
      target = readCurrent();
      const question = questions.questions.find(item => item.id === questionId && item.exam_id === examId);
      if ((!target.archetype_code || !target.max_score) && question) {
        const known = { archetype_code: target.archetype_code || (question.assessment_archetype && assessmentArchetypeLabels[question.assessment_archetype] ? question.assessment_archetype : undefined), max_score: target.max_score || (question.score > 0 ? question.score : undefined) };
        onKnownQuestion(known); target = { ...target, ...known };
      }
      if (!target.archetype_code || !(Number(target.max_score) > 0)) throw new Error("请先核对本题作答类型和满分");
      const result: typeof items = [];
      // 只读取同场同分值题目的正式已保存标准，不冒充学校审批模板；每条读取沿用既有授权接口。
      const candidates = questions.questions.filter(question => question.id !== questionId && question.score === target.max_score).slice(0, 100);
      for (const question of candidates) {
        if (!current()) return;
        try {
          const { policy } = await getCalibrationPolicy(examId, question.id);
          if (policy && policy.exam_id === examId && policy.question_id === question.id && policy.archetype_code === target.archetype_code && policy.max_score === target.max_score) result.push({ label: `第 ${question.question_no} 题 · 修订 ${policy.revision} · ${policy.minimum_samples} 份样本`, policy });
        } catch { /* 无标准或无读取权限的题目不作为可复用来源。 */ }
      }
      if (current()) { setItems(result); if (!result.length) setError("本场没有同作答类型、同满分的已保存通过标准，请人工核对本题要求。"); }
    } catch (cause) { if (current()) setError(getUserErrorMessage(cause, "可复用标准读取失败，请先核对本题类型和满分")); }
    finally { if (current()) setLoading(false); }
  }
  function apply() {
    const source = items.find(item => item.policy.id === selected)?.policy, target = readCurrent();
    // 打开来源后管理员仍可能改类型和满分；应用时复核匹配并保留目标修订，绝不复制来源修订去覆盖本题。
    if (!source || source.archetype_code !== target.archetype_code || source.max_score !== target.max_score) { setError("本题类型或满分已改变，请重新读取可复用标准。"); return; }
    onApply(calibrationPolicyValues(source, target.expected_revision ?? 0));
  }
  return <details><summary>复用本场已保存标准</summary><p>仅匹配相同作答类型和满分。带入后仍需核对并保存本题，不会继承其他教师的通过资格。</p>
    <Space wrap><Button disabled={disabled} loading={loading} onClick={() => void load()}>读取可复用标准</Button>
      {items.length ? <><Select aria-label="可复用试评标准" style={{ minWidth: 260 }} value={selected || undefined} options={items.map(item => ({ value: item.policy.id, label: item.label }))} onChange={setSelected} disabled={disabled || loading} /><Button disabled={disabled || loading || !selected} onClick={apply}>带入本题待核对</Button></> : null}</Space>
    {error ? <Alert type="info" message={error} /> : null}
  </details>;
}

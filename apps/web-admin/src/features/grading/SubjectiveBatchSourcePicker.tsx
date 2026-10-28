import { useEffect, useRef, useState } from "react";
import { Alert, Button } from "antd";
import { listAllExams, type Exam } from "../../api/exams";
import { listSubjectiveGradingSources, type SubjectiveGradingSource } from "../../api/subjectiveGrading";
import { getUserErrorMessage } from "../../api/client";
import { getBusinessCommandSession } from "../../api/businessCommand";

// 材料目录覆盖尚未生成阅卷任务的片段；只读匿名材料，不领取任务或写正式成绩。
export function SubjectiveBatchSourcePicker({ onSelect, disabled }: Readonly<{ onSelect: (ids: string[]) => void; disabled: boolean }>) {
  const [exams, setExams] = useState<Exam[]>([]); const [examId, setExamId] = useState("");
  const [tasks, setTasks] = useState<SubjectiveGradingSource[]>([]); const [questionId, setQuestionId] = useState("");
  const [selected, setSelected] = useState<string[]>([]); const [cursor, setCursor] = useState("");
  const [loading, setLoading] = useState(false); const [error, setError] = useState("");
  const request = useRef(0);
  useEffect(() => {
    let active = true; const session = getBusinessCommandSession();
    void listAllExams().then(result => { if (active && session === getBusinessCommandSession()) setExams(result.exams); }).catch(cause => { if (active && session === getBusinessCommandSession()) setError(getUserErrorMessage(cause, "考试目录读取失败")); });
    return () => { active = false; request.current++; };
  }, []);
  const load = async (id: string, next = "") => {
    const generation = ++request.current; const session = getBusinessCommandSession(); setLoading(true); setError("");
    try {
      const result = await listSubjectiveGradingSources(id, next || undefined);
      if (generation !== request.current || session !== getBusinessCommandSession()) return;
      setTasks(previous => next ? [...previous, ...result.sources] : result.sources); setCursor(result.has_more ? result.next_cursor ?? "" : "");
    } catch (cause) { if (generation === request.current && session === getBusinessCommandSession()) setError(getUserErrorMessage(cause, "阅卷目录读取失败")); }
    finally { if (generation === request.current && session === getBusinessCommandSession()) setLoading(false); }
  };
  const rows = [...new Map(tasks.filter(task => task.answer_segment_id && (!questionId || task.question_id === questionId)).map(task => [task.answer_segment_id, task])).values()];
  return <section aria-label="选择影子评分答卷">
    <label>考试 <select disabled={disabled} aria-label="影子评分考试" value={examId} onChange={event => { const id = event.target.value; request.current++; setLoading(false); setCursor(""); setError(""); setExamId(id); setTasks([]); setQuestionId(""); setSelected([]); onSelect([]); if (id) void load(id); }}><option value="">选择考试</option>{exams.map(exam => <option key={exam.id} value={exam.id}>{exam.name}</option>)}</select></label>
    <label>题目 <select disabled={disabled || loading || !examId} aria-label="影子评分题目" value={questionId} onChange={event => setQuestionId(event.target.value)}><option value="">已读取的全部题目</option>{[...new Map(tasks.map(task => [task.question_id, task.question_no])).entries()].map(([id, no]) => <option key={id} value={id}>第 {no} 题</option>)}</select></label>
    {rows.map(task => <label key={task.answer_segment_id} style={{ display: "block" }}><input type="checkbox" disabled={disabled || !task.ready || selected.length >= 1000 && !selected.includes(task.answer_segment_id)} checked={selected.includes(task.answer_segment_id)} onChange={event => { const next = event.target.checked ? [...selected, task.answer_segment_id] : selected.filter(id => id !== task.answer_segment_id); setSelected(next); onSelect(next); }} />第 {task.question_no} 题 · {task.submission_label || "匿名答卷"}{!task.ready ? ` · ${task.unavailable_reason}` : ""}</label>)}
    {cursor ? <Button disabled={disabled} loading={loading} onClick={() => void load(examId, cursor)}>加载更多答卷</Button> : null}
    {error ? <Alert type="warning" message={error} /> : null}
    <p>已选 {selected.length} 份；只显示当前考试可读的主观题材料，创建时会重新核对作答与评分依据。</p>
  </section>;
}

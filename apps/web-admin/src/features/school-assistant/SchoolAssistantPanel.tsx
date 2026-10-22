import { useEffect, useState } from "react";
import { Alert, Button, Input, Radio, Select, Spin, Tooltip } from "antd";
import { ArrowUp, RotateCcw, Square } from "lucide-react";
import { listAllExams, type Exam } from "../../api/exams";
import { getUserErrorMessage } from "../../api/client";
import type { SchoolAssistantIntent } from "../../api/schoolAssistant";
import { examSubjectLabel } from "../../constants/examStatus";
import { useSchoolAssistantContext } from "./SchoolAssistantProvider";
import { AssistantSources, SchoolAssistantResult } from "./SchoolAssistantResult";

const taskLabels: Record<SchoolAssistantIntent, string> = { exam_progress: "查看考试进度", publish_diagnosis: "为什么不能发布" };

function SchoolAssistantAnswer({ assistant }: Readonly<{ assistant: NonNullable<ReturnType<typeof useSchoolAssistantContext>> }>) {
  const { query } = assistant;
  return <div className="school-assistant-answer" aria-live="polite">
    {query.status === "running" ? <output className="school-assistant-progress"><Spin size="small" /><span>{query.progress}</span></output> : null}
    {query.status === "cancelled" ? <Alert type="info" message="已停止查询" description="可以更换范围或重新发送任务。" /> : null}
    {query.status === "error" ? <Alert type="error" message={query.error} action={<Button size="small" icon={<RotateCcw size={14} />} onClick={assistant.retry}>重新查询</Button>} /> : null}
    {query.status === "completed" && query.result ? <><SchoolAssistantResult result={query.result} user={assistant.user} onNavigate={assistant.navigate} /><Button size="small" icon={<RotateCcw size={14} />} onClick={assistant.retry}>重新核实</Button></> : <AssistantSources sources={query.sources} />}
    {query.status === "idle" ? <div className="school-assistant-empty"><strong>选择任务后发送查询</strong><p>结果会显示实际来源、读取时间和可用的处理入口。</p></div> : null}
  </div>;
}

export function SchoolAssistantPanel({ compact = false }: Readonly<{ compact?: boolean }>) {
  const assistant = useSchoolAssistantContext();
  const [intent, setIntent] = useState<SchoolAssistantIntent>("exam_progress");
  const [question, setQuestion] = useState("");
  const [exams, setExams] = useState<Exam[]>([]);
  const [examLoading, setExamLoading] = useState(false);
  const [examError, setExamError] = useState("");
  const [examRetry, setExamRetry] = useState(0);
  const canListExams = Boolean(assistant?.user.permissions.includes("exam:manage"));
  // 重试计数用于主动触发新请求，保留该依赖才能让重试按钮重新读取考试列表。
  // biome-ignore lint/correctness/useExhaustiveDependencies: examRetry 用于手动重试考试列表，删除后同一布局和权限下重试无效。
  useEffect(() => {
    if (compact || !canListExams) return;
    // 布局或列表权限变化后，旧请求不能再回写此轮列表状态。
    let live = true;
    setExamLoading(true); setExamError("");
    listAllExams().then((response) => { if (live) setExams(response.exams); })
      .catch((error) => { if (live) setExamError(getUserErrorMessage(error, "考试列表读取失败")); })
      .finally(() => { if (live) setExamLoading(false); });
    return () => { live = false; };
  }, [compact, canListExams, examRetry]);
  if (!assistant) return <Alert type="warning" message="学校业务助手仅对学校管理员开放" />;
  const { context, query, capabilities, capabilitiesLoading, capabilitiesError } = assistant;
  const capability = capabilities?.intents.find((item) => item.intent === intent);
  const running = query.status === "running";
  const scopeLabel = context.exam_name || context.exam_id || "请先选择一场考试";
  return <section className={`school-assistant-panel${compact ? " is-compact" : ""}`} aria-label="学校业务助手">
    {!compact ? <header className="school-assistant-title"><h1>学校业务</h1><p>查询考试进度、核实学生端发布条件，并进入对应环节处理。</p></header> : null}
    <div className="school-assistant-range"><span>查询范围</span><strong>{scopeLabel}</strong>{context.revision != null ? <small>打开时版本 {context.revision}</small> : null}<small>服务器按当前授权范围读取</small></div>
    {!compact ? <div className="school-assistant-exam-selector"><label htmlFor="school-assistant-exam">学科考试</label><Select id="school-assistant-exam" aria-label="选择学科考试" showSearch optionFilterProp="label" allowClear loading={examLoading} value={context.exam_id} disabled={!canListExams} placeholder={canListExams ? "选择要查询的考试" : "请从已获授权的考试工作区打开助手"}
      options={exams.map((exam) => ({ value: exam.id, label: `${exam.exam_session_name || exam.name} · ${examSubjectLabel(exam.subject)}` }))}
      onChange={(examId) => { const exam = exams.find((item) => item.id === examId); assistant.setContext(exam ? { exam_id: exam.id, exam_name: `${exam.exam_session_name || exam.name} · ${examSubjectLabel(exam.subject)}`, revision: exam.revision, origin_module: "school_assistant" } : {}); }} />
      {examError ? <Alert type="error" message={examError} action={<Button size="small" onClick={() => setExamRetry((value) => value + 1)}>重试</Button>} /> : null}
    </div> : null}
    <fieldset className="school-assistant-task"><legend>查询任务</legend><Radio.Group value={intent} onChange={(event) => setIntent(event.target.value)}>
      {(Object.keys(taskLabels) as SchoolAssistantIntent[]).map((task) => {
        const item = capabilities?.intents.find((cap) => cap.intent === task);
        return <Tooltip key={task} title={item?.available === false ? item.reason : undefined}><Radio.Button value={task} disabled={capabilitiesLoading || !item?.available}>{taskLabels[task]}</Radio.Button></Tooltip>;
      })}
    </Radio.Group><p>{intent === "publish_diagnosis" ? "核实向学生发布成绩的完整门禁，包含警告确认和重评阻塞。" : "读取考试工作区的当前阶段、阻塞事项和下一步。"}</p></fieldset>
    {capabilitiesError ? <Alert type="error" message={capabilitiesError} action={<Button size="small" onClick={assistant.refreshCapabilities}>重试</Button>} /> : null}
    {!capabilitiesLoading && capability?.available === false ? <Alert type="warning" message={capability.reason || "当前账户没有此任务的权限"} /> : null}
    <form className="school-assistant-composer" onSubmit={(event) => { event.preventDefault(); void assistant.run(intent, question.trim() || taskLabels[intent]); }}>
      <label htmlFor="school-assistant-question">补充问题（可选）</label><Input.TextArea id="school-assistant-question" aria-label="补充问题" value={question} onChange={(event) => setQuestion(event.target.value)} maxLength={2000} autoSize={{ minRows: 2, maxRows: 4 }} placeholder="例如：现在最先需要处理哪些问题？" disabled={running} />
      <div><span>按所选任务查询；当前不执行业务修改。</span>{running ? <Button icon={<Square size={14} />} onClick={assistant.cancel}>停止查询</Button> : <Button htmlType="submit" type="primary" icon={<ArrowUp size={15} />} disabled={!context.exam_id || !capability?.available || capabilitiesLoading}>发送查询</Button>}</div>
    </form>
    <SchoolAssistantAnswer assistant={assistant} />
  </section>;
}

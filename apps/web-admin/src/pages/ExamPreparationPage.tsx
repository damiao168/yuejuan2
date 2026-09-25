import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Checkbox, Space } from "antd";
import { ArrowRight, ClipboardCheck, Play, RefreshCw, Save } from "lucide-react";
import { ApiClientError, getSafeUserText, getUserErrorMessage } from "../api/client";
import { confirmExamReadiness, getExamReadiness, startExamCollection, type ExamReadiness } from "../api/configuration";
import { getExam, refreshExamCandidates, updateExam, type Exam } from "../api/exams";
import { listClasses, listGrades, type Grade, type SchoolClass } from "../api/org";
import { ErrorState, LoadingState } from "../components/PageState";
import { preparationSteps } from "../features/exams/preparation/preparationSteps";

export function readinessCheckRoute(examId: string, section: string) {
  const routeSection = preparationSteps.find((step) => step.key === section)?.route ?? "settings";
  return `/exams/${encodeURIComponent(examId)}/${routeSection}`;
}

export function mergeClassSelection(selected: string[], groupIds: string[], groupValues: string[]) {
  const group = new Set(groupIds);
  return [...selected.filter((id) => !group.has(id)), ...groupValues];
}

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error(`开考准备操作失败：${error.status} ${error.code}`, error);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

const questionTypeLabels: Record<string, string> = {
  single_choice: "单选题",
  multiple_choice: "多选题",
  true_false: "判断题",
  fill_blank: "填空题",
  numeric: "数值题",
  formula: "公式题",
  short_answer: "简答题",
  calculation: "计算题",
  essay: "作文",
  discussion: "论述题",
  coding: "编程题"
};

export function ExamStudentScopePage({ examId, canManage, onExamChanged }: { examId: string; canManage: boolean; onExamChanged?: () => void }) {
  const { message } = App.useApp();
  const [exam, setExam] = useState<Exam>();
  const [classes, setClasses] = useState<SchoolClass[]>([]);
  const [grades, setGrades] = useState<Grade[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
	const [syncing, setSyncing] = useState(false);
  const [error, setError] = useState<string>();
  const currentExamId = useRef(examId);
  currentExamId.current = examId;
  const loadVersion = useRef(0);

  const load = useCallback(async () => {
    const version = ++loadVersion.current;
    setLoading(true);
    setError(undefined);
    try {
      const [examResponse, classResponse, gradeResponse] = await Promise.all([getExam(examId), listClasses(), listGrades()]);
      if (version !== loadVersion.current || currentExamId.current !== examId) return;
      setExam(examResponse.exam);
      setClasses(classResponse.classes.filter((item) => item.school_id === examResponse.exam.school_id));
      setGrades(gradeResponse.grades.filter((item) => item.school_id === examResponse.exam.school_id));
      setSelected(examResponse.exam.class_ids);
    } catch (loadError) { if (version === loadVersion.current && currentExamId.current === examId) setError(formatError(loadError)); }
    finally { if (version === loadVersion.current && currentExamId.current === examId) setLoading(false); }
  }, [examId]);

  useEffect(() => {
    setExam(undefined); setClasses([]); setGrades([]); setSelected([]); setLoading(true); setSaving(false); setSyncing(false);
    void load();
    return () => { loadVersion.current++; };
  }, [load]);

  const gradeById = useMemo(() => new Map(grades.map((item) => [item.id, item])), [grades]);
  const changed = exam ? [...selected].sort().join(",") !== [...exam.class_ids].sort().join(",") : false;

  async function save() {
    if (!exam || exam.id !== currentExamId.current || !selected.length) { message.error("请等待当前考试加载并至少选择一个班级"); return; }
    const requestExamId = examId;
    setSaving(true);
    try {
      const response = await updateExam(exam.id, { class_ids: selected, expected_revision: exam.revision });
      if (currentExamId.current !== requestExamId) return;
      setExam(response.exam);
      onExamChanged?.();
      message.success("学生范围已保存；开考准备需要重新确认。");
    } catch (saveError) { if (currentExamId.current === requestExamId) message.error(formatError(saveError)); }
    finally { if (currentExamId.current === requestExamId) setSaving(false); }
  }

  async function syncCandidates() {
	if (!exam || exam.id !== currentExamId.current) return;
	const requestExamId = examId;
	setSyncing(true);
	try {
	  const { candidate_refresh: result } = await refreshExamCandidates(exam.id);
	  if (currentExamId.current !== requestExamId) return;
	  const changes = [result.added_count ? `新增 ${result.added_count} 人` : "", result.removed_count ? `移除 ${result.removed_count} 人` : ""].filter(Boolean).join("，");
	  message.success(changes ? `学生名单已同步：${changes}，当前 ${result.after_count} 人` : `学生名单已是最新，共 ${result.after_count} 人`);
	} catch (syncError) { if (currentExamId.current === requestExamId) message.error(formatError(syncError)); }
	finally { if (currentExamId.current === requestExamId) setSyncing(false); }
  }

  if (loading || (exam && exam.id !== examId)) return <LoadingState label="正在加载学生范围" />;
  if (error) return <ErrorState message={error} onRetry={() => void load()} />;

  return (
    <div className="preparation-page">
      <section className="preparation-heading"><div><h2>学生范围</h2><p>选择参加本场考试的班级；确认准备完成时，系统将按当前在读学籍生成最终冻结名单。</p></div><Space><Button icon={<RefreshCw size={16} />} disabled={!canManage || changed || !exam || !["draft", "configured"].includes(exam.status)} loading={syncing} onClick={() => void syncCandidates()}>同步学生名单</Button><Button type="primary" icon={<Save size={16} />} disabled={!canManage || !changed || !selected.length} loading={saving} onClick={() => void save()}>保存范围</Button></Space></section>
      {!classes.length ? <Alert type="warning" showIcon message="当前学校还没有班级" description="请先在组织与用户中创建年级、班级并导入学生。" /> : (
        <div className="class-scope-list">
          {grades.map((grade) => {
            const gradeClasses = classes.filter((item) => item.grade_id === grade.id);
            if (!gradeClasses.length) return null;
            return <section key={grade.id}><div><strong>{grade.name}</strong><span>{grade.academic_year}</span></div><Checkbox.Group value={selected} onChange={(values) => setSelected((previous) => mergeClassSelection(previous, gradeClasses.map((item) => item.id), values as string[]))} disabled={!canManage}>{gradeClasses.map((item) => <Checkbox key={item.id} value={item.id}><span>{item.name}</span><small>{item.code}</small></Checkbox>)}</Checkbox.Group></section>;
          })}
          {classes.filter((item) => !gradeById.has(item.grade_id)).length ? <section><div><strong>未分组班级</strong></div><Checkbox.Group value={selected} onChange={(values) => setSelected((previous) => mergeClassSelection(previous, classes.filter((item) => !gradeById.has(item.grade_id)).map((item) => item.id), values as string[]))} disabled={!canManage}>{classes.filter((item) => !gradeById.has(item.grade_id)).map((item) => <Checkbox key={item.id} value={item.id}>{item.name}</Checkbox>)}</Checkbox.Group></section> : null}
        </div>
      )}
    </div>
  );
}

export function ExamReadinessPage({ examId, canManage, onNavigate, onExamChanged }: { examId: string; canManage: boolean; onNavigate: (path: string) => void; onExamChanged?: () => void }) {
  const { message, modal } = App.useApp();
  const [exam, setExam] = useState<Exam>();
  const [readiness, setReadiness] = useState<ExamReadiness>();
  const [loading, setLoading] = useState(true);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<string>();
  const currentExamId = useRef(examId);
  currentExamId.current = examId;
  const loadVersion = useRef(0);

  const load = useCallback(async () => {
    const version = ++loadVersion.current;
    setLoading(true);
    setError(undefined);
    try {
      const [examResponse, readinessResponse] = await Promise.all([getExam(examId), getExamReadiness(examId)]);
      if (version !== loadVersion.current || currentExamId.current !== examId) return;
      setExam(examResponse.exam);
      setReadiness(readinessResponse.readiness);
    } catch (loadError) { if (version === loadVersion.current && currentExamId.current === examId) setError(formatError(loadError)); }
    finally { if (version === loadVersion.current && currentExamId.current === examId) setLoading(false); }
  }, [examId]);

  useEffect(() => {
    setExam(undefined); setReadiness(undefined); setLoading(true); setWorking(false);
    void load();
    return () => { loadVersion.current++; };
  }, [load]);

  const failedChecks = readiness?.checks.filter((check) => !check.passed) ?? [];

  async function confirm() {
    if (exam?.id !== currentExamId.current) return;
    const requestExamId = examId;
    setWorking(true);
    try {
      const response = await confirmExamReadiness(examId);
      if (currentExamId.current !== requestExamId) return;
      setReadiness(response.readiness);
      await load();
      onExamChanged?.();
      message.success("开考准备已确认");
    } catch (confirmError) { if (currentExamId.current === requestExamId) message.error(formatError(confirmError)); }
    finally { if (currentExamId.current === requestExamId) setWorking(false); }
  }

  function confirmStart() {
    if (exam?.id !== currentExamId.current) return;
    const requestExamId = examId;
    modal.confirm({ title: "开始导入答卷", content: "开始后，考试将进入答卷导入阶段。请确认试卷、答案和答题卡模板已完成最终核对。", okText: "开始导入", cancelText: "取消", onOk: async () => {
      if (currentExamId.current !== requestExamId) return;
      setWorking(true);
      try {
        await startExamCollection(examId);
        if (currentExamId.current !== requestExamId) return;
        message.success("考试已进入答卷导入阶段");
        await load();
        onExamChanged?.();
      } catch (startError) { if (currentExamId.current === requestExamId) message.error(formatError(startError)); }
      finally { if (currentExamId.current === requestExamId) setWorking(false); }
    }});
  }

  if ((loading && !readiness) || (exam && exam.id !== examId)) return <LoadingState label="正在执行开考准备检查" />;
  if (error && !readiness) return <ErrorState message={error} onRetry={() => void load()} />;
  if (!readiness || !exam) return null;

  return (
    <div className="preparation-page readiness-page exam-setup-form-page">
      <section className="preparation-heading"><div><h2>准备检查与确认</h2><p>检查当前考试的准备情况；需要处理的项目可以直接跳转到对应任务。</p></div><Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>重新检查</Button></section>
      {failedChecks.length ? <ul className="exam-setup-inline-issues exam-readiness-issues">{failedChecks.map((check) => <li key={check.code}>
        <div><strong>{getSafeUserText(check.label, "需要完善")}</strong><span>{getSafeUserText(check.message, "请完成相关设置")}</span></div>
        <Button type="link" onClick={() => onNavigate(readinessCheckRoute(examId, check.section))}>去处理 <ArrowRight size={15} /></Button>
      </li>)}</ul> : null}

      {readiness.ready && !readiness.confirmed && exam.status !== "collecting" ? <div className="exam-setup-actions"><Button type="primary" icon={<ClipboardCheck size={16} />} disabled={!canManage} loading={working} onClick={() => void confirm()}>确认准备完成</Button></div> : null}
      {readiness.confirmed && exam.status === "ready" ? <div className="exam-setup-actions"><Button type="primary" icon={<Play size={16} />} disabled={!canManage} loading={working} onClick={confirmStart}>开始导入答卷</Button></div> : null}

      {(readiness.advisories ?? []).map((advisory) => (
        <Alert
          key={advisory.code}
          className="readiness-advisory"
          type={advisory.severity === "warning" ? "warning" : "info"}
          showIcon
          message={<span>{advisory.label}<small>提醒项，不阻断开考</small></span>}
          description={
            <>
              <p>{advisory.message}</p>
              {advisory.questions.length ? (
                <ul>
                  {advisory.questions.map((question) => (
                    <li key={question.question_id}><strong>{question.question_no}</strong><span>{questionTypeLabels[question.question_type] ?? question.question_type}</span><span>{question.score.toFixed(2)} 分</span></li>
                  ))}
                </ul>
              ) : null}
            </>
          }
          action={<Button size="small" onClick={() => onNavigate(`/exams/${encodeURIComponent(examId)}/${advisory.section}`)}>查看题目</Button>}
        />
      ))}

      {readiness.ready && !readiness.confirmed ? <Alert type="info" showIcon message="已满足准备确认条件" description="请由考试负责人确认准备完成。确认后若修改题目、答案、模板或学生范围，系统会自动撤销本次确认。" /> : null}
      {readiness.confirmed && exam.status === "ready" ? <Alert type="success" showIcon message="准备已确认" description="可以开始导入学生答卷。" /> : null}
      {exam.status === "collecting" ? <Alert type="success" showIcon message="考试已进入答卷导入阶段" description="后续答卷导入与页面处理将在答卷导入工作区完成。" /> : null}
    </div>
  );
}

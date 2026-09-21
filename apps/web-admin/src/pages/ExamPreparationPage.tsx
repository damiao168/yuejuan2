import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Checkbox, Space } from "antd";
import { ArrowRight, ClipboardCheck, Play, RefreshCw, Save } from "lucide-react";
import { ApiClientError, getSafeUserText, getUserErrorMessage } from "../api/client";
import { confirmExamReadiness, getExamReadiness, startExamCollection, type ExamReadiness } from "../api/configuration";
import { getExam, refreshExamCandidates, updateExam, type Exam } from "../api/exams";
import { listClasses, listGrades, type Grade, type SchoolClass } from "../api/org";
import { ErrorState, LoadingState } from "../components/PageState";

const readinessSectionRoutes: Record<string, string> = {
  students: "students",
  paper: "paper",
  questions: "questions",
  template: "template"
};

export function readinessCheckRoute(examId: string, section: string) {
  const routeSection = readinessSectionRoutes[section] ?? "settings";
  return `/exams/${encodeURIComponent(examId)}/${routeSection}`;
}

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error(`开考准备操作失败：${error.status} ${error.code}`, error);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

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

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const [examResponse, classResponse, gradeResponse] = await Promise.all([getExam(examId), listClasses(), listGrades()]);
      setExam(examResponse.exam);
      setClasses(classResponse.classes.filter((item) => item.school_id === examResponse.exam.school_id));
      setGrades(gradeResponse.grades.filter((item) => item.school_id === examResponse.exam.school_id));
      setSelected(examResponse.exam.class_ids);
    } catch (loadError) { setError(formatError(loadError)); } finally { setLoading(false); }
  }, [examId]);

  useEffect(() => { void load(); }, [load]);

  const gradeById = useMemo(() => new Map(grades.map((item) => [item.id, item])), [grades]);
  const changed = exam ? [...selected].sort().join(",") !== [...exam.class_ids].sort().join(",") : false;

  async function save() {
    if (!exam || !selected.length) { message.error("至少选择一个班级"); return; }
    setSaving(true);
    try {
      const response = await updateExam(exam.id, { class_ids: selected, expected_revision: exam.revision });
      setExam(response.exam);
      onExamChanged?.();
      message.success("学生范围已保存；开考准备需要重新确认。");
    } catch (saveError) { message.error(formatError(saveError)); } finally { setSaving(false); }
  }

  async function syncCandidates() {
	if (!exam) return;
	setSyncing(true);
	try {
	  const { candidate_refresh: result } = await refreshExamCandidates(exam.id);
	  const changes = [result.added_count ? `新增 ${result.added_count} 人` : "", result.removed_count ? `移除 ${result.removed_count} 人` : ""].filter(Boolean).join("，");
	  message.success(changes ? `学生名单已同步：${changes}，当前 ${result.after_count} 人` : `学生名单已是最新，共 ${result.after_count} 人`);
	} catch (syncError) { message.error(formatError(syncError)); } finally { setSyncing(false); }
  }

  if (loading) return <LoadingState label="正在加载学生范围" />;
  if (error) return <ErrorState message={error} onRetry={() => void load()} />;

  return (
    <div className="preparation-page">
      <section className="preparation-heading"><div><h2>学生范围</h2><p>选择参加本场考试的班级；确认准备完成时，系统将按当前在读学籍生成最终冻结名单。</p></div><Space><Button icon={<RefreshCw size={16} />} disabled={!canManage || changed || !exam || !["draft", "configured"].includes(exam.status)} loading={syncing} onClick={() => void syncCandidates()}>同步学生名单</Button><Button type="primary" icon={<Save size={16} />} disabled={!canManage || !changed || !selected.length} loading={saving} onClick={() => void save()}>保存范围</Button></Space></section>
      {!classes.length ? <Alert type="warning" showIcon message="当前学校还没有班级" description="请先在组织与用户中创建年级、班级并导入学生。" /> : (
        <div className="class-scope-list">
          {grades.map((grade) => {
            const gradeClasses = classes.filter((item) => item.grade_id === grade.id);
            if (!gradeClasses.length) return null;
            return <section key={grade.id}><div><strong>{grade.name}</strong><span>{grade.academic_year}</span></div><Checkbox.Group value={selected} onChange={(values) => setSelected(values as string[])} disabled={!canManage}>{gradeClasses.map((item) => <Checkbox key={item.id} value={item.id}><span>{item.name}</span><small>{item.code}</small></Checkbox>)}</Checkbox.Group></section>;
          })}
          {classes.filter((item) => !gradeById.has(item.grade_id)).length ? <section><div><strong>未分组班级</strong></div><Checkbox.Group value={selected} onChange={(values) => setSelected(values as string[])} disabled={!canManage}>{classes.filter((item) => !gradeById.has(item.grade_id)).map((item) => <Checkbox key={item.id} value={item.id}>{item.name}</Checkbox>)}</Checkbox.Group></section> : null}
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

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const [examResponse, readinessResponse] = await Promise.all([getExam(examId), getExamReadiness(examId)]);
      setExam(examResponse.exam);
      setReadiness(readinessResponse.readiness);
    } catch (loadError) { setError(formatError(loadError)); } finally { setLoading(false); }
  }, [examId]);

  useEffect(() => { void load(); }, [load]);

  const setupQuestions = [
    { key: "students", label: "学生范围", sections: ["students"], route: "students", action: "设置范围" },
    { key: "questions", label: "小题与分值", sections: ["questions"], route: "questions", action: "核对题目" },
    { key: "materials", label: "分学科考试资料", sections: ["paper"], route: "paper", action: "上传资料" },
    { key: "answer-sheets", label: "答题卡导入", sections: ["template"], route: exam?.status === "collecting" ? "capture" : "template", action: exam?.status === "collecting" ? "导入答题卡" : "设置答题卡" }
  ].map((question) => {
    const checks = readiness?.checks.filter((check) => question.sections.includes(check.section)) ?? [];
    return { ...question, checks };
  });

  async function confirm() {
    setWorking(true);
    try {
      const response = await confirmExamReadiness(examId);
      setReadiness(response.readiness);
      await load();
      onExamChanged?.();
      message.success("开考准备已确认");
    } catch (confirmError) { message.error(formatError(confirmError)); } finally { setWorking(false); }
  }

  function confirmStart() {
    modal.confirm({ title: "开始导入答卷", content: "开始后，考试将进入答卷导入阶段。请确认试卷、答案和答题卡模板已完成最终核对。", okText: "开始导入", cancelText: "取消", onOk: async () => {
      setWorking(true);
      try {
        await startExamCollection(examId);
        message.success("考试已进入答卷导入阶段");
        await load();
        onExamChanged?.();
      } catch (startError) { message.error(formatError(startError)); } finally { setWorking(false); }
    }});
  }

  if (loading && !readiness) return <LoadingState label="正在执行开考准备检查" />;
  if (error && !readiness) return <ErrorState message={error} onRetry={() => void load()} />;
  if (!readiness || !exam) return null;

  return (
    <div className="preparation-page readiness-page exam-setup-form-page">
      <ol className="exam-setup-questions" aria-label="考试设置问题">
        {setupQuestions.map((question, index) => {
          const issues = question.checks.filter((check) => !check.passed);
          return <li key={question.key} className={issues.length ? "has-issues" : "is-complete"}>
            <div className="exam-setup-question-content"><div className="exam-setup-question-heading"><h3>{index + 1}. {question.label}</h3><Button type="link" onClick={() => onNavigate(`/exams/${encodeURIComponent(examId)}/${question.route}`)}>{question.action} <ArrowRight size={15} /></Button></div>
              {issues.length ? <ul className="exam-setup-inline-issues">{issues.map((check) => <li key={check.code}><strong>{getSafeUserText(check.label, "需要完善")}</strong><span>{getSafeUserText(check.message, "请完成相关设置")}</span></li>)}</ul> : null}
            </div>
          </li>;
        })}
      </ol>

      {readiness.ready && !readiness.confirmed && exam.status !== "collecting" ? <div className="exam-setup-actions"><Button type="primary" icon={<ClipboardCheck size={16} />} disabled={!canManage} loading={working} onClick={() => void confirm()}>确认准备完成</Button></div> : null}
      {readiness.confirmed && exam.status === "ready" ? <div className="exam-setup-actions"><Button type="primary" icon={<Play size={16} />} disabled={!canManage} loading={working} onClick={confirmStart}>开始导入答题卡</Button></div> : null}

      {readiness.ready && !readiness.confirmed ? <Alert type="info" showIcon message="所有检查已通过" description="请由考试负责人确认准备完成。确认后若修改题目、答案、模板或学生范围，系统会自动撤销本次确认。" /> : null}
      {exam.status === "collecting" ? <Alert type="success" showIcon message="考试已进入答卷导入阶段" description="后续答卷导入与页面处理将在答卷导入工作区完成。" /> : null}
    </div>
  );
}

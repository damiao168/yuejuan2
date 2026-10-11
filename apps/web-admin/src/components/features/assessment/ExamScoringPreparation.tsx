import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, App, Button, Select, Space, Table } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { getQuestionAssessmentProfile, listQuestionArchetypes, listSubjectProfiles, updateQuestionAssessmentProfile, type AssessmentProfile, type EducationStage, type QuestionArchetype, type SubjectProfile } from "../../../api/assessment";
import type { Exam } from "../../../api/exams";
import { listClasses, listGrades } from "../../../api/org";
import { listQuestions, type Question } from "../../../api/papers";
import { normalizeAssessmentSubject } from "./assessmentSubjectScope";
import { examSubjectLabel } from "../../../constants/examStatus";
import { examEducationStage, preparationAssessmentInput, preparationScoringModeLabels } from "./assessmentPreparation";

type Row = { question: Question; saved?: AssessmentProfile };

export function ExamScoringPreparation({ exam, canManage, onChanged, onNavigate }: Readonly<{ exam: Exam; canManage: boolean; onChanged: () => void; onNavigate: (path: string) => void }>) {
  const { message, modal } = App.useApp();
  const [rows, setRows] = useState<Row[]>([]);
  const [profiles, setProfiles] = useState<SubjectProfile[]>([]);
  const [archetypes, setArchetypes] = useState<QuestionArchetype[]>([]);
  const [stage, setStage] = useState<EducationStage>();
  const [profileId, setProfileId] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const writing = useRef(false);
  const currentExam = useRef(exam.id); currentExam.current = exam.id;
  const schoolScope = JSON.stringify({ school_id: exam.school_id, class_ids: exam.class_ids, exam_session_grade_id: exam.exam_session_grade_id });
  const subject = normalizeAssessmentSubject(exam.subject);
  const editable = canManage && ["draft", "configured"].includes(exam.status);
  const choices = profiles.filter((item) => item.status !== "retired" && item.education_stage === stage && item.subject_code === subject);
  const chosen = choices.find((item) => item.id === profileId);
  const missing = rows.filter((row) => !row.saved);
  const verified = rows.filter((row) => row.saved && preparationScoringModeLabels[row.saved.scoring_policy.mode]).length;
  const proposed = missing.map((row) => ({ ...row, input: preparationAssessmentInput(row.question, chosen, archetypes, exam.grading_mode) }));
  const canApply = editable && !loading && !saving && !error && proposed.length > 0 && proposed.every((row) => row.input);

  const load = useCallback(async () => {
    const version = ++generation.current;
    setLoading(true); setError("");
    try {
      const [questions, catalog, types] = await Promise.all([listQuestions(exam.id), listSubjectProfiles(), listQuestionArchetypes()]);
      const records = await Promise.all(questions.questions.map(async (question): Promise<Row> => {
        try { return { question, saved: (await getQuestionAssessmentProfile(exam.id, question.id)).assessment_profile }; }
        catch (error_) {
          // 只有 404 表示尚未配置；权限或网络失败不能当作缺失配置继续补齐。
          if (error_ instanceof ApiClientError && error_.status === 404) {
            return { question };
          }
          throw error_;
        }
      }));
      if (generation.current !== version || currentExam.current !== exam.id) return;
      setRows(records); setProfiles(catalog.subject_profiles); setArchetypes(types.question_archetypes);
    } catch (error_) { if (generation.current === version) setError(getUserErrorMessage(error_, "评分方式读取失败，请重试")); }
    finally { if (generation.current === version) setLoading(false); }
  }, [exam.id]);

  useEffect(() => {
    setRows([]); setProfiles([]); setArchetypes([]);
    void load();
    return () => { generation.current++; };
  }, [load]);
  useEffect(() => {
    let active = true;
    setStage(undefined); setProfileId("");
    // 组织数据只用于建议学段；无组织读取权限时仍可明确选择，不能把失败冒充初中默认值。
    const scope = JSON.parse(schoolScope) as Pick<Exam, "school_id" | "class_ids" | "exam_session_grade_id">;
    void Promise.all([listGrades(), listClasses()]).then(([grades, classes]) => {
      // 延迟返回的建议不能覆盖教师已经明确选择的学段。
      if (active) setStage((previous) => previous ?? examEducationStage(scope, grades.grades, classes.classes));
    }).catch(() => undefined);
    return () => { active = false; };
  }, [schoolScope]);

  function apply() {
    if (!canApply || writing.current) return;
    const requestExamId = exam.id;
    const plan = proposed;
    modal.confirm({ title: `保存 ${plan.length} 道题的评分方式？`, content: "仅补齐下表中未保存的配置，已有评分方式会保留。自动判分仍须答案、评分规则和作答区域满足要求；系统建议分数仍须教师确认。保存后重新核对考试准备。",
      okText: "确认并保存评分方式", cancelText: "返回核对", onOk: async () => {
        if (writing.current || currentExam.current !== requestExamId) return;
        writing.current = true; setSaving(true);
        let completed = 0;
        try {
          // 逐题保存不具备整卷事务；中途失败保留已成功的配置，重试只补剩余项。
          for (const row of plan) {
            if (currentExam.current !== requestExamId) break;
            if (!row.input) throw new Error("评分配置尚未完成，请返回核对");
            await updateQuestionAssessmentProfile(requestExamId, row.question.id, row.input); completed++;
          }
          if (currentExam.current === requestExamId) message.success(`已保存 ${completed} 道题的评分方式，请重新核对后冻结。`);
        } catch (error_) { if (currentExam.current === requestExamId) message.error(`已保存 ${completed} 道题；其余未完成。${getUserErrorMessage(error_, "请刷新后重试")}`); }
        finally { writing.current = false; if (currentExam.current === requestExamId) { setSaving(false); await load(); onChanged(); } }
      } });
  }

  return <section className="workspace-section"><div className="section-head"><div><h2>逐题评分方式（开考前必填）</h2><p>已核实 {verified} / {rows.length} 题。先核对谁来判分，再冻结本场考试。</p></div><Button disabled={saving} loading={loading} onClick={() => void load()}>刷新评分方式</Button></div>
    {error ? <Alert type="error" showIcon message={error} /> : null}
    {missing.length > 0 && !error && <Alert type="warning" showIcon message={`${missing.length} 道题尚未保存评分方式`} description={editable ? "选择本场学段和学科模板，核对下表建议后，可一次补齐未配置的题目；也可进入题目页逐题调整。" : "请联系本场考试负责人或学校管理员保存评分方式，再重新核对准备。"} />}
    {editable && missing.length > 0 && <Space wrap><span>本场学段</span><Select aria-label="本场评分学段" placeholder="请选择本场学段" value={stage} disabled={saving} style={{ width: 150 }} options={[{ label: "初中", value: "junior" }, { label: "高中", value: "senior" }]} onChange={(value: EducationStage) => { setStage(value); setProfileId(""); }} />
      <span>学科评分模板</span><Select aria-label="本场评分模板" placeholder={choices.length ? "请选择模板" : "该学段学科暂无模板，请逐题配置"} value={profileId || undefined} disabled={saving || !stage} style={{ width: 240 }} options={choices.map((item) => ({ value: item.id, label: `${stage === "senior" ? "高中" : "初中"}${examSubjectLabel(exam.subject)} · 第 ${item.version} 版` }))} onChange={setProfileId} />
      <Button type="primary" disabled={!canApply} loading={saving} onClick={apply}>核对并补齐评分方式</Button></Space>}
    <Table<Row> loading={loading} rowKey={(row) => row.question.id} dataSource={rows} size="small" pagination={{ pageSize: 10 }} columns={[
      { title: "题号", render: (_, row) => `第 ${row.question.question_no} 题` },
      { title: "评分方式", render: (_, row) => row.saved ? preparationScoringModeLabels[row.saved.scoring_policy.mode] ?? "原配置无法核实，请逐题检查" : preparationScoringModeLabels[preparationAssessmentInput(row.question, chosen, archetypes, exam.grading_mode)?.scoring_policy.mode ?? ""] ?? "待选择模板或逐题配置" },
      { title: "保存状态", render: (_, row) => row.saved ? "已保存" : "未保存，建议待确认" }
    ]} />
    <Button disabled={saving} onClick={() => onNavigate(`/exams/${encodeURIComponent(exam.id)}/questions`)}>前往题目页逐题配置</Button>
  </section>;
}

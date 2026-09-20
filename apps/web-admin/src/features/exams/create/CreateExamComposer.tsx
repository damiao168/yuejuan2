import { useMemo, useState } from "react";
import { Button, Checkbox, Drawer, Input, InputNumber, Radio, Select } from "antd";
import { AnimatePresence, motion } from "framer-motion";
import { Check, ChevronDown, FileText, Gauge, LayoutTemplate, Sparkles, Upload, Zap } from "lucide-react";
import type { ExamTemplate } from "../../../api/examTemplates";
import type { Grade, School, SchoolClass } from "../../../api/org";
import { examTypeOptions } from "../../../constants/examCatalog";
import { examSubjectLabel, examSubjectOptions } from "../../../constants/examStatus";
import { createSubjectDraft, subjectDraftFromTemplate } from "./createExamDraft";
import { createExamFieldId, type CreateExamValidationIssue } from "./createExamValidation";
import { examTypeLabel, gradingLabel, gradingPresentation, publishPolicyOptions } from "./presentation";
import type { CreateExamDraft, ExamCreationMode, GradingChoice, SubjectExamDraft } from "./types";
import "./exam-create.css";

const creationModes: Array<{
  value: ExamCreationMode;
  title: string;
  description: string;
  icon: typeof Upload;
  recommended?: boolean;
}> = [
  { value: "materials", title: "从试卷资料创建", description: "已有 Word、PDF、图片或文本，创建后直接上传识别", icon: Upload, recommended: true },
  { value: "quick", title: "快速创建", description: "先建立考试与科目，稍后继续完善资料和设置", icon: Zap },
  { value: "template", title: "使用考试方案", description: "套用学校或系统方案中的科目和试卷结构", icon: LayoutTemplate }
];

const sectionItems = [
  { id: "basic", label: "基本信息" },
  { id: "scope", label: "参考范围" },
  { id: "subjects", label: "考试科目" },
  { id: "advanced", label: "高级设置" }
];

function issueFor(issues: CreateExamValidationIssue[], field: string) {
  return issues.find((issue) => issue.field === field)?.message;
}

function FieldError({ message }: { message?: string }) {
  return message ? <span className="exam-composer-field-error" role="alert">{message}</span> : null;
}

function ClassChips({ classes, selectedIds, onChange, compact = false }: {
  classes: SchoolClass[];
  selectedIds: string[];
  onChange: (classIds: string[]) => void;
  compact?: boolean;
}) {
  const selected = new Set(selectedIds);
  return <div className={`exam-composer-chip-grid${compact ? " is-compact" : ""}`}>
    {classes.map((schoolClass) => {
      const active = selected.has(schoolClass.id);
      return <button
        type="button"
        key={schoolClass.id}
        className={active ? "exam-class-chip is-selected" : "exam-class-chip"}
        aria-pressed={active}
        onClick={() => onChange(active ? selectedIds.filter((id) => id !== schoolClass.id) : [...selectedIds, schoolClass.id])}
      >
        <span className="exam-chip-check" aria-hidden="true">{active ? <Check size={14} strokeWidth={2.4} /> : null}</span>
        <span>{schoolClass.name}</span>
      </button>;
    })}
  </div>;
}

function SubjectCard({ subject, classes, issues, onChange }: {
  subject: SubjectExamDraft;
  classes: SchoolClass[];
  issues: CreateExamValidationIssue[];
  onChange: (subject: SubjectExamDraft) => void;
}) {
  const prefix = `subject.${subject.subject}`;
  const scoreError = issueFor(issues, `${prefix}.totalScore`);
  const durationError = issueFor(issues, `${prefix}.durationMinutes`);
  const classError = issueFor(issues, `${prefix}.classIds`);
  const customScope = subject.candidateRule === "subject_selected_classes";
  return <motion.article layout className="exam-subject-card" data-exam-field={prefix}>
    <header>
      <div><span className="exam-subject-mark">{examSubjectLabel(subject.subject).slice(0, 1)}</span><strong>{examSubjectLabel(subject.subject)}</strong></div>
      {subject.sections.length ? <span className="exam-template-applied"><Check size={13} /> 已套用试卷结构</span> : <span>试卷待上传</span>}
    </header>
    <div className="exam-subject-fields">
      <label className="exam-composer-field" data-exam-field={`${prefix}.totalScore`} htmlFor={createExamFieldId(`${prefix}.totalScore`)}>
        <span>满分</span>
        <InputNumber id={createExamFieldId(`${prefix}.totalScore`)} min={1} precision={0} value={subject.totalScore} status={scoreError ? "error" : undefined} aria-invalid={Boolean(scoreError)} addonAfter="分" onChange={(value) => onChange({ ...subject, totalScore: Number(value ?? 0) })} />
        <FieldError message={scoreError} />
      </label>
      <label className="exam-composer-field" data-exam-field={`${prefix}.durationMinutes`} htmlFor={createExamFieldId(`${prefix}.durationMinutes`)}>
        <span>考试时间</span>
        <InputNumber id={createExamFieldId(`${prefix}.durationMinutes`)} min={1} precision={0} value={subject.durationMinutes} status={durationError ? "error" : undefined} aria-invalid={Boolean(durationError)} addonAfter="分钟" onChange={(value) => onChange({ ...subject, durationMinutes: Number(value ?? 0) })} />
        <FieldError message={durationError} />
      </label>
    </div>
    <button type="button" className="exam-subject-scope-toggle" aria-expanded={customScope} onClick={() => onChange({ ...subject, candidateRule: customScope ? "all_selected_classes" : "subject_selected_classes", classIds: customScope ? [] : subject.classIds })}>
      {customScope ? "使用统一参考范围" : "单独设置该科参考班级"}
    </button>
    <AnimatePresence initial={false}>
      {customScope ? <motion.div className="exam-subject-scope" initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: "auto" }} exit={{ opacity: 0, height: 0 }} transition={{ duration: .18 }} data-exam-field={`${prefix}.classIds`}>
        <ClassChips classes={classes} selectedIds={subject.classIds} onChange={(classIds) => onChange({ ...subject, classIds })} compact />
        <FieldError message={classError} />
      </motion.div> : null}
    </AnimatePresence>
  </motion.article>;
}

export function CreateExamComposer({ draft, schools, grades, classes, templates, savedAt, submitting, issues, onChange, onSubmit }: {
  draft: CreateExamDraft;
  schools: School[];
  grades: Grade[];
  classes: SchoolClass[];
  templates: ExamTemplate[];
  savedAt: string;
  submitting: boolean;
  issues: CreateExamValidationIssue[];
  onChange: (patch: Partial<CreateExamDraft>) => void;
  onSubmit: () => void;
}) {
  const [templateOpen, setTemplateOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const school = schools.find((item) => item.id === draft.schoolId);
  const grade = grades.find((item) => item.id === draft.gradeId);
  const availableClasses = classes.filter((item) => item.grade_id === draft.gradeId && item.status === "active");
  const selectedClasses = availableClasses.filter((item) => draft.classIds.includes(item.id));
  const allClassesSelected = availableClasses.length > 0 && availableClasses.every((item) => draft.classIds.includes(item.id));
  const visibleTemplates = useMemo(() => templates.filter((template) => !grade || template.education_stage === grade.education_stage), [grade, templates]);
  const selectedTemplate = templates.find((template) => template.id === draft.templateId);
  const mode = creationModes.find((item) => item.value === draft.creationMode) ?? creationModes[0];
  const updateSubject = (next: SubjectExamDraft) => onChange({ subjects: draft.subjects.map((subject) => subject.subject === next.subject ? next : subject) });
  const changeSubjects = (subjects: string[]) => onChange({
    subjects: subjects.map((subject) => draft.subjects.find((item) => item.subject === subject) ?? createSubjectDraft(subject))
  });
  const selectMode = (creationMode: ExamCreationMode) => {
    if (creationMode === "template") {
      onChange({ creationMode });
      setTemplateOpen(true);
      return;
    }
    onChange({ creationMode, templateId: "", subjects: draft.subjects.map((subject) => ({ ...subject, sections: [] })) });
  };
  const selectTemplate = (template: ExamTemplate) => {
    onChange({ creationMode: "template", templateId: template.id, subjects: template.subjects.map(subjectDraftFromTemplate) });
    setTemplateOpen(false);
  };
  const scrollTo = (id: string) => document.getElementById(`exam-section-${id}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
  const cta = draft.creationMode === "materials" ? "创建考试并上传试卷" : draft.creationMode === "template" ? "使用方案创建考试" : "创建考试";

  return <div className="exam-composer">
    <div className="exam-composer-inner">
      <header className="exam-composer-heading">
        <div><h1>新建考试</h1><p>先创建考试，试卷、答案和评分标准可在下一步继续配置。</p></div>
        <span className="exam-draft-status" role="status"><Check size={14} /> {savedAt ? "已自动保存" : "正在保存草稿"}<small>{savedAt ? `保存在当前浏览器 · ${savedAt}` : ""}</small></span>
      </header>

      <section className="exam-composer-mode" aria-labelledby="creation-mode-title">
        <div className="exam-composer-section-heading"><div><span className="exam-section-kicker">创建方式</span><h2 id="creation-mode-title">你准备怎样开始？</h2></div>{selectedTemplate ? <button type="button" onClick={() => setTemplateOpen(true)}>当前方案：{selectedTemplate.name}</button> : null}</div>
        <div className="exam-mode-grid">
          {creationModes.map((item) => {
            const Icon = item.icon;
            const selected = draft.creationMode === item.value;
            return <button type="button" key={item.value} className={`exam-mode-option${selected ? " is-selected" : ""}`} aria-pressed={selected} onClick={() => selectMode(item.value)}>
              <span className="exam-mode-icon"><Icon size={20} /></span>
              <span><strong>{item.title}{item.recommended ? <em>推荐</em> : null}</strong><small>{item.description}</small></span>
              <span className="exam-mode-radio">{selected ? <Check size={14} /> : null}</span>
            </button>;
          })}
        </div>
        <FieldError message={issueFor(issues, "templateId")} />
      </section>

      <div className="exam-composer-layout">
        <nav className="exam-composer-nav" aria-label="考试设置章节">
          <span>考试设置</span>
          {sectionItems.map((item, index) => <button type="button" key={item.id} onClick={() => scrollTo(item.id)}><span>{index + 1}</span>{item.label}</button>)}
        </nav>

        <main className="exam-composer-main">
          <section className="exam-section" id="exam-section-basic">
            <header><h2>基本信息</h2><p>名称与类型用于考试列表、阅卷和成绩发布。</p></header>
            <label className="exam-composer-field" data-exam-field="name" htmlFor={createExamFieldId("name")}>
              <span>考试名称 <em>*</em></span>
              <Input id={createExamFieldId("name")} value={draft.name} maxLength={100} placeholder="例如：2026—2027学年高二第一学期期中考试" status={issueFor(issues, "name") ? "error" : undefined} aria-invalid={Boolean(issueFor(issues, "name"))} onChange={(event) => onChange({ name: event.target.value })} />
              <FieldError message={issueFor(issues, "name")} />
            </label>
            <div className="exam-composer-field-row">
              <label className="exam-composer-field" data-exam-field="examType" htmlFor={createExamFieldId("examType")}>
                <span>考试类型 <em>*</em></span>
                <Select id={createExamFieldId("examType")} value={draft.examType || undefined} placeholder="选择考试类型" status={issueFor(issues, "examType") ? "error" : undefined} options={examTypeOptions} onChange={(examType) => onChange({ examType })} />
                <FieldError message={issueFor(issues, "examType")} />
              </label>
              <label className="exam-composer-field" data-exam-field="gradeId" htmlFor={createExamFieldId("gradeId")}>
                <span>年级 <em>*</em></span>
                <Select id={createExamFieldId("gradeId")} value={draft.gradeId || undefined} placeholder="选择年级" status={issueFor(issues, "gradeId") ? "error" : undefined} options={grades.map((item) => ({ label: `${item.name} · ${item.academic_year}`, value: item.id }))} onChange={(gradeId) => onChange({ gradeId, classIds: [], subjects: draft.subjects.map((subject) => ({ ...subject, classIds: [] })) })} />
                <FieldError message={issueFor(issues, "gradeId")} />
              </label>
            </div>
            <div className="exam-composer-school" data-exam-field="schoolId">
              <span>所属学校</span>
              {schools.length > 1 ? <Select id={createExamFieldId("schoolId")} value={draft.schoolId || undefined} status={issueFor(issues, "schoolId") ? "error" : undefined} options={schools.map((item) => ({ label: item.name, value: item.id }))} onChange={(schoolId) => onChange({ schoolId, gradeId: "", templateId: "", classIds: [], subjects: [] })} /> : <strong>{school?.name ?? "尚未选择"}</strong>}
              <FieldError message={issueFor(issues, "schoolId")} />
            </div>
          </section>

          <section className="exam-section" id="exam-section-scope" data-exam-field="classIds">
            <header className="exam-section-toolbar"><div><h2>参考范围</h2><p>选择本场考试统一覆盖的班级。</p></div><Button type="link" disabled={!availableClasses.length} onClick={() => onChange({ classIds: allClassesSelected ? [] : availableClasses.map((item) => item.id) })}>{allClassesSelected ? "取消全选" : "全选本年级"}</Button></header>
            {availableClasses.length ? <ClassChips classes={availableClasses} selectedIds={draft.classIds} onChange={(classIds) => onChange({ classIds })} /> : <div className="exam-composer-empty">选择年级后，可在这里确定参考班级。</div>}
            <p className="exam-selection-count">已选择 <strong>{draft.classIds.length}</strong> 个班级</p>
            <FieldError message={issueFor(issues, "classIds")} />
          </section>

          <section className="exam-section" id="exam-section-subjects" data-exam-field="subjects">
            <header><h2>考试科目</h2><p>这里只设置满分和时长，题型与评分信息将在上传资料后识别。</p></header>
            <div className="exam-subject-picker" role="group" aria-label="选择考试科目">
              {examSubjectOptions.map((item) => {
                const selected = draft.subjects.some((subject) => subject.subject === item.value);
                return <button type="button" key={item.value} className={selected ? "is-selected" : ""} aria-pressed={selected} onClick={() => changeSubjects(selected ? draft.subjects.map((subject) => subject.subject).filter((subject) => subject !== item.value) : [...draft.subjects.map((subject) => subject.subject), item.value])}>{selected ? <Check size={14} /> : null}{item.label}</button>;
              })}
            </div>
            <FieldError message={issueFor(issues, "subjects")} />
            <motion.div layout className="exam-subject-list">
              <AnimatePresence initial={false}>
                {draft.subjects.map((subject) => <SubjectCard key={subject.subject} subject={subject} classes={availableClasses} issues={issues} onChange={updateSubject} />)}
              </AnimatePresence>
            </motion.div>
          </section>

          <section className="exam-section" id="exam-section-advanced">
            <button type="button" className="exam-advanced-trigger" aria-expanded={advancedOpen} onClick={() => setAdvancedOpen((open) => !open)}>
              <span><Gauge size={18} /><span><strong>高级设置</strong><small>阅卷：{gradingLabel(draft.gradingMode)} · 成绩：管理员确认后发布</small></span></span>
              <ChevronDown size={18} className={advancedOpen ? "is-open" : ""} />
            </button>
            <AnimatePresence initial={false}>
              {advancedOpen ? <motion.div className="exam-advanced-content" initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: "auto" }} exit={{ opacity: 0, height: 0 }} transition={{ duration: .2 }}>
                <div className="exam-advanced-group" data-exam-field="gradingMode"><span>阅卷方式</span><Radio.Group value={draft.gradingMode} onChange={(event) => onChange({ gradingMode: event.target.value as GradingChoice })}>
                  {(["ai_assisted", "auto_objective_only", "human_review_required"] as GradingChoice[]).map((value) => <Radio key={value} value={value}><span className="exam-radio-copy"><strong>{gradingPresentation[value].title}{value === "ai_assisted" ? <em>推荐</em> : null}</strong><small>{gradingPresentation[value].description}</small></span></Radio>)}
                </Radio.Group></div>
                <div className="exam-advanced-inline"><label className="exam-composer-field"><span>更多阅卷方式</span><Radio.Group value={draft.gradingMode} onChange={(event) => onChange({ gradingMode: event.target.value as GradingChoice })}><Radio value="double_mark">双评</Radio><Radio value="blind_double_mark">盲双评</Radio></Radio.Group></label></div>
                <label className="exam-composer-field"><span>成绩发布</span><Select value={draft.publishPolicy} options={publishPolicyOptions} onChange={(publishPolicy) => onChange({ publishPolicy })} /></label>
                <Checkbox checked={draft.appealEnabled} onChange={(event) => onChange({ appealEnabled: event.target.checked })}>成绩发布后允许申诉</Checkbox>
              </motion.div> : null}
            </AnimatePresence>
          </section>
        </main>

        <aside className="exam-composer-summary">
          <div className="exam-summary-icon"><FileText size={20} /></div>
          <span className="exam-section-kicker">创建摘要</span>
          <h2>{draft.name.trim() || "尚未命名的考试"}</h2>
          <p>{draft.examType ? examTypeLabel(draft.examType) : "请选择考试类型"}</p>
          <dl>
            <div><dt>范围</dt><dd>{grade?.name ?? "未选择年级"}<small>{selectedClasses.length ? `${selectedClasses.length} 个班级` : "未选择班级"}</small></dd></div>
            <div><dt>科目</dt><dd>{draft.subjects.length ? draft.subjects.map((subject) => <span key={subject.subject}>{examSubjectLabel(subject.subject)} <small>{subject.totalScore} 分</small></span>) : <small>尚未选择科目</small>}</dd></div>
            <div><dt>阅卷</dt><dd>{gradingLabel(draft.gradingMode)}</dd></div>
          </dl>
          <div className="exam-summary-next"><Sparkles size={17} /><div><strong>创建后下一步</strong><span>{draft.creationMode === "materials" ? "上传试卷、答案和评分标准，系统将自动识别资料。" : "进入考试设置，继续完善资料与开考准备。"}</span></div></div>
          <Button type="primary" size="large" block loading={submitting} onClick={onSubmit}>{cta}</Button>
          <small className="exam-summary-mode">{mode.title} · 所有设置创建后仍可修改</small>
        </aside>
      </div>
    </div>

    <div className="exam-composer-mobile-action"><span>已选 {draft.subjects.length} 科 · {draft.classIds.length} 个班级</span><Button type="primary" loading={submitting} onClick={onSubmit}>{draft.creationMode === "materials" ? "创建并上传" : "创建考试"}</Button></div>

    <Drawer className="exam-template-drawer" title="选择考试方案" placement="right" width={480} open={templateOpen} onClose={() => setTemplateOpen(false)}>
      <p className="exam-template-drawer-intro">方案会带入科目、满分、时长和已有试卷结构，选中后仍可调整。</p>
      <div className="exam-template-drawer-list">
        {visibleTemplates.map((template) => <button type="button" key={template.id} className={draft.templateId === template.id ? "is-selected" : ""} onClick={() => selectTemplate(template)}>
          <span className="exam-template-drawer-icon"><LayoutTemplate size={18} /></span>
          <span><strong>{template.name}{template.recommended ? <em>推荐</em> : null}</strong><small>{template.description}</small><span>{template.subjects.map((subject) => examSubjectLabel(subject.subject)).join(" / ")} · 版本 {template.version}</span></span>
          {draft.templateId === template.id ? <Check size={18} /> : null}
        </button>)}
        {!visibleTemplates.length ? <div className="exam-composer-empty">当前年级暂无可用考试方案。</div> : null}
      </div>
    </Drawer>
  </div>;
}

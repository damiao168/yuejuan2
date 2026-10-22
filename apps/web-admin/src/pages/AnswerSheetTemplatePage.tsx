import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Upload, type UploadProps } from "antd";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { getExamTemplateBinding, listAnswerSheetTemplates, type AnswerSheetTemplate } from "../api/configuration";
import { listPapers, listQuestions, type PaperVersion, type Question } from "../api/papers";
import { getExam } from "../api/exams";
import { ErrorState, LoadingState } from "../components/PageState";
import { useOMRCalibration } from "../features/answer-sheet-template/calibration/useOMRCalibration";
import { useTemplateBinding } from "../features/answer-sheet-template/binding/useTemplateBinding";
import { useStudentPrinting } from "../features/answer-sheet-template/printing/useStudentPrinting";
import { useTemplatePreview } from "../features/answer-sheet-template/editor/useTemplatePreview";
import { useTemplateEditor } from "../features/answer-sheet-template/editor/useTemplateEditor";
import { useAnswerSheetSourceUpload } from "../features/answer-sheet-template/editor/useAnswerSheetSourceUpload";
import { useAnswerSheetSourceDelete } from "../features/answer-sheet-template/editor/useAnswerSheetSourceDelete";
import { EmptyAnswerSheetSources, SourceUploadStatus, TemplateToolbar, TemplateWorkspace } from "../features/answer-sheet-template/view/TemplateWorkspaceView";
import { CalibrationActionModal, CalibrationDrawer } from "../features/answer-sheet-template/view/OMRCalibrationView";
import { StudentPrintModal } from "../features/answer-sheet-template/view/StudentPrintingView";
import type { TemplatePageViewState } from "../features/answer-sheet-template/view/templateViewTypes";
import { useUnsavedChangesGuard } from "../router/useUnsavedChangesGuard";
import { useTemplateLibrary } from "../features/answer-sheet-template/library/useTemplateLibrary";
import { TemplateLibraryPanel } from "../features/answer-sheet-template/library/TemplateLibraryPanel";
import { createTemplatePageAnalyzer } from "../features/answer-sheet-template/editor/analyzeTemplatePage";

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error("请求失败", error.status, error.code, error.message);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

export function AnswerSheetTemplatePage({ examId, canManage, canCalibrate = false, onExamChanged, scopeKey = examId }: Readonly<{ examId: string; canManage: boolean; canCalibrate?: boolean; onExamChanged?: () => void; scopeKey?: string }>) {
  const { message, modal } = App.useApp();
  const dataRequestRef = useRef(0);
  const [papers, setPapers] = useState<PaperVersion[]>([]);
  const [questions, setQuestions] = useState<Question[]>([]);
  const [templates, setTemplates] = useState<AnswerSheetTemplate[]>([]);
  const [selectedPaperId, setSelectedPaperId] = useState("");
  const [selectedTemplateId, setSelectedTemplateId] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 换考试或来源后关闭旧设置；草稿仍由原编辑器持有。
  useEffect(() => { setSettingsOpen(false); }, [scopeKey, examId, selectedPaperId, selectedTemplateId]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [examStatus, setExamStatus] = useState("");
  const [loadedExamId, setLoadedExamId] = useState("");
  const automaticDraftRef = useRef(new Set<string>());
  const automaticResolveRef = useRef(new Set<string>());
  const automaticScanRef = useRef(new Set<string>());
  const [libraryWorking, setLibraryWorking] = useState(false);
  const [reusedTemplateId, setReusedTemplateId] = useState("");
  const [sourceDeleting, setSourceDeleting] = useState(false);

  const selectedPaper = useMemo(() => papers.find((item) => item.id === selectedPaperId), [papers, selectedPaperId]);
  const selectedTemplate = useMemo(() => templates.find((item) => item.id === selectedTemplateId), [templates, selectedTemplateId]);
  // 准备确认后文件与作答区域已冻结；管理员仍可查看，但不能追加或再次启用模板。
  const canEditSources = canManage && loadedExamId === examId && ["draft", "configured"].includes(examStatus);
  const binding = useTemplateBinding(examId, onExamChanged, canEditSources);
  const { setBindingState, bindingBusy } = binding;
  const printing = useStudentPrinting(selectedTemplate, canManage);
  const previewController = useTemplatePreview(selectedPaper);
  const { preview, pdfRef, pageNo, setPageNo } = previewController;
  const isTemplateDifference = selectedTemplate?.status === "locked" && selectedTemplate.layout.omr_profile?.mode === "template_difference";
  // 校准针对已锁定的差分识别模板；查看/编辑模板权限本身不代表可审批自动确认。
  const calibration = useOMRCalibration({
    template: selectedTemplate,
    enabled: Boolean(isTemplateDifference && canCalibrate),
    message,
    formatError
  });
  const loadData = useCallback(async () => {
    const requestId = ++dataRequestRef.current;
    setLoading(true);
    setError(undefined);
    try {
      const [paperResponse, legacyPaperResponse, questionResponse, templateResponse, bindingResponse, examResponse] = await Promise.all([listPapers(examId, "answer_sheet"), listPapers(examId), listQuestions(examId), listAnswerSheetTemplates(examId), getExamTemplateBinding(examId), getExam(examId)]);
      if (requestId !== dataRequestRef.current) return;
      // 新模板使用独立答题卡源文件；历史模板仍能读取原底图，不把其他试卷默认为答题卡。
      const legacySourceIds = new Set(templateResponse.templates.map(template => template.exam_paper_id));
      const sources = [...new Map([...paperResponse.papers, ...legacyPaperResponse.papers.filter(paper => legacySourceIds.has(paper.id))]
        .map(paper => [paper.id, paper])).values()];
      setPapers(sources);
      setExamStatus(examResponse.exam.status);
      setLoadedExamId(examId);
      setQuestions(questionResponse.questions);
      setTemplates(templateResponse.templates);
		setBindingState(bindingResponse);
      const latest = templateResponse.templates[0];
      setSelectedPaperId((current) =>
        sources.some((paper) => paper.id === current)
          ? current
          : latest?.exam_paper_id || sources[0]?.id || ""
      );
      setSelectedTemplateId((current) =>
        templateResponse.templates.some((template) => template.id === current)
          ? current
          : bindingResponse.binding?.template_id || latest?.id || ""
      );
    } catch (loadError) {
      if (requestId !== dataRequestRef.current) return;
      setError(formatError(loadError));
    } finally {
      if (requestId === dataRequestRef.current) setLoading(false);
    }
  }, [examId, setBindingState]);

  useEffect(() => { void loadData(); }, [loadData]);
  useEffect(() => {
    if (selectedTemplate) setSelectedPaperId(selectedTemplate.exam_paper_id);
  }, [selectedTemplate]);

  const sourceUpload = useAnswerSheetSourceUpload({ examId, identityKey: scopeKey, canManage: canEditSources, existingPapers: papers, onCreated: (paper: PaperVersion): void => {
    setPapers(current => [paper, ...current.filter(item => item.id !== paper.id)]);
    setSelectedPaperId(paper.id); setSelectedTemplateId(""); setLayout({ pages: [] });
    onExamChanged?.();
  } });
  // 上传成功会切换底图并清空布局，等待期间暂停模板修改；失败后恢复编辑并保留原草稿。
  const editor = useTemplateEditor({
    examId, canManage: canEditSources && !sourceUpload.uploading && !sourceDeleting && !libraryWorking, selectedPaper, selectedTemplate, questions, setTemplates,
    setSelectedTemplateId, onChanged: onExamChanged, onActivate: binding.activateTemplate, pdfRef, preview,
    pageNo, setPageNo,
    analyzePage: selectedTemplate && selectedPaper ? createTemplatePageAnalyzer({ templateId: selectedTemplate.id,
      revision: selectedTemplate.revision, sourceHash: selectedPaper.file.hash_sha256, questions }) : undefined
  });
  const { name, layout, setLayout, saving, suggestingRegions } = editor;
  const library = useTemplateLibrary({ examId, scopeKey, enabled: loadedExamId === examId && !loading,
    canManage, canEditSources: canEditSources && !sourceDeleting, source: selectedPaper, template: selectedTemplate, layout, name,
    onBusyChange: setLibraryWorking,
    onTemplateChanged: template => {
      setTemplates(current => current.map(item => item.id === template.id ? template : item));
      onExamChanged?.();
    },
    onInstance: instance => {
      setPapers(current => [instance.paper, ...current.filter(item => item.id !== instance.paper.id)]);
      setTemplates(current => [instance.template, ...current.filter(item => item.id !== instance.template.id)]);
      setSelectedPaperId(instance.paper.id); setSelectedTemplateId(instance.template.id);
      setReusedTemplateId(instance.template.id);
      onExamChanged?.();
    }
  });
  useEffect(() => {
    if (!selectedPaper || !canEditSources || sourceUpload.uploading || preview.sourceId !== selectedPaper.id
      || preview.loading || preview.error || selectedTemplate || saving || sourceDeleting || libraryWorking) return;
    const existing = templates.filter(item => item.exam_paper_id === selectedPaper.id && item.status !== "retired")
      .sort((left, right) => right.version_no - left.version_no)[0];
    if (existing) { setSelectedTemplateId(existing.id); return; }
    const key = `${scopeKey}:${examId}:${selectedPaper.id}`;
    const resolveKey = `${key}:${library.scopeVersion}`;
    if (library.busy) return;
    if (!automaticResolveRef.current.has(resolveKey)) {
      automaticResolveRef.current.add(resolveKey);
      void library.resolveSource();
      return;
    }
    // 请求失败或多个模板冲突时先复核；不能把“未取到结果”当作确认未知。
    if (library.resolution?.status !== "unknown") return;
    if (automaticDraftRef.current.has(key)) return;
    // 同一套来源只建一份草稿；建成后自动识别，失败后保留手动重试入口。
    automaticDraftRef.current.add(key);
    void editor.createDraft();
  }, [scopeKey, examId, selectedPaper, templates, canEditSources, sourceUpload.uploading, preview.sourceId, preview.loading, preview.error, selectedTemplate, saving, sourceDeleting, libraryWorking, editor.createDraft, library.busy, library.resolution, library.resolveSource, library.scopeVersion]);
  useEffect(() => {
    if (!selectedPaper || !selectedTemplate || selectedTemplate.status !== "draft" || !canEditSources
      || editor.readonly || saving || suggestingRegions || libraryWorking || sourceUpload.uploading || sourceDeleting
      || preview.sourceId !== selectedPaper.id || preview.loading || preview.error) return;
    const key = `${scopeKey}:${examId}:${selectedPaper.id}`;
    if (!automaticDraftRef.current.has(key) || automaticScanRef.current.has(selectedTemplate.id)) return;
    automaticScanRef.current.add(selectedTemplate.id);
    void editor.suggestQuestionRegions();
  }, [scopeKey, examId, selectedPaper, selectedTemplate, canEditSources, editor.readonly, saving, suggestingRegions,
    libraryWorking, sourceUpload.uploading, sourceDeleting, preview.sourceId, preview.loading, preview.error, editor.suggestQuestionRegions]);
  const templateDirty = Boolean(selectedTemplate?.status === "draft" && (
    name !== selectedTemplate.name || JSON.stringify(layout) !== JSON.stringify(selectedTemplate.layout)
  ));
  const hasUnassignedCandidates = Boolean(editor.unassignedRegions?.size || editor.objectiveGroups?.size);
  const discardIdentity = `${scopeKey}:${examId}:${selectedPaperId}:${selectedTemplateId}:${canEditSources}:${sourceDeleting}`;
  const discardScope = useRef({ identity: discardIdentity, version: 0 });
  if (discardScope.current.identity !== discardIdentity) {
    discardScope.current = { identity: discardIdentity, version: discardScope.current.version + 1 };
  }
  useEffect(() => () => { discardScope.current.version++; }, []);
  // 候选和丢弃记录已随 editor_state 保存；待核对不等于未保存，离开只保护相对已保存布局的修改。
  const confirmDiscardTemplate = (action: () => void) => {
    if (saving || suggestingRegions || sourceUpload.uploading || sourceDeleting || printing.printBusy || bindingBusy || library.busy) { message.warning("请等待当前模板或打印操作完成后再切换。"); return; }
    if (!templateDirty) return action();
    const version = discardScope.current.version;
    modal.confirm({
      title: "放弃未保存的模板修改？",
      content: "当前答题区域、名称、待关联候选或待核对选择题区域尚未保存，切换后无法恢复。",
      okText: "放弃并切换",
      okButtonProps: { danger: true },
      cancelText: "继续编辑",
      // 上传确认可能跨越换考试或权限变化；不能让旧选文件通过hook的最新作用域传到另一场考试。
      onOk: () => { if (discardScope.current.version === version) action(); },
    });
  };

  useUnsavedChangesGuard({ dirty: templateDirty, busy: saving || suggestingRegions || sourceUpload.uploading || sourceDeleting || printing.printBusy || bindingBusy || library.busy, description: "答题区域或待核对候选尚未保存，离开后无法恢复这些修改。" });
  const sourceManagement = useAnswerSheetSourceDelete({
    examId, identityKey: scopeKey, canManage: canEditSources, selectedPaper, templates,
    busy: saving || suggestingRegions || sourceUpload.uploading || printing.printBusy || bindingBusy || library.busy,
    confirmDiscard: confirmDiscardTemplate, onBusyChange: setSourceDeleting,
    onDeleted: async paper => {
      // 服务端删除成功后才清理当前草稿和来源；重载列表后由原自动匹配流程选择剩余文件。
      setPapers(current => current.filter(item => item.id !== paper.id));
      setTemplates(current => current.filter(item => item.exam_paper_id !== paper.id));
      setSelectedPaperId(""); setSelectedTemplateId(""); setLayout({ pages: [] }); setReusedTemplateId("");
      await loadData(); onExamChanged?.();
    }
  });

  const uploadProps: UploadProps = {
    accept: ".pdf,.png,.jpg,.jpeg",
    multiple: true,
    showUploadList: false,
    disabled: !canEditSources || sourceUpload.uploading || sourceDeleting || saving || suggestingRegions || bindingBusy || library.busy,
    beforeUpload: (file, files) => {
      // AntD会对批次每个文件回调；只由首项发起一次确认和整份上传，避免拆成多个源版本。
      if (file !== files[0]) return Upload.LIST_IGNORE;
      if (files.some(item => !/\.(pdf|png|jpe?g)$/i.test(item.name))) {
        message.error("请上传 PDF、PNG 或 JPG 格式的答题卡，可一次选择多页图片");
        return Upload.LIST_IGNORE;
      }
      confirmDiscardTemplate(() => void sourceUpload.uploadBatch(files));
      return Upload.LIST_IGNORE;
    }
  };

  const view: TemplatePageViewState = {
    canManage, canCalibrate, papers, questions, templates, selectedPaperId, setSelectedPaperId,
    selectedTemplateId, setSelectedTemplateId, selectedTemplate, settingsOpen, setSettingsOpen,
    templateDirty, canEditSources, uploadProps, confirmDiscardTemplate, loadData,
    binding, printing, previewController, calibration, sourceUpload, sourceManagement, editor,
    sourceRecognitionBlocked: library.busy || sourceDeleting || (!selectedTemplate && library.resolution?.status !== "unknown")
  };
  // 换考试的首帧仍可能持有旧模板；读取所属考试前不显示旧的完成标识或编辑入口。
  if (loading || (!error && loadedExamId !== examId)) return <LoadingState label="正在加载答题卡模板" />;
  if (error) return <ErrorState message={error} onRetry={() => void loadData()} />;
  const libraryPanel = <TemplateLibraryPanel key={`${scopeKey}:${examId}:${selectedPaperId}:${selectedTemplateId}:${canManage}:${canEditSources}`}
    library={library} canManage={canManage} canEditSources={canEditSources} hasTemplate={Boolean(selectedTemplate)}
    reviewKey={JSON.stringify([selectedTemplate?.revision, name, layout])}
    busy={saving || suggestingRegions || sourceUpload.uploading || sourceDeleting || bindingBusy || printing.printBusy}
    canCollect={Boolean(selectedTemplate && ["draft", "locked"].includes(selectedTemplate.status)
      && (selectedTemplate.status === "locked" || canEditSources) && questions.length
      && selectedPaper?.id === selectedTemplate.exam_paper_id && preview.sourceId === selectedPaper.id && !preview.loading && !preview.error
      && questions.every(question => layout.pages.some(page => page.question_regions.some(region => region.question_id === question.id)))
      && !hasUnassignedCandidates)} confirmDiscard={confirmDiscardTemplate} />;
  if (!papers.length) return <div className="page-stack">{libraryPanel}<EmptyAnswerSheetSources view={view} /></div>;

  return (
    <div className="template-editor-page">
      <TemplateToolbar view={view} />
      <section className="template-workspace-configuration" aria-label="模板库与考试配置">
      {libraryPanel}
      {selectedTemplate?.id === reusedTemplateId ? <Alert type="success" showIcon message="已复用学校模板的答题区域"
        description="已关联本场题目；请核对页面和区域，再完成本场答题卡设置。" /> : null}
      </section>
      <SourceUploadStatus view={view} />
      {editor.activationError ? <Alert type="error" showIcon message="答题卡尚未完成启用" description={editor.activationError} /> : null}
      <TemplateWorkspace view={view} />
      <CalibrationDrawer view={view} />
      <StudentPrintModal view={view} />
      <CalibrationActionModal view={view} />
    </div>
  );
}

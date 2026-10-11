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
import { EmptyAnswerSheetSources, SourceUploadStatus, TemplateToolbar, TemplateWorkspace } from "../features/answer-sheet-template/view/TemplateWorkspaceView";
import { CalibrationActionModal, CalibrationDrawer } from "../features/answer-sheet-template/view/OMRCalibrationView";
import { StudentPrintModal } from "../features/answer-sheet-template/view/StudentPrintingView";
import type { TemplatePageViewState } from "../features/answer-sheet-template/view/templateViewTypes";
import { useUnsavedChangesGuard } from "../router/useUnsavedChangesGuard";

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error("请求失败", error.status, error.code, error.message);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

export function AnswerSheetTemplatePage({ examId, canManage, canCalibrate = false, onExamChanged }: Readonly<{ examId: string; canManage: boolean; canCalibrate?: boolean; onExamChanged?: () => void }>) {
  const { message, modal } = App.useApp();
  const dataRequestRef = useRef(0);
  const [papers, setPapers] = useState<PaperVersion[]>([]);
  const [questions, setQuestions] = useState<Question[]>([]);
  const [templates, setTemplates] = useState<AnswerSheetTemplate[]>([]);
  const [selectedPaperId, setSelectedPaperId] = useState("");
  const [selectedTemplateId, setSelectedTemplateId] = useState("");
  const [questionPaneOpen, setQuestionPaneOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [examStatus, setExamStatus] = useState("");

  const selectedPaper = useMemo(() => papers.find((item) => item.id === selectedPaperId), [papers, selectedPaperId]);
  const selectedTemplate = useMemo(() => templates.find((item) => item.id === selectedTemplateId), [templates, selectedTemplateId]);
  const binding = useTemplateBinding(examId);
  const { setExamBinding, bindingBusy } = binding;
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
      const sources = [...paperResponse.papers, ...legacyPaperResponse.papers.filter(paper => legacySourceIds.has(paper.id))];
      setPapers(sources);
      setExamStatus(examResponse.exam.status);
      setQuestions(questionResponse.questions);
      setTemplates(templateResponse.templates);
		setExamBinding(bindingResponse.binding);
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
  }, [examId]);

  useEffect(() => { void loadData(); }, [loadData]);
  useEffect(() => {
    if (selectedTemplate) setSelectedPaperId(selectedTemplate.exam_paper_id);
  }, [selectedTemplate]);

  // 准备确认后文件与作答区域已冻结；管理员仍可查看，但不能追加新的答题卡源文件。
  const canEditSources = canManage && ["draft", "configured"].includes(examStatus);
  const sourceUpload = useAnswerSheetSourceUpload({ examId, canManage: canEditSources, onCreated: (paper: PaperVersion): void => {
    setPapers(current => [paper, ...current.filter(item => item.id !== paper.id)]);
    setSelectedPaperId(paper.id); setSelectedTemplateId(""); setLayout({ pages: [] });
    onExamChanged?.();
  } });
  // 上传成功会切换底图并清空布局，等待期间暂停模板修改；失败后恢复编辑并保留原草稿。
  const editor = useTemplateEditor({
    examId, canManage: canManage && !sourceUpload.uploading, selectedPaper, selectedTemplate, questions, setTemplates,
    setSelectedTemplateId, onChanged: onExamChanged, onActivate: binding.activateTemplate, pdfRef, preview,
    pageNo, setPageNo
  });
  const { name, layout, setLayout, saving, suggestingRegions } = editor;
  const templateDirty = Boolean(selectedTemplate?.status === "draft" && (
    name !== selectedTemplate.name || JSON.stringify(layout) !== JSON.stringify(selectedTemplate.layout)
  ));
  // 模板未保存区域只在本页内存中；切换答题卡文件、版本或刷新前须明确是否丢弃。
  const confirmDiscardTemplate = (action: () => void) => {
    if (saving || suggestingRegions || sourceUpload.uploading || printing.printBusy || bindingBusy) { message.warning("请等待当前模板或打印操作完成后再切换或刷新。"); return; }
    if (!templateDirty) return action();
    modal.confirm({
      title: "放弃未保存的模板修改？",
      content: "当前框选区域或模板名称尚未保存，切换后无法恢复。",
      okText: "放弃并切换",
      okButtonProps: { danger: true },
      cancelText: "继续编辑",
      onOk: action,
    });
  };

  useUnsavedChangesGuard({ dirty: templateDirty, busy: saving || suggestingRegions || sourceUpload.uploading || printing.printBusy || bindingBusy, description: "框选区域或模板名称尚未保存，离开后无法恢复这些修改。" });

  const uploadProps: UploadProps = {
    accept: ".pdf,.png,.jpg,.jpeg",
    showUploadList: false,
    disabled: !canEditSources || sourceUpload.uploading || saving || suggestingRegions || bindingBusy,
    beforeUpload: (file: File) => {
      if (!/\.(pdf|png|jpe?g)$/i.test(file.name)) { message.error("请上传 PDF、PNG 或 JPG 格式的空白答题卡"); return Upload.LIST_IGNORE; }
      confirmDiscardTemplate(() => void sourceUpload.upload(file));
      return false;
    }
  };

  const view: TemplatePageViewState = {
    canManage, canCalibrate, papers, questions, templates, selectedPaperId, setSelectedPaperId,
    selectedTemplateId, setSelectedTemplateId, selectedTemplate, questionPaneOpen, setQuestionPaneOpen,
    templateDirty, canEditSources, uploadProps, confirmDiscardTemplate, loadData,
    binding, printing, previewController, calibration, sourceUpload, editor
  };
  if (loading) return <LoadingState label="正在加载答题卡模板" />;
  if (error) return <ErrorState message={error} onRetry={() => void loadData()} />;
  if (!papers.length) return <EmptyAnswerSheetSources view={view} />;

  return (
    <div className="template-editor-page">
      <TemplateToolbar view={view} />
      <SourceUploadStatus view={view} />
      {templateDirty ? <Alert type="warning" showIcon message="模板有未保存修改" description="可保存草稿稍后继续，或核对全部区域后点击“确认区域并用于本场考试”。" /> : null}
      {editor.activationError ? <Alert type="error" showIcon message="答题卡尚未完成启用" description={editor.activationError} /> : null}
      <TemplateWorkspace view={view} />
      <CalibrationDrawer view={view} />
      <StudentPrintModal view={view} />
      <CalibrationActionModal view={view} />
    </div>
  );
}

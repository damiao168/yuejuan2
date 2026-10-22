import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { App } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import {
  cloneAnswerSheetTemplate, createAnswerSheetTemplate, lockAnswerSheetTemplate,
  type AnswerSheetTemplate, type TemplateLayout
} from "../../../api/configuration";
import type { PaperVersion, Question } from "../../../api/papers";
import { saveTemplateDraft } from "./saveTemplateDraft";
import type { PreviewState } from "./useTemplatePreview";

function emptyLayout(pageCount: number, width: number, height: number, pageSizes?: PreviewState["pageSizes"]): TemplateLayout {
  return {
    omr_profile: { mode: "manual_only", version: "opencv-fill-v1" },
    pages: Array.from({ length: pageCount }, (_, index) => ({
      page_no: index + 1, width: pageSizes?.[index]?.width ?? width, height: pageSizes?.[index]?.height ?? height, registration_marks: [],
      identity_regions: [], question_regions: []
    }))
  };
}

export function useTemplateLifecycle({
  examId, selectedPaper, selectedTemplate, questions, layout, coveredQuestions,
  readonly, canManage = !readonly, setTemplates, setSelectedTemplateId, onChanged, onActivate, preview
}: {
  examId: string;
  selectedPaper?: PaperVersion;
  selectedTemplate?: AnswerSheetTemplate;
  questions: Question[];
  layout: TemplateLayout;
  coveredQuestions: Set<string | undefined>;
  readonly: boolean;
  canManage?: boolean;
  setTemplates: Dispatch<SetStateAction<AnswerSheetTemplate[]>>;
  setSelectedTemplateId: Dispatch<SetStateAction<string>>;
  onChanged?: () => void;
  onActivate?: (template: AnswerSheetTemplate) => Promise<unknown>;
  preview: PreviewState;
}) {
  const { message, modal } = App.useApp();
  const [name, setName] = useState("答题卡模板");
  const [revision, setRevision] = useState(0);
  const [saving, setSaving] = useState(false);
  const latestDraft = useRef({ layout, name });
  latestDraft.current = { layout, name };
  const activationInFlight = useRef<{ identityVersion: number } | null>(null);
  const mutationInFlight = useRef<{ identityVersion: number; reviewVersion: number } | null>(null);
  const [activationError, setActivationError] = useState<string>();
  const mounted = useRef(true);
  const previewLoading = useRef(preview.loading);
  previewLoading.current = preview.loading;
  // 同一底图翻页只改变渲染状态，不作废已经提交的保存；新操作另行等待当前页渲染完成。
  const sourceReady = !selectedPaper || (preview.sourceId === selectedPaper.id && !preview.error
    && (!selectedTemplate || selectedTemplate.exam_paper_id === selectedPaper.id));
  const activationScope = useRef({ examId, paperId: selectedPaper?.id, templateId: selectedTemplate?.id,
    readonly, canManage, sourceReady, identityVersion: 0, reviewVersion: 0 });
  const previousScope = activationScope.current;
  const identityChanged = previousScope.examId !== examId || previousScope.paperId !== selectedPaper?.id || previousScope.templateId !== selectedTemplate?.id;
  if (identityChanged || previousScope.readonly !== readonly || previousScope.canManage !== canManage || previousScope.sourceReady !== sourceReady) {
    activationScope.current = { examId, paperId: selectedPaper?.id, templateId: selectedTemplate?.id, readonly, canManage, sourceReady,
      identityVersion: previousScope.identityVersion + (identityChanged ? 1 : 0), reviewVersion: previousScope.reviewVersion + 1 };
  }
  const { identityVersion, reviewVersion } = activationScope.current;
  const isCurrentActivation = () => mounted.current && activationScope.current.identityVersion === identityVersion;
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => {
    // 换考试、底图或权限后，旧请求不能占用新编辑器；自身锁定导致只读时仍保留启用链的忙碌状态。
    setSaving(activationInFlight.current?.identityVersion === identityVersion
      || (mutationInFlight.current?.identityVersion === identityVersion && mutationInFlight.current.reviewVersion === reviewVersion));
    setActivationError(undefined);
  }, [identityVersion, reviewVersion]);

  const canMutateCurrentReview = (allowLocked = false) => isCurrentActivation()
    && activationScope.current.reviewVersion === reviewVersion && activationScope.current.canManage
    && ((allowLocked && selectedTemplate?.status === "locked") || !activationScope.current.readonly) && activationScope.current.sourceReady
    && (!selectedPaper || !previewLoading.current);
  const isCurrentMutation = (operation: { identityVersion: number; reviewVersion: number }) => mounted.current
    && activationScope.current.identityVersion === operation.identityVersion && activationScope.current.reviewVersion === operation.reviewVersion;
  const beginMutation = (allowLocked = false) => {
    if (!canMutateCurrentReview(allowLocked) || activationInFlight.current?.identityVersion === identityVersion
      || (mutationInFlight.current?.identityVersion === identityVersion && mutationInFlight.current.reviewVersion === reviewVersion)) return;
    // ref 在同一事件中立即占位，避免两次点击都读取尚未更新的 saving state。
    const operation = { identityVersion, reviewVersion };
    mutationInFlight.current = operation;
    setSaving(true);
    return operation;
  };
  const finishMutation = (operation: { identityVersion: number; reviewVersion: number }) => {
    if (mutationInFlight.current === operation) mutationInFlight.current = null;
    if (isCurrentMutation(operation)) setSaving(false);
  };

  useEffect(() => {
    if (!selectedTemplate) return;
    setName(selectedTemplate.name);
    setRevision(selectedTemplate.revision);
  }, [selectedTemplate]);

  const createDraft = async () => {
    if (!selectedPaper) { message.error("请先上传并选择答题卡文件"); return; }
    const operation = beginMutation();
    if (!operation) return;
    try {
      const response = await createAnswerSheetTemplate(examId, {
        exam_paper_id: selectedPaper.id,
        name: `${selectedPaper.file.original_name || "空白答题卡"}答题卡模板`,
        page_count: preview.pageCount || 1,
        layout: emptyLayout(preview.pageCount || 1, preview.width, preview.height, preview.pageSizes)
      });
      if (!isCurrentMutation(operation)) return;
      setTemplates((current) => [response.template, ...current]);
      setSelectedTemplateId(response.template.id);
      onChanged?.();
      message.success("答题卡草稿已就绪，系统将自动识别并对应题目，发现错误可直接修改");
    } catch (error) {
      if (isCurrentMutation(operation)) message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
    } finally {
      finishMutation(operation);
    }
  };
  const saveDraft = async () => {
    if (!selectedTemplate) return;
    const operation = beginMutation();
    if (!operation) return;
    try {
      const result = await saveTemplateDraft({
        templateId: selectedTemplate.id,
        payload: {
          exam_paper_id: selectedTemplate.exam_paper_id, name,
          page_count: layout.pages.length, layout
        },
        expectedRevision: revision
      });
      if (!isCurrentMutation(operation)) return;
      if (result.status === "saved") {
        setTemplates((current) => current.map((item) => item.id === result.template.id ? result.template : item));
        setRevision(result.template.revision);
        onChanged?.();
        message.success("模板已保存");
      } else {
        message.error(getUserErrorMessage(result.error, "操作失败，请稍后重试"));
        // 冲突时重读会触发编辑器重置；保留本地框选，用户核对后再主动刷新。
        if (result.status === "revision_conflict") {
          message.warning("模板版本已变化，当前未保存编辑仍在本页；请记录修改后再刷新核对最新版本。");
        }
      }
    } catch (error) {
      if (isCurrentMutation(operation)) message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
    } finally {
      finishMutation(operation);
    }
  };
  const confirmLock = () => {
    if (!selectedTemplate || !canMutateCurrentReview() || saving) return;
    const missing = questions.filter((question) => !coveredQuestions.has(question.id));
    if (missing.length) { message.error(`还有 ${missing.length} 道题未配置区域`); return; }
    modal.confirm({
      title: "锁定答题卡模板", content: "锁定后不能原地修改；如需调整必须克隆新版本。",
      okText: "确认锁定", cancelText: "取消",
      onOk: async () => {
        // 确认窗口可能跨越换考试或权限冻结；重新开放权限也不能复活旧窗口。
        if (latestDraft.current.layout !== layout || latestDraft.current.name !== name) {
          message.warning("确认期间草稿已有修改，请重新核对后确认"); return;
        }
        const operation = beginMutation();
        if (!operation) return;
        try {
          // 锁定只能使用刚保存的服务端版本，不能锁定编辑前的旧草稿。
          const saved = await saveTemplateDraft({ templateId: selectedTemplate.id,
            payload: { exam_paper_id: selectedTemplate.exam_paper_id, name, page_count: layout.pages.length, layout },
            expectedRevision: revision });
          if (!isCurrentMutation(operation)) return;
          if (saved.status !== "saved") throw saved.error;
          setTemplates(current => current.map(item => item.id === saved.template.id ? saved.template : item));
          setRevision(saved.template.revision);
          const response = await lockAnswerSheetTemplate(saved.template.id, saved.template.revision);
          if (!isCurrentMutation(operation)) return;
          setTemplates((current) => current.map((item) => item.id === response.template.id ? response.template : item));
          onChanged?.();
          message.success("模板已锁定");
        } catch (error) {
          if (isCurrentMutation(operation)) message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
        } finally {
          finishMutation(operation);
        }
      }
    });
  };
  const canActivateCurrentReview = () => isCurrentActivation()
    && activationScope.current.reviewVersion === reviewVersion && !activationScope.current.readonly
    && activationScope.current.canManage && activationScope.current.sourceReady && (!selectedPaper || !previewLoading.current)
    && activationInFlight.current?.identityVersion !== identityVersion
    && !(mutationInFlight.current?.identityVersion === identityVersion && mutationInFlight.current.reviewVersion === reviewVersion);
  async function saveActivationDraft(template: AnswerSheetTemplate) {
    const saved = await saveTemplateDraft({ templateId: template.id,
      payload: { exam_paper_id: template.exam_paper_id, name, page_count: layout.pages.length, layout },
      expectedRevision: revision });
    if (!isCurrentActivation()) return;
    if (saved.status !== "saved") throw saved.error;
    setTemplates(current => current.map(item => item.id === saved.template.id ? saved.template : item));
    setRevision(saved.template.revision);
    if (activationScope.current.readonly || !activationScope.current.canManage || !activationScope.current.sourceReady) {
      throw new Error("考试或模板已不可修改，请重新核对当前状态");
    }
    return saved.template;
  }
  async function activateSavedTemplate(template: AnswerSheetTemplate, progress: { locked: boolean; notified: boolean }) {
    const saved = await saveActivationDraft(template);
    if (!saved) return;
    // 三步没有跨接口事务；每次响应后核对身份，保留已保存或锁定的事实，不把后续失败当作回滚。
    const response = await lockAnswerSheetTemplate(saved.id, saved.revision);
    if (!isCurrentActivation()) return;
    progress.locked = true;
    setTemplates(current => current.map(item => item.id === response.template.id ? response.template : item));
    // 自身锁定允许继续绑定，但考试冻结或管理权限撤回后不能再发下一步请求。
    if (!activationScope.current.canManage) throw new Error("考试已冻结或当前权限已改变，请重新核对本场模板设置");
    await onActivate?.(response.template);
    if (!isCurrentActivation()) return;
    progress.notified = true;
    onChanged?.();
    message.success(onActivate ? `第 ${response.template.version_no} 版已用于本场考试，可在下方生成学生打印包` : "区域已保存并锁定，请用于本场考试后继续");
  }
  function reportActivationFailure(error: unknown, progress: { locked: boolean; notified: boolean }) {
    if (!isCurrentActivation()) return;
    const detail = getUserErrorMessage(error, "请稍后重试");
    const text = progress.locked ? `区域已保存并锁定，但尚未用于本场考试。请点击“确认答题卡设置”重试：${detail}` : `尚未完成启用；已保存的草稿仍保留，请核对后重试：${detail}`;
    setActivationError(text); message.error(text);
    // 绑定失败不会回滚锁定；导航仍要读取已通过的区域事实，不能等整条链成功才刷新。
    if (progress.locked && !progress.notified) onChanged?.();
  }
  const confirmActivate = () => {
    if (!selectedTemplate || readonly || saving || activationInFlight.current?.identityVersion === identityVersion) return;
    const missing = questions.filter(question => !coveredQuestions.has(question.id));
    if (!questions.length || missing.length) { message.error(`还有 ${missing.length || "待录入的"} 道题未配置区域`); return; }
    modal.confirm({
      title: "确认答题卡设置？",
      content: "系统会依次保存当前区域、锁定此版本并设为本场模板。锁定后修改需另建版本；成功后可生成学生打印包。",
      okText: "确认保存并启用", cancelText: "继续核对",
      onOk: async () => {
        // 旧窗口须先核对考试、模板及权限；提交后自身锁定会变为只读，不能因此中断最后的明确绑定。
        if (!canActivateCurrentReview()) return;
        if (latestDraft.current.layout !== layout || latestDraft.current.name !== name) {
          message.warning("确认期间草稿已有修改，请重新核对后确认"); return;
        }
        const operation = { identityVersion };
        activationInFlight.current = operation;
        setSaving(true); setActivationError(undefined);
        const progress = { locked: false, notified: false };
        try {
          await activateSavedTemplate(selectedTemplate, progress);
        } catch (error) {
          reportActivationFailure(error, progress);
        } finally {
          if (activationInFlight.current === operation) activationInFlight.current = null;
          if (isCurrentActivation()) setSaving(false);
        }
      }
    });
  };
  const cloneTemplate = async () => {
    if (!selectedTemplate) return;
    // 锁定模板仍可另建版本；考试冻结或底图未就绪则不能创建新版本。
    const operation = beginMutation(true);
    if (!operation) return;
    try {
      const response = await cloneAnswerSheetTemplate(selectedTemplate.id);
      if (!isCurrentMutation(operation)) return;
      setTemplates((current) => [response.template, ...current]);
      setSelectedTemplateId(response.template.id);
      onChanged?.();
      message.success("已创建可编辑的新版本");
    } catch (error) {
      if (isCurrentMutation(operation)) message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
    } finally {
      finishMutation(operation);
    }
  };

  const editName = (value: SetStateAction<string>) => {
    if (!readonly && !saving) setName(value);
  };
  return { name, setName: editName, revision, saving, activationError, createDraft, saveDraft, confirmLock, confirmActivate, cloneTemplate };
}

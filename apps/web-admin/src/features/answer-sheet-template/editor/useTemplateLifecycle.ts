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

function emptyLayout(pageCount: number, width: number, height: number): TemplateLayout {
  return {
    omr_profile: { mode: "manual_only", version: "opencv-fill-v1" },
    pages: Array.from({ length: pageCount }, (_, index) => ({
      page_no: index + 1, width, height, registration_marks: [],
      identity_regions: [], question_regions: []
    }))
  };
}

export function useTemplateLifecycle({
  examId, selectedPaper, selectedTemplate, questions, layout, coveredQuestions,
  readonly, setTemplates, setSelectedTemplateId, onChanged, onActivate, preview
}: {
  examId: string;
  selectedPaper?: PaperVersion;
  selectedTemplate?: AnswerSheetTemplate;
  questions: Question[];
  layout: TemplateLayout;
  coveredQuestions: Set<string | undefined>;
  readonly: boolean;
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
  const activationInFlight = useRef(false);
  const [activationError, setActivationError] = useState<string>();

  useEffect(() => {
    if (!selectedTemplate) return;
    setName(selectedTemplate.name);
    setRevision(selectedTemplate.revision);
  }, [selectedTemplate]);

  const createDraft = async () => {
    if (saving) return;
    if (!selectedPaper) { message.error("请先上传并选择答题卡文件"); return; }
    setSaving(true);
    try {
      const response = await createAnswerSheetTemplate(examId, {
        exam_paper_id: selectedPaper.id,
        name: `${selectedPaper.file.original_name || "空白答题卡"}答题卡模板`,
        page_count: preview.pageCount || 1,
        layout: emptyLayout(preview.pageCount || 1, preview.width, preview.height)
      });
      setTemplates((current) => [response.template, ...current]);
      setSelectedTemplateId(response.template.id);
      onChanged?.();
      message.success("模板草稿已创建，可以开始框选题目区域");
    } catch (error) {
      message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
    } finally {
      setSaving(false);
    }
  };
  const saveDraft = async () => {
    if (!selectedTemplate || readonly || saving) return;
    setSaving(true);
    try {
      const result = await saveTemplateDraft({
        templateId: selectedTemplate.id,
        payload: {
          exam_paper_id: selectedTemplate.exam_paper_id, name,
          page_count: layout.pages.length, layout
        },
        expectedRevision: revision
      });
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
    } finally {
      setSaving(false);
    }
  };
  const confirmLock = () => {
    if (!selectedTemplate || readonly || saving) return;
    const missing = questions.filter((question) => !coveredQuestions.has(question.id));
    if (missing.length) { message.error(`还有 ${missing.length} 道题未配置区域`); return; }
    modal.confirm({
      title: "锁定答题卡模板", content: "锁定后不能原地修改；如需调整必须克隆新版本。",
      okText: "确认锁定", cancelText: "取消",
      onOk: async () => {
        setSaving(true);
        try {
          const response = await lockAnswerSheetTemplate(selectedTemplate.id);
          setTemplates((current) => current.map((item) => item.id === response.template.id ? response.template : item));
          onChanged?.();
          message.success("模板已锁定");
        } finally {
          setSaving(false);
        }
      }
    });
  };
  const confirmActivate = () => {
    if (!selectedTemplate || readonly || saving || activationInFlight.current) return;
    const missing = questions.filter(question => !coveredQuestions.has(question.id));
    if (!questions.length || missing.length) { message.error(`还有 ${missing.length || "待录入的"} 道题未配置区域`); return; }
    modal.confirm({
      title: "确认区域并用于本场考试？",
      content: "系统会依次保存当前区域、锁定此版本并设为本场模板。锁定后修改需另建版本；成功后可生成学生打印包。",
      okText: "保存并用于本场考试", cancelText: "继续核对",
      onOk: async () => {
        if (activationInFlight.current) return;
        activationInFlight.current = true;
        setSaving(true); setActivationError(undefined);
        let locked = false;
        try {
          const saved = await saveTemplateDraft({ templateId: selectedTemplate.id,
            payload: { exam_paper_id: selectedTemplate.exam_paper_id, name, page_count: layout.pages.length, layout },
            expectedRevision: revision });
          if (saved.status !== "saved") throw saved.error;
          setTemplates(current => current.map(item => item.id === saved.template.id ? saved.template : item));
          setRevision(saved.template.revision);
          // 三步没有跨接口事务；保存或锁定成功的事实必须保留，重试不能伪装成全部回滚。
          const response = await lockAnswerSheetTemplate(saved.template.id);
          locked = true;
          setTemplates(current => current.map(item => item.id === response.template.id ? response.template : item));
          await onActivate?.(response.template);
          onChanged?.();
          message.success(onActivate ? `第 ${response.template.version_no} 版已用于本场考试，可在下方生成学生打印包` : "区域已保存并锁定，请用于本场考试后继续");
        } catch (error) {
          const detail = getUserErrorMessage(error, "请稍后重试");
          const text = locked ? `区域已保存并锁定，但尚未用于本场考试。请点击“用于本场考试”重试：${detail}` : `尚未完成启用；已保存的草稿仍保留，请核对后重试：${detail}`;
          setActivationError(text); message.error(text);
        } finally {
          activationInFlight.current = false; setSaving(false);
        }
      }
    });
  };
  const cloneTemplate = async () => {
    if (!selectedTemplate || saving) return;
    setSaving(true);
    try {
      const response = await cloneAnswerSheetTemplate(selectedTemplate.id);
      setTemplates((current) => [response.template, ...current]);
      setSelectedTemplateId(response.template.id);
      onChanged?.();
      message.success("已创建可编辑的新版本");
    } catch (error) {
      message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
    } finally {
      setSaving(false);
    }
  };

  const editName = (value: SetStateAction<string>) => {
    if (!readonly && !saving) setName(value);
  };
  return { name, setName: editName, revision, saving, activationError, createDraft, saveDraft, confirmLock, confirmActivate, cloneTemplate };
}

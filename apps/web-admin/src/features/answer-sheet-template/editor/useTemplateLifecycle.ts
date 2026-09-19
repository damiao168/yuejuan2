import { useEffect, useState, type Dispatch, type SetStateAction } from "react";
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
  readonly, setTemplates, setSelectedTemplateId, loadData, onChanged, preview
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
  loadData: () => Promise<void>;
  onChanged?: () => void;
  preview: PreviewState;
}) {
  const { message, modal } = App.useApp();
  const [name, setName] = useState("答题卡模板");
  const [revision, setRevision] = useState(0);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!selectedTemplate) return;
    setName(selectedTemplate.name);
    setRevision(selectedTemplate.revision);
  }, [selectedTemplate]);

  const createDraft = async () => {
    if (!selectedPaper) { message.error("请先上传并选择试卷版本"); return; }
    setSaving(true);
    try {
      const response = await createAnswerSheetTemplate(examId, {
        exam_paper_id: selectedPaper.id,
        name: `${selectedPaper.file.original_name || "试卷"}答题卡模板`,
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
    if (!selectedTemplate || readonly) return;
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
        if (result.status === "revision_conflict") void loadData();
      }
    } finally {
      setSaving(false);
    }
  };
  const confirmLock = () => {
    if (!selectedTemplate || readonly) return;
    const missing = questions.filter((question) => !coveredQuestions.has(question.id));
    if (missing.length) { message.error(`还有 ${missing.length} 道题未配置区域`); return; }
    modal.confirm({
      title: "锁定答题卡模板", content: "锁定后不能原地修改；如需调整必须克隆新版本。",
      okText: "确认锁定", cancelText: "取消",
      onOk: async () => {
        const response = await lockAnswerSheetTemplate(selectedTemplate.id);
        setTemplates((current) => current.map((item) => item.id === response.template.id ? response.template : item));
        onChanged?.();
        message.success("模板已锁定");
      }
    });
  };
  const cloneTemplate = async () => {
    if (!selectedTemplate) return;
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

  return { name, setName, revision, saving, createDraft, saveDraft, confirmLock, cloneTemplate };
}

import { useCallback, useEffect, useState } from "react";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import {
  createReviewAnnotation,
  createReviewCommentTemplate,
  applyReviewCommentTemplate,
  deleteReviewAnnotation,
  deleteReviewCommentTemplate,
  listReviewAnnotations,
  listReviewCommentTemplates,
  updateReviewAnnotation,
  updateReviewCommentTemplate,
  type CreateReviewAnnotationRequest,
  type CreateReviewCommentTemplateRequest,
  type ReviewAnnotation,
  type ReviewCommentTemplate,
  type UpdateReviewCommentTemplateRequest
} from "../../../api/reviewAnnotations";

export type AnnotationSaveState = "idle" | "saving" | "saved" | "conflict" | "error";

export class AnnotationRevisionConflict extends Error {
  constructor(public readonly latest?: ReviewAnnotation) {
    super("批注已被其他人更新，已刷新为最新版本，请确认后再保存");
    this.name = "AnnotationRevisionConflict";
  }
}

function errorMessage(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function isRevisionConflict(error: unknown) {
  return error instanceof ApiClientError && error.status === 409;
}

export function useReviewAnnotations(taskId: string) {
  const [annotations, setAnnotations] = useState<ReviewAnnotation[]>([]);
  const [templates, setTemplates] = useState<ReviewCommentTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [saveState, setSaveState] = useState<AnnotationSaveState>("idle");
  const [error, setError] = useState<string>();

  const reloadAnnotations = useCallback(async (signal?: AbortSignal) => {
    const response = await listReviewAnnotations(taskId, signal);
    setAnnotations(response.annotations);
    return response.annotations;
  }, [taskId]);

  const reloadTemplates = useCallback(async (signal?: AbortSignal) => {
    const response = await listReviewCommentTemplates(signal);
    setTemplates(response.comment_templates);
    return response.comment_templates;
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(undefined);
    Promise.all([reloadAnnotations(controller.signal), reloadTemplates(controller.signal)])
      .catch((loadError: unknown) => {
        if (!controller.signal.aborted) setError(errorMessage(loadError));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [reloadAnnotations, reloadTemplates]);

  const saveAnnotation = useCallback(async (
    input: CreateReviewAnnotationRequest,
    current?: ReviewAnnotation
  ) => {
    setSaveState("saving");
    setError(undefined);
    try {
      const response = current
        ? await updateReviewAnnotation(current.id, {
            ...input,
            visibility: input.visibility ?? "private",
            expected_revision: current.revision
          })
        : await createReviewAnnotation(taskId, input);
      setAnnotations((items) => {
        const index = items.findIndex((item) => item.id === response.annotation.id);
        if (index < 0) return [...items, response.annotation];
        return items.map((item) => item.id === response.annotation.id ? response.annotation : item);
      });
      setSaveState("saved");
      return response.annotation;
    } catch (saveError) {
      if (isRevisionConflict(saveError)) {
        // 冲突后带回服务端最新批注供用户核对，不自动重试覆盖其他会话的修改。
        setSaveState("conflict");
        setError("批注已被其他人更新，已刷新为最新版本，请确认后再保存");
        const latest = await reloadAnnotations();
        throw new AnnotationRevisionConflict(current ? latest.find((item) => item.id === current.id) : undefined);
      } else {
        setSaveState("error");
        setError(errorMessage(saveError));
      }
      throw saveError;
    }
  }, [reloadAnnotations, taskId]);

  const removeAnnotation = useCallback(async (item: ReviewAnnotation) => {
    setSaveState("saving");
    setError(undefined);
    try {
      await deleteReviewAnnotation(item.id, item.revision);
      setAnnotations((items) => items.filter((candidate) => candidate.id !== item.id));
      setSaveState("saved");
    } catch (deleteError) {
      if (isRevisionConflict(deleteError)) {
        setSaveState("conflict");
        setError("批注已被其他人更新，已刷新为最新版本");
        await reloadAnnotations();
      } else {
        setSaveState("error");
        setError(errorMessage(deleteError));
      }
      throw deleteError;
    }
  }, [reloadAnnotations]);

  const createTemplate = useCallback(async (input: CreateReviewCommentTemplateRequest) => {
    const response = await createReviewCommentTemplate(input);
    setTemplates((items) => [...items, response.comment_template]);
    return response.comment_template;
  }, []);

  const updateTemplate = useCallback(async (
    current: ReviewCommentTemplate,
    input: Omit<UpdateReviewCommentTemplateRequest, "expected_revision">
  ) => {
    try {
      const response = await updateReviewCommentTemplate(current.id, {
        ...input,
        expected_revision: current.revision
      });
      setTemplates((items) => items.map((item) => item.id === current.id ? response.comment_template : item));
      return response.comment_template;
    } catch (updateError) {
      if (isRevisionConflict(updateError)) await reloadTemplates();
      throw updateError;
    }
  }, [reloadTemplates]);

  const removeTemplate = useCallback(async (current: ReviewCommentTemplate) => {
    try {
      await deleteReviewCommentTemplate(current.id, current.revision);
      setTemplates((items) => items.filter((item) => item.id !== current.id));
    } catch (deleteError) {
      if (isRevisionConflict(deleteError)) await reloadTemplates();
      throw deleteError;
    }
  }, [reloadTemplates]);

  const applyTemplate = useCallback(async (shortcut: string) => {
    const response = await applyReviewCommentTemplate(shortcut);
    setTemplates((items) => items.map((item) => item.id === response.comment_template.id ? response.comment_template : item));
    return response.comment_template;
  }, []);

  return {
    annotations,
    templates,
    loading,
    saveState,
    error,
    reloadAnnotations,
    saveAnnotation,
    removeAnnotation,
    createTemplate,
    updateTemplate,
    removeTemplate,
    applyTemplate
  };
}

import { useCallback, useEffect, useRef } from "react";
import type { FormInstance } from "antd";
import { MODEL_CONFIG_DRAFT_TTL_MS, type ConfigFormValues, type ModelConfigDraft } from "../lib/modelConfig";

export function useModelConfigDrafts(form: FormInstance<ConfigFormValues>) {
  // 模型表单可能包含 API Key，草稿只留在当前组件内存中，超时和卸载都会丢弃，不写浏览器存储。
  const draftsRef = useRef(new Map<string, ModelConfigDraft>());
  const timersRef = useRef(new Map<string, ReturnType<typeof setTimeout>>());

  const clearFormDraft = useCallback((key: string) => {
    draftsRef.current.delete(key);
    const timer = timersRef.current.get(key);
    if (timer) clearTimeout(timer);
    timersRef.current.delete(key);
  }, []);

  const saveFormDraft = useCallback((key: string, values: ConfigFormValues) => {
    clearFormDraft(key);
    const draft = { values: { ...values }, expiresAt: Date.now() + MODEL_CONFIG_DRAFT_TTL_MS };
    draftsRef.current.set(key, draft);
    timersRef.current.set(key, setTimeout(() => {
      draftsRef.current.delete(key);
      timersRef.current.delete(key);
    }, MODEL_CONFIG_DRAFT_TTL_MS));
  }, [clearFormDraft]);

  const restoreFormDraft = useCallback((key: string, fallback: Partial<ConfigFormValues>) => {
    const draft = draftsRef.current.get(key);
    if (!draft || draft.expiresAt <= Date.now()) {
      clearFormDraft(key);
      form.setFieldsValue(fallback);
      return false;
    }
    form.setFieldsValue({ ...fallback, ...draft.values });
    return true;
  }, [clearFormDraft, form]);

  useEffect(() => () => {
    for (const timer of timersRef.current.values()) clearTimeout(timer);
    timersRef.current.clear();
    draftsRef.current.clear();
  }, []);

  return { clearFormDraft, saveFormDraft, restoreFormDraft };
}

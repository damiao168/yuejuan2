import { useCallback, useEffect, useRef, useState } from "react";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { getModelApplications, saveAnswerSheetModelSelection, saveGradingModelSelection, saveDocumentModelSelection, type AnswerSheetModelSelection, type DocumentModelSelection, type ModelApplications } from "../../../api/modelApiConfig";
import type { ModelOperationQueue } from "../lib/modelOperationQueue";

export function useModelApplications(tenantID: string, queue: ModelOperationQueue) {
  const [state, setState] = useState<{ tenantID: string; data?: ModelApplications; loading: boolean; error?: string; conflict?: boolean }>({ tenantID: "", loading: false });
  const [saving, setSaving] = useState(false);
  const tenantRef = useRef(tenantID);
  tenantRef.current = tenantID;
  const requestRef = useRef(0);
  const savingRef = useRef(false);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; requestRef.current += 1; }; }, []);

  const refresh = useCallback(async () => {
    if (!tenantID || savingRef.current) return;
    const generation = ++requestRef.current;
    setState(current => ({ tenantID, data: current.tenantID === tenantID ? current.data : undefined, loading: true }));
    try {
      const data = await getModelApplications(tenantID);
      if (!data.document_selection?.revision || !["follow_chat", "model"].includes(data.document_selection.mode) || !Array.isArray(data.grading_model_ids)) throw new Error("模型用途响应不完整，请更新 API 服务后刷新");
      if (data.answer_sheet_selection && (!data.answer_sheet_selection.revision || !["unconfigured", "model"].includes(data.answer_sheet_selection.mode))) throw new Error("答题卡模型用途响应不完整，请更新 API 服务后刷新");
      if (mounted.current && tenantRef.current === tenantID && generation === requestRef.current) setState({ tenantID, data, loading: false });
    } catch (error) {
      if (mounted.current && tenantRef.current === tenantID && generation === requestRef.current) setState(current => ({ ...current, loading: false, error: getUserErrorMessage(error, "用途配置读取失败，请刷新重试") }));
    }
  }, [tenantID]);
  useEffect(() => { void refresh(); return () => { requestRef.current += 1; }; }, [refresh]);

  const save = useCallback(async (mode: DocumentModelSelection["mode"], modelID?: string) => {
    if (savingRef.current || state.tenantID !== tenantID || !state.data || state.loading || state.error) return false;
    const release = queue.acquireWrite(tenantID, "__document__");
    if (!release) return false;
    const releaseModel = mode === "model" && modelID ? queue.acquireWrite(tenantID, modelID) : undefined;
    if (mode === "model" && !releaseModel) { release(); return false; }
    savingRef.current = true;
    setSaving(true);
    // 保存递增代次，使此前的整份用途读取不能覆盖刚返回的解析选择。
    const generation = ++requestRef.current;
    try {
      const response = await saveDocumentModelSelection(tenantID, { mode, model_config_id: mode === "model" ? modelID : undefined, expected_revision: state.data.document_selection.revision });
      if (mounted.current && tenantRef.current === tenantID && generation === requestRef.current) {
        setState(current => ({ ...current, error: undefined, conflict: false, data: { ...state.data!, document_selection: response.selection } }));
        return true;
      }
      return false;
    } catch (error) {
      if (mounted.current && tenantRef.current === tenantID && generation === requestRef.current) setState(current => ({ ...current, error: getUserErrorMessage(error), conflict: error instanceof ApiClientError && error.code === "document_model_selection_changed" }));
      return false;
    } finally {
      releaseModel?.(); release(); savingRef.current = false;
      if (mounted.current) setSaving(false);
    }
  }, [queue, state, tenantID]);

  const saveAnswerSheet = useCallback(async (mode: AnswerSheetModelSelection["mode"], modelID?: string) => {
    const selection = state.data?.answer_sheet_selection;
    if (savingRef.current || state.tenantID !== tenantID || !selection || state.loading || state.error) return false;
    const release = queue.acquireWrite(tenantID, "__answer_sheet__");
    if (!release) return false;
    const releaseModel = mode === "model" && modelID ? queue.acquireWrite(tenantID, modelID) : undefined;
    if (mode === "model" && !releaseModel) { release(); return false; }
    savingRef.current = true; setSaving(true);
    // 答题卡用途与解析用途分别保存；旧整份读取及其他学校的迟到响应不能覆盖本次选择。
    const generation = ++requestRef.current;
    try {
      const response = await saveAnswerSheetModelSelection(tenantID, { mode, model_config_id: mode === "model" ? modelID : undefined, expected_revision: selection.revision });
      if (mounted.current && tenantRef.current === tenantID && generation === requestRef.current) {
        setState(current => ({ ...current, error: undefined, conflict: false, data: { ...state.data!, answer_sheet_selection: response.selection } }));
        return true;
      }
      return false;
    } catch (error) {
      if (mounted.current && tenantRef.current === tenantID && generation === requestRef.current) setState(current => ({ ...current, error: getUserErrorMessage(error), conflict: error instanceof ApiClientError && error.code === "answer_sheet_model_selection_changed" }));
      return false;
    } finally {
      releaseModel?.(); release(); savingRef.current = false;
      if (mounted.current) setSaving(false);
    }
  }, [queue, state, tenantID]);
  const saveGrading = useCallback(async (mode: AnswerSheetModelSelection["mode"], modelID?: string) => {
    const selection = state.data?.grading_selection;
    if (savingRef.current || state.tenantID !== tenantID || !selection || state.loading || state.error) return false;
    const release = queue.acquireWrite(tenantID, "__grading__");
    if (!release) return false;
    const releaseModel = mode === "model" && modelID ? queue.acquireWrite(tenantID, modelID) : undefined;
    if (mode === "model" && !releaseModel) { release(); return false; }
    savingRef.current = true; setSaving(true);
    const generation = ++requestRef.current;
    try {
      const response = await saveGradingModelSelection(tenantID, { mode, model_config_id: mode === "model" ? modelID : undefined, expected_revision: selection.revision });
      if (mounted.current && tenantRef.current === tenantID && generation === requestRef.current) {
        setState(current => ({ ...current, error: undefined, conflict: false, data: { ...state.data!, grading_selection: response.selection } }));
        return true;
      }
      return false;
    } catch (error) {
      if (mounted.current && tenantRef.current === tenantID && generation === requestRef.current) setState(current => ({ ...current, error: getUserErrorMessage(error), conflict: error instanceof ApiClientError && error.code === "grading_model_selection_changed" }));
      return false;
    } finally {
      releaseModel?.(); release(); savingRef.current = false;
      if (mounted.current) setSaving(false);
    }
  }, [queue, state, tenantID]);
  return { data: state.tenantID === tenantID ? state.data : undefined, loading: state.tenantID !== tenantID || state.loading, error: state.tenantID === tenantID ? state.error : undefined, saving, refresh, save, saveAnswerSheet, saveGrading };
}

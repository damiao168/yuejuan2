import { useCallback, useEffect, useRef, useState } from "react";
import { getUserErrorMessage } from "../../../api/client";
import { instantiateAnswerSheetTemplate, listAnswerSheetTemplateLibrary, publishAnswerSheetTemplate, resolveAnswerSheetTemplate,
  type AnswerSheetTemplateLibrary, type TemplateLibraryInstance, type TemplateLibraryResolution } from "../../../api/answerSheetTemplateLibrary";
import { lockAnswerSheetTemplate, type AnswerSheetTemplate, type TemplateLayout } from "../../../api/configuration";
import type { PaperVersion } from "../../../api/papers";
import { saveTemplateDraft } from "../editor/saveTemplateDraft";

export function useTemplateLibrary({ examId, scopeKey, enabled, canManage, canEditSources, source, template, layout, name,
  onInstance, onTemplateChanged, onBusyChange }: {
  examId: string; scopeKey: string; enabled: boolean; canManage: boolean; canEditSources: boolean;
  source?: PaperVersion; template?: AnswerSheetTemplate; layout: TemplateLayout; name: string;
  onInstance: (instance: TemplateLibraryInstance) => void;
  onTemplateChanged: (template: AnswerSheetTemplate) => void;
  onBusyChange?: (busy: boolean) => void;
}) {
  const [libraries, setLibraries] = useState<AnswerSheetTemplateLibrary[]>([]);
  const [loading, setLoading] = useState(false);
  const [listError, setListError] = useState<string>();
  const [error, setError] = useState<string>();
  const [errorStage, setErrorStage] = useState<"match" | "instantiate" | "publish">();
  const [busy, setBusy] = useState(false);
  const [resolution, setResolution] = useState<TemplateLibraryResolution>();
  const [published, setPublished] = useState<AnswerSheetTemplateLibrary>();
  useEffect(() => { onBusyChange?.(busy); }, [busy, onBusyChange]);
  const generation = useRef(0);
  const operationBusy = useRef(false);
  const identity = JSON.stringify([scopeKey, examId, source?.id, template?.id, canManage, canEditSources, enabled]);
  const currentIdentity = useRef(identity);
  if (identity !== currentIdentity.current) {
    // 身份、来源或权限变化立刻废弃旧请求；权限撤回再恢复也不能复活旧结果。
    currentIdentity.current = identity; generation.current++; operationBusy.current = false;
  }
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; generation.current++; }; }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 作用域变化要清除旧结果，不能删除这个仅作重置触发器的依赖。
  useEffect(() => { setBusy(false); setError(undefined); setResolution(undefined); setPublished(undefined); }, [identity]);
  const listIdentity = JSON.stringify([scopeKey, examId, enabled]);
  const listScope = useRef(listIdentity);
  const listGeneration = useRef(0);
  listScope.current = listIdentity;
  const refresh = useCallback(async (signal?: AbortSignal) => {
    if (!enabled) return;
    const requestScope = listIdentity;
    const requestGeneration = ++listGeneration.current;
    const current = () => mounted.current && listScope.current === requestScope && listGeneration.current === requestGeneration && !signal?.aborted;
    setLoading(true); setListError(undefined);
    try {
      const response = await listAnswerSheetTemplateLibrary(examId, signal);
      if (current()) setLibraries(response.libraries);
    } catch (cause) {
      if (current()) setListError(`学校模板库未能加载：${getUserErrorMessage(cause, "请稍后重试。")}`);
    } finally {
      if (current()) setLoading(false);
    }
  }, [listIdentity, enabled, examId]);
  useEffect(() => {
    setLibraries([]); setListError(undefined);
    const abort = new AbortController();
    void refresh(abort.signal);
    return () => abort.abort();
    // 列表只按账户及考试重读；编辑框选不应反复请求整个库。
  }, [refresh]);

  const begin = () => {
    if (!enabled || !canManage || operationBusy.current || currentIdentity.current !== identity) return;
    const requestGeneration = generation.current;
    const current = () => mounted.current && generation.current === requestGeneration && currentIdentity.current === identity;
    operationBusy.current = true; setBusy(true); setError(undefined);
    return { current, finish: () => { if (current()) { operationBusy.current = false; setBusy(false); } } };
  };
  const resolveSource = async () => {
    if (!source || !canEditSources) return;
    const operation = begin();
    if (!operation) return;
    try {
      const response = await resolveAnswerSheetTemplate(examId, source.id);
      if (!operation.current()) return;
      if (response.status === "matched" && !response.instance) throw new Error("模板匹配响应缺少考试实例");
      setResolution(response);
      if (response.status === "matched" && response.instance) onInstance(response.instance);
      return response;
    } catch (cause) {
      if (operation.current()) { setErrorStage("match"); setError(getUserErrorMessage(cause, "模板匹配未完成，请重试；暂不创建新模板。")); }
    } finally { operation.finish(); }
    return undefined;
  };
  const instantiate = async (libraryId: string) => {
    if (!canEditSources) return;
    const operation = begin();
    if (!operation) return;
    try {
      const instance = await instantiateAnswerSheetTemplate(examId, libraryId);
      if (operation.current()) onInstance(instance);
    } catch (cause) {
      if (operation.current()) { setErrorStage("instantiate"); setError(getUserErrorMessage(cause, "无法用于本场考试，请核对题号和题型是否一致。")); }
    } finally { operation.finish(); }
  };
  const publishKey = useRef<{ payload: string; key: string } | undefined>(undefined);
  const publish = async () => {
    if (!template || (template.status !== "locked" && (!canEditSources || template.status !== "draft"))) return;
    const operation = begin();
    if (!operation) return;
    let locked = template;
    try {
      if (locked.status === "draft") {
        const saved = await saveTemplateDraft({ templateId: locked.id, expectedRevision: locked.revision,
          payload: { exam_paper_id: locked.exam_paper_id, name, page_count: layout.pages.length, layout } });
        if (!operation.current()) return;
        if (saved.status !== "saved") throw saved.error;
        onTemplateChanged(saved.template);
        const response = await lockAnswerSheetTemplate(saved.template.id, saved.template.revision);
        if (!operation.current()) return;
        locked = response.template;
        // 锁定和收录为独立事实；收录失败保留锁定结果，重试可继续收录。
        onTemplateChanged(locked);
      }
      const payload = { name: locked.name, expected_revision: locked.revision,
        confirmed_blank_reference: true, confirmed_reusable_layout: true };
      const signature = JSON.stringify([locked.id, payload]);
      if (publishKey.current?.payload !== signature) publishKey.current = { payload: signature, key: crypto.randomUUID() };
      const response = await publishAnswerSheetTemplate(locked.id, payload, publishKey.current.key);
      if (!operation.current()) return;
      // 收录响应比在途列表更新；丢弃旧列表，避免成功收录的项目被旧快照移除。
      listGeneration.current++; setLoading(false);
      setPublished(response.library);
      setLibraries(current => [response.library, ...current.filter(item => item.id !== response.library.id)]);
    } catch (cause) {
      if (operation.current()) { setErrorStage("publish"); setError(getUserErrorMessage(cause, locked.status === "locked"
        ? "模板已锁定，但尚未收录；请重试收录。" : "尚未完成收录，已保存的草稿仍然保留。")); }
    } finally { operation.finish(); }
  };
  return { libraries, loading, listError, error, errorStage, busy, resolution, published, scopeVersion: generation.current,
    refresh, resolveSource, instantiate, publish };
}

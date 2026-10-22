import { useCallback, useEffect, useRef, useState } from "react";
import { getChatModelAccess, saveChatModelAccess, type ChatModelAccess } from "../../../api/chatModelAccess";
import { getUserErrorMessage } from "../../../api/client";

export function useChatModelAccess(tenantID: string) {
  const [state, setState] = useState<{ tenantID: string; access?: ChatModelAccess; busy: boolean; error: string }>({ tenantID: "", busy: false, error: "" });
  const version = useRef(0);
  const saving = useRef<number | undefined>(undefined);
  const tenantRef = useRef(tenantID);
  tenantRef.current = tenantID;
  const refresh = useCallback(async () => {
    if (saving.current === version.current && state.tenantID === tenantID) return;
    const current = ++version.current;
    setState({ tenantID, busy: Boolean(tenantID), error: "" });
    if (!tenantID) return;
    try {
      const { access } = await getChatModelAccess(tenantID);
      if (!access.revision || !Array.isArray(access.model_ids) || access.model_ids.some(id => typeof id !== "string")) throw new Error("对话开放设置响应不完整，请更新 API 服务后刷新");
      // 学校切换和刷新各有代次；旧学校或旧版本的读取不能覆盖当前开放范围。
      if (current === version.current && tenantRef.current === tenantID) setState({ tenantID, access, busy: false, error: "" });
    } catch (error) {
      if (current === version.current && tenantRef.current === tenantID) setState({ tenantID, busy: false, error: getUserErrorMessage(error, "对话开放设置读取失败，请刷新页面") });
    }
  }, [tenantID, state.tenantID]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 刷新函数还读取当前状态，自动读取只由学校变化触发。
  useEffect(() => {
    void refresh();
    return () => { version.current++; };
  }, [tenantID]);
  const toggle = useCallback(async (id: string) => {
    if (state.tenantID !== tenantID || !state.access || state.busy || saving.current === version.current) return;
    const current = ++version.current;
    saving.current = current;
    setState(previous => ({ ...previous, busy: true, error: "" }));
    const ids = state.access.model_ids.includes(id) ? state.access.model_ids.filter(value => value !== id) : [...state.access.model_ids, id];
    try {
      const { access } = await saveChatModelAccess(tenantID, state.access, ids);
      if (current === version.current && tenantRef.current === tenantID) setState({ tenantID, access, busy: false, error: "" });
    } catch (error) {
      // 冲突时保留旧选择供核对，不自动覆盖其他管理员的开放设置。
      if (current === version.current && tenantRef.current === tenantID) setState(previous => ({ ...previous, busy: false, error: getUserErrorMessage(error, "对话开放设置保存失败，请刷新后重试") }));
    } finally { if (saving.current === current) saving.current = undefined; }
  }, [state, tenantID]);
  return { ids: state.tenantID === tenantID ? state.access?.model_ids : undefined, busy: state.tenantID !== tenantID || state.busy, error: state.tenantID === tenantID ? state.error : "", toggle, refresh };
}

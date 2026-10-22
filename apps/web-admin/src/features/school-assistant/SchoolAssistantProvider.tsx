import { createContext, lazy, Suspense, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { Button, Drawer } from "antd";
import { ErrorBoundary } from "../../components/ErrorBoundary";
import type { SessionUser } from "../../auth/session";
import { canUseSchoolAssistant } from "./navigation";
import { useSchoolAssistant, type AssistantContext } from "./useSchoolAssistant";
import { conversationAccountKey } from "./conversationState";
import "./school-assistant.css";

// 助手正文含 Markdown 等重依赖；仅打开抽屉时加载，账户与权限 key 仍控制整个挂载生命周期。
const SchoolConversation = lazy(() => import("./SchoolConversation").then((module) => ({ default: module.SchoolConversation })));

interface AssistantValue extends ReturnType<typeof useSchoolAssistant> {
  user: SessionUser;
  open: (context: AssistantContext) => void;
  navigate: (path: string) => void;
}
const AssistantState = createContext<AssistantValue | null>(null);

export function useSchoolAssistantContext() { return useContext(AssistantState); }

function AccountAssistant({ user, accountKey, onNavigate, locationKey, children }: Readonly<{
  user: SessionUser; accountKey: string; onNavigate: (path: string) => void; locationKey: string; children: ReactNode;
}>) {
  const assistant = useSchoolAssistant(accountKey);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const { cancel } = assistant;
  // 页面位置变化后关闭并取消本轮助手，避免旧模块的查询范围继续显示在新页面。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 页面位置变化时关闭并取消旧助手查询，不能把旧模块范围显示在新页面。
  useEffect(() => { setDrawerOpen(false); cancel(); }, [locationKey, cancel]);
  const close = useCallback(() => { setDrawerOpen(false); cancel(); }, [cancel]);
  const value = useMemo<AssistantValue>(() => ({
    ...assistant, user,
    // 模块入口只提示当前考试；管理员发送问题后，模型才按本轮权限选择业务工具。
    open: (context) => { assistant.setContext(context); setDrawerOpen(true); },
    navigate: (path) => { close(); onNavigate(path); }
  }), [assistant, user, close, onNavigate]);
  return <AssistantState.Provider value={value}>
    {children}
    <Drawer title="学校 AI 助手" open={drawerOpen} onClose={close} width="min(620px, 100vw)" className="school-assistant-drawer" styles={{ body: { padding: 0 } }}>
      {/* 模块加载失败只影响助手抽屉；关闭它不能重载或丢弃主页面正在编辑的工作。 */}
      {drawerOpen && <ErrorBoundary fallback={<div role="alert" className="school-assistant-loading"><p>助手暂时无法加载。可以关闭助手继续当前工作，保存后刷新页面再试。</p><Button onClick={close}>关闭助手</Button></div>}>
        <Suspense fallback={<output aria-live="polite" aria-atomic="true" className="school-assistant-loading" style={{ display: "block" }}>正在加载学校 AI 助手</output>}><SchoolConversation user={user} initialContext={assistant.context} compact onNavigate={value.navigate} /></Suspense>
      </ErrorBoundary>}
    </Drawer>
  </AssistantState.Provider>;
}

export function SchoolAssistantProvider({ user, onNavigate, locationKey, children }: Readonly<{
  user: SessionUser; onNavigate: (path: string) => void; locationKey: string; children: ReactNode;
}>) {
  if (!canUseSchoolAssistant(user)) return children;
  // 身份或授权发生变化时整组内存重建，旧事实不能短暂显示给新账户或缩小后的权限范围。
  const accountKey = conversationAccountKey(user);
  return <AccountAssistant key={accountKey} user={user} accountKey={accountKey} onNavigate={onNavigate} locationKey={locationKey}>{children}</AccountAssistant>;
}

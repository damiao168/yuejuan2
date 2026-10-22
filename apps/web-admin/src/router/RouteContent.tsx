import { isValidElement, Suspense, useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { ErrorBoundary } from "../components/ErrorBoundary";
import { LoadingState } from "../components/PageState";
import { createPageReadiness, PageReadinessContext, PageVisibilityContext } from "../components/pageReadiness";

interface RouteContentProps {
  children: ReactNode;
  locationKey: string;
  title: string;
  scopeKey: string;
}

interface RouteSlot {
  id: number;
  locationKey: string;
  children: ReactNode;
  ready: boolean;
}

function samePage(left: ReactNode, right: ReactNode) {
  return isValidElement(left) && isValidElement(right) && left.type === right.type && left.key === right.key;
}

function RouteStage({ slot, hidden, disabled, onReady }: Readonly<{
  slot: RouteSlot; hidden: boolean; disabled: boolean; onReady: (id: number) => void;
}>) {
  const readiness = useMemo(createPageReadiness, []);
  useEffect(() => {
    readiness.start(() => onReady(slot.id));
    return () => readiness.stop();
  }, [readiness, onReady, slot.id]);

  return <div className={`route-content-stage${hidden ? " route-content-stage-pending" : ""}`} inert={disabled} aria-hidden={hidden || undefined}>
    <PageReadinessContext.Provider value={readiness}>
      <PageVisibilityContext.Provider value={!hidden}>
        <ErrorBoundary resetKey={slot.locationKey}>
          <Suspense fallback={<LoadingState label="正在加载页面" />}>{slot.children}</Suspense>
        </ErrorBoundary>
      </PageVisibilityContext.Provider>
    </PageReadinessContext.Provider>
  </div>;
}

function StagedRouteContent({ children, locationKey, title }: Readonly<Omit<RouteContentProps, "scopeKey">>) {
  const [state, setState] = useState(() => ({
    current: { id: 0, locationKey, children, ready: false },
    previous: null as RouteSlot | null,
    nextId: 1
  }));

  if (state.current.locationKey !== locationKey) {
    if (state.previous?.locationKey === locationKey) {
      // 返回仍在显示的页面时取消候选页，保留原节点及未提交的编辑。
      setState({ ...state, current: { ...state.previous, children }, previous: null });
    } else if (!state.previous && state.current.locationKey.split(/[?#]/, 1)[0] === locationKey.split(/[?#]/, 1)[0]
      && samePage(state.current.children, children)) {
      // 同一组件的参数变化沿用 React 原有状态，不能因查询条件变化重新挂载编辑器。
      setState({ ...state, current: { ...state.current, locationKey, children } });
    } else {
      setState({
        current: { id: state.nextId, locationKey, children, ready: false },
        previous: state.previous ?? (state.current.ready ? state.current : null),
        nextId: state.nextId + 1
      });
    }
  } else if (state.current.children !== children) {
    setState({ ...state, current: { ...state.current, children } });
  }

  const onReady = useCallback((id: number) => {
    // 连续切换时，已取消页面的晚到结果不能替换当前目标；展示后刷新也不再退回旧页。
    setState(value => value.current.id === id && !value.current.ready
      ? { ...value, current: { ...value.current, ready: true }, previous: null }
      : value);
  }, []);
  const pending = state.previous !== null;
  const slots = state.previous ? [state.previous, state.current] : [state.current];

  return <div className="route-content" aria-busy={pending}>
    {/* 最多保留显示页和候选页；候选页先挂载取数，提升时仅移除旧页，不重新挂载或重复请求。 */}
    {slots.map(slot => <RouteStage key={slot.id} slot={slot} hidden={pending && slot.id === state.current.id} disabled={pending} onReady={onReady} />)}
    {pending ? <output className="route-content-progress" aria-live="polite" aria-atomic="true">正在打开{title}</output> : null}
  </div>;
}

/** 只在同一身份、授权和工作端内保留旧画面；作用域变化时立即丢弃两个页面及其编辑状态。 */
export function RouteContent({ scopeKey, ...props }: Readonly<RouteContentProps>) {
  return <StagedRouteContent key={scopeKey} {...props} />;
}

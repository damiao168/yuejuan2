interface BeforeUnloadSubscription {
  shouldBlock: () => boolean;
}

interface BeforeUnloadState {
  subscriptions: Set<BeforeUnloadSubscription>;
  previous: Window["onbeforeunload"];
  wrapper: NonNullable<Window["onbeforeunload"]>;
  listener: (event: BeforeUnloadEvent) => void;
}

const beforeUnloadStates = new WeakMap<Window, BeforeUnloadState>();

/**
 * subscribeBeforeUnload 聚合同一窗口的卸载保护，返回幂等取消订阅函数。
 * shouldBlock 必须同步且只读：一次事件可能读取两次，异常交给事件调用方处理。
 * 旧内核还需要属性处理器的非空返回值；是否显示提示仍由浏览器决定。
 * 外部覆盖 onbeforeunload 后只保留现代取消路径，不夺回属性。
 */
export function subscribeBeforeUnload(shouldBlock: () => boolean, target: Window = window): () => void {
  let state = beforeUnloadStates.get(target);
  if (!state) {
    const subscriptions = new Set<BeforeUnloadSubscription>();
    const previous = target.onbeforeunload;
    const shouldBlockAny = () => {
      for (const subscription of subscriptions) {
        if (subscription.shouldBlock()) return true;
      }
      return false;
    };
    const wrapper: NonNullable<Window["onbeforeunload"]> = (event) => {
      // 只调用原处理器一次，并保持它的窗口 this；未取消时原样交回 false、0、空字符串等结果。
      const previousResult = previous?.call(target, event);
      if (shouldBlockAny() || event.defaultPrevented) {
        event.preventDefault();
        return "unsaved";
      }
      return previousResult;
    };
    const listener = (event: BeforeUnloadEvent) => {
      if (shouldBlockAny()) event.preventDefault();
    };
    state = { subscriptions, previous, wrapper, listener };
    beforeUnloadStates.set(target, state);
    target.onbeforeunload = wrapper;
    target.addEventListener("beforeunload", listener);
  }

  // 用独立令牌记录订阅，同一 predicate 的两次注册也必须能分别卸载。
  const subscription = { shouldBlock };
  state.subscriptions.add(subscription);
  return () => {
    if (!state.subscriptions.delete(subscription) || state.subscriptions.size > 0) return;
    target.removeEventListener("beforeunload", state.listener);
    // 只恢复仍由本模块持有的属性；外部接管后不能用旧处理器覆盖新处理器。
    if (target.onbeforeunload === state.wrapper) target.onbeforeunload = state.previous;
    beforeUnloadStates.delete(target);
  };
}

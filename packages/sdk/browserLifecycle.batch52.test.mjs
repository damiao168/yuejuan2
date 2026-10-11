import assert from "node:assert/strict";
import { test } from "node:test";
import { subscribeBeforeUnload } from "./src/browserLifecycle.ts";

// 独立 EventTarget 只检查注册、取消和属性返回值，不模拟浏览器显示卸载确认框。
class LifecycleWindow extends EventTarget {
  onbeforeunload = null;
  listeners = new Set();
  additions = 0;
  removals = 0;

  addEventListener(type, listener, options) {
    super.addEventListener(type, listener, options);
    if (type === "beforeunload") {
      this.listeners.add(listener);
      this.additions += 1;
    }
  }

  removeEventListener(type, listener, options) {
    super.removeEventListener(type, listener, options);
    if (type === "beforeunload") {
      this.listeners.delete(listener);
      this.removals += 1;
    }
  }
}

function beforeUnload() {
  return new Event("beforeunload", { cancelable: true });
}

function propertyResult(target, event = beforeUnload()) {
  return target.onbeforeunload?.call(target, event);
}

test("SSR 无 window 时模块已能加载，显式窗口也能订阅", () => {
  assert.equal(typeof globalThis.window, "undefined");
  const target = new LifecycleWindow();
  const stop = subscribeBeforeUnload(() => false, target);
  assert.equal(target.listeners.size, 1);
  stop();
});

test("同窗口只装一组保护，任一订阅当前为真就取消卸载", () => {
  const target = new LifecycleWindow();
  let dirty = false;
  let busy = false;
  const stopDirty = subscribeBeforeUnload(() => dirty, target);
  const wrapper = target.onbeforeunload;
  const stopBusy = subscribeBeforeUnload(() => busy, target);
  assert.equal(target.onbeforeunload, wrapper);
  assert.equal(target.additions, 1);
  assert.equal(propertyResult(target), undefined);
  for (const values of [[true, false], [false, true], [true, true]]) {
    [dirty, busy] = values;
    const propertyEvent = beforeUnload();
    assert.equal(propertyResult(target, propertyEvent), "unsaved");
    assert.equal(propertyEvent.defaultPrevented, true);
    const listenerEvent = beforeUnload();
    target.dispatchEvent(listenerEvent);
    assert.equal(listenerEvent.defaultPrevented, true);
  }
  dirty = false;
  busy = false;
  const saved = beforeUnload();
  target.dispatchEvent(saved);
  assert.equal(saved.defaultPrevented, false);
  stopDirty();
  stopBusy();
});

test("非后进先出取消与重复取消都不影响剩余订阅，最后才恢复原处理器", () => {
  const target = new LifecycleWindow();
  const previous = () => undefined;
  target.onbeforeunload = previous;
  const stopFirst = subscribeBeforeUnload(() => false, target);
  const wrapper = target.onbeforeunload;
  const stopSecond = subscribeBeforeUnload(() => true, target);
  stopFirst();
  stopFirst();
  assert.equal(target.onbeforeunload, wrapper);
  assert.equal(target.removals, 0);
  assert.equal(propertyResult(target), "unsaved");
  stopSecond();
  stopSecond();
  assert.equal(target.onbeforeunload, previous);
  assert.equal(target.listeners.size, 0);
  assert.equal(target.removals, 1);
});

test("同一个 predicate 注册两次仍是独立订阅", () => {
  const target = new LifecycleWindow();
  const predicate = () => true;
  const stopFirst = subscribeBeforeUnload(predicate, target);
  const stopSecond = subscribeBeforeUnload(predicate, target);
  stopFirst();
  assert.equal(propertyResult(target), "unsaved");
  assert.equal(target.listeners.size, 1);
  stopSecond();
  assert.equal(target.onbeforeunload, null);
  assert.equal(target.listeners.size, 0);
});

test("未取消时原处理器只调用一次，并保持 this、事件及各种返回值", () => {
  for (const expected of [undefined, null, false, 0, "", "previous warning"]) {
    const target = new LifecycleWindow();
    const event = beforeUnload();
    let calls = 0;
    const previous = function (received) {
      calls += 1;
      assert.equal(this, target);
      assert.equal(received, event);
      return expected;
    };
    target.onbeforeunload = previous;
    const stop = subscribeBeforeUnload(() => false, target);
    assert.equal(propertyResult(target, event), expected);
    assert.equal(calls, 1);
    assert.equal(event.defaultPrevented, false);
    stop();
    assert.equal(target.onbeforeunload, previous);
  }
});

test("订阅要拦截时也先调用原处理器一次，再返回固定非空字符串", () => {
  const target = new LifecycleWindow();
  let calls = 0;
  target.onbeforeunload = () => { calls += 1; return "previous warning"; };
  const stop = subscribeBeforeUnload(() => true, target);
  const event = beforeUnload();
  assert.equal(propertyResult(target, event), "unsaved");
  assert.equal(calls, 1);
  assert.equal(event.defaultPrevented, true);
  stop();
});

test("已有 defaultPrevented 或原处理器取消事件时保留旧内核的非空返回路径", () => {
  for (const cancelledBeforeHandler of [false, true]) {
    const target = new LifecycleWindow();
    target.onbeforeunload = (event) => { if (!cancelledBeforeHandler) event.preventDefault(); };
    const stop = subscribeBeforeUnload(() => false, target);
    const event = beforeUnload();
    if (cancelledBeforeHandler) event.preventDefault();
    assert.equal(propertyResult(target, event), "unsaved");
    assert.equal(event.defaultPrevented, true);
    stop();
  }
});

test("外部接管属性后保留现代监听，不在新增或取消订阅时夺回属性", () => {
  const target = new LifecycleWindow();
  const stopFirst = subscribeBeforeUnload(() => true, target);
  const external = () => "external warning";
  target.onbeforeunload = external;
  const stopSecond = subscribeBeforeUnload(() => false, target);
  assert.equal(target.onbeforeunload, external);
  const event = beforeUnload();
  target.dispatchEvent(event);
  assert.equal(event.defaultPrevented, true);
  stopFirst();
  assert.equal(target.onbeforeunload, external);
  stopSecond();
  assert.equal(target.onbeforeunload, external);
  assert.equal(target.listeners.size, 0);
  const saved = beforeUnload();
  target.dispatchEvent(saved);
  assert.equal(saved.defaultPrevented, false);
  const stopAgain = subscribeBeforeUnload(() => false, target);
  stopAgain();
  assert.equal(target.onbeforeunload, external);
});

test("外部清空属性也不会被清理恢复成过时处理器", () => {
  const target = new LifecycleWindow();
  target.onbeforeunload = () => "previous warning";
  const stop = subscribeBeforeUnload(() => true, target);
  target.onbeforeunload = null;
  const event = beforeUnload();
  target.dispatchEvent(event);
  assert.equal(event.defaultPrevented, true);
  stop();
  assert.equal(target.onbeforeunload, null);
});

test("不同窗口的订阅与属性恢复互不影响", () => {
  const first = new LifecycleWindow();
  const second = new LifecycleWindow();
  const secondPrevious = () => "second warning";
  second.onbeforeunload = secondPrevious;
  const stopFirst = subscribeBeforeUnload(() => true, first);
  const stopSecond = subscribeBeforeUnload(() => false, second);
  assert.equal(propertyResult(first), "unsaved");
  assert.equal(propertyResult(second), "second warning");
  stopFirst();
  assert.equal(first.onbeforeunload, null);
  assert.equal(second.listeners.size, 1);
  stopSecond();
  assert.equal(second.onbeforeunload, secondPrevious);
});

test("两个事件入口各自读取纯 predicate，不缓存旧业务状态", () => {
  const target = new LifecycleWindow();
  let reads = 0;
  const stop = subscribeBeforeUnload(() => { reads += 1; return true; }, target);
  const event = beforeUnload();
  propertyResult(target, event);
  target.dispatchEvent(event);
  assert.equal(reads, 2);
  stop();
});

test("predicate 与原处理器异常保留同步传播，不转换成拦截结果", () => {
  const predicateTarget = new LifecycleWindow();
  const predicateError = new Error("predicate failed");
  const stopPredicate = subscribeBeforeUnload(() => { throw predicateError; }, predicateTarget);
  const predicateEvent = beforeUnload();
  assert.throws(() => propertyResult(predicateTarget, predicateEvent), (error) => error === predicateError);
  // 直接调用监听器核对同步边界；真实 DOM 会自行报告事件回调异常。
  assert.throws(() => [...predicateTarget.listeners][0](predicateEvent), (error) => error === predicateError);
  assert.equal(predicateEvent.defaultPrevented, false);
  stopPredicate();

  const previousTarget = new LifecycleWindow();
  const previousError = new Error("previous failed");
  previousTarget.onbeforeunload = () => { throw previousError; };
  let reads = 0;
  const stopPrevious = subscribeBeforeUnload(() => { reads += 1; return true; }, previousTarget);
  assert.throws(() => propertyResult(previousTarget), (error) => error === previousError);
  assert.equal(reads, 0);
  stopPrevious();
});

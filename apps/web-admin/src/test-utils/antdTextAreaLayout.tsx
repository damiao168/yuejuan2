import { forwardRef, type ComponentProps, type ComponentRef } from "react";

// jsdom 没有真实排版；只关闭文本框自动测高，保留真实输入事件、AntD 表单和组件 ref。
// 保存顺序、草稿恢复等测试仍运行原组件，避免测量隐藏文本框的开销占满测试时限。
export function withFixedTextAreaLayout(antd: typeof import("antd")) {
  const OriginalInput = antd.Input;
  const OriginalTextArea = OriginalInput.TextArea;
  const TextArea = forwardRef<ComponentRef<typeof OriginalTextArea>, ComponentProps<typeof OriginalTextArea>>((props, ref) =>
    <OriginalTextArea {...props} ref={ref} autoSize={false} />);
  const Input = Object.assign(forwardRef<ComponentRef<typeof OriginalInput>, ComponentProps<typeof OriginalInput>>((props, ref) =>
    <OriginalInput {...props} ref={ref} />), OriginalInput, { TextArea });
  return { ...antd, Input };
}

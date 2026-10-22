import { cloneElement, useState, type ReactElement, type ReactNode } from "react";

export function MockTemplatePopover({ children, content, open, onOpenChange }: {
  children: ReactNode; content: ReactNode; open?: boolean; onOpenChange?: (open: boolean) => void;
}) {
  const [localOpen, setLocalOpen] = useState(false);
  const visible = open ?? localOpen;
  return <>{cloneElement(children as ReactElement<{ onClick: () => void }>, { onClick: () => {
    setLocalOpen(!visible); onOpenChange?.(!visible);
  } })}
    {visible ? <div data-template-popover>{content}</div> : null}</>;
}

export function MockTemplateSourceDropdown({ children, disabled, buttonsRender, menu }: {
  children?: ReactNode;
  disabled?: boolean;
  buttonsRender: (buttons: ReactNode[]) => ReactNode[];
  menu: { items: { key: string; label: string; disabled?: boolean; danger?: boolean }[]; onClick: (event: { key: string }) => void };
}) {
  const [open, setOpen] = useState(false);
  return <div data-source-upload-action>
    {buttonsRender([
      <button key="upload" type="button" disabled={disabled}>{children}</button>,
      <button key="menu" type="button" aria-label="答题卡文件操作" disabled={disabled} onClick={() => setOpen(value => !value)}>文件操作</button>
    ])}
    {open ? menu.items.map(item => <button key={item.key} type="button" role="menuitem" data-danger={item.danger} disabled={disabled || item.disabled}
      onClick={() => menu.onClick({ key: item.key })}>{item.label}</button>) : null}
  </div>;
}

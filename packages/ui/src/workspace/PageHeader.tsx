import type { ReactNode } from "react";

/**
 * 紧凑的业务页头。
 * 按钮和输入框由宿主应用提供，
 * 不再封装组件库的控件。
 */
export function PageHeader({
  title,
  eyebrow,
  description,
  actions,
  className
}: Readonly<{
  title: ReactNode;
  eyebrow?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  className?: string;
}>) {
  return (
    <header className={`eg-page-header ${className ?? ""}`.trim()}>
      <div className="eg-page-header-copy">
        {eyebrow ? <span>{eyebrow}</span> : null}
        <h1>{title}</h1>
        {description ? <p>{description}</p> : null}
      </div>
      {actions ? <div className="eg-page-header-actions">{actions}</div> : null}
    </header>
  );
}

import type { ReactNode } from "react";

/** 让业务表格的标题、操作和表格内容保持对齐。 */
export function DataTableShell({
  title,
  description,
  actions,
  children
}: Readonly<{
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}>) {
  return (
    <section className="eg-data-table-shell">
      <div className="eg-data-table-heading">
        <div><h2>{title}</h2>{description ? <p>{description}</p> : null}</div>
        {actions ? <div>{actions}</div> : null}
      </div>
      {children}
    </section>
  );
}

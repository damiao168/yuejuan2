import { ArrowLeft, ArrowRight } from "lucide-react";
import { useId, useRef, type KeyboardEvent, type ReactNode } from "react";
import type { TableProps } from "antd";

export function keyboardAccessibleRows<T extends object>(onRow: TableProps<T>["onRow"]): TableProps<T>["onRow"] {
  if (!onRow) return undefined;
  return (record, index) => {
    const rowProps = onRow(record, index);
    if (!rowProps.onClick) return rowProps;
    return {
      ...rowProps,
      tabIndex: rowProps.tabIndex ?? 0,
      onKeyDown: (event: KeyboardEvent<HTMLElement>) => {
        rowProps.onKeyDown?.(event);
        // 桌面表格仍保留整行操作；列内按钮由浏览器处理，不能再激活外层行。
        if (event.target !== event.currentTarget || event.defaultPrevented) return;
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        event.currentTarget.click();
      },
    };
  };
}

function scrollableTableElement(region: HTMLElement): HTMLElement {
  const candidates = [...region.querySelectorAll<HTMLElement>(".ant-table-content, .ant-table-body")];
  return candidates.reduce(
    (widest, element) => element.scrollWidth - element.clientWidth > widest.scrollWidth - widest.clientWidth ? element : widest,
    region,
  );
}

export function ScrollableTableRegion({ children }: Readonly<{ children: ReactNode }>) {
  const regionId = useId();
  const region = useRef<HTMLElement>(null);
  const scrollTable = (direction: number) => {
    if (!region.current) return;
    // AntD 有纵向滚动时使用 body，否则使用 content；操作真实溢出的容器。
    const element = scrollableTableElement(region.current);
    element.scrollBy({ left: direction * Math.max(80, element.clientWidth * 0.75), behavior: "auto" });
  };
  return (
    <>
      <div className="responsive-table-scroll-controls">
        <button type="button" aria-label="向左查看表格列" title="向左查看表格列" aria-controls={regionId} onClick={() => scrollTable(-1)}>
          <ArrowLeft size={16} aria-hidden="true" />
        </button>
        <button type="button" aria-label="向右查看表格列" title="向右查看表格列" aria-controls={regionId} onClick={() => scrollTable(1)}>
          <ArrowRight size={16} aria-hidden="true" />
        </button>
      </div>
      <section ref={region} id={regionId} className="responsive-table-region" aria-label="数据表格">
        {children}
      </section>
    </>
  );
}

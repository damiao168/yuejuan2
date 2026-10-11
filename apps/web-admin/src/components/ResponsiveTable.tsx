import { Fragment, useEffect, useMemo, useState, type Key, type ReactNode } from "react";
import { ArrowUpRight } from "lucide-react";
import { Empty, Grid, Pagination, Spin, Table, type TableColumnsType, type TableProps } from "antd";
import type { ColumnGroupType, ColumnType } from "antd/es/table";
import { primitiveDisplayValue } from "../utils/displayValue";
import { keyboardAccessibleRows, ScrollableTableRegion } from "./responsiveTableInteraction";

type ResponsiveTableProps<T extends object> = Readonly<Omit<TableProps<T>, "columns"> & {
  columns: TableColumnsType<T>;
  mobilePrimaryCount?: number;
  mobilePrimaryKeys?: string[];
}>;

function flattenColumns<T extends object>(columns: TableColumnsType<T>): ColumnType<T>[] {
  return columns.flatMap((column) => {
    const group = column as ColumnGroupType<T>;
    return group.children ? flattenColumns(group.children) : [column as ColumnType<T>];
  });
}

function valueAt(record: object, dataIndex: ColumnType<object>["dataIndex"]): unknown {
  if (dataIndex === undefined) return undefined;
  const path = Array.isArray(dataIndex) ? dataIndex : [dataIndex];
  return path.reduce<unknown>((value, part) => {
    if (!value || typeof value !== "object") return undefined;
    return (value as Record<string, unknown>)[String(part)];
  }, record);
}

function columnLabel<T extends object>(column: ColumnType<T>): ReactNode {
  return typeof column.title === "function" ? "详情" : column.title ?? "详情";
}

function renderedValue<T extends object>(column: ColumnType<T>, record: T, index: number): ReactNode {
  const raw = valueAt(record, column.dataIndex as ColumnType<object>["dataIndex"]);
  const rendered = column.render ? column.render(raw, record, index) : raw;
  if (rendered && typeof rendered === "object" && !Array.isArray(rendered) && "children" in rendered && "props" in rendered) {
    return (rendered as { children?: ReactNode }).children ?? null;
  }
  if (rendered === null || rendered === undefined || rendered === "") return <span className="muted">-</span>;
  if (typeof rendered === "string" || typeof rendered === "number" || typeof rendered === "boolean") return String(rendered);
  return rendered as ReactNode;
}

function columnIdentity<T extends object>(column: ColumnType<T>, index?: number): string {
  const value = column.key ?? column.dataIndex ?? index;
  // AntD 的复合 dataIndex 是字段路径，保留已有逗号拼接；对象不适合作为列标识。
  if (Array.isArray(value)) return value.map((part) => primitiveDisplayValue(part, "")).join(",");
  return primitiveDisplayValue(value, index === undefined ? "undefined" : String(index));
}

function recordKey<T extends object>(rowKey: TableProps<T>["rowKey"], record: T, index: number): Key {
  if (typeof rowKey === "function") return rowKey(record);
  const field = typeof rowKey === "string" ? rowKey : "key";
  const value = (record as Record<string, unknown>)[field];
  // 对象不能作为稳定行标识，否则不同记录都会变成同一个 [object Object]。
  if (typeof value === "string" || typeof value === "number" || typeof value === "bigint") return String(value);
  return index;
}

export function ResponsiveTable<T extends object>({
  columns,
  dataSource = [],
  rowKey,
  loading,
  locale,
  onRow,
  rowClassName,
  className,
  mobilePrimaryCount = 3,
  mobilePrimaryKeys,
  pagination,
  scroll,
  ...tableProps
}: ResponsiveTableProps<T>) {
  const screens = Grid.useBreakpoint();
  const mobile = !screens.lg;
  const flatColumns = useMemo(() => flattenColumns(columns), [columns]);
  const paginationConfig = pagination === false ? null : (pagination ?? {});
  const hasPagination = pagination !== false;
  const [mobilePage, setMobilePage] = useState(paginationConfig?.defaultCurrent ?? 1);
  const [mobilePageSize, setMobilePageSize] = useState(paginationConfig?.defaultPageSize ?? 10);
  const pageSize = paginationConfig?.pageSize ?? mobilePageSize;
  const total = paginationConfig?.total ?? dataSource.length;
  const currentPage = paginationConfig?.current ?? mobilePage;
  const controlledPagination = paginationConfig?.current !== undefined;
  const serverPaginated = typeof paginationConfig?.total === "number" && paginationConfig.total > dataSource.length;
  const mobileDataSource = !paginationConfig || serverPaginated
    ? dataSource
    : dataSource.slice((currentPage - 1) * pageSize, currentPage * pageSize);

  useEffect(() => {
    if (!hasPagination || controlledPagination) return;
    const lastPage = Math.max(1, Math.ceil(total / Math.max(1, pageSize)));
    setMobilePage((current) => Math.min(current, lastPage));
  }, [controlledPagination, hasPagination, pageSize, total]);

  const accessibleOnRow = keyboardAccessibleRows(onRow);

  // 选择、筛选、展开和排序依赖完整表格；移动端也不能把这些控件变成只读卡片。
  const needsFullTable = Boolean(tableProps.rowSelection || tableProps.expandable || flatColumns.some((column) => Boolean(column.sorter || column.filters || column.filterDropdown)));
  if (!mobile) {
    return (
      <section className="responsive-table-region" aria-label="数据表格">
      <Table<T>
        {...tableProps}
        columns={columns}
        dataSource={dataSource}
        rowKey={rowKey}
        loading={loading}
        locale={locale}
        onRow={accessibleOnRow}
        rowClassName={rowClassName}
        className={`responsive-desktop-table ${className ?? ""}`.trim()}
        tableLayout="fixed"
        pagination={pagination}
        scroll={scroll}
        size={tableProps.size ?? "small"}
      />
      </section>
    );
  }

  if (needsFullTable) {
    // 原生按钮提供键盘滚动入口，排序、筛选和选择仍由完整表格处理。
    return (
      <ScrollableTableRegion>
        <Table<T>
          {...tableProps}
          columns={columns}
          dataSource={dataSource}
          rowKey={rowKey}
          loading={loading}
          locale={locale}
          onRow={accessibleOnRow}
          rowClassName={rowClassName}
          className={`responsive-desktop-table ${className ?? ""}`.trim()}
          tableLayout="fixed"
          pagination={pagination}
          scroll={{ ...scroll, x: scroll?.x ?? "max-content" }}
          size={tableProps.size ?? "small"}
        />
      </ScrollableTableRegion>
    );
  }

  if (loading) {
    return <div className="responsive-table-loading"><Spin /></div>;
  }
  if (!dataSource.length) {
    const emptyText = typeof locale?.emptyText === "function" ? locale.emptyText() : locale?.emptyText;
    return <div className="responsive-table-empty">{emptyText ?? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} />}</div>;
  }

  return (
    <>
      <ul className="responsive-record-list">
      {mobileDataSource.map((record, pageIndex) => {
        const index = serverPaginated ? pageIndex : (currentPage - 1) * pageSize + pageIndex;
        const primaryColumns = flatColumns.filter((column, columnIndex) => (mobilePrimaryKeys ? mobilePrimaryKeys.includes(columnIdentity(column)) : columnIndex < mobilePrimaryCount) || columnLabel(column) === "操作");
        const detailColumns = flatColumns.filter((column) => !primaryColumns.includes(column));
        const rowProps = onRow?.(record, index) ?? {};
        const className = typeof rowClassName === "function" ? rowClassName(record, index, 0) : rowClassName;
        return (
          <li
            className={`responsive-record ${className ?? ""}`.trim()}
            key={recordKey(rowKey, record, index)}
          >
            {/* 行操作与详情、列内按钮分开；原生按钮负责 Enter/Space，避免嵌套交互与重复激活。 */}
            {rowProps.onClick ? (
              <button {...rowProps} type="button" className="responsive-record-action" aria-label={`打开第 ${index + 1} 条记录`} title="打开记录">
                <ArrowUpRight size={16} aria-hidden="true" />
              </button>
            ) : null}
            <dl>
              {primaryColumns.map((column, columnIndex) => (
                <Fragment key={columnIdentity(column, columnIndex)}>
                  <div className="responsive-record-field">
                    <dt>{columnLabel(column)}</dt>
                    <dd>{renderedValue(column, record, index)}</dd>
                  </div>
                </Fragment>
              ))}
            </dl>
            {detailColumns.length ? (
              <details className="responsive-record-details">
                <summary>查看完整信息</summary>
                <dl>
                  {detailColumns.map((column, columnIndex) => (
                    <div className="responsive-record-field" key={columnIdentity(column, columnIndex)}>
                      <dt>{columnLabel(column)}</dt>
                      <dd>{renderedValue(column, record, index)}</dd>
                    </div>
                  ))}
                </dl>
              </details>
            ) : null}
          </li>
        );
      })}
      </ul>
      {paginationConfig && total > pageSize ? (
        <Pagination
          className="responsive-record-pagination"
          current={currentPage}
          pageSize={pageSize}
          total={total}
          showSizeChanger={paginationConfig.showSizeChanger}
          showQuickJumper={paginationConfig.showQuickJumper}
          showTotal={paginationConfig.showTotal}
          pageSizeOptions={paginationConfig.pageSizeOptions}
          onChange={(page, nextPageSize) => {
            setMobilePage(page);
            setMobilePageSize(nextPageSize);
            paginationConfig.onChange?.(page, nextPageSize);
          }}
          onShowSizeChange={(page, nextPageSize) => {
            setMobilePage(page);
            setMobilePageSize(nextPageSize);
            paginationConfig.onShowSizeChange?.(page, nextPageSize);
          }}
        />
      ) : null}
    </>
  );
}

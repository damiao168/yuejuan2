import { useCallback, useEffect, useMemo, useState } from "react";
import { App as AntApp, Button, Modal, Popconfirm, Space, type TableColumnsType } from "antd";
import { Building2, Plus, RefreshCw } from "lucide-react";
import { listTenants, updateTenantStatus, type Tenant } from "../api/org";
import { ResponsiveTable } from "../components/ResponsiveTable";
import { StatusTag } from "../components/StatusTag";
import { CreateSchoolForm } from "../features/platform/schools/CreateSchoolForm";
import { isStepUpCancelledError, useStepUp } from "../auth/stepUpContext";

export function PlatformSchoolsPage() {
  const { message } = AntApp.useApp();
  const { runWithStepUp } = useStepUp();
  const [schools, setSchools] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [nextCursor, setNextCursor] = useState("");
  const [hasMore, setHasMore] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await listTenants({ limit: 50 });
      setSchools(response.tenants.filter((tenant) => tenant.code !== "platform"));
      setNextCursor(response.next_cursor);
      setHasMore(response.has_more);
    } catch {
      message.error("学校列表加载失败");
    } finally {
      setLoading(false);
    }
  }, [message]);

  const loadMore = useCallback(async () => {
    if (!hasMore || !nextCursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const response = await listTenants({ limit: 50, cursor: nextCursor });
      setSchools((current) => {
        const byId = new Map(current.map((tenant) => [tenant.id, tenant]));
        for (const tenant of response.tenants) {
          if (tenant.code !== "platform") byId.set(tenant.id, tenant);
        }
        return [...byId.values()];
      });
      setNextCursor(response.next_cursor);
      setHasMore(response.has_more);
    } catch {
      message.error("学校列表加载失败");
    } finally {
      setLoadingMore(false);
    }
  }, [hasMore, loadingMore, message, nextCursor]);

  useEffect(() => {
    void load();
  }, [load]);

  const changeStatus = useCallback(async (school: Tenant) => {
    const status = school.status === "active" ? "disabled" : "active";
    try {
      const response = await runWithStepUp({
        reason: `${status === "active" ? "启用" : "停用"}学校“${school.name}”`,
        description: "此操作会改变整个学校机构的可用状态，因此需要验证平台管理员身份。验证后将自动继续。",
        action: () => updateTenantStatus(school.id, status)
      });
      setSchools((current) => current.map((item) => item.id === school.id ? response.tenant : item));
      message.success(status === "active" ? "学校已启用" : "学校已停用");
    } catch (error) {
      if (!isStepUpCancelledError(error)) message.error("状态更新失败");
    }
  }, [message, runWithStepUp]);

  const columns = useMemo<TableColumnsType<Tenant>>(() => [
    {
      title: "学校",
      dataIndex: "name",
      key: "name",
      render: (name: string) => <strong>{name}</strong>
    },
    {
      title: "学校代码",
      dataIndex: "code",
      key: "code"
    },
    {
      title: "状态",
      dataIndex: "status",
      key: "status",
      render: (status: string) => (
        <StatusTag tone={status === "active" ? "success" : "neutral"}>
          {status === "active" ? "使用中" : "已停用"}
        </StatusTag>
      )
    },
    {
      title: "操作",
      key: "actions",
      render: (_value, school) => (
        <Popconfirm
          title={school.status === "active" ? "停用这所学校？" : "启用这所学校？"}
          description={school.status === "active" ? "停用后该学校账号将无法登录。" : undefined}
          okText="确认"
          cancelText="取消"
          onConfirm={() => void changeStatus(school)}
        >
          <Button size="small" danger={school.status === "active"}>
            {school.status === "active" ? "停用" : "启用"}
          </Button>
        </Popconfirm>
      )
    }
  ], [changeStatus]);

  return (
    <div className="page-stack">
      <section className="page-heading">
        <div>
          <h1>学校管理</h1>
          <p>创建和启停学校</p>
        </div>
        <Space>
          <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button>
          <Button type="primary" icon={<Plus size={16} />} onClick={() => setCreateOpen(true)}>新建学校</Button>
        </Space>
      </section>

      <ResponsiveTable<Tenant>
        className="dense-data-table"
        rowKey="id"
        loading={loading}
        columns={columns}
        dataSource={schools}
        pagination={false}
        locale={{ emptyText: "暂无学校" }}
      />
      {hasMore ? (
        <div className="load-more-row">
          <Button loading={loadingMore} onClick={() => void loadMore()}>加载更多学校</Button>
        </div>
      ) : null}

      <Modal
        title={<Space><Building2 size={18} />新建学校</Space>}
        open={createOpen}
        footer={null}
        onCancel={() => setCreateOpen(false)}
        destroyOnHidden
      >
        <CreateSchoolForm
          onCancel={() => setCreateOpen(false)}
          onCreated={(tenant) => {
            setSchools((current) => [tenant, ...current]);
            setCreateOpen(false);
          }}
        />
      </Modal>
    </div>
  );
}

import { useEffect, useState } from "react";
import { Alert, DatePicker, Segmented, Spin } from "antd";
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { getPlatformSchoolUsage, type SchoolUsageResponse, type UsageBreakdown, type UsageWindow } from "../../../api/platformSchools";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { numberText, tokenText } from "./schoolPresentation";

const { RangePicker } = DatePicker;

export function SchoolUsageTab({ tenantId }: { tenantId: string }) {
  const [windowValue, setWindowValue] = useState<UsageWindow | "custom">("30d");
  const [customRange, setCustomRange] = useState<{ start_date: string; end_date: string } | null>(null);
  const [data, setData] = useState<SchoolUsageResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  useEffect(() => {
    if (windowValue === "custom" && !customRange) return;
    let active = true;
    setLoading(true); setError(false);
    void getPlatformSchoolUsage(tenantId, windowValue === "custom" ? customRange ?? {} : { window: windowValue }).then((result) => { if (active) setData(result); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [tenantId, windowValue, customRange]);

  return <div className="platform-school-tab-stack">
    <div className="platform-school-usage-controls">
      <Segmented<UsageWindow | "custom"> value={windowValue} onChange={setWindowValue} options={[{label:"今天",value:"today"},{label:"7 天",value:"7d"},{label:"30 天",value:"30d"},{label:"90 天",value:"90d"},{label:"自定义",value:"custom"}]} />
      {windowValue === "custom" ? <RangePicker onChange={(dates) => setCustomRange(dates?.[0] && dates[1] ? { start_date: dates[0].format("YYYY-MM-DD"), end_date: dates[1].format("YYYY-MM-DD") } : null)} /> : null}
    </div>
    {loading ? <div className="platform-school-panel-loading"><Spin /></div> : error ? <Alert type="error" showIcon message="AI 用量加载失败" /> : data ? <>
      <div className="platform-school-usage-metrics">
        <div><span>总 Token</span><strong>{tokenText(data.summary.total_tokens)}</strong></div>
        <div><span>输入 Token</span><strong>{tokenText(data.summary.input_tokens)}</strong></div>
        <div><span>输出 Token</span><strong>{tokenText(data.summary.output_tokens)}</strong></div>
        <div><span>AI 请求</span><strong>{numberText(data.summary.requests)}</strong></div>
      </div>
      <section className="platform-school-usage-trend"><h3>Token 趋势</h3><div className="platform-school-chart">
        <ResponsiveContainer width="100%" height="100%"><LineChart data={data.trend} margin={{ top: 8, right: 12, bottom: 0, left: 0 }}>
          <CartesianGrid stroke="#e9edf2" vertical={false} /><XAxis dataKey="date" tickFormatter={(value: string) => value.slice(5)} tickLine={false} axisLine={false} fontSize={14} /><YAxis tickFormatter={(value: number) => tokenText(value)} tickLine={false} axisLine={false} fontSize={14} width={62} /><Tooltip formatter={(value) => numberText(Number(value))} /><Line type="monotone" dataKey="total_tokens" name="Token" stroke="#1769aa" strokeWidth={2} dot={false} activeDot={{ r: 4 }} /></LineChart></ResponsiveContainer>
      </div></section>
      <section><h3>按功能</h3><ResponsiveTable<UsageBreakdown> className="dense-data-table platform-school-detail-table" rowKey="key" pagination={false} dataSource={data.by_feature} columns={[{title:"功能",dataIndex:"label"},{title:"Token",dataIndex:"total_tokens",align:"right",render:tokenText},{title:"请求",dataIndex:"requests",align:"right",render:numberText},{title:"占比",dataIndex:"share",align:"right",render:(value:number)=>`${(value*100).toFixed(1)}%`}]}/></section>
      <section><h3>按模型</h3><ResponsiveTable<UsageBreakdown> className="dense-data-table platform-school-detail-table" rowKey="key" pagination={false} dataSource={data.by_model} columns={[{title:"模型",dataIndex:"label"},{title:"Token",dataIndex:"total_tokens",align:"right",render:tokenText},{title:"请求",dataIndex:"requests",align:"right",render:numberText}]}/></section>
      <p className="platform-school-readonly-note">用量来自平台调用账本；预估成本不等于供应商最终账单。</p>
    </> : <p>请选择时间范围。</p>}
  </div>;
}

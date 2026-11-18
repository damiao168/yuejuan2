import { useState } from "react";
import { Button } from "antd";
import { Copy, Download } from "lucide-react";
import "./school-membership.css";

const plans = [
  { name: "试用版", audience: "单班试点", price: "¥0", period: "30 天", seats: "3 个", papers: "500 份 / 试用期", use: "答卷采集、阅卷与成绩查看" },
  { name: "教研组版", audience: "同学科多班协作", price: "¥1,980", period: "每校 / 年", seats: "10 个", papers: "6,000 份 / 年", use: "教研组协作、成绩分析与导出" },
  { name: "学校版", audience: "全校多学科统筹", price: "¥9,800 起", period: "每校 / 年", seats: "100 个", papers: "50,000 份 / 年", use: "统一成员管理、跨学科协作与上线支持" },
] as const;

const pricingRules = [
  { title: "答卷与席位", detail: "同一学生在同一场考试、同一科目的首份成功接收答卷计 1 份。补扫、重传和复核不重复计量，接收失败不计。教师席位按启用的教师账号数计算。" },
  { title: "采购与续费", detail: "付费方案按学校年度采购，超出额度时先确认扩容与报价。续费由学校确认，学校版费用以合同为准。已有学校按现有约定执行。" },
  { title: "AI 模型费用", detail: "学校可使用自有模型 API 密钥。模型服务由对应供应商另行按用量结算，平台年费不包含这部分费用。" },
] as const;

function quoteText() {
  // 报价与页面共用同一份套餐数据，避免采购转发时把展示值写成另一套价格或额度。
  return [
    "阅卷平台 · 套餐参考报价",
    "试行参考报价，正式价格与权益以采购确认或合同为准。",
    ...plans.map(plan => `${plan.name}｜${plan.price}（${plan.period}）｜最多 ${plan.seats}教师席位｜最多 ${plan.papers}`),
    ...pricingRules.map(rule => `${rule.title}：${rule.detail}`),
  ].join("\r\n");
}

export function SchoolMembershipPage({ school }: Readonly<{ school: string }>) {
  const [copyStatus, setCopyStatus] = useState("");
  const quote = quoteText();

  async function copyQuote() {
    // 本地 HTTP 地址可能没有 Clipboard API，下载报价仍可使用。
    try {
      await navigator.clipboard.writeText(quote);
      setCopyStatus("已复制参考报价。");
    } catch {
      setCopyStatus("当前浏览器无法复制，请下载报价。");
    }
  }

  return <div className="school-membership">
    <header className="school-membership__heading">
      <div>
        <h1>会员中心</h1>
        <p>{school ? `${school} · ` : ""}学校套餐与采购</p>
      </div>
      <div className="school-membership__actions">
        <Button icon={<Copy size={15} aria-hidden="true" />} onClick={() => { void copyQuote(); }}>复制报价</Button>
        <Button type="primary" icon={<Download size={16} aria-hidden="true" />} download="阅卷平台-套餐参考报价.txt"
          href={`data:text/plain;charset=utf-8,${encodeURIComponent(`\ufeff${quote}`)}`}>
          下载报价
        </Button>
      </div>
    </header>
    {copyStatus ? <p className="school-membership__feedback" role="status">{copyStatus}</p> : null}

    <section aria-labelledby="school-membership-plans">
      <div className="school-membership__section-heading">
        <h2 id="school-membership-plans">套餐对比</h2>
        <p>试行参考报价，采购时确认正式价格与权益。</p>
      </div>
      <div className="school-membership__table-wrap" role="region" aria-labelledby="school-membership-plans" tabIndex={0}>
        <table className="school-membership__table" aria-labelledby="school-membership-plans">
          <thead>
            <tr>
              <th scope="col">套餐</th>
              {plans.map(plan => <th key={plan.name} scope="col"><strong>{plan.name}</strong><span>{plan.audience}</span></th>)}
            </tr>
          </thead>
          <tbody>
            <tr>
              <th scope="row">参考费用</th>
              {plans.map(plan => <td key={plan.name}><strong className="school-membership__price">{plan.price}</strong><span>{plan.period}</span></td>)}
            </tr>
            <tr>
              <th scope="row">教师席位上限</th>
              {plans.map(plan => <td key={plan.name}>{plan.seats}</td>)}
            </tr>
            <tr>
              <th scope="row">答卷额度上限</th>
              {plans.map(plan => <td key={plan.name}>{plan.papers}</td>)}
            </tr>
            <tr>
              <th scope="row">适用功能</th>
              {plans.map(plan => <td key={plan.name}>{plan.use}</td>)}
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section className="school-membership__rules" aria-labelledby="school-membership-billing">
      <div className="school-membership__section-heading">
        <h2 id="school-membership-billing">计量与采购</h2>
        <p>下载报价用于内部评估。开通与续费由学校线下确认。</p>
      </div>
      <dl>
        {pricingRules.map(rule => <div key={rule.title}><dt>{rule.title}</dt><dd>{rule.detail}</dd></div>)}
      </dl>
    </section>
  </div>;
}

// 平台调用账本以微美元计价；金额输入先按十进制转整数，避免二进制浮点误差改写预算。
export function costToMicros(
	value: number | string | null | undefined,
): number {
	const text = String(value ?? "").trim();
	if (!/^\d+(?:\.\d{1,6})?$/.test(text))
		throw new Error("请输入非负金额，最多保留六位小数");
	const [whole, fraction = ""] = text.split(".");
	const micros = Number(
		BigInt(whole) * 1_000_000n + BigInt(fraction.padEnd(6, "0")),
	);
	if (!Number.isSafeInteger(micros))
		throw new Error("金额超出可准确保存的范围");
	return micros;
}

export function microsToCost(value: number): string {
	if (!Number.isSafeInteger(value) || value < 0)
		throw new Error("成本数据超出可准确显示的范围");
	const whole = Math.floor(value / 1_000_000);
	const fraction = String(value % 1_000_000).padStart(6, "0");
	let end = fraction.length;
	// 只在六位小数中去掉尾零，整数部分的零仍是金额的一部分。
	while (end > 0 && fraction[end - 1] === "0") end -= 1;
	return end === 0 ? String(whole) : `${whole}.${fraction.slice(0, end)}`;
}

export function formatUSDCost(micros: number): string {
	return new Intl.NumberFormat("zh-CN", {
		style: "currency",
		currency: "USD",
		maximumFractionDigits: 6,
	}).format(micros / 1_000_000);
}

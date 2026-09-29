// 这是规则构造的可靠性信号，不是模型自报概率；需用带 Gold 标签的数据再校准。
export function reliabilitySignal(observation) {
  let signal = 1;
  if (!observation.schema_valid) signal -= 0.45;
  if (!observation.evidence_valid) signal -= 0.3;
  if (!observation.first_pass) signal -= 0.1;
  signal -= Math.max(0, 1 - Number(observation.ocr_confidence ?? 0)) * 0.25;
  if (!observation.in_scope) signal -= 0.2;
  if (!observation.rule_consistent) signal -= 0.25;
  return Math.max(0, Math.min(1, signal));
}

export function acceptableAgreement(observation, toleranceFraction = 0.1) {
  if (!Number.isFinite(observation.max_score) || observation.max_score <= 0) return false;
  const normalizedError = Math.abs(observation.model_score - observation.gold_score) / observation.max_score;
  return observation.schema_valid && observation.evidence_valid && normalizedError <= toleranceFraction;
}

export function fitIsotonicCalibration(observations) {
  if (!observations.length) throw new Error("calibration observations are required");
  const sorted = observations.map((observation) => ({
    signal: reliabilitySignal(observation),
    outcome: acceptableAgreement(observation) ? 1 : 0
  })).sort((left, right) => left.signal - right.signal);
  const blocks = [];
  for (const point of sorted) {
    const existing = blocks.at(-1);
    if (existing && existing.max_signal === point.signal) {
      existing.sum += point.outcome;
      existing.count += 1;
    } else {
      blocks.push({ min_signal: point.signal, max_signal: point.signal, sum: point.outcome, count: 1 });
    }
    // 相邻块的成功率逆序时按样本数合并，得到随信号不下降的分段常数概率。
    while (blocks.length >= 2) {
      const right = blocks.at(-1);
      const left = blocks.at(-2);
      if (left.sum / left.count <= right.sum / right.count) break;
      blocks.splice(-2, 2, {
        min_signal: left.min_signal,
        max_signal: right.max_signal,
        sum: left.sum + right.sum,
        count: left.count + right.count
      });
    }
  }
  return {
    schema_version: "isotonic-calibration-v1",
    training_records: observations.length,
    blocks: blocks.map((block) => ({
      min_signal: block.min_signal,
      max_signal: block.max_signal,
      probability: block.sum / block.count,
      count: block.count
    }))
  };
}

export function predictCalibratedProbability(model, signal) {
  if (!model?.blocks?.length) throw new Error("calibration model has no blocks");
  if (!Number.isFinite(signal)) throw new Error("calibration signal must be finite");
  if (signal < model.blocks[0].min_signal) return model.blocks[0].probability;
  for (let index = 0; index < model.blocks.length; index += 1) {
    const block = model.blocks[index];
    if (signal <= block.max_signal) return block.probability;
    const next = model.blocks[index + 1];
    // 未观测到的区间沿用左块概率，不插值制造新的校准结论。
    if (next && signal < next.min_signal) return block.probability;
  }
  return model.blocks.at(-1).probability;
}

export function evaluateCalibration(observations, model, bins = 10) {
  if (!Array.isArray(observations) || observations.length === 0) throw new Error("evaluation observations are required");
  if (!Number.isInteger(bins) || bins <= 0) throw new Error("calibration bins must be a positive integer");
  const predictions = observations.map((observation) => {
    const signal = reliabilitySignal(observation);
    return {
      observation,
      signal,
      probability: predictCalibratedProbability(model, signal),
      outcome: acceptableAgreement(observation) ? 1 : 0
    };
  });
  const buckets = Array.from({ length: bins }, () => []);
  for (const prediction of predictions) {
    const index = Math.min(bins - 1, Math.floor(prediction.probability * bins));
    buckets[index].push(prediction);
  }
  let ece = 0;
  const nonEmptyBins = [];
  for (const [index, bucket] of buckets.entries()) {
    if (!bucket.length) continue;
    const averageProbability = bucket.reduce((sum, item) => sum + item.probability, 0) / bucket.length;
    const accuracy = bucket.reduce((sum, item) => sum + item.outcome, 0) / bucket.length;
    ece += bucket.length / predictions.length * Math.abs(accuracy - averageProbability);
    nonEmptyBins.push({ bin: index, count: bucket.length, average_probability: averageProbability, empirical_accuracy: accuracy });
  }
  const brier = predictions.reduce((sum, item) => sum + (item.probability - item.outcome) ** 2, 0) / predictions.length;
  // 阈值下没有入选样本时风险未知，返回 null；不能解释成零风险。
  const coverageRisk = [0, 0.5, 0.6, 0.7, 0.8, 0.9].map((threshold) => {
    const selected = predictions.filter((item) => item.probability >= threshold);
    return {
      threshold,
      coverage: predictions.length ? selected.length / predictions.length : 0,
      risk: selected.length ? selected.filter((item) => item.outcome === 0).length / selected.length : null
    };
  });
  return { evaluation_records: predictions.length, ece, brier_score: brier, bins: nonEmptyBins, coverage_risk: coverageRisk, predictions };
}

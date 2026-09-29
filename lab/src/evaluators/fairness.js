function mean(values) {
  return values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : 0;
}

function ocrBand(value) {
  return value < 0.85 ? "low" : "high";
}

export function evaluateOperationalSlices(predictions, minimumRecords = 1) {
  // 按业务条件切片定位误差差异；这些维度不代表人口属性公平性结论。
  const dimensions = {
    subject: (item) => item.observation.subject,
    question_type: (item) => item.observation.question_type,
    ocr_band: (item) => ocrBand(item.observation.ocr_confidence),
    answer_length_band: (item) => item.observation.answer_length_band
  };
  const slices = {};
  const disparities = {};
  const insufficient = [];
  for (const [dimension, keyOf] of Object.entries(dimensions)) {
    const groups = new Map();
    for (const prediction of predictions) {
      const key = keyOf(prediction) ?? "unknown";
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(prediction);
    }
    slices[dimension] = [...groups.entries()].map(([value, records]) => {
      const normalizedErrors = records.map((item) => Math.abs(item.observation.model_score - item.observation.gold_score) / item.observation.max_score);
      const overgrades = records.map((item) => item.observation.model_score > item.observation.gold_score ? 1 : 0);
      const undergrades = records.map((item) => item.observation.model_score < item.observation.gold_score ? 1 : 0);
      const result = {
        value,
        count: records.length,
        normalized_mae: mean(normalizedErrors),
        acceptable_agreement_rate: mean(records.map((item) => item.outcome)),
        overgrade_rate: mean(overgrades),
        undergrade_rate: mean(undergrades),
        mean_calibrated_probability: mean(records.map((item) => item.probability))
      };
      if (records.length < minimumRecords) insufficient.push({ dimension, value, count: records.length, required: minimumRecords });
      return result;
    });
    // 小样本分组仍展示，但不参与组间差距；不足样本另交门禁判断。
    const eligible = slices[dimension].filter((slice) => slice.count >= minimumRecords);
    const nmaes = eligible.map((slice) => slice.normalized_mae);
    disparities[dimension] = { eligible_slices: eligible.length, normalized_mae_gap: nmaes.length >= 2 ? Math.max(...nmaes) - Math.min(...nmaes) : null };
  }
  return { dimensions: slices, disparities, insufficient_slices: insufficient };
}

export function checkCalibrationFairnessGate(calibration, fairness, level, config) {
  const gate = config[level];
  if (!gate) throw new Error(`Unknown calibration gate: ${level}`);
  const reasons = [];
  if (!Number.isInteger(calibration?.evaluation_records) || calibration.evaluation_records < gate.minimum_evaluation_records) {
    reasons.push(`evaluation_records ${calibration?.evaluation_records} < ${gate.minimum_evaluation_records}`);
  }
  if (!Number.isFinite(calibration?.ece) || calibration.ece < 0 || calibration.ece > gate.maximum_ece) {
    reasons.push(`ece ${calibration?.ece} > ${gate.maximum_ece}`);
  }
  if (!Number.isFinite(calibration?.brier_score) || calibration.brier_score < 0 || calibration.brier_score > gate.maximum_brier_score) {
    reasons.push(`brier_score ${calibration?.brier_score} > ${gate.maximum_brier_score}`);
  }
  const insufficientSlices = Array.isArray(fairness?.insufficient_slices) ? fairness.insufficient_slices : null;
  if (insufficientSlices === null) reasons.push("fairness insufficient_slices is missing");
  else if (insufficientSlices.length) reasons.push(`${insufficientSlices.length} slices are below minimum_records_per_slice ${gate.minimum_records_per_slice}`);
  const disparities = fairness?.disparities && typeof fairness.disparities === "object" ? fairness.disparities : null;
  if (disparities === null) reasons.push("fairness disparities are missing");
  for (const dimension of ["subject", "question_type", "ocr_band", "answer_length_band"]) {
    if (disparities !== null && !(dimension in disparities)) reasons.push(`${dimension} disparity is missing`);
  }
  for (const [dimension, disparity] of Object.entries(disparities ?? {})) {
    if (disparity?.normalized_mae_gap !== null && !Number.isFinite(disparity?.normalized_mae_gap)) {
      reasons.push(`${dimension}.normalized_mae_gap is not finite`);
      continue;
    }
    if (disparity.normalized_mae_gap !== null && disparity.normalized_mae_gap > gate.maximum_slice_nmae_gap) {
      reasons.push(`${dimension}.normalized_mae_gap ${disparity.normalized_mae_gap} > ${gate.maximum_slice_nmae_gap}`);
    }
  }
  return { level, passed: reasons.length === 0, reasons };
}

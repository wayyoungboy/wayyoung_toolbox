const clamp = (value, minimum, maximum) => Math.min(maximum, Math.max(minimum, value));

export function normalizeCurvePoints(points) {
  return (Array.isArray(points) ? points : [])
    .map((point) => ({
      temperature_c: clamp(Number(point.temperature_c) || 0, 0, 120),
      duty_percent: clamp(Number(point.duty_percent) || 0, 0, 100),
    }))
    .sort((left, right) => left.temperature_c - right.temperature_c);
}

export function calculateDutyPercent(points, temperature) {
  const normalized = normalizeCurvePoints(points);
  if (!normalized.length) return 100;
  if (temperature <= normalized[0].temperature_c) return Math.round(normalized[0].duty_percent);
  const last = normalized.at(-1);
  if (temperature >= last.temperature_c) return Math.round(last.duty_percent);

  for (let index = 1; index < normalized.length; index += 1) {
    const upper = normalized[index];
    const lower = normalized[index - 1];
    if (temperature <= upper.temperature_c) {
      const span = upper.temperature_c - lower.temperature_c;
      if (span <= 0) return Math.round(upper.duty_percent);
      const ratio = (temperature - lower.temperature_c) / span;
      return Math.round(lower.duty_percent + ratio * (upper.duty_percent - lower.duty_percent));
    }
  }
  return Math.round(last.duty_percent);
}

export function policySectionsForMode(mode) {
  return {
    controlled: mode === "curve" || mode === "fixed",
    curve: mode === "curve",
    fixed: mode === "fixed",
  };
}

export function shouldRefreshFanEditor({ selected, dirty }) {
  return Boolean(selected) && !dirty;
}

export function temperatureSourceOptions(sources, selected) {
  const options = (Array.isArray(sources) ? sources : []).map((source) => {
    const temperature = Number(source.temperature_c);
    const available = Boolean(source.available) && Number.isFinite(temperature);
    const prefix = source.kind === "disk" ? `硬盘 · ${source.label}` : source.label || "CPU Package";
    return {
      value: source.id,
      label: available ? `${prefix} · ${temperature.toFixed(1)}°C` : `${prefix} · 温度不可用`,
      currentTemperature: available ? temperature : null,
      available,
    };
  });
  if (!options.some((option) => option.value === "cpu")) {
    options.unshift({value: "cpu", label: "CPU Package · 温度不可用", currentTemperature: null, available: false});
  }
  if (selected && !options.some((option) => option.value === selected)) {
    options.push({
      value: selected,
      label: selected.startsWith("disk:") ? "硬盘 · 已保存的硬盘（当前未找到或温度不可用）" : "已保存的温度源（当前不可用）",
      currentTemperature: null,
      available: false,
    });
  }
  return options;
}

export function prepareCurveForSubmission(points) {
  const normalized = normalizeCurvePoints(points);
  for (let index = 1; index < normalized.length; index += 1) {
    if (normalized[index].temperature_c === normalized[index - 1].temperature_c) {
      throw new Error("温度节点不能重复");
    }
  }
  return normalized;
}

function linePath(points) {
  return points.map((point, index) => `${index ? "L" : "M"}${point.x.toFixed(1)} ${point.y.toFixed(1)}`).join(" ");
}

export function buildFanCurveChart({ points, currentTemperature, criticalTemperature, sourceLabel = "CPU 温度" }) {
  const normalized = normalizeCurvePoints(points);
  if (!normalized.length) return '<div class="curve-empty">请至少设置一个曲线节点</div>';

  const width = 820;
  const height = 310;
  const plot = { left: 64, right: 792, top: 28, bottom: 246 };
  const xMinimum = 20;
  const xMaximum = Math.max(100, criticalTemperature || 0, normalized.at(-1).temperature_c);
  const x = (temperature) => plot.left + ((clamp(temperature, xMinimum, xMaximum) - xMinimum) / (xMaximum - xMinimum)) * (plot.right - plot.left);
  const y = (duty) => plot.bottom - (clamp(duty, 0, 100) / 100) * (plot.bottom - plot.top);
  const chartPoints = [
    { temperature_c: xMinimum, duty_percent: normalized[0].duty_percent },
    ...normalized,
    { temperature_c: xMaximum, duty_percent: normalized.at(-1).duty_percent },
  ].map((point) => ({ ...point, x: x(point.temperature_c), y: y(point.duty_percent) }));
  const path = linePath(chartPoints);
  const area = `${path} L${plot.right} ${plot.bottom} L${plot.left} ${plot.bottom} Z`;
  const xTicks = [20, 40, 60, 80, 100].filter((tick) => tick <= xMaximum);
  if (!xTicks.includes(xMaximum)) xTicks.push(xMaximum);
  const yTicks = [0, 25, 50, 75, 100];
  const current = typeof currentTemperature === "number" ? currentTemperature : Number.NaN;
	const hasCurrent = Number.isFinite(current);
  const currentDuty = hasCurrent ? calculateDutyPercent(normalized, current) : 0;
  const critical = clamp(Number(criticalTemperature) || 80, xMinimum, xMaximum);

  return `<svg class="fan-curve-svg" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="fan-curve-title fan-curve-description">
    <title id="fan-curve-title">PWM 风扇温控曲线</title>
    <desc id="fan-curve-description">横轴为${sourceLabel}，纵轴为 PWM 占空比，并标出当前温度与临界温度。</desc>
    <text class="axis-title" x="${plot.left}" y="16">PWM 占空比</text>
    ${yTicks.map((tick) => `<g class="grid-line"><line x1="${plot.left}" y1="${y(tick)}" x2="${plot.right}" y2="${y(tick)}"></line><text x="${plot.left - 12}" y="${y(tick) + 4}" text-anchor="end">${tick}%</text></g>`).join("")}
    ${xTicks.map((tick) => `<text class="axis-tick" x="${x(tick)}" y="${plot.bottom + 25}" text-anchor="middle">${tick}°C</text>`).join("")}
    <path class="curve-area" d="${area}"></path>
    <path class="curve-line" d="${path}"></path>
    <g class="critical-marker"><line x1="${x(critical)}" y1="${plot.top}" x2="${x(critical)}" y2="${plot.bottom}"></line><text x="${x(critical) - 8}" y="${plot.top + 14}" text-anchor="end">临界 ${Math.round(critical)}°C</text></g>
    ${normalized.map((point) => `<g class="curve-node"><circle cx="${x(point.temperature_c)}" cy="${y(point.duty_percent)}" r="5"></circle><text x="${x(point.temperature_c)}" y="${y(point.duty_percent) - 12}" text-anchor="middle">${Math.round(point.temperature_c)}°C · ${Math.round(point.duty_percent)}%</text></g>`).join("")}
    ${hasCurrent ? `<g class="current-marker"><line x1="${x(current)}" y1="${plot.top}" x2="${x(current)}" y2="${plot.bottom}"></line><circle cx="${x(current)}" cy="${y(currentDuty)}" r="6"></circle><text x="${x(current) + 10}" y="${Math.max(plot.top + 18, y(currentDuty) - 12)}">当前 ${current.toFixed(1)}°C · ${currentDuty}%</text></g>` : ""}
    <text class="axis-title" x="${plot.right}" y="${height - 8}" text-anchor="end">${sourceLabel}</text>
  </svg>`;
}

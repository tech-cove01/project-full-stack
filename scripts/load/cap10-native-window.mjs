// Pure k6 scenario-window validation shared by the two CAP10 helpers.
export function nativeScenarioWindow({ metric, durationMetric, seconds, wrapperStartedAt, wrapperEndedAt, exit, externalStop, metrics = {} }) {
  const valid = value => Number.isSafeInteger(value) && value >= 0
  if (!Number.isInteger(seconds) || seconds < 1 || seconds > 180 ||
      !valid(wrapperStartedAt) || !valid(wrapperEndedAt) || wrapperEndedAt < wrapperStartedAt ||
      !metric || !valid(metric.min) || metric.min !== metric.max ||
      metric.min < wrapperStartedAt || metric.min > wrapperEndedAt ||
      !durationMetric || durationMetric.min !== seconds * 1000 || durationMetric.max !== seconds * 1000) {
    throw new Error('missing or inconsistent native scenario timing evidence')
  }
  const startedAt = metric.min, durationMs = durationMetric.min, scheduledEndedAt = startedAt + durationMs
  const failedThresholdMetrics = Object.entries(metrics).filter(([, value]) =>
    value.thresholds && Object.values(value.thresholds).some(failed => failed === true)).map(([name]) => name)
  const onlyTerminalLatencyFailure = exit === 99 && failedThresholdMetrics.length > 0 &&
    failedThresholdMetrics.every(name => name === 'cap_like_all_ms')
  return { startedAt, durationMs, scheduledEndedAt,
    complete: (exit === 0 || onlyTerminalLatencyFailure) && externalStop == null && wrapperEndedAt >= scheduledEndedAt,
    onlyTerminalLatencyFailure, failedThresholdMetrics,
    startMetric: { min: metric.min, max: metric.max },
    durationMetric: { min: durationMetric.min, max: durationMetric.max },
    scope: 'k6 scenario.startTime plus configured offer duration. Latency thresholds never abort CAP10: exit99 with only terminal latency failures may cover a complete window. Safety/correctness/external stops never establish completion. Scheduled end is not an exact stop timestamp for incomplete phases.' }
}

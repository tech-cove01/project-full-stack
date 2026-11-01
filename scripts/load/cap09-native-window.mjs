// Pure k6 scenario-window validation shared by the two CAP09 helpers.
export function nativeScenarioWindow({ metric, durationMetric, seconds, wrapperStartedAt, wrapperEndedAt, exit, externalStop }) {
  const valid = value => Number.isSafeInteger(value) && value >= 0
  if (!Number.isInteger(seconds) || seconds < 1 || seconds > 180 ||
      !valid(wrapperStartedAt) || !valid(wrapperEndedAt) || wrapperEndedAt < wrapperStartedAt ||
      !metric || !valid(metric.min) || metric.min !== metric.max ||
      metric.min < wrapperStartedAt || metric.min > wrapperEndedAt ||
      !durationMetric || durationMetric.min !== seconds * 1000 || durationMetric.max !== seconds * 1000) {
    throw new Error('missing or inconsistent native scenario timing evidence')
  }
  const startedAt = metric.min, durationMs = durationMetric.min, scheduledEndedAt = startedAt + durationMs
  return { startedAt, durationMs, scheduledEndedAt,
    complete: exit === 0 && externalStop == null && wrapperEndedAt >= scheduledEndedAt,
    startMetric: { min: metric.min, max: metric.max },
    durationMetric: { min: durationMetric.min, max: durationMetric.max },
    scope: 'k6 scenario.startTime plus configured native offer duration. Complete requires successful unsignalled process coverage; failed/early-stop stages retain scheduled end without claiming actual completion or an exact stop timestamp.' }
}

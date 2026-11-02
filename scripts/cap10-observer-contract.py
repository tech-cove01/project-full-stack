"""Pure CAP10 observer policy; no runtime, SQL, service or admission actions."""

FULL_HISTORY = 'full-history-v1'
LIKE_VECTOR = 'like-vector-v1'
AUDIT_SCOPE = 'full-snapshot-audit-only'
LOAD_BUDGET_MS = 4000
READ_TIMEOUT_MS = 3000
CADENCE_SECONDS = 5
NATIVE_RECOVERY_SECONDS = 60


def _policy(mode):
    if type(mode) is not str or mode not in (FULL_HISTORY, LIKE_VECTOR):
        raise ValueError('explicit supported observer mode required')
    return {'mode': mode, 'fullAuditTimeoutMs': 30000 if mode == FULL_HISTORY else 120000,
            'scope': AUDIT_SCOPE}


def environment_policy(environment):
    if type(environment) is not dict:
        raise ValueError('observer environment object required')
    return _policy(environment.get('CAP10_OBSERVER_MODE', FULL_HISTORY))


def _checked_policy(value):
    if type(value) is not dict or set(value) != {'mode', 'fullAuditTimeoutMs', 'scope'}:
        raise ValueError('exact observer policy fields required')
    expected = _policy(value['mode'])
    if (type(value['fullAuditTimeoutMs']) is not int or type(value['scope']) is not str
            or value != expected):
        raise ValueError('fixed observer audit budget and scope required')
    return expected


def metadata_policy(metadata, expected_policy, *, allow_missing_legacy=False):
    expected = _checked_policy(expected_policy)
    if type(metadata) is not dict or type(allow_missing_legacy) is not bool:
        raise ValueError('metadata object and explicit legacy compatibility flag required')
    if 'observerPolicy' not in metadata:
        if allow_missing_legacy and expected['mode'] == FULL_HISTORY:
            return expected
        raise ValueError('observer policy missing from metadata')
    actual = _checked_policy(metadata['observerPolicy'])
    if actual != expected:
        raise ValueError('metadata observer policy differs from selected environment')
    return actual


def sampling_limits():
    # These describe existing limits for future wiring; no consumer is changed.
    return {'loadBudgetMs': LOAD_BUDGET_MS, 'readTimeoutMs': READ_TIMEOUT_MS,
            'cadenceSeconds': CADENCE_SECONDS, 'nativeRecoverySeconds': NATIVE_RECOVERY_SECONDS}

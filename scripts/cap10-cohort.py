"""Bounded CAP10 cohort dimensions; no filesystem, network or business actions.

Historical manifests and commands without poolSize retain their 512-slot
meaning. New dimensions must be explicit; a current deployment never redefines
the cardinality of an old fixture or recovery proof.
"""

DEFAULT_POOL_SIZE = 512
DEFAULT_ENGINES = 2
MAX_POOL_SIZE = 4096
MAX_ENGINES = 16


def pool_size(value=DEFAULT_POOL_SIZE):
    if type(value) is not int or not 1 <= value <= MAX_POOL_SIZE:
        raise ValueError('bounded integer relationship pool size required')
    return value


def engine_count(value=DEFAULT_ENGINES, *, pool=DEFAULT_POOL_SIZE):
    pool_size(pool)
    if type(value) is not int or not 1 <= value <= min(MAX_ENGINES, pool):
        raise ValueError('bounded integer engine count required')
    return value


def pts_concurrency(pool=DEFAULT_POOL_SIZE, engines=DEFAULT_ENGINES):
    pool_size(pool); engine_count(engines, pool=pool)
    # Retain the exact old PTS identity. Larger cohorts reserve enough normal
    # workers for uneven CSV shards; surplus EOF workers send no requests.
    return 513 if (pool, engines) == (512, 2) else pool + engines


def fixture_pool_size(manifest):
    if not isinstance(manifest, dict):
        raise ValueError('fixture object required')
    count = pool_size(manifest.get('poolSize', DEFAULT_POOL_SIZE))
    for field in ('users', 'multiNotes', 'multiRelations'):
        if field in manifest and (type(manifest[field]) is not int or manifest[field] != count):
            raise ValueError('fixture declared counts differ from its relationship pool')
    for field in ('userIds', 'multiNoteIds'):
        if field in manifest and (not isinstance(manifest[field], list) or len(manifest[field]) != count):
            raise ValueError('fixture identity arrays differ from its relationship pool')
    return count


def command_pool_size(command, fixture=None):
    if not isinstance(command, dict):
        raise ValueError('reserved command object required')
    count = pool_size(command.get('poolSize', DEFAULT_POOL_SIZE))
    if fixture is not None and fixture_pool_size(fixture) != count:
        raise ValueError('reserved command differs from its original fixture pool')
    return count


def dimensions(configuration, fixture):
    if not isinstance(configuration, dict):
        raise ValueError('collector configuration object required')
    count = fixture_pool_size(fixture)
    if pool_size(configuration.get('poolSize', DEFAULT_POOL_SIZE)) != count:
        raise ValueError('collector pool differs from its bound fixture')
    engines = engine_count(configuration.get('expectedEngines', DEFAULT_ENGINES), pool=count)
    concurrency = pts_concurrency(count, engines)
    value = configuration.get('ptsConcurrency', concurrency)
    if type(value) is not int or value != concurrency:
        raise ValueError('PTS concurrency differs from the declared cohort dimensions')
    return {'poolSize': count, 'expectedEngines': engines, 'ptsConcurrency': concurrency}

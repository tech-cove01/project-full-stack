import groovy.json.JsonSlurper

// Pure contract shared by the uploaded JSR223 script and offline checks.
class Cap10PTSContract {
    static final long MAX_SAFE = 9007199254740991L
    static final int MAX_FRAME_RECORDS = 128
    static String clockProtocol(Map config) {
        def value = config.containsKey('clockProtocol') ? config.clockProtocol : 'embedded-v1'
        if (!(value instanceof String) || !(value in ['embedded-v1','dedicated-v1'])) throw new IllegalArgumentException('invalid clock protocol')
        value
    }
    static String preheatProtocol(Map config) {
        def value = config.containsKey('preheatProtocol') ? config.preheatProtocol : 'none'
        if (!(value instanceof String) || !(value in ['none','relation-noop-v1'])) throw new IllegalArgumentException('invalid preparation protocol')
        value
    }
    static Map classifyPreheat(int status, String text, String noteId, Map previous) {
        Map outcome = classify(status,text,noteId,previous,previous.liked as boolean)
        def likes = null
        try { def value = new JsonSlurper().parseText(text)?.data?.likes; if (integer(value)) likes = value.longValue() }
        catch (Exception ignored) { }
        if (outcome.kind == 'noop' && likes != (previous.liked ? 1L : 0L)) outcome.kind = 'invalid'
        [observedLiked:null,observedVersion:null,observedNoteId:null] + outcome + [observedLikes:likes]
    }
    static List framePrefix(List pending, int remaining) {
        if (remaining < 0 || remaining > pending.size()) throw new IllegalArgumentException('invalid pending frame snapshot')
        new ArrayList(pending.subList(0, Math.min(remaining, MAX_FRAME_RECORDS)))
    }
    static int poolSize(Object value = 512) {
        if (!(value instanceof Integer || value instanceof Long || value instanceof BigInteger) || value < 1 || value > 4096) throw new IllegalArgumentException('bounded integer relationship pool size required')
        value.intValue()
    }
    static int engineCount(Object value = 2, int pool = 512) {
        poolSize(pool)
        if (!(value instanceof Integer || value instanceof Long || value instanceof BigInteger) || value < 1 || value > Math.min(16, pool)) throw new IllegalArgumentException('bounded integer engine count required')
        value.intValue()
    }
    static int ptsConcurrency(int pool = 512, int engines = 2) {
        poolSize(pool); engineCount(engines, pool)
        pool == 512 && engines == 2 ? 513 : pool + engines
    }
    static Map dimensions(Map config) {
        int pool = poolSize(config.containsKey('poolSize') ? config.poolSize : 512)
        int engines = engineCount(config.containsKey('expectedEngines') ? config.expectedEngines : 2, pool)
        int concurrency = ptsConcurrency(pool, engines)
        if (config.containsKey('ptsConcurrency') && (!(config.ptsConcurrency instanceof Integer || config.ptsConcurrency instanceof Long || config.ptsConcurrency instanceof BigInteger) || config.ptsConcurrency != concurrency)) throw new IllegalArgumentException('PTS concurrency differs from cohort dimensions')
        [poolSize:pool, expectedEngines:engines, ptsConcurrency:concurrency]
    }
    static boolean integer(Object n) {
        n instanceof Number && !(n instanceof Float) && !(n instanceof Double) &&
            new BigDecimal(n.toString()).stripTrailingZeros().scale() <= 0 && n >= 0 && n <= MAX_SAFE
    }
    static Map classify(int status, String text, String noteId, Map previous, boolean wanted) {
        if (status == 0 || status >= 500) return [kind:'uncertain']
        if (status != 200) return [kind:'rejected']
        def body = null
        try { body = new JsonSlurper().parseText(text) } catch (Exception ignored) { return [kind:'invalid'] }
        def data = body instanceof Map && body.success == true && body.data instanceof Map ? body.data : null
        def valid = data && data.noteId instanceof String && data.noteId == noteId && data.liked instanceof Boolean &&
            integer(data.version) && integer(data.likes)
        def observed = [observedLiked:data?.liked instanceof Boolean ? data.liked : null,
            observedVersion:integer(data?.version) ? data.version.longValue() : null,
            observedNoteId:data?.noteId instanceof String ? data.noteId : null]
        if (!valid || data.liked != wanted) return observed + [kind:'invalid']
        if (data.version == previous.version + 1 && wanted != previous.liked) return observed + [kind:'mutation']
        if (data.version == previous.version) return observed + [kind:'noop']
        observed + [kind:'invalid']
    }
    static long ordinal(int slot, long iteration, int pool = 512) {
        poolSize(pool)
        if (slot < 0 || slot >= pool || iteration < 0) throw new IllegalArgumentException('invalid slot/iteration')
        Math.addExact(slot as long, Math.multiplyExact(pool as long, iteration))
    }
    static long dueNano(long startNano, long ordinal, int rate) {
        if (rate < 1 || rate > 5000 || ordinal < 0) throw new IllegalArgumentException('invalid rate/ordinal')
        startNano + Math.floorDiv(Math.multiplyExact(ordinal, 1000000000L), rate as long)
    }
    static long preparationNano(long startNano, long ordinal, int rate) {
        dueNano(startNano, ordinal, rate) - 100000000L
    }
    static long handoffNano(long deadline) {
        long now = System.nanoTime()
        while (now < deadline) {
            java.util.concurrent.locks.LockSupport.parkNanos(Math.min(50000000L, deadline-now))
            now = System.nanoTime()
        }
        now
    }
    static int launchGuardMs(int seconds) {
        if (!(seconds in [10,60,180])) throw new IllegalArgumentException('invalid native duration')
        seconds == 10 ? 0 : 100
    }
    static long plannedTotal(int rate, int seconds) {
        if (rate < 1 || rate > 5000) throw new IllegalArgumentException('invalid rate')
        Math.floorDiv(Math.multiplyExact(rate as long, seconds * 1000L - launchGuardMs(seconds)), 1000L)
    }
    static long planned(Collection<Integer> slots, int rate, int seconds, int pool = 512) {
        poolSize(pool)
        if (slots.toSet().size() != slots.size() || slots.any { !(it instanceof Integer) || it < 0 || it >= pool }) throw new IllegalArgumentException('invalid exclusive slot shard')
        long total = plannedTotal(rate, seconds)
        slots.sum(0L) { int slot -> slot >= total ? 0L : 1L + Math.floorDiv(total - 1L - slot, pool as long) } as long
    }
}

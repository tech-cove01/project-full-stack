import groovy.json.JsonSlurper

// Pure contract shared by the uploaded JSR223 script and offline checks.
class Cap10PTSContract {
    static final long MAX_SAFE = 9007199254740991L
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
    static long ordinal(int slot, long iteration) {
        if (slot < 0 || slot >= 512 || iteration < 0) throw new IllegalArgumentException('invalid slot/iteration')
        Math.addExact(slot as long, Math.multiplyExact(512L, iteration))
    }
    static long dueNano(long startNano, long ordinal, int rate) {
        if (rate < 1 || rate > 5000 || ordinal < 0) throw new IllegalArgumentException('invalid rate/ordinal')
        startNano + Math.floorDiv(Math.multiplyExact(ordinal, 1000000000L), rate as long)
    }
    static long planned(Collection<Integer> slots, int rate, int seconds) {
        long total = (long)rate * seconds
        slots.sum(0L) { int slot -> slot >= total ? 0L : 1L + Math.floorDiv(total - 1L - slot, 512L) } as long
    }
}

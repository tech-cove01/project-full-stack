import groovy.json.JsonOutput
import groovy.json.JsonSlurper
import java.lang.management.ManagementFactory
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.locks.LockSupport
import org.apache.jmeter.services.FileServer

class Cap10PTSRun {
    final String engineId = UUID.randomUUID().toString()
    final long resourceOriginNano = System.nanoTime()
    final String collectorUrl, controlToken, scriptHash
    final List<Integer> slots
    final Map<Integer,Map> relations = new ConcurrentHashMap<Integer,Map>()
    final Set<Integer> bound = Collections.newSetFromMap(new ConcurrentHashMap<Integer,Boolean>())
    final Set<Integer> done = Collections.newSetFromMap(new ConcurrentHashMap<Integer,Boolean>())
    final AtomicBoolean stop = new AtomicBoolean(false)
    final AtomicLong attempted = new AtomicLong(), completed = new AtomicLong(), inFlight = new AtomicLong()
    final Map<String,AtomicLong> counts = ['mutation','noop','uncertain','rejected','invalid','dropped'].collectEntries { [(it):new AtomicLong()] }
    final List<Map> pending = []
    final Object io = new Object(), recordsLock = new Object()
    volatile Map config, clock
    volatile boolean anchorFrozen = false, terminalAck = false
    volatile long anchorNano, anchorServerMs, startNano = Long.MIN_VALUE
    volatile int readyReported = -1
    long seq = 0
    Thread reporter

    Cap10PTSRun(String hash) {
        scriptHash = hash
        def file = FileServer.getFileServer().getResolvedFile('cap10-pts-private.json')
        def secret
        try { secret = new JsonSlurper().parse(file, 'UTF-8') } catch (Exception ignored) { throw new IllegalStateException('private collector configuration invalid') }
        if (!(secret instanceof Map) || secret.keySet() != ['collectorUrl','controlToken'] as Set ||
            !(secret.controlToken instanceof String) || secret.controlToken.size() < 24) throw new IllegalStateException('private collector configuration invalid')
        def uri = new URI(secret.collectorUrl.toString())
        if (uri.scheme != 'http' || uri.userInfo || uri.fragment || uri.query || !(uri.host ==~ /(?:127\.0\.0\.1|10\.\d+\.\d+\.\d+|192\.168\.\d+\.\d+|172\.(?:1[6-9]|2\d|3[01])\.\d+\.\d+)/)) throw new IllegalStateException('collector must be explicit loopback/private IPv4 HTTP')
        collectorUrl = secret.collectorUrl.toString().replaceAll('/+$',''); controlToken = secret.controlToken
        def lines = FileServer.getFileServer().getResolvedFile('slots.csv').readLines('UTF-8').findAll { it.trim() }
        if (lines && lines[0].trim() == 'slot') lines = lines.drop(1)
        slots = lines.collect { if (!(it.trim() ==~ /\d{1,3}/)) throw new IllegalStateException('invalid slot CSV'); it.trim().toInteger() }
        if (!slots || slots.size() > 512 || slots.toSet().size() != slots.size() || slots.any { it < 0 || it >= 512 }) throw new IllegalStateException('invalid exclusive slot shard')
        for (int attempt=0; attempt<3; attempt++) {
            try { control(); break }
            catch (IllegalStateException failure) {
                if (failure.message != 'collector clock uncertainty invalid' || attempt == 2) throw failure
            }
        }
        def registered = request('/register',[runId:config.runId, engineId:engineId, slots:slots, readyWorkers:0, scriptSHA256:scriptHash, fixtureSHA256:config.fixtureSHA256])
        registered.relations.each { r ->
            if (!(r.slot in slots) || relations.containsKey(r.slot as int) || !(r.userId ==~ /[1-9]\d*/) || !(r.noteId ==~ /[1-9]\d*/) || !(r.active in [0,1]) || !Cap10PTSContract.integer(r.version) || !(r.token instanceof String) || !r.token) throw new IllegalStateException('private relation shard invalid')
            relations[r.slot as int] = [slot:r.slot as int, userId:r.userId, noteId:r.noteId, liked:r.active == 1, version:r.version as long, token:r.token]
        }
        if (relations.keySet() != slots.toSet()) throw new IllegalStateException('private relation coverage differs')
        reporter = new Thread({ reportLoop() } as Runnable, 'cap10-telemetry')
        reporter.daemon = true; reporter.start()
    }
    Map request(String path, Map body = null) {
        synchronized (io) {
            long beforeNano = System.nanoTime(), beforeWall = System.currentTimeMillis()
            HttpURLConnection c = (HttpURLConnection)new URL(collectorUrl + path).openConnection()
            c.instanceFollowRedirects = false; c.connectTimeout = 5000; c.readTimeout = 5000
            c.setRequestProperty('Authorization','Bearer ' + controlToken)
            try {
                if (body != null) { c.requestMethod = 'POST'; c.doOutput = true; c.setRequestProperty('Content-Type','application/json'); c.outputStream.withCloseable { it.write(JsonOutput.toJson(body).getBytes('UTF-8')) } }
                if (c.responseCode != 200) throw new IllegalStateException('collector rejected request')
                ByteArrayOutputStream bytes = new ByteArrayOutputStream()
                c.inputStream.withCloseable { input -> byte[] buffer = new byte[8192]; int n; while ((n = input.read(buffer)) != -1) { if (bytes.size() + n > 2097152) throw new IllegalStateException('collector response exceeds bound'); bytes.write(buffer,0,n) } }
                long afterNano = System.nanoTime(), afterWall = System.currentTimeMillis()
                Map result = (Map)new JsonSlurper().parseText(bytes.toString('UTF-8'))
                if (result.serverUnixMs instanceof Number) {
                    double rtt = (afterNano-beforeNano)/1000000d, uncertainty = rtt/2d + 1d
                    if (uncertainty > 50d || Math.abs((afterWall-beforeWall) - rtt) > 50d) {
                        System.err.println('CAP10 rejected clock sample rttMs='+rtt+' uncertaintyMs='+uncertainty)
                        throw new IllegalStateException('collector clock uncertainty invalid')
                    }
                    long middle = beforeNano + Math.floorDiv(afterNano-beforeNano,2L)
                    if (anchorFrozen && Math.abs(serverAt(middle) - result.serverUnixMs.doubleValue()) > clock.uncertaintyMs + uncertainty + 2d) throw new IllegalStateException('collector clock interval disagrees')
                    if (!anchorFrozen && (clock == null || uncertainty < clock.uncertaintyMs)) {
                        anchorNano = middle; anchorServerMs = result.serverUnixMs.longValue()
                        clock = [uncertaintyMs:uncertainty, offsetMs:result.serverUnixMs.doubleValue()-(beforeWall+afterWall)/2d, roundTripMs:rtt]
                    }
                }
                if (result.stop == true) stop.set(true)
                return result
            } finally { c.disconnect() }
        }
    }
    double serverAt(long nano) { anchorServerMs + (nano-anchorNano)/1000000d }
    void control() {
        Map c = request('/control')
        if (c.version != 1 || !(c.runId instanceof String) || !(c.phase in ['warm','stable']) ||
            !(c.rate instanceof Integer) || c.rate < 1 || c.rate > 5000 || !(c.seconds in (c.phase == 'warm' ? [10] : [60,180])) ||
            c.jitterBudgetMs != 50 || c.clockUncertaintyMaxMs != 50 || c.scriptSHA256 != scriptHash || !(c.fixtureSHA256 ==~ /[a-f0-9]{64}/)) throw new IllegalStateException('collector phase contract differs')
        if (config != null && [c.runId,c.phase,c.rate,c.seconds,c.fixtureSHA256] != [config.runId,config.phase,config.rate,config.seconds,config.fixtureSHA256]) throw new IllegalStateException('phase binding changed')
        def api = new URI(c.apiUrl.toString())
        if (api.scheme != 'http' || api.userInfo || api.fragment || api.query || !(api.path in ['', '/']) ||
            !(api.host ==~ /(?:127\.0\.0\.1|10\.\d+\.\d+\.\d+|192\.168\.\d+\.\d+|172\.(?:1[6-9]|2\d|3[01])\.\d+\.\d+)/)) throw new IllegalStateException('private API endpoint invalid')
        if (config != null && c.apiUrl != config.apiUrl) throw new IllegalStateException('API endpoint changed')
        config = c
        if (c.startUnixMs != null && startNano == Long.MIN_VALUE) {
            if (!(c.startUnixMs instanceof Number) || c.startUnixMs < serverAt(System.nanoTime()) + 100d) throw new IllegalStateException('common start was not safely in the future')
            anchorFrozen = true
            startNano = anchorNano + ((c.startUnixMs.longValue()-anchorServerMs)*1000000L)
        }
    }
    Map bind(int slot) {
        if (!relations.containsKey(slot) || !bound.add(slot)) { stop.set(true); throw new IllegalStateException('duplicate/unowned slot') }
        relations[slot]
    }
    Map gate(Map relation, long iteration) {
        while (startNano == Long.MIN_VALUE && !stop.get()) Thread.sleep(50)
        if (stop.get()) return null
        long ordinal = Cap10PTSContract.ordinal(relation.slot as int, iteration)
        if (ordinal >= (long)config.rate * config.seconds) return null
        long deadline = Cap10PTSContract.dueNano(startNano, ordinal, config.rate as int)
        long preparation = Cap10PTSContract.preparationNano(startNano, ordinal, config.rate as int)
        while (!stop.get() && System.nanoTime() < preparation) LockSupport.parkNanos(Math.min(50000000L, preparation-System.nanoTime()))
        if (stop.get()) return null
        long now = System.nanoTime()
        if ((now-deadline)/1000000d > 50d) {
            record([ordinal:ordinal,slot:relation.slot,kind:'dropped',startedUnixMs:null,elapsedMs:null,status:null])
            stop.set(true); flush(); return null
        }
        // Give JMeter its setup lead without claiming an HTTP attempt yet.
        [ordinal:ordinal,slot:relation.slot,wanted:!relation.liked,previousVersion:relation.version]
    }
    void begin(Map offer) {
        if (offer.containsKey('startedUnixMs')) throw new IllegalStateException('offer already handed to HTTP sampler')
        long deadline = Cap10PTSContract.dueNano(startNano, offer.ordinal as long, config.rate as int)
        long now = Cap10PTSContract.handoffNano(deadline)
        double scheduled = config.startUnixMs.doubleValue() + offer.ordinal * 1000d / config.rate
        // Retain the actual clock reading; wait out sub-microsecond rounding
        // rather than clamping a timestamp forward to its planned deadline.
        while (serverAt(now) < scheduled) now = Cap10PTSContract.handoffNano(now + 1000L)
        // This already selected sampler may execute even if a stop arrived
        // during setup, so do not return early and send it before its deadline.
        attempted.incrementAndGet(); inFlight.incrementAndGet()
        offer.startedUnixMs = serverAt(System.nanoTime())
    }
    void terminal(Map relation, Map offer, def sample) {
        int status = sample.responseCode ==~ /\d{3}/ ? sample.responseCode.toInteger() : 0
        Map outcome = Cap10PTSContract.classify(status,sample.responseDataAsString,relation.noteId,relation,offer.wanted as boolean)
        Map event = offer + [kind:outcome.kind,elapsedMs:sample.time as double,status:status] + outcome
        completed.incrementAndGet(); inFlight.decrementAndGet()
        if (outcome.kind == 'mutation') { relation.liked = outcome.observedLiked; relation.version = outcome.observedVersion }
        else stop.set(true)
        record(event)
        // Remove credential-bearing sampler context before every listener/PTS log.
        sample.setRequestHeaders(''); sample.setResponseHeaders(''); sample.setSamplerData(''); sample.setResponseData(new byte[0]); sample.setResponseMessage('CAP10 ' + outcome.kind)
        sample.setSuccessful(outcome.kind == 'mutation')
        if (outcome.kind != 'mutation') flush()
    }
    void record(Map record) {
        counts[record.kind].incrementAndGet()
        synchronized (recordsLock) { if (pending.size() >= 30000) { stop.set(true); throw new IllegalStateException('bounded telemetry buffer exhausted') }; pending.add(record) }
    }
    Map resources() {
        def bean = (com.sun.management.OperatingSystemMXBean)ManagementFactory.operatingSystemMXBean
        Long rss = null
        File status = new File('/proc/self/status')
        if (status.isFile()) { def line = status.readLines('UTF-8').find { it.startsWith('VmRSS:') }; if (line) rss = line.tokenize()[1].toLong()*1024L }
        else if (System.getProperty('os.name').toLowerCase().contains('windows')) {
            // Local finite wiring only: actual working set, never JVM heap as RSS.
            String pid = ManagementFactory.runtimeMXBean.name.tokenize('@')[0]
            if (!(pid ==~ /\d+/)) throw new IllegalStateException('process identity unavailable')
            Process child = new ProcessBuilder('powershell.exe','-NoProfile','-NonInteractive','-WindowStyle','Hidden','-Command','(Get-Process -Id '+pid+').WorkingSet64').start()
            if (child.waitFor(5L,java.util.concurrent.TimeUnit.SECONDS) && child.exitValue() == 0) {
                String raw = child.inputStream.getText('UTF-8').trim()
                if (raw ==~ /\d+/) rss = raw.toLong()
            } else { child.destroyForcibly() }
        }
        [cpuTimeNs:bean.processCpuTime,monotonicNs:System.nanoTime()-resourceOriginNano,rssBytes:rss,logicalCPUs:Runtime.runtime.availableProcessors()]
    }
    void flush() {
        synchronized (io) {
            List<Map> batch
            synchronized (recordsLock) { batch = new ArrayList<Map>(pending) }
            Map body = [runId:config.runId,engineId:engineId,seq:seq,records:batch,clock:clock,resources:resources()]
            Map response = null
            for (int retry=0; retry<3 && response == null; retry++) {
                try { response = request('/batch',body) } catch (Exception ignored) { if (retry == 2) throw new IllegalStateException('telemetry ACK unavailable'); Thread.sleep(200) }
            }
            if (response.ack != seq) throw new IllegalStateException('telemetry sequence ACK differs')
            synchronized (recordsLock) { pending.subList(0,batch.size()).clear() }
            seq++
        }
    }
    void reportLoop() {
        try {
            long nextReport = 0L
            long setupDeadline = System.nanoTime() + 90000000000L
            while (done.size() < slots.size() || (!stop.get() && startNano != Long.MIN_VALUE && System.nanoTime() < startNano + config.seconds*1000000000L)) {
                if (startNano == Long.MIN_VALUE && System.nanoTime() >= setupDeadline) stop.set(true)
                if (stop.get() && inFlight.get() == 0 && done.size() >= bound.size()) break
                int ready = bound.size()
                if (ready != readyReported) { request('/register',[runId:config.runId,engineId:engineId,slots:slots,readyWorkers:ready,scriptSHA256:scriptHash,fixtureSHA256:config.fixtureSHA256]); readyReported=ready }
                control()
                if (System.nanoTime() >= nextReport) { flush(); nextReport = System.nanoTime() + 5000000000L }
                Thread.sleep(200L)
            }
            flush()
            Map totals = [planned:Cap10PTSContract.planned(slots,config.rate as int,config.seconds as int),attempted:attempted.get(),completed:completed.get()]
            counts.each { key,value -> totals[key]=value.get() }
            Map response = request('/complete',[runId:config.runId,engineId:engineId,lastSeq:seq-1,counts:totals,clock:clock,stoppedOffering:true,inFlight:inFlight.get()])
            terminalAck = response.accepted == true
        } catch (Exception ignored) { stop.set(true); System.err.println('CAP10 telemetry/measurement failure; no body or credentials retained') }
    }
    void finish(int slot) { done.add(slot) }
}

// The exact same cached script is used by standard JSR223 nodes; Parameters
// selects a small lifecycle action. Only private register responses hold tokens.
try {
    Cap10PTSRun run
    synchronized (props) {
        if (props.get('cap10.pts.initFailure') == true) throw new IllegalStateException('previous sanitized setup failure')
        run = (Cap10PTSRun)props.get('cap10.pts.instance')
        if (run == null) {
            try { run = new Cap10PTSRun(vars.get('cap10.scriptSHA256')); props.put('cap10.pts.instance',run) }
            catch (Exception ignored) { props.put('cap10.pts.initFailure',true); throw new IllegalStateException('sanitized initial setup failure') }
        }
    }
    if (Parameters == 'bind') {
        vars.putObject('cap10.relation',run.bind(vars.get('slot').toInteger())); vars.put('cap10.iteration','0'); vars.put('cap10.continue','true')
    } else if (Parameters == 'gate') {
        Map relation = (Map)vars.getObject('cap10.relation')
        Map offer = run.gate(relation,vars.get('cap10.iteration').toLong())
        vars.put('cap10.doWrite',offer != null ? 'true' : 'false')
        if (offer == null) vars.put('cap10.continue','false')
        else {
            vars.putObject('cap10.offer',offer); vars.put('cap10.iteration',(vars.get('cap10.iteration').toLong()+1).toString())
            vars.put('cap10.noteId',relation.noteId); vars.put('cap10.token',relation.token); vars.put('cap10.wanted',offer.wanted.toString())
            def uri = new URI(run.config.apiUrl.toString()); vars.put('cap10.apiHost',uri.host); vars.put('cap10.apiPort',(uri.port == -1 ? 80 : uri.port).toString())
        }
    } else if (Parameters == 'pre') {
        // Monotonic handoff immediately before the standard HTTP sampler. Its
        // native elapsed time remains independent of Groovy classification.
        run.begin((Map)vars.getObject('cap10.offer'))
    } else if (Parameters == 'post') {
        run.terminal((Map)vars.getObject('cap10.relation'),(Map)vars.getObject('cap10.offer'),prev)
        vars.remove('cap10.token')
    } else if (Parameters == 'done') {
        run.finish(((Map)vars.getObject('cap10.relation')).slot as int)
    } else if (Parameters == 'finish') {
        run.reporter.join(20000L)
        if (run.reporter.alive || !run.terminalAck) throw new IllegalStateException('terminal ACK missing')
        synchronized (props) {
            if (props.get('cap10.pts.instance').is(run)) props.remove('cap10.pts.instance')
            props.remove('cap10.pts.initFailure')
        }
    }
    if (binding.hasVariable('SampleResult')) SampleResult.setIgnore()
} catch (Exception ignored) {
    def run = props.get('cap10.pts.instance'); if (run != null) run.stop.set(true)
    vars.put('cap10.doWrite','false'); vars.put('cap10.continue','false')
    vars.remove('cap10.token')
    if (Parameters == 'post') {
        prev.setRequestHeaders(''); prev.setResponseHeaders(''); prev.setSamplerData(''); prev.setResponseData(new byte[0]); prev.setResponseMessage('CAP10 sanitized classification failure')
    }
    throw new IllegalStateException('CAP10 sanitized workload failure; no payload retained')
}

// Pure actual-class checks; never starts HTTP, JMeter or a business service.
def loader = new GroovyClassLoader()
def type = loader.parseClass(new File('scripts/pts/cap10-pts-contract.groovy'))
int checks = 0
def check = { String label, Closure test -> assert test(); checks++; println('PASS ' + label) }
def body = { liked, version, likes=1, id='101' -> groovy.json.JsonOutput.toJson([success:true,data:[noteId:id,liked:liked,version:version,likes:likes]]) }
check('true transition') { type.classify(200,body(true,1),'101',[liked:false,version:0],true).kind == 'mutation' }
check('false transition') { type.classify(200,body(false,2,0),'101',[liked:true,version:1],false).kind == 'mutation' }
check('noop remains separate') { type.classify(200,body(true,1),'101',[liked:false,version:1],true).kind == 'noop' }
check('version jump invalid') { type.classify(200,body(true,3),'101',[liked:false,version:0],true).kind == 'invalid' }
check('wrong note invalid') { type.classify(200,body(true,1,1,'102'),'101',[liked:false,version:0],true).kind == 'invalid' }
check('wrong liked invalid') { type.classify(200,body(false,1),'101',[liked:false,version:0],true).kind == 'invalid' }
check('bad count invalid') { [-1,.5,'1'].every { type.classify(200,body(true,1,it),'101',[liked:false,version:0],true).kind == 'invalid' } }
check('transport effect uncertain') { [0,500,503].every { type.classify(it,'','101',[liked:false,version:0],true).kind == 'uncertain' } }
check('rejection separate') { type.classify(403,'','101',[liked:false,version:0],true).kind == 'rejected' }
check('malformed body invalid') { type.classify(200,'not json','101',[liked:false,version:0],true).kind == 'invalid' }
check('512 slots partition guarded exact integer schedule') { [1,50,800,5000].every { rate -> [10,60,180].every { seconds -> type.planned((0..<512).toList(),rate,seconds) == Math.floorDiv(rate*(seconds*1000L-(seconds==10 ? 0L : 100L)),1000L) } } }
check('arbitrary disjoint engine shards preserve exact guarded plans') { [1,50,800,5000].every { rate -> type.planned((0..<512).findAll { it%2==0 },rate,180) + type.planned((0..<512).findAll { it%2==1 },rate,180) == type.plannedTotal(rate,180) } }
check('warm remains unchanged and stable reports actual guarded totals') { type.plannedTotal(25,10)==250 && type.plannedTotal(800,60)==47920 && type.plannedTotal(800,180)==143920 && type.launchGuardMs(10)==0 && type.launchGuardMs(180)==100 }
check('last guarded due precedes 100ms launch guard and next ordinal is unplanned') {
    [1,50,800,5000].every { rate -> [60,180].every { seconds ->
        long total=type.plannedTotal(rate,seconds), end=seconds*1000000000L
        type.dueNano(0L,total-1,rate)<end-100000000L && total==Math.floorDiv(rate*(seconds*1000L-100L),1000L)
    } }
}
check('ordinal independent of completed request duration') { type.ordinal(7,2) == 1031 && type.dueNano(1000L,1031L,50) == 20620001000L }
check('prepare lead leaves exact offer deadline unchanged') {
    [50,200,800,3200,5000].every { rate ->
        type.preparationNano(1000000000L,513L,rate) == type.dueNano(1000000000L,513L,rate) - 100000000L
    }
}
check('handoff waits for actual monotonic deadline') {
    long deadline = System.nanoTime() + 5000000L
    long actual = type.handoffNano(deadline)
    actual >= deadline && actual <= System.nanoTime()
}
check('late handoff retains the real late timestamp') {
    long before = System.nanoTime(), deadline = before - 60000000L
    long actual = type.handoffNano(deadline)
    actual >= before && actual-deadline >= 60000000L && actual <= System.nanoTime()
}
check('actual JMeter runtime with separate handoff compiles without starting it') {
    loader.parseClass(new File('scripts/pts/cap10-jmeter.groovy'))
    loader.loadClass('Cap10PTSRun').getMethod('begin', Map) != null
}
check('safe integers remain strict') { !type.integer(true) && !type.integer(.5) && !type.integer('1') && !type.integer(9007199254740992G) }
check('legacy omitted dimensions remain 512/2/513') { type.dimensions([:]) == [poolSize:512,expectedEngines:2,ptsConcurrency:513] && type.ordinal(511,1)==1023 }
check('explicit large and uneven dimensions are bounded') { type.dimensions([poolSize:4096,expectedEngines:9,ptsConcurrency:4105]) == [poolSize:4096,expectedEngines:9,ptsConcurrency:4105] && type.ptsConcurrency(17,3)==20 }
check('four digit slots and pool stride preserve original ordinal ownership') { type.ordinal(4095,2,4096)==12287 && type.ordinal(16,3,17)==67 }
check('nine disjoint uneven shards cover the complete 5k guarded plan') {
    def shards=(0..<9).collect { engine -> (0..<4096).findAll { it%9==engine } }
    shards.flatten().toSet().size()==4096 && shards.collect { type.planned(it,5000,180,4096) }.sum()==899500L && shards.every { it.size() in [455,456] }
}
check('three small shards preserve native schedule and guard') { (0..<3).collect { engine -> type.planned((0..<17).findAll { it%3==engine },50,60,17) }.sum()==2995L }
check('cohort validation rejects wrong types and bounds') {
    [0,4097,true,null,'4096',4096.0G].every { value -> try { type.poolSize(value); false } catch (IllegalArgumentException expected) { true } } &&
    [0,17,true,null,2.0G].every { value -> try { type.engineCount(value,4096); false } catch (IllegalArgumentException expected) { true } }
}
check('wrong explicit concurrency and out-of-pool or duplicate shards fail') {
    try { type.dimensions([poolSize:4096,expectedEngines:9,ptsConcurrency:513]); return false } catch (IllegalArgumentException expected) { }
    [[0,17],[1,1]].every { slots -> try { type.planned(slots,50,60,17); false } catch (IllegalArgumentException expected) { true } }
}
check('bounded telemetry prefixes preserve exact ordering without editing pending') {
    def pending=(0..<1000).collect { [ordinal:it] }, snapshot=new ArrayList(pending), sizes=[], received=[]
    int remaining=pending.size()
    while (remaining>0) {
        def frame=type.framePrefix(pending,remaining)
        sizes.add(frame.size());received.addAll(frame)
        pending.subList(0,frame.size()).clear();remaining-=frame.size()
    }
    sizes==[128,128,128,128,128,128,128,104] && received==snapshot && !pending
}
check('snapshot chunks retain later arrivals and empty resource heartbeats') {
    def pending=(0..<300).toList(), frame=type.framePrefix(pending,300)
    pending.addAll([300,301]);pending.subList(0,frame.size()).clear()
    def next=type.framePrefix(pending,172)
    pending.subList(0,next.size()).clear()
    def tail=type.framePrefix(pending,44)
    next==(128..<256).toList() && tail==(256..<300).toList() && pending[-2..-1]==[300,301] && type.framePrefix([],0)==[]
}
check('invalid snapshot bounds fail rather than deleting unacknowledged rows') {
    [-1,3].every { remaining -> try { type.framePrefix([0,1],remaining); false } catch (IllegalArgumentException expected) { true } }
}
// Compile the actual flush method with only its protocol transport replaced.
// This exercises queue/ACK ownership without starting JMeter, HTTP or services.
String runtimeSource=new File('scripts/pts/cap10-jmeter.groovy').getText('UTF-8')
String actualFlush=runtimeSource.substring(runtimeSource.indexOf('    void flush() {'),runtimeSource.indexOf('    void reportLoop() {'))
def flushType=loader.parseClass('''class Cap10ActualFlushCheck {
    Object io=new Object(), recordsLock=new Object()
    List pending=[], requests=[]
    Map config=[runId:'synthetic'], clock=[:]
    String engineId='synthetic'
    long seq=0
    int failSeq=-1, wrongAck=-1
    boolean laterArrival=false
    Map resources() { [synthetic:true] }
    Map request(String path,Map body) {
        assert path=='/batch';requests.add(body)
        if(laterArrival){pending.add([ordinal:600]);laterArrival=false}
        if(body.seq==failSeq)throw new IllegalStateException('synthetic ACK unavailable')
        [ack:body.seq==wrongAck ? 999 : body.seq]
    }
''' + actualFlush + '}')
check('actual flush owns bounded snapshot and leaves concurrent arrivals for next cadence') {
    def run=flushType.newInstance();run.pending=(0..<600).collect { [ordinal:it] };run.laterArrival=true;run.flush()
    run.requests.collect { it.records.size() }==[128,128,128,128,88] && run.requests.collect { it.seq }==[0,1,2,3,4] &&
        run.requests.collectMany { it.records }.collect { it.ordinal }==(0..<600).toList() && run.pending==[[ordinal:600]] && run.seq==5
}
check('actual flush retries identical frame and never deletes an unacknowledged prefix') {
    def run=flushType.newInstance();run.pending=(0..<600).collect { [ordinal:it] };run.failSeq=1
    try{run.flush();return false}catch(IllegalStateException expected){assert expected.message=='telemetry ACK unavailable'}
    run.requests.collect { it.seq }==[0,1,1,1] && run.requests[1].is(run.requests[2]) && run.requests[2].is(run.requests[3]) &&
        run.seq==1 && run.pending.collect { it.ordinal }==(128..<600).toList()
}
check('actual flush keeps resource heartbeat and refuses wrong ACK before removal') {
    def empty=flushType.newInstance();empty.flush()
    def bad=flushType.newInstance();bad.pending=[[ordinal:0]];bad.wrongAck=0
    try{bad.flush();return false}catch(IllegalStateException expected){assert expected.message=='telemetry sequence ACK differs'}
    empty.seq==1 && empty.requests.size()==1 && empty.requests[0].records==[] && bad.seq==0 && bad.pending==[[ordinal:0]]
}
println(groovy.json.JsonOutput.toJson([checks:checks,failed:0,servicesStarted:false,loadWindows:0]))

// Actual runtime-method checks with transport replaced; no HTTP/JMeter/service.
def loader = new GroovyClassLoader()
def contract = loader.parseClass(new File('scripts/pts/cap10-pts-contract.groovy'))
String source = new File('scripts/pts/cap10-jmeter.groovy').getText('UTF-8')
String actualMethods = source.substring(source.indexOf('    Map gate('), source.indexOf('    void record('))
def runtime = loader.parseClass('''
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.locks.LockSupport
class Cap10PreparedActualMethods {
    String declaredPreheatProtocol='relation-noop-v1'
    int declaredPoolSize=2
    Set preheated=[] as Set
    AtomicBoolean stop=new AtomicBoolean(false)
    AtomicLong attempted=new AtomicLong(),completed=new AtomicLong(),inFlight=new AtomicLong()
    AtomicLong preheatAttempted=new AtomicLong(),preheatCompleted=new AtomicLong()
    Map counts=['mutation','noop','uncertain','rejected','invalid','dropped'].collectEntries { [(it):new AtomicLong()] }
    Map preheatCounts=['mutation','noop','uncertain','rejected','invalid','dropped'].collectEntries { [(it):new AtomicLong()] }
    List preheatPending=[],nativeRecords=[]
    Object recordsLock=new Object()
    long anchorNano=System.nanoTime()-1000000000L,anchorServerMs=1800000000000L
    long preheatNano=anchorNano,startNano=anchorNano
    Map config=[preheatStartUnixMs:anchorServerMs,startUnixMs:anchorServerMs,rate:100,seconds:60]
    int preparationFlushes=0,nativeFlushes=0
    double serverAt(long now) { anchorServerMs+(now-anchorNano)/1000000d }
    void record(Map row) { nativeRecords.add(row);counts[row.kind].incrementAndGet() }
    void flush() { nativeFlushes++ }
    void flushPreheat() { preparationFlushes++ }
''' + actualMethods + '}')
def sampleType = loader.parseClass('''class Cap10SyntheticHTTP {
    String responseCode='200',responseDataAsString
    long time=2
    boolean successful
    void setRequestHeaders(String ignored) { }
    void setResponseHeaders(String ignored) { }
    void setSamplerData(String ignored) { }
    void setResponseData(byte[] ignored) { }
    void setResponseMessage(String ignored) { }
    void setSuccessful(boolean value) { successful=value }
}''')
def body = { liked,version,likes -> groovy.json.JsonOutput.toJson([success:true,data:[noteId:'20',liked:liked,version:version,likes:likes]]) }
def sample = { liked,version,likes -> def result=sampleType.newInstance();result.responseDataAsString=body(liked,version,likes);result }
int checks=0
def check={ String label,Closure fn -> assert fn();checks++;println('PASS '+label) }
check('strict preparation protocol cannot silently enable unknown values') {
    contract.preheatProtocol([:])=='none' && contract.preheatProtocol([preheatProtocol:'relation-noop-v1'])=='relation-noop-v1' &&
        [null,true,'other'].every { value -> try{contract.preheatProtocol([preheatProtocol:value]);false}catch(IllegalArgumentException expected){true} }
}
check('same runtime path prepares without consuming native ordinal version or counters') {
    def run=runtime.newInstance(),relation=[slot:0,noteId:'20',liked:false,version:7L]
    def offer=run.gate(relation,0L)
    assert offer.preheat==true && offer.ordinal==0 && offer.wanted==false
    run.begin(offer)
    assert run.preheatAttempted.get()==1 && run.attempted.get()==0 && run.inFlight.get()==1
    def prep=sample(false,7,0);run.terminal(relation,offer,prep)
    assert prep.successful && run.preheated==[0] as Set && relation.version==7L && relation.liked==false
    assert run.preheatCounts.noop.get()==1 && run.preheatPending.size()==1 && !run.preheatPending[0].containsKey('preheat')
    assert run.nativeRecords==[] && run.completed.get()==0 && run.inFlight.get()==0
    run.startNano=System.nanoTime()+10000000L
    run.config.startUnixMs=run.serverAt(run.startNano)
    def measured=run.gate(relation,0L)
    assert !measured.containsKey('preheat') && measured.ordinal==0 && measured.previousVersion==7L && measured.wanted==true
    run.begin(measured);def actual=sample(true,8,1);run.terminal(relation,measured,actual)
    actual.successful && run.attempted.get()==1 && run.completed.get()==1 && run.counts.mutation.get()==1 &&
        run.nativeRecords.size()==1 && run.preheatPending.size()==1 && relation.version==8L && relation.liked==true
}
check('preparation changed version or count cannot set readiness or native counters') {
    [[false,8,0],[false,7,1],[true,7,1]].every { values ->
        def run=runtime.newInstance(),relation=[slot:0,noteId:'20',liked:false,version:7L],offer=run.gate(relation,0L)
        run.begin(offer);def response=sample(*values);run.terminal(relation,offer,response)
        run.stop.get() && !response.successful && !run.preheated && run.preheatPending[0].kind=='invalid' &&
            run.preparationFlushes==1 && run.attempted.get()==0 && relation.version==7L && !relation.liked
    }
}
check('unknown preparation retains complete nullable fact shape and does not retry HTTP') {
    def run=runtime.newInstance(),relation=[slot:0,noteId:'20',liked:false,version:7L],offer=run.gate(relation,0L)
    run.begin(offer);def response=sampleType.newInstance();response.responseCode='503';response.responseDataAsString='{}'
    run.terminal(relation,offer,response)
    def row=run.preheatPending[0]
    run.stop.get() && run.preheatAttempted.get()==1 && run.preheatCompleted.get()==1 &&
        row.kind=='uncertain' && ['observedLiked','observedVersion','observedNoteId','observedLikes'].every { row.containsKey(it) && row[it]==null } &&
        run.nativeRecords==[] && run.attempted.get()==0 && run.preparationFlushes==1
}
check('actual begin retains actual lateness rather than stamping the deadline') {
    def run=runtime.newInstance(),relation=[slot:0,noteId:'20',liked:false,version:7L],offer=run.gate(relation,0L)
    double before=run.serverAt(System.nanoTime());run.begin(offer);double after=run.serverAt(System.nanoTime())
    offer.startedUnixMs>=before && offer.startedUnixMs<=after && offer.startedUnixMs>run.config.preheatStartUnixMs+500
}
check('native iteration only advances for measured offers in actual JSR223 dispatch') {
    source.contains("if (offer.preheat != true) vars.put('cap10.iteration'") &&
        source.contains("vars.put('cap10.iteration','0')")
}
String actualFlush=source.substring(source.indexOf('    void flushPreheat() {'),source.indexOf('    void flush() {'))
def flushType=loader.parseClass('''class Cap10PreparationFlush {
    Object io=new Object(),recordsLock=new Object()
    List preheatPending=[],requests=[]
    Map config=[runId:'synthetic'],clock=[:]
    String engineId='synthetic'
    long preheatSeq=0
    int failSeq=-1,wrongAck=-1
    Map resources() { [synthetic:true] }
    Map request(String path,Map body) {
        assert path=='/preheat';requests.add(body)
        if(body.seq==failSeq)throw new IllegalStateException('synthetic ACK unavailable')
        [ack:body.seq==wrongAck ? 999 : body.seq]
    }
''' + actualFlush + '}')
check('preparation uses independent bounded durable ACK sequence') {
    def run=flushType.newInstance();run.preheatPending=(0..<300).collect { [slot:it] };run.flushPreheat()
    run.requests.collect { it.records.size() }==[128,128,44] && run.requests.collect { it.seq }==[0L,1L,2L] && !run.preheatPending && run.preheatSeq==3L
}
check('preparation telemetry retries the identical frame but retains unacknowledged rows') {
    def run=flushType.newInstance();run.preheatPending=(0..<300).collect { [slot:it] };run.failSeq=1
    try{run.flushPreheat();return false}catch(IllegalStateException expected){assert expected.message=='preparation ACK unavailable'}
    run.requests.collect { it.seq }==[0L,1L,1L,1L] && run.requests[1].is(run.requests[2]) &&
        run.preheatSeq==1L && run.preheatPending.collect { it.slot }==(128..<300).toList()
}
check('wrong preparation ACK never removes the prepared terminal') {
    def run=flushType.newInstance();run.preheatPending=[[slot:0]];run.wrongAck=0
    try{run.flushPreheat();return false}catch(IllegalStateException expected){assert expected.message=='preparation sequence ACK differs'}
    run.preheatSeq==0L && run.preheatPending==[[slot:0]]
}
println(groovy.json.JsonOutput.toJson([checks:checks,failed:0,servicesStarted:false,loadWindows:0]))

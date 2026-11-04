// Actual constructor/control/dispatcher with a fake collector; no HTTP or service.
import groovy.json.JsonOutput
import java.nio.file.Files
import org.apache.jmeter.services.FileServer
import org.apache.jmeter.threads.JMeterVariables

def loader = new GroovyClassLoader()
loader.parseClass(new File('scripts/pts/cap10-pts-contract.groovy'))
String source = new File('scripts/pts/cap10-jmeter.groovy').getText('UTF-8')
loader.parseClass(source)
def runtime = loader.parseClass('''
class Cap10EndpointSyntheticCollector extends Cap10PTSRun {
    static String endpoint = 'http://10.1.2.3:8281'
    Cap10EndpointSyntheticCollector() { super('a'*64,2,1,'embedded-v1','none') }
    Map request(String path,Map body=null) {
        if(path=='/control') return [version:1,runId:'endpoint-synthetic',phase:'warm',rate:25,seconds:10,
            jitterBudgetMs:50,clockUncertaintyMaxMs:50,scriptSHA256:'a'*64,fixtureSHA256:'b'*64,
            poolSize:2,expectedEngines:1,ptsConcurrency:3,launchGuardMs:0,plannedTotal:250,apiUrl:endpoint]
        if(path=='/register') return [relations:body.slots.collect { slot ->
            [slot:slot,userId:String.valueOf(100+slot),noteId:String.valueOf(200+slot),active:0,version:0,token:'synthetic-'+slot] }]
        if(path=='/batch') return [ack:body.seq]
        if(path=='/complete') return [accepted:true]
        throw new AssertionError('unexpected collector action')
    }
    Map resources() { [cpuTimeNs:1L,monotonicNs:1L,rssBytes:1024L,logicalCPUs:1] }
    Map gate(Map relation,long iteration) {
        iteration==0L ? [ordinal:relation.slot,slot:relation.slot,wanted:false,previousVersion:relation.version,preheat:true] :
            [ordinal:relation.slot+2L*iteration,slot:relation.slot,wanted:true,previousVersion:relation.version]
    }
}
''')
String bind = source.substring(source.indexOf("    if (Parameters == 'bind') {"),source.indexOf("    } else if (Parameters == 'gate') {")) + '\n}'
String gate = 'if (true) {' + source.substring(source.indexOf("    } else if (Parameters == 'gate') {") + "    } else if (Parameters == 'gate') {".length(),source.indexOf("    } else if (Parameters == 'pre') {")) + '\n}'
def folder = Files.createTempDirectory('cap10-endpoint-').toFile()
def script = new File(folder,'synthetic.jmx'); script.text='synthetic only'
new File(folder,'cap10-pts-private.json').text=JsonOutput.toJson([collectorUrl:'http://127.0.0.1:1',controlToken:'synthetic-control-token-0000000000'])
new File(folder,'slots.csv').text='slot\n0\n1\n'
FileServer.getFileServer().setBaseForScript(script)
int checks=0
def check={ String label,Closure fn -> assert fn();checks++;println('PASS '+label) }
def runFor={ String endpoint ->
    runtime.endpoint=endpoint
    def run=runtime.newInstance()
    run.stop.set(true); run.reporter.join(2000L)
    assert !run.reporter.alive
    run
}
try {
    check('validated explicit private endpoint retains exact sampler values') {
        def run=runFor('http://10.1.2.3:8281')
        run.apiHost=='10.1.2.3' && run.apiPort=='8281'
    }
    check('default HTTP port remains 80 and trailing slash stays accepted') {
        def run=runFor('http://172.16.2.3/')
        run.apiHost=='172.16.2.3' && run.apiPort=='80'
    }
    check('same URL refresh remains valid and a valid changed URL is still rejected') {
        def run=runFor('http://192.168.2.3:9999');run.control()
        runtime.endpoint='http://192.168.2.4:9999'
        try{run.control();return false}catch(IllegalStateException expected){ assert expected.message=='API endpoint changed' }
        run.apiHost=='192.168.2.3' && run.apiPort=='9999' && run.config.apiUrl=='http://192.168.2.3:9999'
    }
    check('unsafe endpoint forms cannot create a cached sampler endpoint') {
        ['https://10.1.2.3','http://example.invalid','http://user@10.1.2.3','http://10.1.2.3/x',
         'http://10.1.2.3?x=1','http://10.1.2.3#fragment'].every { value ->
            try{runFor(value);false}catch(IllegalStateException expected){true}
        }
    }
    check('actual bind sets host port once for each independent relationship thread') {
        def run=runFor('http://10.1.2.3:8281')
        [0,1].every { slot ->
            def vars=new JMeterVariables();vars.put('slot',String.valueOf(slot))
            new GroovyShell(loader,new Binding([run:run,vars:vars,Parameters:'bind'])).evaluate(bind)
            vars.get('cap10.apiHost')=='10.1.2.3' && vars.get('cap10.apiPort')=='8281' &&
                vars.getObject('cap10.relation').slot==slot && vars.get('cap10.iteration')=='0'
        }
    }
    check('actual gate preserves cached endpoint and native versus preheat iteration fields') {
        def run=runFor('http://10.1.2.3:8281'),vars=new JMeterVariables()
        vars.put('slot','0');def shell=new GroovyShell(loader,new Binding([run:run,vars:vars,Parameters:'bind']))
        shell.evaluate(bind)
        shell.evaluate(gate)
        assert vars.get('cap10.iteration')=='0' && vars.getObject('cap10.offer').preheat==true
        vars.put('cap10.iteration','1');shell.evaluate(gate)
        vars.get('cap10.apiHost')=='10.1.2.3' && vars.get('cap10.apiPort')=='8281' &&
            vars.get('cap10.iteration')=='2' && vars.getObject('cap10.offer').ordinal==2L &&
            vars.get('cap10.token')=='synthetic-0' && vars.get('cap10.noteId')=='200' && vars.get('cap10.wanted')=='true'
    }
    println(JsonOutput.toJson([checks:checks,passed:true,collectorHTTPRequests:0,businessRequests:0,cloudCalls:0]))
} finally {
    FileServer.getFileServer().closeFiles()
    assert folder.canonicalFile.parentFile == new File(System.getProperty('java.io.tmpdir')).canonicalFile
    assert folder.name.startsWith('cap10-endpoint-')
    folder.eachFile { file -> assert file.isFile();assert file.delete() }
    assert folder.delete()
}

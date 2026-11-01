// Pure actual-class checks; never starts HTTP, JMeter or a business service.
def type = new GroovyClassLoader().parseClass(new File('scripts/pts/cap10-pts-contract.groovy'))
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
check('512 slots partition complete fixed schedule') { [1,50,5000].every { rate -> [60,180].every { seconds -> type.planned((0..<512).toList(),rate,seconds) == (long)rate*seconds } } }
check('arbitrary disjoint engine shards preserve exact nominal plans') { [1,50,5000].every { rate -> type.planned((0..<512).findAll { it%2==0 },rate,180) + type.planned((0..<512).findAll { it%2==1 },rate,180) == (long)rate*180 } }
check('ordinal independent of completed request duration') { type.ordinal(7,2) == 1031 && type.dueNano(1000L,1031L,50) == 20620001000L }
check('safe integers remain strict') { !type.integer(true) && !type.integer(.5) && !type.integer('1') && !type.integer(9007199254740992G) }
println(groovy.json.JsonOutput.toJson([checks:checks,failed:0,servicesStarted:false,loadWindows:0]))

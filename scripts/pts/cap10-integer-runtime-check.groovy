// Actual contract with the original numeric predicate as a compatibility oracle.
// Pure values/JSON only: no JMeter process, HTTP, cloud or business dependencies.
import groovy.json.JsonOutput
import java.util.concurrent.atomic.AtomicLong

def type = new GroovyClassLoader().parseClass(new File('scripts/pts/cap10-pts-contract.groovy'))
def legacy = { Object n -> n instanceof Number && !(n instanceof Float) && !(n instanceof Double) &&
    new BigDecimal(n.toString()).stripTrailingZeros().scale() <= 0 && n >= 0 && n <= 9007199254740991L }
class OtherNumeric extends Number {
    String text; int strings=0
    int intValue() { new BigDecimal(text).intValue() }
    long longValue() { new BigDecimal(text).longValue() }
    float floatValue() { new BigDecimal(text).floatValue() }
    double doubleValue() { new BigDecimal(text).doubleValue() }
    String toString() { strings++; text }
}
class ExtendedBigInteger extends BigInteger {
    int strings=0
    ExtendedBigInteger(String value) { super(value) }
    String toString() { strings++; super.toString() }
}
int checks=0
def check={ String label,Closure fn -> assert fn();checks++;println('PASS '+label) }
check('standard integral classes keep nonnegative safe-integer bounds') {
    [Byte,Short,Integer,Long,BigInteger].every { kind ->
        def zero=kind==BigInteger ? BigInteger.ZERO : kind.valueOf('0')
        def positive=kind==BigInteger ? BigInteger.valueOf(7L) : kind.valueOf('7')
        def negative=kind==BigInteger ? BigInteger.valueOf(-1L) : kind.valueOf('-1')
        type.integer(zero) && type.integer(positive) && !type.integer(negative)
    } && type.integer(9007199254740991L) && type.integer(9007199254740991G) &&
        !type.integer(9007199254740992L) && !type.integer(9007199254740992G) &&
        !type.integer(Long.MAX_VALUE) && !type.integer(Long.MIN_VALUE)
}
check('decimal integrality negative zero and safe boundaries retain original behavior') {
    ['0','-0.000','1.000','9007199254740991.00'].every { type.integer(new BigDecimal(it)) } &&
        ['-1.00','0.5','9007199254740991.1','9007199254740992.00'].every { !type.integer(new BigDecimal(it)) }
}
check('float double nonfinite booleans strings and null remain rejected') {
    [0f,1f,Float.NaN,Float.POSITIVE_INFINITY,0d,1d,Double.NaN,Double.NEGATIVE_INFINITY,true,false,'1',null].every { !type.integer(it) }
}
check('other Number and BigInteger subclass still execute the original fallback') {
    def integral=new OtherNumeric(text:'7.000'),fractional=new OtherNumeric(text:'7.5'),extended=new ExtendedBigInteger('7')
    type.integer(integral) && !type.integer(fractional) && type.integer(extended) &&
        integral.strings>0 && fractional.strings>0 && extended.strings>0 && type.integer(new AtomicLong(7L))
}
check('seeded long and decimal values agree with the retained original predicate') {
    def random=new Random(1701L)
    (0..<1000).every {
        long value=random.nextLong()
        [value,BigInteger.valueOf(value),BigDecimal.valueOf(value,random.nextInt(4))].every { n -> type.integer(n)==legacy(n) }
    }
}
def text={ liked,version,likes=1,note='101' -> JsonOutput.toJson([success:true,data:[noteId:note,liked:liked,version:version,likes:likes]]) }
def observed={ liked,version,note,kind -> [observedLiked:liked,observedVersion:version,observedNoteId:note,kind:kind] }
check('successful mutation noop and decimal JSON preserve exact output fields') {
    type.classify(200,text(true,1),'101',[liked:false,version:0],true)==observed(true,1L,'101','mutation') &&
        type.classify(200,text(false,2,0),'101',[liked:true,version:1],false)==observed(false,2L,'101','mutation') &&
        type.classify(200,text(true,1.000G),'101',[liked:false,version:1],true)==observed(true,1L,'101','noop')
}
check('invalid version liked identity or likes preserve independent observations') {
    type.classify(200,text(true,9007199254740992L),'101',[liked:false,version:0],true)==observed(true,null,'101','invalid') &&
        type.classify(200,text(true,1,0.5G),'101',[liked:false,version:0],true)==observed(true,1L,'101','invalid') &&
        type.classify(200,text('true',1),'101',[liked:false,version:0],true)==observed(null,1L,'101','invalid') &&
        type.classify(200,text(true,1,1,'102'),'101',[liked:false,version:0],true)==observed(true,1L,'102','invalid')
}
check('uncertain rejected malformed and preparation noop retain original domains') {
    [0,500,503].every { type.classify(it,'','101',[liked:false,version:0],true)==[kind:'uncertain'] } &&
        type.classify(403,'','101',[liked:false,version:0],true)==[kind:'rejected'] &&
        type.classify(200,'not json','101',[liked:false,version:0],true)==[kind:'invalid'] &&
        type.classifyPreheat(200,text(false,2,0),'101',[liked:false,version:2])==
            [observedLiked:false,observedVersion:2L,observedNoteId:'101',kind:'noop',observedLikes:0L]
}
println(JsonOutput.toJson([checks:checks,passed:true,networkCalls:0,businessRequests:0,loadWindows:0]))

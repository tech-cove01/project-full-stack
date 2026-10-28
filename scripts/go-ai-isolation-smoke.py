"""Run the real trade demo with AI disabled, missing its key, and unreachable.

Run from the repository root with explicit isolated MYSQL_*/REDIS_* settings.
GO_AI_ISOLATION_BINARY selects a prebuilt API (default backend/bin/api-go075.exe).
GO_AI_ISOLATION_EVIDENCE selects a new output directory. No paid provider calls.
"""
import json, os, signal, socket, subprocess, time
from pathlib import Path
from urllib.request import build_opener, ProxyHandler

root=Path.cwd()
required=['MYSQL_HOST','MYSQL_PORT','MYSQL_DATABASE','MYSQL_USER','MYSQL_PASSWORD','REDIS_HOST','REDIS_PORT']
if any(not os.environ.get(key) for key in required):
  raise SystemExit('Explicit isolated MYSQL_*/REDIS_* settings are required')
out=Path(os.environ.get('GO_AI_ISOLATION_EVIDENCE',root/'docs/evidence/go-ai-isolation'))
if out.exists() and any(out.iterdir()):
  raise SystemExit('Choose an empty evidence directory; previous runs must be retained')
out.mkdir(parents=True,exist_ok=True)
binary=Path(os.environ.get('GO_AI_ISOLATION_BINARY',root/'backend/bin/api-go075.exe')).resolve()
if not binary.is_file(): raise SystemExit('Build the API binary before running the smoke test')
opener=build_opener(ProxyHandler({}))
report={'task':'GO-075b','passed':False,'modes':[]}
try:
  for mode in ['disabled','missing-key','unreachable']:
    with socket.socket() as sock:
      sock.bind(('127.0.0.1',0)); port=sock.getsockname()[1]
    env=dict(os.environ)
    env.update(API_ADDR=f'127.0.0.1:{port}',API_BASE=f'http://127.0.0.1:{port}',KAFKA_BROKERS='',
       AI_ENABLED='false' if mode=='disabled' else 'true', AI_PROVIDER='openai-compatible',
       AI_API_KEY='' if mode=='missing-key' else 'synthetic-isolation-only',
       AI_BASE_URL='http://127.0.0.1:1/v1',AI_CHAT_MODEL='unreachable-chat',
       AI_EMBEDDING_MODEL='unreachable-embedding',AI_EMBEDDING_DIMENSIONS='1536',
       FLOW_EVIDENCE=str(out/f'isolation-trade-{mode}.json'))
    entry={'mode':mode,'passed':False,'forcedCleanup':False}
    report['modes'].append(entry)
    with (out/f'isolation-api-{mode}.txt').open('w',encoding='utf8') as log:
      child=subprocess.Popen([str(binary)],env=env,stdout=log,stderr=log,
         creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name=='nt' else 0)
      try:
        for _ in range(100):
          if child.poll() is not None: raise RuntimeError('API exited before ready')
          try:
            with opener.open(env['API_BASE']+'/health/ready',timeout=1) as r:
              if r.status==200: break
          except Exception: time.sleep(.15)
        else: raise RuntimeError('readiness deadline')
        with opener.open(env['API_BASE']+'/ai/capabilities',timeout=3) as r:
          caps=json.load(r)['data']
        entry['capabilities']={k:caps[k] for k in ['enabled','ready','mode','providerHealth']}
        assert caps['enabled']==(mode!='disabled')
        assert caps['ready']==(mode=='unreachable')
        run=subprocess.run(['node','scripts/go-flow-demo.mjs','trade'],cwd=root,env=env,
           capture_output=True,text=True,timeout=75)
        entry['tradeExit']=run.returncode
        if run.returncode!=0: raise RuntimeError('real trade demo failed')
        flow=json.loads((out/f'isolation-trade-{mode}.json').read_text(encoding='utf8'))
        assert flow['passed']
        entry['tradeChecks']=len(flow['checks'])
        entry['passed']=True
      finally:
        if child.poll() is None:
          child.send_signal(signal.CTRL_BREAK_EVENT if os.name=='nt' else signal.SIGINT)
          try: child.wait(timeout=15)
          except subprocess.TimeoutExpired:
            entry['forcedCleanup']=True;child.terminate();child.wait(timeout=10)
        entry['exitCode']=child.returncode
        if child.returncode!=0 or entry['forcedCleanup']: entry['passed']=False
        with socket.socket() as sock:
          try: sock.bind(('127.0.0.1',port));entry['portReusable']=True
          except OSError: entry['portReusable']=False;entry['passed']=False
  report['passed']=all(x['passed'] for x in report['modes'])
except Exception as err:
  report['error']=type(err).__name__+'; details suppressed'
finally:
  (out/'isolation-final.json').write_text(json.dumps(report,indent=2,ensure_ascii=False),encoding='utf8')
print(json.dumps(report,ensure_ascii=False))
raise SystemExit(0 if report['passed'] else 1)

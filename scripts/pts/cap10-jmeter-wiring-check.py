"""Finite loopback-only JMeter/real-collector wiring. No cloud or business DB."""
import argparse
import importlib.util
import json
from pathlib import Path
import secrets
import subprocess
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import xml.etree.ElementTree as ET

REPO = Path(__file__).resolve().parents[2]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--java', type=Path, required=True)
    parser.add_argument('--jmeter', type=Path, required=True)
    parser.add_argument('--case', choices=('success','noop','uncertain','preheat-uncertain','preheat-version'), default='success')
    parser.add_argument('--pool-size', type=int, default=512)
    parser.add_argument('--expected-engines', type=int, default=2)
    args = parser.parse_args()
    assert args.java.is_file() and (args.jmeter / 'bin/ApacheJMeter.jar').is_file()
    cohort = module('cap10_jmeter_wiring_cohort', REPO / 'scripts/cap10-cohort.py')
    dimensions = cohort.dimensions({'poolSize': args.pool_size, 'expectedEngines': args.expected_engines}, {'poolSize': args.pool_size})
    pool, engines = dimensions['poolSize'], dimensions['expectedEngines']
    root = REPO.parent / '.runtime-cap10-jmeter-tools' / ('wiring-' + uuid.uuid4().hex)
    root.mkdir(parents=True, mode=0o700)
    store_module = module('cap10_jmeter_actual_collector', REPO / 'scripts/pts/cap10-pts-collector.py')
    phase = module('cap10_jmeter_actual_http', REPO / 'scripts/pts/cap10-pts-phase.py')
    facts = {str(1001 + slot): [False, 0] for slot in range(pool)}
    observations = []
    prepared_slots = set()
    class Stub(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass
        def do_PUT(self):
            note = self.path.split('/')[2]
            slot = int(note) - 1001
            assert self.headers.get('authorization') == 'synthetic-rel-token-' + str(slot)
            wanted = json.loads(self.rfile.read(int(self.headers['Content-Length'])))['liked']
            prior = facts[note]
            preparation = slot not in prepared_slots
            if preparation:
                assert wanted is prior[0]
                prepared_slots.add(slot)
            else:
                assert wanted is not prior[0]
            if not preparation and args.case != 'noop':
                prior[0], prior[1] = wanted, prior[1] + 1
            observations.append({'slot': slot, 'wanted': wanted, 'version': prior[1], 'preparation': preparation})
            version = prior[1] + (1 if preparation and args.case == 'preheat-version' else 0)
            raw = json.dumps({'success': True, 'data': {'noteId': note, 'liked': wanted, 'version': version, 'likes': int(wanted)}}).encode()
            failed = (preparation and args.case == 'preheat-uncertain') or (not preparation and args.case == 'uncertain')
            self.send_response(503 if failed else 200); self.send_header('Content-Length', str(len(raw))); self.end_headers(); self.wfile.write(raw)
    api = ThreadingHTTPServer(('127.0.0.1', 0), Stub)
    threading.Thread(target=api.serve_forever, daemon=True).start()
    # Generate a public bundle and a private, loopback-only control input.
    common = root / 'common'
    generated = subprocess.run(['node', 'scripts/pts/cap10-jmeter.mjs', 'bundle', str(common),
        '--pool-size', str(pool), '--expected-engines', str(engines)], cwd=REPO, capture_output=True, check=True)
    manifest = json.loads(generated.stdout)
    token = secrets.token_hex(32)
    relations = [{'slot': slot, 'userId': str(2001+slot), 'noteId': str(1001+slot), 'active': 0, 'version': 0,
                  'token': 'synthetic-rel-token-'+str(slot)} for slot in range(pool)]
    store = store_module.EngineStore({'runId': 'finite-local-warm', 'phase': 'warm', 'rate': 2, 'seconds': 10,
        'controlToken': token, **dimensions, 'clockProtocol': manifest['clockProtocol'],
        'preheatProtocol': manifest['preheatProtocol'],
        'fixtureSHA256': 'a'*64, 'scriptSHA256': manifest['scriptSHA256'],
        'apiUrl': 'http://127.0.0.1:'+str(api.server_port), 'relations': relations}, root / 'collector.ndjson')
    collector = phase.CollectorHTTPServer(('127.0.0.1', 0), phase.handler_type(store, token, threading.RLock()))
    threading.Thread(target=collector.serve_forever, daemon=True).start()
    children, handles = [], []
    try:
        workers_per_engine = (pool + engines - 1) // engines
        for index in range(engines):
            folder = root / ('engine-'+str(index)); folder.mkdir()
            tree = ET.parse(common / 'cap10-jmeter.jmx')
            for group in tree.findall('.//ThreadGroup'):
                group.find("./stringProp[@name='ThreadGroup.num_threads']").text = str(workers_per_engine)
            tree.write(folder / 'cap10-jmeter.jmx', encoding='utf-8', xml_declaration=True)
            # Explicitly simulate PTS splitting, not a different workload source.
            (folder / 'slots.csv').write_text('slot\n'+'\n'.join(str(s) for s in range(pool) if s % engines == index)+'\n')
            (folder / 'cap10-pts-private.json').write_text(json.dumps({'collectorUrl':'http://127.0.0.1:'+str(collector.server_port), 'controlToken':token}))
            log = (folder / 'console.txt').open('wb'); handles.append(log)
            argv = [str(args.java.resolve()), '-Xms128m', '-Xmx512m', '-Djmeter.home='+str(args.jmeter.resolve()),
                    '-Duser.language=en', '-Duser.country=US', '-Djava.awt.headless=true', '-Dfile.encoding=UTF-8',
                    '-jar', str((args.jmeter / 'bin/ApacheJMeter.jar').resolve()),
                    '-n', '-t', str(folder / 'cap10-jmeter.jmx'), '-j', str(folder / 'jmeter.log'),
                    '-Jjmeter.save.saveservice.response_data=false', '-Jjmeter.save.saveservice.samplerData=false',
                    '-Jjmeter.save.saveservice.requestHeaders=false', '-Jjmeter.save.saveservice.responseHeaders=false']
            children.append(subprocess.Popen(argv, cwd=folder, stdout=log, stderr=subprocess.STDOUT,
                creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0)))
        deadline = time.monotonic() + 120
        while any(child.poll() is None for child in children) and time.monotonic() < deadline:
            store.tick(); time.sleep(.2)
        if any(child.poll() is None for child in children):
            store.fail('finite local wiring deadline')
            raise AssertionError('finite wiring did not finish')
        summary = store.summary()
        preparation_puts = sum(row['preparation'] for row in observations)
        native_puts = len(observations) - preparation_puts
        if args.case == 'success':
            expected = (summary.get('complete') is True and native_puts == 20 and preparation_puts == pool
                        and summary['preparation']['passed'] is True
                        and summary['counts']['mutation'] == 20 and summary['counts']['noop'] == 0
                        and summary['latencySampleCount'] == 20)
        elif args.case.startswith('preheat-'):
            kind = 'uncertain' if args.case == 'preheat-uncertain' else 'invalid'
            expected = (summary.get('complete') is False and summary['startUnixMs'] is None
                        and native_puts == 0 and 1 <= preparation_puts <= pool
                        and summary['preparation']['counts'][kind] >= 1 and summary['counts']['attempted'] == 0)
        else:
            expected = (summary.get('complete') is False and preparation_puts == pool
                        and 1 <= native_puts < 20 and summary['counts'][args.case] >= 1)
        result = {'scope':'finite loopback synthetic HTTP; no business DB/cloud/capacity slots', 'case':args.case, 'exitCodes':[c.returncode for c in children],
                  'stubPUTs':len(observations), 'stubPreparationPUTs':preparation_puts,
                  'stubNativePUTs':native_puts, 'collectorSummary':summary, 'manifest':manifest,
                  'actualJMeterProcesses':engines, 'normalWorkers':workers_per_engine * engines,
                  'exclusiveRelationships':pool, 'surplusEOFWorkers':workers_per_engine * engines - pool,
                  'teardownWorkers':engines, 'csvSplitSimulation':'disjoint slot modulo engine count; unequal shards retain EOF surplus workers',
                  'passed':expected and all(c.returncode==0 for c in children)}
        (root / 'result.json').write_text(json.dumps(result, indent=2))
        print(json.dumps({'passed':result['passed'],'artifactDirectory':str(root),'stubPUTs':len(observations),
                          'exitCodes':result['exitCodes'],'complete':summary.get('complete')}))
        return 0 if result['passed'] else 1
    finally:
        for child in children:
            if child.poll() is None:
                child.terminate(); child.wait(timeout=10)
        for handle in handles:
            handle.close()
        collector.shutdown(); collector.server_close(); api.shutdown(); api.server_close(); store.close()
        # Do not leave even synthetic collector credentials in local artifacts.
        for path in root.glob('engine-*/cap10-pts-private.json'):
            path.unlink()


if __name__ == '__main__':
    raise SystemExit(main())

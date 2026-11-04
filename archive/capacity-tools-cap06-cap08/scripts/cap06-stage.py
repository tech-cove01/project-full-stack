"""Run one CAP-06 stage using the existing guarded read workload/resource wrapper.

Diagnostic rounds are predeclared multi100/180 and consume one of three slots.
No credential values are printed; inherited private settings stay outside Git.
"""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.request

import psutil


REPO = Path(__file__).resolve().parent.parent
EVIDENCE = REPO / 'docs/evidence/2026-10-30-cap06'


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def save(path, value):
    with path.open('x', encoding='utf-8') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--environment', type=Path, required=True)
    parser.add_argument('--api-metadata', type=Path, required=True)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--kind', choices=('probe', 'diagnostic', 'formal'), required=True)
    parser.add_argument('--mode', choices=('hot', 'multi'), default='multi')
    parser.add_argument('--rate', type=int, default=100)
    parser.add_argument('--seconds', type=int, default=180)
    args = parser.parse_args()
    if Path.cwd().resolve() != REPO or not re.fullmatch(r'[a-z0-9][a-z0-9-]{1,60}', args.tag):
        parser.error('run from repository root with a new bounded stage tag')
    if not EVIDENCE.is_dir() or not 1 <= args.rate <= 200 or not 1 <= args.seconds <= 180:
        parser.error('existing CAP06 evidence root and bounded CAP06 workload required')
    if args.kind == 'diagnostic' and (args.mode, args.rate, args.seconds) != ('multi', 100, 180):
        parser.error('predeclared diagnostic rounds are multi100/180')
    if args.kind == 'probe' and (args.rate > 25 or args.seconds > 15):
        parser.error('probe rate/duration outside predeclared short bounds')
    settings, api = read(args.environment), read(args.api_metadata)
    private_path = args.environment.resolve()
    if private_path.parent.parent != REPO.parent or not re.fullmatch(r'\.runtime-fullstack-cap06-[a-f0-9]{12}', private_path.parent.name):
        parser.error('current CAP06 private runtime required')
    if args.api_metadata.resolve().parent != EVIDENCE or not re.fullmatch(r'api-[a-z0-9-]+\.json', args.api_metadata.name):
        parser.error('current CAP06 public API metadata required')
    if api['sourceSha'] != settings['CAP_SOURCE_SHA'] or api['expectedVersion'] != 'capacity-' + settings['CAP_SOURCE_SHA']:
        parser.error('current API metadata/source/revision mismatch')
    if settings['MYSQL_PORT'] != '3451' or settings['REDIS_PORT'] != '6551' or settings['API_BASE'] != 'http://127.0.0.1:8251':
        parser.error('dedicated read configuration required')
    process = psutil.Process(api['pid'])
    if os.path.normcase(process.exe()) != os.path.normcase(api['exe']) or abs(process.create_time() * 1000 - api['creationUnixMs']) > 2000:
        parser.error('current API executable/creation identity mismatch')
    if hashlib.sha256(Path(process.exe()).read_bytes()).hexdigest() != api['sha256']:
        parser.error('current API binary hash mismatch')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open('http://127.0.0.1:8251/health/ready', timeout=3) as response:
        health = json.loads(response.read(8193))
    if health.get('version') != api['expectedVersion'] or health.get('status') != 'UP':
        parser.error('current API version/health mismatch')
    if args.kind == 'diagnostic' and settings.get('GO_FEED_DIAGNOSTICS') != 'true':
        parser.error('diagnostic round requires explicit bounded request diagnostics')
    stem = EVIDENCE / args.tag
    load, control, diagnostic = (Path(str(stem) + suffix) for suffix in ('-load', '-control', '-diagnostic'))
    command_file = Path(str(stem) + '-command.json')
    if any(p.exists() for p in (load, control, diagnostic, command_file)):
        parser.error('stage paths already exist; preserve failures and select a new tag')
    env = {**os.environ, **{key: str(value) for key, value in settings.items()}}
    env.update(CAP_READ_MODE=args.mode, CAP_READ_RATE=str(args.rate), CAP_READ_SECONDS=str(args.seconds),
               CAP_READ_WARM_SECONDS='1' if args.kind == 'probe' else '10',
               CAP_READ_WARM_RATE=str(min(args.rate, 25)), CAP_READ_PRE_VUS='256', CAP_READ_MAX_VUS='512',
               CAP_READ_EVIDENCE=str(load), CAP_STOP_FILE=str(control / 'stop.json'))
    slot = None
    if args.kind == 'diagnostic':
        for number in range(1, 4):
            try:
                save(EVIDENCE / f'diagnostic-slot-{number}.json', {'task': 'CAP-06B', 'number': number,
                     'stage': args.tag, 'at': dt.datetime.now(dt.timezone.utc).isoformat(),
                     'apiSourceSha': api['sourceSha'], 'maximumRounds': 3, 'failedStartsAlsoConsumeSlot': True})
                slot = number
                break
            except FileExistsError:
                continue
        if slot is None:
            parser.error('three diagnostic rounds already reserved; no unbounded reruns')
    save(command_file, {'task': 'CAP-06', 'kind': args.kind, 'tag': args.tag, 'diagnosticSlot': slot,
                       'apiSourceSha': api['sourceSha'], 'apiPid': api['pid'], 'apiMetadataPath': str(args.api_metadata.resolve()),
                       'apiMetadataSHA256': hashlib.sha256(args.api_metadata.read_bytes()).hexdigest(),
                       'mode': args.mode, 'rate': args.rate, 'seconds': args.seconds,
                       'warmSeconds': int(env['CAP_READ_WARM_SECONDS']), 'preVUs': 256, 'maxVUs': 512,
                       'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'environmentValuesExported': False,
                       'scriptHashes': {name: hashlib.sha256((REPO / 'scripts' / name).read_bytes()).hexdigest()
                                        for name in ['cap06-stage.py', 'cap06-tail.py', 'capacity-stage.py', 'capacity-resources.py', 'go073-cap-read.mjs']}})
    flags = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0
    watcher = None
    with Path(str(stem) + '-diagnostic-console.txt').open('xb') as watcher_log:
        if args.kind == 'diagnostic':
            watcher = subprocess.Popen([sys.executable, '-X', 'utf8', str(REPO / 'scripts/cap06-tail.py'),
                       '--api-metadata', str(args.api_metadata.resolve()), '--load-dir', str(load), '--control-dir', str(control),
                       '--output-dir', str(diagnostic), '--max-seconds', '360'],
                       env=env, stdout=watcher_log, stderr=subprocess.STDOUT, creationflags=flags)
        command = [sys.executable, '-X', 'utf8', str(REPO / 'scripts/capacity-stage.py'), '--mode', 'read',
                   '--control-dir', str(control), '--api-pid', str(api['pid']), '--diagnostics',
                   'http://127.0.0.1:8351/debug/capacity', '--interval', '5', '--max-seconds', '360',
                   '--stop-grace-seconds', '90', '--', 'node', 'scripts/go073-cap-read.mjs', 'run']
        result = subprocess.run(command, env=env)
        watcher_code = None
        if watcher:
            try:
                watcher_code = watcher.wait(timeout=15)
            except subprocess.TimeoutExpired:
                watcher.terminate()
                watcher_code = watcher.wait(timeout=10)
        save(Path(str(stem) + '-stage-exit.json'), {'task': 'CAP-06', 'kind': args.kind,
             'capacityWrapperExit': result.returncode, 'extraDiagnosticCollectorExit': watcher_code,
             'finishedAt': dt.datetime.now(dt.timezone.utc).isoformat(),
             'limits': 'Wrapper exit is orchestration, not capacity classification. Diagnostic round is never a formal score.'})
    return result.returncode or (watcher_code or 0)


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('cap06-stage: ' + type(error).__name__ + '; details suppressed')
        raise SystemExit(1)

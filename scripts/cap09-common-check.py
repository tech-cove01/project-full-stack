"""Pure CAP09 shared-tool checks: no API, Docker, SQL or load is started."""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import unittest


REPO = Path(__file__).resolve().parent.parent


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, REPO / 'scripts' / path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


stage = module('cap09_shared_stage_checks', 'capacity-stage.py')
resources = module('cap09_shared_resource_checks', 'capacity-resources.py')


def identity(service):
    cpu, memory, port, host, destination = resources.CAP09_LIMITS[service]
    number = list(resources.CAP09_LIMITS).index(service) + 1
    cid = str(number) * 64
    volume = 'project-full-stack-cap09-' + 'a' * 12 + '-' + service
    expected = {'id': cid, 'cpuNano': cpu, 'memoryBytes': memory,
                'volumes': [{'name': volume, 'destination': destination}]}
    actual = {'id': cid, 'cpuNano': cpu, 'memoryBytes': memory, 'running': True,
              'labels': {'com.docker.compose.project': resources.CAP09_PROJECT, 'com.docker.compose.service': service},
              'ports': {port: [{'HostIp': '127.0.0.1', 'HostPort': host}]},
              'networkMode': resources.CAP09_PROJECT + '_default',
              'networks': {resources.CAP09_PROJECT + '_default': {}},
              'mounts': [{'Type': 'volume', 'Name': volume, 'Destination': destination}]}
    if service == 'kafka':
        # The stock Apache image creates these two additional anonymous mounts.
        for name, target in (('d' * 64, '/etc/kafka/secrets'), ('e' * 64, '/mnt/shared/config')):
            expected['volumes'].append({'name': name, 'destination': target})
            actual['mounts'].append({'Type': 'volume', 'Name': name, 'Destination': target})
    return expected, actual


class Checks(unittest.TestCase):
    def test_explicit_cap09_profile_does_not_change_legacy(self):
        self.assertEqual(stage.stage_profile('read'), {'diagnostics': 'http://127.0.0.1:8351/debug/capacity',
                         'project': 'project-full-stack-cap-read', 'script': 'go073-cap-read.js', 'output': 'CAP_READ_EVIDENCE'})
        self.assertEqual(stage.stage_profile('trade')['diagnostics'], 'http://127.0.0.1:8350/debug/capacity')
        self.assertEqual(stage.stage_profile('read', 'cap09')['project'], resources.CAP09_PROJECT)
        self.assertEqual(stage.stage_profile('like', 'cap09')['output'], 'CAP_LIKE_EVIDENCE')
        for mode, cohort in (('like', 'legacy'), ('trade', 'cap09'), ('read', 'other')):
            with self.subTest(mode=mode, cohort=cohort), self.assertRaises(ValueError):
                stage.stage_profile(mode, cohort)

    def test_owned_four_services_and_resource_limits(self):
        for service in resources.CAP09_LIMITS:
            with self.subTest(service=service):
                expected, actual = identity(service)
                checked = resources.cap09_container(expected, actual, service)
                self.assertEqual(checked['id'], expected['id'])
                self.assertEqual(checked['nonce'], 'a' * 12)
                self.assertEqual(len(checked['volumes']), 3 if service == 'kafka' else 1)

    def test_kafka_keeps_all_three_exact_named_and_anonymous_mounts(self):
        expected, actual = identity('kafka')
        checked = resources.cap09_container(expected, actual, 'kafka')
        self.assertEqual({value['name'] for value in checked['volumes']},
                         {value['name'] for value in expected['volumes']})
        for edit in ('missing', 'foreign_name', 'changed_destination', 'extra', 'duplicate_name', 'extra_bind', 'extra_tmpfs'):
            changed = copy.deepcopy(actual)
            if edit == 'missing':
                changed['mounts'].pop()
            elif edit == 'foreign_name':
                changed['mounts'][1]['Name'] = 'f' * 64
            elif edit == 'changed_destination':
                changed['mounts'][1]['Destination'] = '/somewhere-else'
            elif edit == 'extra':
                changed['mounts'].append({'Type': 'volume', 'Name': 'f' * 64, 'Destination': '/extra'})
            elif edit in ('extra_bind', 'extra_tmpfs'):
                changed['mounts'].append({'Type': 'bind' if edit == 'extra_bind' else 'tmpfs', 'Destination': '/extra'})
            else:
                changed['mounts'][2]['Name'] = changed['mounts'][1]['Name']
            with self.subTest(edit=edit), self.assertRaises(ValueError):
                resources.cap09_container(expected, changed, 'kafka')
        # Even a forged matching manifest may not rename the allowed anonymous
        # destinations or make non-hex names look like image-created volumes.
        for edit in ('wrong_destination', 'nonhex_name', 'duplicate_name'):
            changed_expected, changed = copy.deepcopy(expected), copy.deepcopy(actual)
            if edit == 'wrong_destination':
                changed_expected['volumes'][1]['destination'] = changed['mounts'][1]['Destination'] = '/other'
            elif edit == 'nonhex_name':
                changed_expected['volumes'][1]['name'] = changed['mounts'][1]['Name'] = 'foreign-volume'
            else:
                changed_expected['volumes'][2]['name'] = changed['mounts'][2]['Name'] = changed['mounts'][1]['Name']
            with self.subTest(forged=edit), self.assertRaises(ValueError):
                resources.cap09_container(changed_expected, changed, 'kafka')

    def test_owned_container_rejects_changed_id_limits_namespace_port_mount(self):
        expected, actual = identity('mysql')
        changes = [dict(id='f' * 64), dict(cpuNano=0), dict(memoryBytes=4 << 30), dict(running=False),
                   dict(labels={}), dict(ports={'3306/tcp': [{'HostIp': '0.0.0.0', 'HostPort': '3471'}]}),
                   dict(networkMode='other_default'), dict(networks={'other_default': {}}), dict(mounts=[])]
        for change in changes:
            with self.subTest(change=change), self.assertRaises(ValueError):
                resources.cap09_container(expected, {**actual, **change}, 'mysql')
        other = copy.deepcopy(actual)
        other['mounts'][0]['Name'] = 'an-older-volume'
        with self.assertRaises(ValueError):
            resources.cap09_container(expected, other, 'mysql')

    def test_four_container_stats_missing_foreign_nonfinite_not_zero(self):
        identities = [resources.cap09_container(*identity(service), service) for service in resources.CAP09_LIMITS]
        rows = [{'Name': resources.CAP09_PROJECT + '-' + value['service'] + '-1',
                 'ID': value['id'][:12], 'MemPerc': '5.2%'} for value in identities]
        resources.cap09_stats(rows, identities)
        for bad in (rows[:3], rows + [rows[0]], [*rows[:3], {**rows[3], 'ID': 'f' * 12}],
                    [*rows[:3], {**rows[3], 'Name': 'foreign'}], [*rows[:3], {**rows[3], 'MemPerc': 'NaN%'}]):
            with self.subTest(rows=bad), self.assertRaises(ValueError):
                resources.cap09_stats(bad, identities)

    def test_feed_native_clock_is_bounded_per_vu_and_legacy_is_unchanged(self):
        source = r"""
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
const source = readFileSync('scripts/load/go073-cap-read.js', 'utf8')
  .replace(/^import .*$/gm, '').replace('export const options', 'const options')
  .replace('export default function ()', 'function invoke()')
for (const cohort of ['cap09', undefined]) {
  const values = new Map()
  class Metric { constructor(name) { this.name = name; values.set(name, []) } add(value) { values.get(this.name).push(value) } }
  const ids = Array.from({ length: 20 }, (_, i) => String(100 - i))
  const reader = { expectedIds: ids, authorId: '1', token: 'synthetic-public-placeholder' }
  const fixture = { readers: [reader], pageSize: 20, baseUrl: 'http://127.0.0.1:1' }
  const context = { __ENV: { CAP_COHORT: cohort, CAP_READ_INPUT: 'unused', CAP_READ_RATE: '1', CAP_READ_SECONDS: '45', CAP_READ_MODE: 'multi' },
    open: () => JSON.stringify(fixture), SharedArray: function (_, load) { return load() }, Counter: Metric, Rate: Metric, Trend: Metric,
    exec: { scenario: { startTime: 1720000000000, iterationInTest: 0 } }, validCap09Feed: () => true,
    http: { get: () => ({ status: 200, timings: { duration: 1 }, json: () => ({ success: true, data: { source: 'database',
      items: ids.map(id => ({ id, userId: '1', liked: 0, comments: 0, likedByMe: false, shopId: '1' })) } }) }) }, console }
  vm.runInNewContext(source + '\nglobalThis.invoke = invoke; globalThis.testOptions = options', context)
  context.invoke(); context.invoke()
  if (cohort === 'cap09') {
    assert.deepEqual(values.get('cap_native_scenario_start_unix_ms'), [1720000000000])
    assert.deepEqual(values.get('cap_native_scenario_duration_ms'), [45000])
    assert.ok(context.testOptions.summaryTrendStats.includes('min'))
  } else {
    assert.equal(values.has('cap_native_scenario_start_unix_ms'), false)
    assert.equal(values.has('cap_native_scenario_duration_ms'), false)
    assert.equal(context.testOptions.summaryTrendStats.includes('min'), false)
  }
  assert.equal(values.get('cap_read_attempted').length, 2)
}
console.log(JSON.stringify({ nativeClockContractPassed: true, servicesStarted: false }))
"""
        result = subprocess.run(['node', '--input-type=module', '-e', source], cwd=REPO, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIs(json.loads(result.stdout)['nativeClockContractPassed'], True)

    def test_full_feed_contract_rejects_payload_cursor_and_personal_differences(self):
        # Node imports only the pure contract module: no k6 module, sockets or
        # fixture credentials exist in this deterministic counterexample.
        source = r"""
import assert from 'node:assert/strict'
import { validCap09Feed } from './scripts/load/cap09-feed-contract.js'
const fixture = { pageSize: 20, content: 'x'.repeat(256), shopName: 'Synthetic shop' }
const reader = { authorId: '1', expectedIds: Array.from({ length: 20 }, (_, i) => String(100 - i)),
  expectedTitles: Array.from({ length: 20 }, (_, i) => 'synthetic-' + i),
  expectedCreateTimes: Array(20).fill('2026-11-01 12:00:00') }
const page = { source: 'database', items: reader.expectedIds.map((id, i) => ({ id, userId: '1', shopId: '1',
  shopName: fixture.shopName, title: reader.expectedTitles[i], content: fixture.content, images: [],
  createTime: reader.expectedCreateTimes[i], liked: 0, comments: 0, likedByMe: false })),
  nextCursor: { id: '81', publishedAt: '2026-11-01 12:00:00' } }
assert.equal(validCap09Feed(page, reader, fixture), true)
for (const field of ['id', 'userId', 'shopId', 'shopName', 'title', 'content', 'images', 'createTime', 'liked', 'comments', 'likedByMe']) {
  const bad = structuredClone(page)
  bad.items[0][field] = field === 'images' ? ['wrong'] : field === 'likedByMe' ? true : 'wrong'
  assert.equal(validCap09Feed(bad, reader, fixture), false, field)
}
for (const change of [{ source: 'projection' }, { items: page.items.slice(1) }, { nextCursor: null },
  { nextCursor: { id: '82', publishedAt: '2026-11-01 12:00:00' } },
  { nextCursor: { id: '81', publishedAt: '2026-10-31 12:00:00' } }]) {
  assert.equal(validCap09Feed({ ...page, ...change }, reader, fixture), false)
}
assert.equal(validCap09Feed(page, { ...reader, expectedTitles: undefined }, fixture), false)
console.log(JSON.stringify({ fullFeedContractCounterexamplesPassed: true }))
"""
        result = subprocess.run(['node', '--input-type=module', '-e', source], cwd=REPO, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIs(json.loads(result.stdout)['fullFeedContractCounterexamplesPassed'], True)


if __name__ == '__main__':
    unittest.main(verbosity=2)

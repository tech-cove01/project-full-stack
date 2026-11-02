"""Actual owned fixture cleanup boundaries; synthetic files, no services or load."""
import importlib.util
import hashlib
import json
from pathlib import Path
import tempfile
import unittest


REPO = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location('cap10_fixture_environment_checks', REPO / 'scripts/cap10-env.py')
ENV = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ENV)


class Checks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='.qa-cap10-fixture-', dir=REPO.parent)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.original_root = ENV.ROOT
        self.addCleanup(setattr, ENV, 'ROOT', self.original_root)
        ENV.ROOT = self.root / 'evidence'
        ENV.ROOT.mkdir()
        self.runtime = self.root / 'runtime'
        self.credentials = self.runtime / 'tmp/cohort/input.json'
        self.credentials.parent.mkdir(parents=True)

    def fixture(self, count, *, explicit=True, status='READY', tokens=None):
        folder = ENV.ROOT / 'like-fixture-4096'
        folder.mkdir(exist_ok=True)
        manifest = {'users': count, 'multiNotes': count, 'multiRelations': count, 'status': status,
                    'credentialFile': str(self.credentials)}
        if explicit:
            manifest['poolSize'] = count
        manifest_path = folder / 'fixture.json'
        manifest_path.write_text(json.dumps(manifest), encoding='utf-8')
        self.credentials.write_text(json.dumps({'users': [{'token': token} for token in
            (tokens if tokens is not None else ['synthetic-' + str(i) for i in range(count)])]}), encoding='utf-8')
        return manifest_path

    def test_legacy_and_4096_keep_original_cleanup_cardinality(self):
        for count, explicit in [(512, False), (4096, True)]:
            path = self.fixture(count, explicit=explicit)
            credentials, tokens = ENV.credential_tokens(path, self.runtime)
            self.assertEqual(credentials, self.credentials)
            self.assertEqual(len(tokens), count)

    def test_partial_preparing_allowed_but_ready_and_aliases_rejected(self):
        path = self.fixture(4096, status='PREPARING', tokens=[])
        self.assertEqual(ENV.credential_tokens(path, self.runtime)[1], [])
        self.assertEqual(ENV.own_sessions([]), 0)
        for status, tokens in [('READY', []), ('READY', ['same'] * 4096), ('PREPARING', ['same'] * 2)]:
            with self.subTest(status=status, count=len(tokens)):
                with self.assertRaises(AssertionError):
                    ENV.credential_tokens(self.fixture(4096, status=status, tokens=tokens), self.runtime)
        with self.assertRaises(ValueError):
            ENV.credential_tokens(self.fixture(4096, explicit=False), self.runtime)

    def test_foreign_manifest_and_credentials_cannot_expand_cleanup(self):
        path = self.fixture(4096)
        foreign_manifest = self.root / 'fixture.json'
        foreign_manifest.write_bytes(path.read_bytes())
        with self.assertRaises(AssertionError):
            ENV.credential_tokens(foreign_manifest, self.runtime)
        value = json.loads(path.read_text())
        value['credentialFile'] = str(foreign_manifest)
        path.write_text(json.dumps(value), encoding='utf-8')
        with self.assertRaises(AssertionError):
            ENV.credential_tokens(path, self.runtime)

    def test_missing_private_input_only_allowed_for_preparing_recovery(self):
        for status in ['PREPARING', 'READY']:
            path = self.fixture(4096, status=status)
            self.credentials.unlink()
            if status == 'PREPARING':
                self.assertEqual(ENV.credential_tokens(path, self.runtime), (self.credentials, []))
            else:
                with self.assertRaises(AssertionError):
                    ENV.credential_tokens(path, self.runtime)

    def test_owned_environment_selects_versioned_metadata_without_changing_original(self):
        original = ENV.ROOT / 'environment.json'
        original.write_text('{}', encoding='utf-8')
        versioned = ENV.ROOT / 'environment-012345abcdef.json'
        versioned.write_text('{"resourceProfile":"mysql8-v1"}', encoding='utf-8')
        config = {'CAP10_ENVIRONMENT_METADATA': str(versioned), 'CAP10_RESOURCE_PROFILE': 'mysql8-v1'}
        metadata, path, profile = ENV.environment(config)
        self.assertEqual((path, profile), (versioned, 'mysql8-v1'))
        self.assertEqual(metadata['resourceProfile'], profile)
        self.assertEqual(original.read_text(), '{}')
        with self.assertRaises(ValueError):
            ENV.environment({'CAP10_ENVIRONMENT_METADATA': str(versioned)})
        api = {'environmentPath': versioned.name, 'resourceProfile': profile,
               'environmentSHA256': hashlib.sha256(versioned.read_bytes()).hexdigest()}
        self.assertEqual(ENV.api_environment(config, api)[1], versioned)
        with self.assertRaises(AssertionError):
            ENV.api_environment(config, {key: value for key, value in api.items() if key != 'environmentSHA256'})
        versioned.write_text('{"resourceProfile":"mysql8-v1","changed":true}', encoding='utf-8')
        with self.assertRaises(AssertionError):
            ENV.api_environment(config, api)
        self.assertEqual(ENV.api_environment({'CAP10_ENVIRONMENT_METADATA': str(original)}, {})[1], original)


if __name__ == '__main__':
    unittest.main(verbosity=2)

#!/usr/bin/env python3
"""Regression checks for the chart profile used in the real isolated test."""
import json
import os
import pathlib
import stat
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
CHART = ROOT / 'charts/clipp-relay'
PREPARE = ROOT / 'scripts/prepare-isolated-test-chart.py'

class IsolatedChartTests(unittest.TestCase):
    def setUp(self):
        self.work = tempfile.TemporaryDirectory()
        self.addCleanup(self.work.cleanup)
        self.root = pathlib.Path(self.work.name)
        self.output = self.root / 'chart'

    def prepare(self, destination=None):
        return subprocess.run(['python3', str(PREPARE), str(CHART), str(destination or self.output)], capture_output=True, text=True)

    def test_profile_preserves_source_and_renders_live_repairs(self):
        before = {str(p.relative_to(CHART)): p.read_bytes() for p in CHART.rglob('*') if p.is_file()}
        self.assertEqual(self.prepare().returncode, 0)
        after = {str(p.relative_to(CHART)): p.read_bytes() for p in CHART.rglob('*') if p.is_file()}
        self.assertEqual(before, after)
        rendered = subprocess.check_output(['helm', 'template', 'isolated', str(self.output), '-n', 'relay-portal-test', '-f', str(CHART / 'examples/bundled-test-values.yaml'), '-f', str(CHART / 'examples/letsencrypt-values.yaml')])
        docs = json.loads(subprocess.check_output(['ruby', '-ryaml', '-rjson', '-e', 'puts JSON.generate(YAML.load_stream(STDIN.read).compact)'], input=rendered))
        relay = next(x for x in docs if x['kind'] == 'StatefulSet' and x['metadata']['name'] == 'isolated-clipp-relay')
        self.assertEqual(relay['spec']['template']['spec']['containers'][0]['resources']['requests']['cpu'], '300m')
        pg = next(x for x in docs if x['kind'] == 'StatefulSet' and x['metadata']['name'].endswith('-postgres'))
        self.assertEqual(pg['spec']['template']['spec']['securityContext']['fsGroupChangePolicy'], 'OnRootMismatch')
        certs = [x for x in docs if x['kind'] == 'Certificate']
        self.assertEqual(len(certs), 2)
        for cert in certs:
            annotations = cert['metadata']['annotations']
            self.assertEqual(annotations['cert-manager.io/issue-temporary-certificate'], 'true')
            self.assertEqual(annotations['acme.cert-manager.io/http01-override-ingress-name'], cert['metadata']['name'])
            self.assertTrue(any(x['kind'] == 'Ingress' and x['metadata']['name'] == cert['metadata']['name'] for x in docs))

    def test_existing_data_permissions_are_repaired_after_identity_check(self):
        self.assertEqual(self.prepare().returncode, 0)
        data = self.root / 'pg/18/docker'
        data.mkdir(parents=True)
        (data / 'PG_VERSION').write_text('18')
        marker = self.root / 'pg/.clipp-pg18-owner'
        marker.write_text('relay-portal-test/isolated')
        binary = self.root / 'bin'
        binary.mkdir()
        postgres = binary / 'postgres'
        postgres.write_text('#!/bin/sh\necho "postgres (PostgreSQL) 18.6"\n')
        postgres.chmod(0o755)
        env = dict(os.environ, PATH=str(binary) + os.pathsep + os.environ['PATH'], CLIPP_PG_ROOT=str(self.root / 'pg'), CLIPP_NAMESPACE='relay-portal-test', CLIPP_RELEASE='isolated', CLIPP_INITIALIZE='false')
        data.chmod(0o2770)
        result = subprocess.run(['sh', str(self.output / 'files/postgres/initialize.sh')], env=env, capture_output=True)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(stat.S_IMODE(data.stat().st_mode), 0o700)
        marker.write_text('foreign/claim')
        data.chmod(0o2770)
        original_mode = stat.S_IMODE(data.stat().st_mode)
        result = subprocess.run(['sh', str(self.output / 'files/postgres/initialize.sh')], env=env, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(stat.S_IMODE(data.stat().st_mode), original_mode)

    def test_refuses_source_and_nonempty_destinations(self):
        self.assertNotEqual(self.prepare(CHART).returncode, 0)
        self.output.mkdir()
        (self.output / 'preserve.txt').write_text('user data')
        self.assertNotEqual(self.prepare().returncode, 0)
        self.assertEqual((self.output / 'preserve.txt').read_text(), 'user data')

if __name__ == '__main__':
    unittest.main()

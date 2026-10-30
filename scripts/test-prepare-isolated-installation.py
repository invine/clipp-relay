#!/usr/bin/env python3
"""Behavioral checks for the local installation preparation command."""

import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parent.parent
CHART = ROOT / "charts/clipp-relay"
SCRIPT = ROOT / "scripts/prepare-isolated-installation.py"


class InstallationPreparationTests(unittest.TestCase):
    def setUp(self):
        temp_root = pathlib.Path(tempfile.gettempdir()).resolve()
        self.temporary = tempfile.TemporaryDirectory(prefix="clipp-install-prep-", dir=str(temp_root))
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        self.work = self.root / "work"
        self.deployment = self.root / "deployment"
        self.validator = self.root / "validator"
        self.validator.write_text("#!/bin/sh\n[ -s \"$4\" ] || exit 9\n[ \"$2\" = validate-config ] || exit 8\nprintf 'configuration accepted\\n'\n")
        self.validator.chmod(0o700)

    def command(self):
        return [sys.executable, str(SCRIPT), "--repo", str(ROOT), "--work-dir", str(self.work),
                "--deployment-dir", str(self.deployment), "--source-chart", str(CHART),
                "--values", str(CHART / "examples/bundled-test-values.yaml"),
                "--overlay", str(CHART / "examples/letsencrypt-values.yaml"),
                "--release", "isolated", "--namespace", "relay-portal-test", "--run-id", "deadbeef",
                "--migration-secret", "clipp-db-migration-v1", "--ca-secret", "clipp-db-ca-v1",
                "--native-secret", "clipp-native-oauth-v1", "--install-fingerprint", "a" * 64,
                "--relay-binary", str(self.validator)]

    def run_prepare(self):
        return subprocess.run(self.command(), capture_output=True, text=True)

    def test_prepares_and_reuses_chart_and_both_jobs(self):
        result = self.run_prepare()
        self.assertEqual(result.returncode, 0, result.stderr)
        artifacts = json.loads(result.stdout)
        chart = pathlib.Path(artifacts["chart_dir"])
        migration_path = pathlib.Path(artifacts["migration_manifest"])
        reset_path = pathlib.Path(artifacts["reset_manifest"])
        migration, reset = json.loads(migration_path.read_text()), json.loads(reset_path.read_text())

        self.assertTrue((chart / "Chart.yaml").is_file())
        rendered = subprocess.check_output([
            "helm", "template", "isolated", str(chart), "-n", "relay-portal-test",
            "-f", str(CHART / "examples/bundled-test-values.yaml"),
            "-f", str(CHART / "examples/letsencrypt-values.yaml"),
        ], text=True)
        resources = json.loads(subprocess.check_output([
            "ruby", "-ryaml", "-rjson", "-e",
            "puts JSON.generate(YAML.load_stream(STDIN.read).compact)",
        ], input=rendered, text=True))
        relay = next(doc for doc in resources if doc.get("kind") == "StatefulSet" and doc["metadata"]["name"] == "isolated-clipp-relay")
        postgres = next(doc for doc in resources if doc.get("kind") == "StatefulSet" and doc["metadata"]["name"].endswith("-postgres"))
        self.assertEqual(relay["spec"]["template"]["spec"]["containers"][0]["resources"]["requests"]["cpu"], "300m")
        self.assertEqual(postgres["spec"]["template"]["spec"]["containers"][0]["resources"]["requests"]["cpu"], "400m")
        self.assertEqual(migration["metadata"]["namespace"], "relay-portal-test")
        self.assertEqual(migration["spec"]["template"]["spec"]["containers"][0]["resources"]["requests"]["cpu"], "300m")
        self.assertEqual(migration["spec"]["template"]["spec"]["volumes"][1]["secret"]["secretName"], "clipp-db-migration-v1")
        self.assertEqual(reset["metadata"]["namespace"], "relay-portal-test")
        reset_container = reset["spec"]["template"]["spec"]["containers"][0]
        self.assertEqual(reset_container["args"][1], "reset-auth")
        migration_refs = {v.get("secret", {}).get("secretName") for v in migration["spec"]["template"]["spec"]["volumes"] if v.get("secret")}
        reset_refs = {v.get("secret", {}).get("secretName") for v in reset["spec"]["template"]["spec"]["volumes"] if v.get("secret")}
        self.assertEqual(migration_refs, {"clipp-db-migration-v1", "clipp-db-ca-v1"})
        self.assertEqual(reset_refs, {"clipp-db-migration-v1", "clipp-db-ca-v1", "clipp-native-oauth-v1"})
        for path in (migration_path, reset_path):
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

        again = self.run_prepare()
        self.assertEqual(again.returncode, 0, again.stderr)
        self.assertEqual(json.loads(again.stdout), artifacts)

    def test_changed_retained_artifact_is_refused(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        artifacts = json.loads(first.stdout)
        pathlib.Path(artifacts["reset_manifest"]).write_text("{}\n")
        retry = self.run_prepare()
        self.assertNotEqual(retry.returncode, 0)
        self.assertIn("Retained artifact differs", retry.stderr)

    def test_changed_retained_chart_is_refused(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        chart = pathlib.Path(json.loads(first.stdout)["chart_dir"])
        (chart / "Chart.yaml").write_text("changed\n")
        retry = self.run_prepare()
        self.assertNotEqual(retry.returncode, 0)
        self.assertIn("Retained chart has changed", retry.stderr)

    def test_validator_failure_does_not_create_job_manifests(self):
        self.validator.write_text("#!/bin/sh\necho rejected >&2\nexit 1\n")
        self.validator.chmod(0o700)
        result = self.run_prepare()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("rejected", result.stderr)
        self.assertFalse(list(self.deployment.iterdir()))

    def test_rejects_symlink_work_directory(self):
        real = self.root / "real-work"
        real.mkdir()
        self.work.symlink_to(real)
        result = self.run_prepare()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("symbolic link", result.stderr)


if __name__ == "__main__":
    unittest.main()

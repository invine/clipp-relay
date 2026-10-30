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

    def observe_command(self, observations, extra_args=()):
        path = self.root / "observations.json"
        path.write_text(json.dumps(observations))
        return subprocess.run(self.command() + ["--observations-file", str(path), *extra_args], capture_output=True, text=True)

    def reviewed_values(self, *, phase="migrating", run_id="deadbeef"):
        source = r'''
          require "yaml"; require "json"
          values = YAML.safe_load(File.read(ARGV.fetch(0)))
          overlay = YAML.safe_load(File.read(ARGV.fetch(1)))
          overlay.each { |key, value| values[key] = values.fetch(key, {}).merge(value) }
          puts JSON.generate(values)
        '''
        values_path = CHART / "examples/bundled-test-values.yaml"
        overlay_path = CHART / "examples/letsencrypt-values.yaml"
        values = json.loads(subprocess.check_output([
            "ruby", "-ryaml", "-rjson", "-e", source, str(values_path), str(overlay_path)], text=True))
        values.setdefault("deployment", {}).update({"phase": phase, "runId": run_id})
        values["database"]["bundled"]["initialize"] = phase == "stopped"
        return values

    def rendered_bootstrap_job(self):
        rendered = subprocess.check_output([
            "helm", "template", "isolated", str(CHART), "-n", "relay-portal-test",
            "-f", str(CHART / "examples/bundled-test-values.yaml"),
            "-f", str(CHART / "examples/letsencrypt-values.yaml"),
            "--set", "deployment.phase=migrating", "--set-string", "deployment.runId=deadbeef",
            "--set", "database.bundled.initialize=false",
        ], text=True)
        source = "require 'yaml'; require 'json'; puts JSON.generate(YAML.load_stream(STDIN.read).compact)"
        documents = json.loads(subprocess.check_output(
            ["ruby", "-ryaml", "-rjson", "-e", source], input=rendered, text=True))
        return next(doc for doc in documents if doc.get("kind") == "Job" and
                    doc.get("metadata", {}).get("name", "").endswith("-bootstrap-deadbeef"))

    def completed_observations(self, artifacts):
        migration = json.loads(pathlib.Path(artifacts["migration_manifest"]).read_text())
        reset = json.loads(pathlib.Path(artifacts["reset_manifest"]).read_text())
        success_log = [{"event": "service.startup", "outcome": "success", "schema_revision": 16}]
        bootstrap = self.rendered_bootstrap_job() | {"status": {"succeeded": 1}}
        bootstrap["metadata"]["namespace"] = "relay-portal-test"
        postgres_pod = {"metadata": {"name": "isolated-clipp-relay-postgres-0",
                                      "namespace": "relay-portal-test"},
                       "spec": {"initContainers": [{"name": "initialize", "env": [
                           {"name": "CLIPP_INITIALIZE", "value": "false"}]}]}}
        return {
            "release": {"name": "isolated", "namespace": "relay-portal-test", "status": "deployed",
                        "values": self.reviewed_values()},
            "bootstrap": {"job": bootstrap, "pod": postgres_pod},
            "jobs": {"migration": migration | {"status": {"succeeded": 1}},
                     "migration_logs": success_log,
                     "reset": reset | {"status": {"succeeded": 1}},
                     "reset_logs": success_log},
        }

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

    def test_preparation_interprets_completed_retained_evidence(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        artifacts = json.loads(first.stdout)
        result = self.observe_command(self.completed_observations(artifacts))
        self.assertEqual(result.returncode, 0, result.stderr)
        evidence = json.loads(result.stdout)["observations"]
        self.assertEqual(evidence, {"release": "migrating", "release_run_id": "deadbeef",
                                    "release_initialize": False, "bootstrap": "completed",
                                    "recorded_serving_release": False, "extension_id": None,
                                    "migration": "completed", "reset": "completed"})

    def test_changed_retained_bootstrap_job_identity_or_spec_is_refused(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        artifacts = json.loads(first.stdout)
        mutations = (
            ("labels", lambda job: job["metadata"].setdefault("labels", {}).__setitem__("foreign", "true")),
            ("annotations", lambda job: job["metadata"]["annotations"].__setitem__("foreign", "true")),
            ("full spec", lambda job: job["spec"]["template"]["spec"].__setitem__("automountServiceAccountToken", True)),
        )
        for label, mutate in mutations:
            with self.subTest(label=label):
                observations = self.completed_observations(artifacts)
                mutate(observations["bootstrap"]["job"])
                result = self.observe_command(observations)
                self.assertNotEqual(result.returncode, 0, f"accepted changed bootstrap Job {label}")

    def test_bootstrap_initialization_is_read_from_raw_pod_observation(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        observations = self.completed_observations(json.loads(first.stdout))
        observations["bootstrap"]["pod"]["spec"]["initContainers"][0]["env"][0]["value"] = "true"
        result = self.observe_command(observations)
        self.assertEqual(result.returncode, 0, result.stderr)
        evidence = json.loads(result.stdout)["observations"]
        self.assertEqual(evidence["bootstrap"], "incomplete")

    def test_foreign_retained_job_evidence_is_refused(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        observations = self.completed_observations(json.loads(first.stdout))
        observations["jobs"]["migration"]["metadata"]["annotations"]["clipp-relay-test/install-fingerprint"] = "foreign"
        result = self.observe_command(observations)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("foreign migration Job", result.stderr)

    def test_changed_retained_job_spec_is_refused(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        artifacts = json.loads(first.stdout)
        mutations = (
            ("additional volume", lambda job: job["spec"]["template"]["spec"]["volumes"].append(
                {"name": "foreign", "emptyDir": {}})),
            ("service account token", lambda job: job["spec"]["template"]["spec"].__setitem__(
                "automountServiceAccountToken", True)),
            ("container security context", lambda job: job["spec"]["template"]["spec"]["containers"][0].__setitem__(
                "securityContext", {"privileged": True})),
            ("image pull policy", lambda job: job["spec"]["template"]["spec"]["containers"][0].__setitem__(
                "imagePullPolicy", "Always")),
            ("retry policy", lambda job: job["spec"].__setitem__("backoffLimit", 6)),
        )
        for label, mutate in mutations:
            with self.subTest(label=label):
                observations = self.completed_observations(artifacts)
                mutate(observations["jobs"]["migration"])
                result = self.observe_command(observations)
                self.assertNotEqual(result.returncode, 0, f"accepted changed Job {label}")
                self.assertIn("configuration differs", result.stderr)

    def test_api_defaulted_job_spec_is_still_accepted(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        artifacts = json.loads(first.stdout)
        observations = self.completed_observations(artifacts)
        job = observations["jobs"]["migration"]
        uid = "api-generated-uid"
        job["metadata"]["uid"] = uid
        job["spec"]["parallelism"] = 1
        job["spec"]["completions"] = 1
        job["spec"]["completionMode"] = "NonIndexed"
        job["spec"]["selector"] = {"matchLabels": {
            "batch.kubernetes.io/controller-uid": uid,
            "batch.kubernetes.io/job-name": job["metadata"]["name"],
        }}
        pod_template = job["spec"]["template"]
        pod_template["metadata"]["labels"].update({
            "batch.kubernetes.io/controller-uid": uid,
            "batch.kubernetes.io/job-name": job["metadata"]["name"],
        })
        pod = pod_template["spec"]
        pod.update({"serviceAccountName": "default", "dnsPolicy": "ClusterFirst",
                    "schedulerName": "default-scheduler", "terminationGracePeriodSeconds": 30,
                    "enableServiceLinks": True, "preemptionPolicy": "PreemptLowerPriority"})
        pod["containers"][0].update({"terminationMessagePath": "/dev/termination-log",
                                     "terminationMessagePolicy": "File", "stdin": False,
                                     "stdinOnce": False, "tty": False})
        result = self.observe_command(observations)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["observations"]["migration"], "completed")

    def test_pending_failed_and_incomplete_observations_are_distinguished(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        artifacts = json.loads(first.stdout)
        for job_status, logs, expected in (
            ({"active": 1}, [], "pending"),
            ({"failed": 1}, [], "failed"),
            ({"succeeded": 1}, [], "incomplete"),
        ):
            observations = self.completed_observations(artifacts)
            observations["jobs"]["migration"]["status"] = job_status
            observations["jobs"]["migration_logs"] = logs
            result = self.observe_command(observations)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(result.stdout)["observations"]["migration"], expected)

    def test_mismatched_release_namespace_is_refused(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        observations = self.completed_observations(json.loads(first.stdout))
        observations["release"]["namespace"] = "foreign-namespace"
        result = self.observe_command(observations)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("different release or namespace", result.stderr)

    def test_mismatched_release_run_identity_is_refused(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        observations = self.completed_observations(json.loads(first.stdout))
        observations["release"]["values"]["deployment"]["runId"] = "cafebabe"
        result = self.observe_command(observations)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("does not match the reviewed run identity", result.stderr)

    def test_changed_installed_release_values_are_refused_by_preparation(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        observations = self.completed_observations(json.loads(first.stdout))
        observations["release"]["values"]["image"]["repository"] = "example.invalid/foreign"
        result = self.observe_command(observations)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Installed release values differ", result.stderr)

    def test_changes_to_unselected_installed_release_values_are_refused(self):
        for section in ("listeners", "networkPolicy", "accounting"):
            with self.subTest(section=section):
                first = self.run_prepare()
                self.assertEqual(first.returncode, 0, first.stderr)
                observations = self.completed_observations(json.loads(first.stdout))
                observations["release"]["values"][section] = {"changed": True}
                result = self.observe_command(observations)
                self.assertNotEqual(result.returncode, 0, f"accepted changed {section} values")
                self.assertIn("Installed release values differ", result.stderr)

    def test_recorded_serving_release_exception_is_classified_by_preparation(self):
        first = self.run_prepare()
        self.assertEqual(first.returncode, 0, first.stderr)
        observations = self.completed_observations(json.loads(first.stdout))
        recorded = self.reviewed_values(phase="serving", run_id="")
        recorded["image"]["repository"] = "registry.example.test/reviewed-serving-image"
        recorded["clients"]["extensionRedirect"] = "https://abcdefghijklmnopabcdefghijklmnop.chromiumapp.org/clipp-relay"
        values_path = self.root / "recorded-serving-values.json"
        payload = (json.dumps(recorded, sort_keys=True) + "\n").encode()
        values_path.write_bytes(payload)
        digest = __import__("hashlib").sha256(payload).hexdigest()
        observations["release"]["values"] = recorded
        observations["release"]["values"]["deployment"].update({"phase": "serving", "runId": ""})
        result = self.observe_command(observations, ["--recorded-serving-values", str(values_path),
                                                     "--recorded-serving-values-sha256", digest])
        self.assertEqual(result.returncode, 0, result.stderr)
        evidence = json.loads(result.stdout)["observations"]
        self.assertTrue(evidence["recorded_serving_release"])
        self.assertEqual(evidence["extension_id"], "abcdefghijklmnopabcdefghijklmnop")


if __name__ == "__main__":
    unittest.main()

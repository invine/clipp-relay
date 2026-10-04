#!/usr/bin/env python3
"""Exercise installation evidence through the Helm render and inspection CLI."""
import json
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
INSPECT = ROOT / "scripts/inspect-install-metadata.py"

class InstallMetadataTests(unittest.TestCase):
    def snapshot(self, bundled=True):
        rendered = subprocess.check_output(["helm", "template", "isolated", str(ROOT / "charts/clipp-relay"), "-n", "relay-portal-test", "-f", str(ROOT / ("charts/clipp-relay/examples/bundled-test-values.yaml" if bundled else "charts/clipp-relay/examples/external-values.yaml"))])
        docs = json.loads(subprocess.check_output(["ruby", "-ryaml", "-rjson", "-e", "puts JSON.generate(YAML.load_stream(STDIN.read).compact)"], input=rendered))
        sts = next(d for d in docs if d["kind"] == "StatefulSet" and d["metadata"]["name"] == "isolated-clipp-relay")
        cm = next(d for d in docs if d["kind"] == "ConfigMap" and d["metadata"]["name"] == "isolated-clipp-relay-config")
        for doc in (sts, cm):
            doc["metadata"]["namespace"] = "relay-portal-test"
        # A Ready fixture does not transform bundled mode into external evidence.
        pod = {"metadata": {"name": "isolated-clipp-relay-0", "namespace": "relay-portal-test"}, "spec": sts["spec"]["template"]["spec"], "status": {"conditions": [{"type": "Ready", "status": "True"}]}}
        sts["metadata"]["uid"] = "f733e899-a13c-4b72-81cd-403c176e635e"
        sts["status"] = {"updateRevision": "isolated-clipp-relay-abcdef"}
        pod["metadata"]["ownerReferences"] = [{"apiVersion": "apps/v1", "kind": "StatefulSet", "name": "isolated-clipp-relay", "uid": sts["metadata"]["uid"], "controller": True}]
        pod["metadata"]["labels"] = {"controller-revision-hash": "isolated-clipp-relay-abcdef"}
        return {"statefulset": sts, "configmap": cm, "pod": pod}

    def inspect(self, objects):
        with tempfile.TemporaryDirectory() as work:
            snapshot = pathlib.Path(work) / "snapshot.json"
            snapshot.write_text(json.dumps(objects))
            result = subprocess.run(["python3", str(INSPECT), "--snapshot", str(snapshot), "--namespace", "relay-portal-test", "--release", "isolated"], capture_output=True, text=True)
        return result

    def test_bundled_test_cannot_qualify_external_install(self):
        result = self.inspect(self.snapshot())
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads(result.stdout)
        self.assertEqual(report["checks"]["external_database_config"], "FAIL")
        self.assertFalse(report["acceptance_complete"])
        self.assertEqual(report["runtime_gates"]["postgresql_17_18_tls_scram_schema_rejections"], "NOT RUN")

    def test_live_resource_difference_cannot_inherit_rendered_budget(self):
        objects = self.snapshot(bundled=False)
        # Kubernetes objects are independent snapshots, not shared Python references.
        objects = json.loads(json.dumps(objects))
        objects["pod"]["spec"]["containers"][0]["resources"]["requests"]["cpu"] = "300m"
        result = self.inspect(objects)
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads(result.stdout)
        self.assertEqual(report["checks"]["pod_matches_template_image_resources_security"], "FAIL")
        self.assertEqual(report["observed_pod_cpu_request"], "300m")
        self.assertFalse(report["acceptance_complete"])

    def test_wrong_target_and_malformed_config_fail_without_private_output(self):
        for wrong_target in (True, False):
            with self.subTest(wrong_target=wrong_target):
                objects = self.snapshot(bundled=False)
                canary = "DO-NOT-PRINT-private-config-canary"
                if wrong_target:
                    objects["pod"]["metadata"]["namespace"] = "wrong-namespace"
                else:
                    objects["configmap"]["data"]["config.json"] = json.dumps({"database": {"private": canary}})
                result = self.inspect(objects)
                self.assertEqual(result.returncode, 2)
                self.assertNotIn(canary, result.stdout + result.stderr)
                self.assertNotIn("Traceback", result.stderr)
                self.assertEqual(result.stdout, "")

    def test_matching_external_metadata_keeps_runtime_acceptance_pending(self):
        objects = self.snapshot(bundled=False)
        config = json.loads(objects["configmap"]["data"]["config.json"])
        config["private_test_canary"] = "DO-NOT-PRINT-private-config-canary"
        objects["configmap"]["data"]["config.json"] = json.dumps(config)
        result = self.inspect(objects)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("DO-NOT-PRINT", result.stdout + result.stderr)
        report = json.loads(result.stdout)
        self.assertTrue(all(value == "PASS metadata" for value in report["checks"].values()))
        self.assertTrue(all(value == "NOT RUN" for value in report["runtime_gates"].values()))
        self.assertFalse(report["acceptance_complete"])

    def test_container_override_cannot_hide_unsafe_security_configuration(self):
        for override in ({"capabilities": {"drop": ["ALL"], "add": ["SYS_ADMIN"]}}, {"runAsUser": 0}, {"runAsNonRoot": False}, {"seccompProfile": {"type": "Unconfined"}}):
            with self.subTest(override=override):
                objects = self.snapshot(bundled=False)
                objects["statefulset"]["spec"]["template"]["spec"]["containers"][0]["securityContext"].update(override)
                result = self.inspect(objects)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(json.loads(result.stdout)["checks"]["configured_security"], "FAIL")

    def test_malformed_metadata_never_echoes_private_input(self):
        canary = "DO-NOT-PRINT-private-metadata-canary"
        for field in ("user", "drop", "requests", "cpu"):
            with self.subTest(field=field):
                objects = self.snapshot(bundled=False)
                container = objects["statefulset"]["spec"]["template"]["spec"]["containers"][0]
                if field == "user":
                    container["securityContext"]["runAsUser"] = canary
                elif field == "drop":
                    container["securityContext"]["capabilities"]["drop"] = None
                elif field == "requests":
                    container["resources"]["requests"] = [canary]
                else:
                    container["resources"]["requests"]["cpu"] = canary
                result = self.inspect(objects)
                self.assertEqual(result.returncode, 2)
                self.assertNotIn(canary, result.stdout + result.stderr)
                self.assertNotIn("Traceback", result.stderr)
                self.assertEqual(result.stdout, "")

    def test_named_foreign_or_stale_ready_pod_cannot_claim_current_installation(self):
        for stale in (True, False):
            with self.subTest(stale=stale):
                objects = self.snapshot(bundled=False)
                if stale:
                    objects["pod"]["metadata"]["labels"]["controller-revision-hash"] = "old-revision"
                else:
                    objects["pod"]["metadata"]["ownerReferences"][0]["uid"] = "foreign-uid"
                result = self.inspect(objects)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(json.loads(result.stdout)["checks"]["pod_owner_and_intended_revision"], "FAIL")

    def test_render_only_snapshot_has_no_pod_identity_proof(self):
        objects = self.snapshot(bundled=False)
        objects["statefulset"]["metadata"].pop("uid")
        result = self.inspect(objects)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(json.loads(result.stdout)["checks"]["pod_owner_and_intended_revision"], "NOT RUN")

if __name__ == "__main__":
    unittest.main()

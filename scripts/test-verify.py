#!/usr/bin/env python3
"""Exercise the public verification command with narrow subprocess fixtures."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class VerificationTests(unittest.TestCase):
    def setUp(self):
        self.work = tempfile.TemporaryDirectory(prefix="relay verification ")
        self.addCleanup(self.work.cleanup)
        self.root = Path(self.work.name)
        (self.root / "scripts").mkdir()
        (self.root / "bin").mkdir()
        shutil.copyfile(ROOT / "scripts/verify.sh", self.root / "scripts/verify.sh")
        (self.root / "go.mod").write_text("module fixture\n\ngo 1.27.1\n")
        (self.root / "sample file.go").write_text("package fixture\n")
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        self.env = dict(os.environ, PATH=str(self.root / "bin") + os.pathsep + os.environ["PATH"])
        for suite in (
            "test-image-build.sh", "test-image-smoke.sh", "test-image-index.sh",
            "test-image-values.sh", "test-chart.sh", "test-chart-bundled.sh",
            "test-chart-test-network.sh", "test-postgres-init-guard.sh",
            "test-preflight-image-inputs.sh",
        ):
            self.write_command(self.root / "scripts" / suite, "exit 0\n")
        for suite in ("test-isolated-test-chart.py", "test-install-metadata.py", "test-verify.py"):
            (self.root / "scripts" / suite).write_text("raise SystemExit(0)\n")
        for tool, body in {
            "go": 'case "$*" in\n version) echo "go version go1.27.1 linux/amd64" ;;\n "env CGO_ENABLED") echo "${FIXTURE_CGO:-1}" ;;\n "env CC") echo cc ;;\n *) if [ "$*" = "${FIXTURE_FAIL:-}" ]; then exit 17; fi ;;\nesac\n',
            "gofmt": 'if [ "${FIXTURE_FORMAT:-}" = dirty ]; then echo "sample file.go"; fi\n',
            "helm": 'echo "v4.3.0"\n',
            "ruby": 'echo "ruby fixture"\n',
            "jq": 'echo "jq-1.7"\n',
            "cc": 'echo "cc fixture"\n',
        }.items():
            self.write_command(self.root / "bin" / tool, body)

    def write_command(self, path, body):
        path.write_text("#!/bin/sh\n" + body)
        path.chmod(0o755)

    def verify(self, *args):
        return subprocess.run(["bash", str(self.root / "scripts/verify.sh"), *args], cwd=self.root, env=self.env, capture_output=True, text=True)

    def test_failed_go_check_fails_the_public_command(self):
        self.env["FIXTURE_FAIL"] = "test -count=1 ./..."
        result = self.verify()
        self.assertEqual(result.returncode, 17, result.stdout + result.stderr)
        self.assertIn("FAIL routine: Go tests", result.stderr)
        self.assertNotIn("PASS routine", result.stdout)

    def test_failed_deterministic_suite_fails_the_public_command(self):
        self.write_command(self.root / "scripts/test-image-build.sh", "exit 23\n")
        result = self.verify()
        self.assertEqual(result.returncode, 23, result.stdout + result.stderr)
        self.assertIn("FAIL routine: scripts/test-image-build.sh", result.stderr)
        self.assertNotIn("PASS routine", result.stdout)

    def test_race_profile_propagates_race_failure_separately(self):
        self.env["FIXTURE_FAIL"] = "test -race -count=1 ./..."
        result = self.verify("race")
        self.assertEqual(result.returncode, 17, result.stdout + result.stderr)
        self.assertIn("FAIL race: Go race tests", result.stderr)
        self.assertNotIn("PASS race", result.stdout)

    def test_successful_routine_can_run_from_another_directory(self):
        result = subprocess.run(["bash", str(self.root / "scripts/verify.sh")], cwd=self.root / "bin", env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("PASS routine", result.stdout)
        self.assertNotIn("PASS race", result.stdout)

    def test_routine_result_reports_separate_acceptance_not_run(self):
        result = self.verify()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("NOT RUN by routine: race", result.stdout)
        self.assertIn("NOT RUN: real PostgreSQL", result.stdout)

    def test_failed_late_python_suite_cannot_claim_a_routine_pass(self):
        (self.root / "scripts/test-install-metadata.py").write_text("raise SystemExit(19)\n")
        result = self.verify()
        self.assertEqual(result.returncode, 19, result.stdout + result.stderr)
        self.assertIn("FAIL routine: scripts/test-install-metadata.py", result.stderr)
        self.assertNotIn("PASS routine", result.stdout)

    def test_unknown_profile_is_rejected(self):
        result = self.verify("publish")
        self.assertEqual(result.returncode, 2)
        self.assertIn("[routine|race]", result.stderr)

    def test_missing_required_tool_is_actionable(self):
        tools = self.root / "required tools"
        tools.mkdir()
        for tool in ("dirname", "git", "go", "gofmt", "mktemp", "rm", "bash"):
            target = self.root / "bin" / tool
            (tools / tool).symlink_to(target if target.exists() else shutil.which(tool))
        self.env["PATH"] = str(tools)
        result = subprocess.run(["/bin/bash", str(self.root / "scripts/verify.sh")], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
        self.assertIn("Missing required tool: helm", result.stderr)
        self.assertIn("docs/verification.md", result.stderr)

    def test_unformatted_go_is_reported_without_rewriting_sources(self):
        source = self.root / "sample file.go"
        before = source.read_bytes()
        self.env["FIXTURE_FORMAT"] = "dirty"
        result = self.verify()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Go formatting differs", result.stderr)
        self.assertIn("sample file.go", result.stderr)
        self.assertEqual(source.read_bytes(), before)

    def test_race_requires_cgo(self):
        self.env["FIXTURE_CGO"] = "0"
        result = self.verify("race")
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
        self.assertIn("requires CGO_ENABLED=1", result.stderr)


class VerificationWorkflowTests(unittest.TestCase):
    def test_ci_runs_public_profiles_with_read_only_routine_events(self):
        result = subprocess.run(["ruby", "-ryaml", "-rjson", "-e", "puts JSON.generate(YAML.load_file(ARGV[0]))", str(ROOT / ".github/workflows/verify.yml")], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        workflow = json.loads(result.stdout)
        # Psych versions using YAML 1.1 parse the 'on' key as true.
        events = workflow.get("on", workflow.get("true"))
        self.assertIn("pull_request", events)
        self.assertEqual(events["push"]["branches"], ["main"])
        self.assertEqual(workflow["permissions"], {"contents": "read"})
        runs = []
        for job in workflow["jobs"].values():
            self.assertEqual(job.get("permissions", workflow["permissions"]), {"contents": "read"})
            self.assertNotIn("environment", job)
            runs.extend(step.get("run", "") for step in job["steps"])
            for step in job["steps"]:
                if step.get("uses", "").startswith("actions/checkout@"):
                    self.assertFalse(step["with"]["persist-credentials"])
        self.assertIn("bash scripts/verify.sh routine", runs)
        self.assertIn("bash scripts/verify.sh race", runs)
        self.assertNotIn("secrets.", json.dumps(workflow))


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Prepare a local, immutable bundle for one isolated relay installation.

This command never contacts a cluster. It owns the chart output path, renders
the runtime configuration, validates it with the relay's configuration rules,
and prepares the migration and credential-reset Jobs before the wizard offers
any live operation.
"""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
from typing import NoReturn


ROOT = pathlib.Path(__file__).resolve().parent.parent
PREPARE_CHART = ROOT / "scripts/prepare-isolated-test-chart.py"
NAME_RE = re.compile(r"^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$", re.ASCII)


def fail(message: str) -> NoReturn:
    raise SystemExit(message)


def reject_symlink_components(path: pathlib.Path) -> None:
    current = pathlib.Path(path.anchor)
    for part in path.parts[1:]:
        current /= part
        if current.is_symlink():
            fail(f"Refusing a symbolic link in preparation path: {current}")


def directory(path: str, *, create: bool) -> pathlib.Path:
    result = pathlib.Path(path)
    if not result.is_absolute():
        fail(f"Preparation paths must be absolute: {path}")
    reject_symlink_components(result)
    if create:
        result.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chmod(result, 0o700)
    if not result.is_dir():
        fail(f"Preparation directory is unavailable: {result}")
    return result


def checked(command: list[str], *, cwd: pathlib.Path | None = None, input: str | None = None) -> str:
    result = subprocess.run(command, cwd=cwd, input=input, text=True, capture_output=True)
    if result.returncode:
        diagnostic = result.stderr.strip()
        fail(diagnostic or f"Command failed: {pathlib.Path(command[0]).name}")
    return result.stdout


def render_documents(release: str, chart: pathlib.Path, namespace: str, values: pathlib.Path,
                     overlay: pathlib.Path, *, phase: str, run_id: str,
                     initialize: bool) -> list[dict]:
    rendered = checked([
        "helm", "template", release, str(chart), "-n", namespace,
        "-f", str(values), "-f", str(overlay),
        "--set", f"deployment.phase={phase}", "--set-string", f"deployment.runId={run_id}",
        "--set", f"database.bundled.initialize={'true' if initialize else 'false'}",
    ])
    source = r'''
      require "yaml"; require "json"
      puts JSON.generate(YAML.load_stream(STDIN.read).compact)
    '''
    return json.loads(checked(["ruby", "-ryaml", "-rjson", "-e", source], input=rendered))


def tree_fingerprint(chart: pathlib.Path, *, exclude_marker: bool = False) -> str:
    digest = hashlib.sha256()
    for path in sorted(chart.rglob("*")):
        if path.is_symlink():
            fail(f"Prepared chart contains a symbolic link: {path}")
        if path.is_file() and not (exclude_marker and path.name == ".clipp-installation.json"):
            digest.update(path.relative_to(chart).as_posix().encode())
            digest.update(b"\0")
            digest.update(path.read_bytes())
    return digest.hexdigest()


def safe_write_many(artifacts: list[tuple[pathlib.Path, bytes]]) -> None:
    """Atomically stage new files and roll back new links if a later link fails."""
    pending: list[tuple[pathlib.Path, bytes]] = []
    for path, content in artifacts:
        reject_symlink_components(path.parent)
        if path.is_symlink():
            fail(f"Refusing a symbolic-link artifact: {path}")
        if path.exists():
            if not path.is_file() or path.read_bytes() != content:
                fail(f"Retained artifact differs from this reviewed preparation: {path}")
        else:
            pending.append((path, content))
    staged: list[tuple[pathlib.Path, pathlib.Path]] = []
    created: list[pathlib.Path] = []
    try:
        for path, content in pending:
            fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=path.parent)
            temp_path = pathlib.Path(temporary)
            os.fchmod(fd, 0o600)
            with os.fdopen(fd, "wb") as output:
                output.write(content)
                output.flush()
                os.fsync(output.fileno())
            staged.append((temp_path, path))
        for temporary, target in staged:
            try:
                os.link(temporary, target)
                created.append(target)
            except FileExistsError:
                if not target.is_file() or target.read_bytes() != next(data for path, data in artifacts if path == target):
                    fail(f"Retained artifact differs from this reviewed preparation: {target}")
    except BaseException:
        for path in created:
            path.unlink(missing_ok=True)
        raise
    finally:
        for temporary, _ in staged:
            temporary.unlink(missing_ok=True)


def prepare_chart(source: pathlib.Path, work: pathlib.Path, fingerprint: str) -> pathlib.Path:
    if not (source / "Chart.yaml").is_file():
        fail(f"Source is not a chart: {source}")
    target = work / f"clipp-relay-300m-{fingerprint[:16]}"
    reject_symlink_components(target)
    marker = target / ".clipp-installation.json"
    if target.exists():
        if target.is_symlink() or not target.is_dir() or marker.is_symlink() or not marker.is_file():
            fail(f"Refusing a foreign or incomplete retained chart: {target}")
        try:
            retained = json.loads(marker.read_text())
        except (OSError, json.JSONDecodeError):
            fail(f"Retained chart identity is unreadable: {target}")
        if retained.get("input_fingerprint") != fingerprint or retained.get("chart_sha256") != tree_fingerprint(target, exclude_marker=True):
            fail(f"Retained chart has changed since preparation: {target}")
        return target
    if target == source or target.is_relative_to(source) or source.is_relative_to(target):
        fail("The isolated chart output must not overlap the source chart")
    target.mkdir(mode=0o700)
    try:
        checked([sys.executable, str(PREPARE_CHART), str(source), str(target)])
        metadata = {"format": 1, "input_fingerprint": fingerprint, "chart_sha256": tree_fingerprint(target, exclude_marker=True)}
        marker.write_text(json.dumps(metadata, sort_keys=True) + "\n")
        os.chmod(marker, 0o600)
    except BaseException:
        shutil.rmtree(target, ignore_errors=True)
        raise
    return target


def build_migration_job(documents: list[dict], release: str, namespace: str, job_name: str,
                        migration_secret: str, ca_secret: str, fingerprint: str) -> dict:
    stateful_set_name = f"{release}-clipp-relay"
    stateful_set = next((doc for doc in documents if doc.get("kind") == "StatefulSet" and
                         doc.get("metadata", {}).get("name") == stateful_set_name), None)
    if stateful_set is None:
        fail("Rendered relay StatefulSet is missing")
    pod = stateful_set["spec"]["template"]["spec"]
    base = next((container for container in pod.get("containers", []) if container.get("name") == "relay"), None)
    if base is None:
        fail("Rendered relay container is missing")
    resources = json.loads(json.dumps(base["resources"]))
    resources.setdefault("requests", {})["cpu"] = "300m"
    volumes = pod.get("volumes", [])
    config = next((volume for volume in volumes if volume.get("name") == "config"), None)
    ca = next((volume for volume in volumes if volume.get("name") == "db-ca"), None)
    if config is None or ca is None or ca.get("secret", {}).get("secretName") != ca_secret:
        fail("Rendered migration config or database CA volume differs from the reviewed values")
    mounts = [mount for mount in base.get("volumeMounts", []) if mount.get("name") in {"config", "db", "db-ca"}]
    if {mount.get("name") for mount in mounts} != {"config", "db", "db-ca"}:
        fail("Rendered migration mounts differ from the relay contract")
    if next(mount for mount in mounts if mount["name"] == "db").get("mountPath") != "/run/secrets/db":
        fail("Migration database credentials must use /run/secrets/db")
    db = {"name": "db", "secret": {"secretName": migration_secret,
          "items": [{"key": "username", "path": "username"}, {"key": "password", "path": "password"}]}}
    return {
        "apiVersion": "batch/v1", "kind": "Job",
        "metadata": {"name": job_name, "namespace": namespace,
            "labels": {"app.kubernetes.io/name": "clipp-relay-migration", "app.kubernetes.io/instance": release},
            "annotations": {"clipp-relay-test/install-fingerprint": fingerprint}},
        "spec": {"backoffLimit": 0, "activeDeadlineSeconds": 2400,
            "template": {"metadata": {"labels": {"app.kubernetes.io/name": "clipp-relay-migration", "app.kubernetes.io/instance": release}},
                "spec": {"automountServiceAccountToken": False, "restartPolicy": "Never",
                    "securityContext": pod.get("securityContext", {}), "nodeSelector": pod.get("nodeSelector", {}),
                    "tolerations": pod.get("tolerations", []),
                    "containers": [{"name": "migrate", "image": base["image"], "imagePullPolicy": base["imagePullPolicy"],
                        "args": ["-command", "migrate", "-config", "/etc/clipp/config.json"],
                        "securityContext": base.get("securityContext", {}), "resources": resources, "volumeMounts": mounts}],
                    "volumes": [config, db, ca]}}}}


def normalized_job_spec(observed: dict, expected: dict) -> dict:
    """Remove only Kubernetes-generated identity and documented API defaults."""
    actual = copy.deepcopy(observed.get("spec", {}))
    expected_spec = expected["spec"]

    # Jobs receive a selector and matching template labels from the API server
    # unless manualSelector is set. These values identify this exact Job.
    metadata = observed.get("metadata", {})
    uid = metadata.get("uid")
    selector = actual.get("selector", {})
    match_labels = selector.get("matchLabels", {}) if isinstance(selector, dict) else {}
    generated_selectors = (
        {"batch.kubernetes.io/controller-uid": uid, "batch.kubernetes.io/job-name": metadata.get("name")},
        {"controller-uid": uid, "job-name": metadata.get("name")},
    )
    if selector and any(match_labels == generated for generated in generated_selectors) and not selector.get("matchExpressions"):
        actual.pop("selector", None)
    for key in ("parallelism", "completions"):
        if key not in expected_spec and actual.get(key) == 1:
            actual.pop(key, None)
    if "podReplacementPolicy" not in expected_spec and actual.get("podReplacementPolicy") == "TerminatingOrFailed":
        actual.pop("podReplacementPolicy", None)

    template = actual.get("template", {})
    template_metadata = template.get("metadata", {}) if isinstance(template, dict) else {}
    labels = template_metadata.get("labels", {}) if isinstance(template_metadata, dict) else {}
    if isinstance(labels, dict):
        for key, value in (("batch.kubernetes.io/controller-uid", uid),
                           ("batch.kubernetes.io/job-name", metadata.get("name")),
                           ("controller-uid", uid), ("job-name", metadata.get("name"))):
            if value is not None and labels.get(key) == value:
                labels.pop(key)
        if not labels:
            template_metadata.pop("labels", None)
    template_metadata.pop("creationTimestamp", None)

    pod = template.get("spec", {}) if isinstance(template, dict) else {}
    expected_pod = expected_spec.get("template", {}).get("spec", {})
    pod_defaults = {
        "serviceAccountName": "default", "dnsPolicy": "ClusterFirst",
        "schedulerName": "default-scheduler", "terminationGracePeriodSeconds": 30,
        "enableServiceLinks": True, "preemptionPolicy": "PreemptLowerPriority",
        "hostNetwork": False, "hostPID": False, "hostIPC": False,
        "shareProcessNamespace": False, "setHostnameAsFQDN": False,
        "hostUsers": True, "priority": 0,
    }
    for key, value in pod_defaults.items():
        if key not in expected_pod and pod.get(key) == value:
            pod.pop(key, None)
    if "imagePullSecrets" not in expected_pod and pod.get("imagePullSecrets") == []:
        pod.pop("imagePullSecrets", None)
    for container in pod.get("containers", []):
        expected_container = next((item for item in expected_pod.get("containers", [])
                                   if item.get("name") == container.get("name")), {})
        container_defaults = {
            "terminationMessagePath": "/dev/termination-log",
            "terminationMessagePolicy": "File", "stdin": False,
            "stdinOnce": False, "tty": False,
        }
        for key, value in container_defaults.items():
            if key not in expected_container and container.get(key) == value:
                container.pop(key, None)
    return actual


def job_observation(name: str, observed: dict | None, expected: dict, logs: list[str | dict]) -> str:
    if observed is None:
        return "missing"
    if not isinstance(observed, dict) or not isinstance(logs, list):
        fail(f"{name.capitalize()} Job observations have an invalid shape")
    metadata = observed.get("metadata", {})
    expected_metadata = expected["metadata"]
    if metadata.get("name") != expected_metadata["name"] or metadata.get("namespace") != expected_metadata["namespace"]:
        fail(f"Refusing foreign {name} Job evidence")
    annotations = metadata.get("annotations", {})
    if (metadata.get("labels", {}) != expected_metadata.get("labels", {}) or
            annotations != expected_metadata.get("annotations", {})):
        fail(f"Refusing foreign {name} Job evidence")
    normalized = normalized_job_spec(observed, expected)
    if normalized != expected["spec"]:
        fail(f"Retained {name} Job configuration differs from the reviewed artifact")
    actual_pod = normalized.get("template", {}).get("spec", {})
    status = observed.get("status", {})
    if not isinstance(status, dict):
        fail(f"{name.capitalize()} Job status evidence is invalid")
    if status.get("failed", 0) > 0:
        return "failed"
    if status.get("succeeded") != 1:
        return "pending" if status.get("active", 0) > 0 or not status else "incomplete"
    records: list[dict] = []
    for line in logs:
        if isinstance(line, dict):
            records.append(line)
        elif isinstance(line, str):
            try:
                records.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    complete = any(record.get("event") == "service.startup" and record.get("outcome") == "success" and
                   record.get("schema_revision") == 16 for record in records)
    return "completed" if complete else "incomplete"


def reviewed_release_values(values_path: pathlib.Path, overlay_path: pathlib.Path, phase: str) -> dict:
    source = r'''
      require "yaml"; require "json"
      values = YAML.safe_load(File.read(ARGV.fetch(0)))
      overlay = YAML.safe_load(File.read(ARGV.fetch(1)))
      abort "reviewed values must be mappings" unless values.is_a?(Hash) && overlay.is_a?(Hash)
      overlay.each { |key, value| values[key] = values.fetch(key, {}).merge(value) }
      puts JSON.generate(values)
    '''
    try:
        values = json.loads(checked(["ruby", "-ryaml", "-rjson", "-e", source,
                                     str(values_path), str(overlay_path)]))
    except (json.JSONDecodeError, SystemExit):
        fail("Reviewed chart values could not be compared")
    try:
        values["database"]["bundled"]["initialize"] = phase == "stopped"
    except (KeyError, TypeError):
        fail("Reviewed chart values have no bundled database configuration")
    return values


def release_values_match(observed: dict, reviewed: dict) -> bool:
    """Compare every reviewed chart value except the separately validated run state."""
    observed = json.loads(json.dumps(observed))
    reviewed = json.loads(json.dumps(reviewed))
    for values in (observed, reviewed):
        deployment = values.get("deployment")
        if isinstance(deployment, dict):
            deployment.pop("phase", None)
            deployment.pop("runId", None)
        database = values.get("database")
        bundled = database.get("bundled") if isinstance(database, dict) else None
        if isinstance(bundled, dict):
            bundled.pop("initialize", None)
    return observed == reviewed


def recorded_serving_release(release_values: dict, receipt_path: pathlib.Path | None,
                             receipt_sha256: str | None) -> str | None:
    if receipt_path is None or receipt_sha256 is None:
        return None
    if not receipt_path.is_absolute() or receipt_path.is_symlink() or not receipt_path.is_file():
        fail("Recorded serving values receipt is unavailable")
    payload = receipt_path.read_bytes()
    if hashlib.sha256(payload).hexdigest() != receipt_sha256:
        fail("Recorded serving values receipt digest does not match")
    try:
        expected = json.loads(payload)
    except json.JSONDecodeError:
        fail("Recorded serving values receipt is invalid JSON")
    if release_values != expected:
        fail("Installed values differ from the recorded serving deployment")
    deployment = expected.get("deployment", {})
    database = expected.get("database", {}).get("bundled", {})
    if deployment.get("phase") != "serving" or deployment.get("runId") != "" or database.get("initialize") is not False:
        fail("Recorded deployment is not a completed serving release")
    extension = expected.get("clients", {}).get("extensionRedirect", "")
    match = re.fullmatch(r"https://([a-p]{32})\.chromiumapp\.org/clipp-relay", extension)
    if match is None:
        fail("Recorded Chrome callback is invalid")
    return match.group(1)


def interpret_observations(path: pathlib.Path, *, release: str, namespace: str, run_id: str,
                           migration: dict, reset: dict, values_path: pathlib.Path,
                           overlay_path: pathlib.Path, recorded_values_path: pathlib.Path | None,
                           recorded_values_sha256: str | None) -> dict:
    try:
        observations = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as error:
        fail(f"Installation observations are unreadable: {error}")
    if not isinstance(observations, dict):
        fail("Installation observations must be a JSON object")
    release_observation = observations.get("release", {})
    if not isinstance(release_observation, dict):
        fail("Helm release observations have an invalid shape")
    if release_observation.get("name") != release or release_observation.get("namespace") != namespace:
        fail("Refusing Helm release evidence for a different release or namespace")
    recorded_serving = False
    extension_id = None
    release_status = release_observation.get("status")
    release_run_id = None
    release_initialize = None
    if release_status == "absent":
        release_state = "absent"
    elif isinstance(release_status, str) and release_status.startswith("pending"):
        release_state = "pending"
    elif release_status == "failed":
        release_state = "failed"
    elif release_status == "deployed":
        values = release_observation.get("values", {})
        if not isinstance(values, dict):
            fail("Installed release values evidence has an invalid shape")
        deployment = values.get("deployment", {})
        database_values = values.get("database", {})
        if not isinstance(deployment, dict) or not isinstance(database_values, dict):
            fail("Installed release phase evidence has an invalid shape")
        database = database_values.get("bundled", {})
        if not isinstance(database, dict):
            fail("Installed database initialization evidence has an invalid shape")
        phase = deployment.get("phase")
        observed_run_id = deployment.get("runId", "")
        initialize = database.get("initialize")
        if phase not in {"stopped", "migrating", "serving"}:
            fail("Installed release has an unsupported phase")
        if phase == "migrating" and (observed_run_id != run_id or initialize is not False):
            fail("Installed migration phase does not match the reviewed run identity")
        if phase == "serving" and (observed_run_id != "" or initialize is not False):
            fail("Installed serving phase does not match the reviewed completion identity")
        expected_values = reviewed_release_values(values_path, overlay_path, phase)
        mismatched = not release_values_match(values, expected_values)
        if mismatched:
            if phase != "serving":
                fail("Installed release values differ from the reviewed preparation")
            extension_id = recorded_serving_release(values, recorded_values_path, recorded_values_sha256)
            if extension_id is None:
                fail("Installed release values differ from the reviewed preparation")
            recorded_serving = True
        release_state = phase
        release_run_id = observed_run_id
        release_initialize = initialize
    else:
        fail("Installed release status is missing or unsupported")

    bootstrap = observations.get("bootstrap", {})
    if not isinstance(bootstrap, dict):
        fail("PostgreSQL bootstrap observations have an invalid shape")
    bootstrap_job = bootstrap.get("job")
    bootstrap_state = "missing"
    if bootstrap_job is not None:
        expected_name = f"{release}-clipp-relay-postgres-bootstrap-{run_id}"
        metadata = bootstrap_job.get("metadata", {})
        if metadata.get("name") != expected_name or metadata.get("namespace") != namespace:
            fail("Refusing foreign PostgreSQL bootstrap Job evidence")
        status = bootstrap_job.get("status", {})
        if status.get("failed", 0) > 0:
            bootstrap_state = "failed"
        elif status.get("succeeded") == 1 and bootstrap.get("pod_initialize") is False:
            bootstrap_state = "completed"
        elif status.get("active", 0) > 0:
            bootstrap_state = "pending"
        else:
            bootstrap_state = "incomplete"

    jobs = observations.get("jobs", {})
    if not isinstance(jobs, dict):
        fail("Application Job observations have an invalid shape")
    return {"release": release_state, "release_run_id": release_run_id,
            "release_initialize": release_initialize, "bootstrap": bootstrap_state,
            "recorded_serving_release": recorded_serving, "extension_id": extension_id,
            "migration": job_observation("migration", jobs.get("migration"), migration, jobs.get("migration_logs", [])),
            "reset": job_observation("reset", jobs.get("reset"), reset, jobs.get("reset_logs", []))}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", required=True, help="absolute relay repository root")
    parser.add_argument("--work-dir", required=True, help="absolute private work directory")
    parser.add_argument("--deployment-dir", required=True, help="absolute private retained-artifact directory")
    parser.add_argument("--source-chart", required=True)
    parser.add_argument("--values", required=True)
    parser.add_argument("--overlay", required=True)
    parser.add_argument("--release", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--migration-secret", required=True)
    parser.add_argument("--ca-secret", required=True)
    parser.add_argument("--native-secret", required=True)
    parser.add_argument("--install-fingerprint", required=True)
    parser.add_argument("--relay-binary", help="built clipp-relay binary; defaults to go run")
    parser.add_argument("--observations-file", help="optional JSON snapshot of retained release and Job evidence")
    parser.add_argument("--recorded-serving-values", help="optional reviewed serving values receipt")
    parser.add_argument("--recorded-serving-values-sha256", help="SHA-256 digest for the serving receipt")
    args = parser.parse_args()

    for label, value in (("release", args.release), ("namespace", args.namespace),
                         ("migration Secret", args.migration_secret), ("CA Secret", args.ca_secret),
                         ("native signing Secret", args.native_secret)):
        if not NAME_RE.fullmatch(value):
            fail(f"Invalid Kubernetes {label}")
    if not re.fullmatch(r"[a-f0-9]{8}", args.run_id):
        fail("The reviewed database run ID must be eight lowercase hexadecimal characters")
    if not re.fullmatch(r"[a-f0-9]{64}", args.install_fingerprint):
        fail("The reviewed installation fingerprint must be a SHA-256 hex digest")
    if bool(args.recorded_serving_values) != bool(args.recorded_serving_values_sha256):
        fail("Both recorded serving receipt path and digest are required")
    if args.recorded_serving_values_sha256 and not re.fullmatch(r"[a-f0-9]{64}", args.recorded_serving_values_sha256):
        fail("The recorded serving receipt digest must be a SHA-256 hex digest")
    migration_name = f"{args.release}-clipp-relay-migrate-{args.run_id}"
    reset_name = f"{args.release}-clipp-relay-reset-auth-{args.run_id}"
    for name in (migration_name, reset_name):
        if len(name) > 63 or not NAME_RE.fullmatch(name):
            fail("A prepared Job name is invalid or longer than 63 characters")

    repo = pathlib.Path(args.repo)
    if not repo.is_absolute() or not (repo / "go.mod").is_file():
        fail("--repo must name an absolute Clipp Relay checkout")
    repo = repo.resolve()
    work = directory(args.work_dir, create=True)
    deployment = directory(args.deployment_dir, create=True)
    for output in (work, deployment):
        resolved = output.resolve()
        if resolved == repo or resolved.is_relative_to(repo):
            fail("Installation artifacts must stay outside the source checkout")
        if subprocess.run(["git", "-C", str(resolved), "rev-parse", "--is-inside-work-tree"],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            fail(f"Refusing to store installation artifacts in a Git checkout: {resolved}")
    source = pathlib.Path(args.source_chart)
    values = pathlib.Path(args.values)
    overlay = pathlib.Path(args.overlay)
    for value in (source, values, overlay):
        if not value.is_absolute() or value.is_symlink() or not value.is_file() and value != source:
            fail(f"Reviewed chart inputs must be absolute existing files: {value}")
        reject_symlink_components(value)
    if not (source / "Chart.yaml").is_file():
        fail(f"Source is not a chart: {source}")
    if any(path.is_symlink() for path in source.rglob("*")):
        fail("The reviewed source chart contains a symbolic link")
    if args.relay_binary:
        binary = pathlib.Path(args.relay_binary)
        if not binary.is_absolute() or binary.is_symlink() or not binary.is_file() or not os.access(binary, os.X_OK):
            fail("--relay-binary must be an absolute executable regular file")

    identity = {"format": 1, "release": args.release, "namespace": args.namespace,
        "run_id": args.run_id, "migration_secret": args.migration_secret, "ca_secret": args.ca_secret,
        "native_secret": args.native_secret, "install_fingerprint": args.install_fingerprint,
        "values_sha256": hashlib.sha256(values.read_bytes()).hexdigest(),
        "overlay_sha256": hashlib.sha256(overlay.read_bytes()).hexdigest(),
        "chart_tree_sha256": tree_fingerprint(source)}
    input_fingerprint = hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest()
    chart = prepare_chart(source, work, input_fingerprint)

    serving_docs = render_documents(args.release, chart, args.namespace, values, overlay,
                                    phase="serving", run_id="", initialize=False)
    config_map = next((doc for doc in serving_docs if doc.get("kind") == "ConfigMap" and
                       str(doc.get("metadata", {}).get("name", "")).endswith("-config")), None)
    config_json = config_map and config_map.get("data", {}).get("config.json")
    if not isinstance(config_json, str):
        fail("Rendered relay runtime configuration is missing")
    try:
        parsed_config = json.loads(config_json)
    except json.JSONDecodeError as error:
        fail(f"Rendered relay runtime configuration is invalid JSON: {error.msg}")
    if not isinstance(parsed_config, dict):
        fail("Rendered relay runtime configuration must be a JSON object")
    fd, config_path = tempfile.mkstemp(prefix=".clipp-runtime-config.", suffix=".json", dir=work)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as config_file:
            json.dump(parsed_config, config_file)
            config_file.write("\n")
        validator = ([args.relay_binary] if args.relay_binary else ["go", "run", "-p", "1", "./cmd/clipp-relay"])
        checked(validator + ["-command", "validate-config", "-config", config_path], cwd=repo)
    finally:
        pathlib.Path(config_path).unlink(missing_ok=True)

    migration_docs = render_documents(args.release, chart, args.namespace, values, overlay,
                                      phase="migrating", run_id=args.run_id, initialize=False)
    migration_config = next((doc for doc in migration_docs if doc.get("kind") == "ConfigMap" and
                             str(doc.get("metadata", {}).get("name", "")).endswith("-config")), None)
    if migration_config is None or migration_config.get("data", {}).get("config.json") != config_json:
        fail("Serving and migration runtime configuration differ; review the rendered installation")
    migration = build_migration_job(migration_docs, args.release, args.namespace, migration_name,
                                    args.migration_secret, args.ca_secret, args.install_fingerprint)
    reset = json.loads(json.dumps(migration))
    reset["metadata"]["name"] = reset_name
    container = reset["spec"]["template"]["spec"]["containers"][0]
    container["name"] = "reset-auth"
    container["args"] = ["-command", "reset-auth", "-config", "/etc/clipp/config.json"]
    container["volumeMounts"].append({"name": "native-oauth", "mountPath": "/run/secrets/native-oauth", "readOnly": True})
    reset["spec"]["template"]["spec"]["volumes"].append({"name": "native-oauth", "secret": {
        "secretName": args.native_secret, "items": [{"key": "signing-key", "path": "signing-key"}]}})

    migration_path = deployment / f"{migration_name}.json"
    reset_path = deployment / f"{reset_name}.json"
    safe_write_many([
        (migration_path, (json.dumps(migration, indent=2) + "\n").encode()),
        (reset_path, (json.dumps(reset, indent=2) + "\n").encode()),
    ])
    result = {"chart_dir": str(chart), "migration_manifest": str(migration_path),
                      "reset_manifest": str(reset_path), "input_fingerprint": input_fingerprint,
                      "runtime_configuration": "accepted"}
    if args.observations_file:
        result["observations"] = interpret_observations(pathlib.Path(args.observations_file),
            release=args.release, namespace=args.namespace, run_id=args.run_id,
            migration=migration, reset=reset, values_path=values, overlay_path=overlay,
            recorded_values_path=pathlib.Path(args.recorded_serving_values) if args.recorded_serving_values else None,
            recorded_values_sha256=args.recorded_serving_values_sha256)
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()

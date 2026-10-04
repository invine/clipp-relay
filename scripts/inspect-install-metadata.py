#!/usr/bin/env python3
"""Read exact relay workload metadata; never claim runtime acceptance from it."""
import argparse
import copy
import datetime
import json
import re
import subprocess
import sys

def private_failure():
    print('FAIL installation metadata unavailable or inconsistent; inspect the exact target privately', file=sys.stderr)
    sys.exit(2)


def typed_fields(value, fields):
    if type(value) is not dict:
        raise TypeError('expected API object')
    for field, kinds in fields.items():
        if field in value and type(value[field]) not in (kinds if type(kinds) is tuple else (kinds,)):
            raise TypeError('invalid API field type')


def typed_strings(value):
    if type(value) is not list or any(type(item) is not str for item in value):
        raise TypeError('expected API string list')


def validate_security(security):
    typed_fields(security, {'runAsUser': int, 'runAsGroup': int, 'fsGroup': int,
                            'runAsNonRoot': bool, 'privileged': bool,
                            'readOnlyRootFilesystem': bool, 'allowPrivilegeEscalation': bool,
                            'capabilities': dict, 'seccompProfile': dict})
    if 'seccompProfile' in security:
        typed_fields(security['seccompProfile'], {'type': str})
    for values in security.get('capabilities', {}).values():
        typed_strings(values)


def validate_pod_fields(spec):
    typed_fields(spec, {'hostNetwork': bool, 'hostPID': bool, 'hostIPC': bool,
                        'automountServiceAccountToken': bool, 'containers': list,
                        'initContainers': list, 'ephemeralContainers': list,
                        'volumes': list, 'securityContext': dict})
    validate_security(spec.get('securityContext', {}))
    for container in spec['containers']:
        typed_fields(container, {'name': str, 'image': str, 'securityContext': dict,
                                 'resources': dict, 'ports': list, 'volumeMounts': list})
        validate_security(container.get('securityContext', {}))
        for field in ('args', 'command'):
            typed_strings(container.get(field, []))
        resources = container.get('resources', {})
        typed_fields(resources, {'requests': dict, 'limits': dict})
        for values in resources.values():
            typed_fields(values, {key: str for key in values})
        for port in container.get('ports', []):
            typed_fields(port, {'hostPort': int, 'containerPort': int, 'name': str, 'protocol': str})
        for mount in container.get('volumeMounts', []):
            typed_fields(mount, {'name': str, 'mountPath': str, 'readOnly': bool,
                                 'subPath': str, 'subPathExpr': str})
        for field in ('startupProbe', 'readinessProbe', 'livenessProbe'):
            if field in container:
                probe = container[field]
                typed_fields(probe, {key: int for key in ('initialDelaySeconds', 'periodSeconds', 'timeoutSeconds', 'successThreshold', 'failureThreshold')})
                if 'httpGet' in probe:
                    typed_fields(probe['httpGet'], {'path': str, 'port': (str, int), 'scheme': str, 'host': str})
    for volume in spec.get('volumes', []):
        typed_fields(volume, {'name': str, 'secret': dict, 'configMap': dict, 'projected': dict, 'hostPath': dict})
        for source in ('secret', 'configMap'):
            if source in volume:
                typed_fields(volume[source], {'defaultMode': int, 'optional': bool, 'items': list})


def probe_with_defaults(probe):
    if probe is None:
        return None
    result = copy.deepcopy(probe)
    for field, default in {'initialDelaySeconds': 0, 'periodSeconds': 10,
                           'timeoutSeconds': 1, 'successThreshold': 1,
                           'failureThreshold': 3}.items():
        result.setdefault(field, default)
    if 'httpGet' in result:
        for field, default in {'scheme': 'HTTP', 'host': '', 'httpHeaders': []}.items():
            result['httpGet'].setdefault(field, default)
    return result


def default_service_account_volume(volume):
    if not volume['name'].startswith('kube-api-access-'):
        return False
    projected = volume.get('projected', {})
    if set(volume) != {'name', 'projected'} or set(projected) != {'sources', 'defaultMode'} or projected['defaultMode'] != 420:
        return False
    sources = projected['sources']
    if len(sources) != 3:
        return False
    token = sources[0].get('serviceAccountToken', {})
    return (
        set(sources[0]) == {'serviceAccountToken'}
        and set(token) == {'expirationSeconds', 'path'}
        and type(token['expirationSeconds']) is int
        and token['expirationSeconds'] >= 600
        and token['path'] == 'token'
        and sources[1] == {'configMap': {'name': 'kube-root-ca.crt', 'items': [{'key': 'ca.crt', 'path': 'ca.crt'}]}}
        and sources[2] == {'downwardAPI': {'items': [{'path': 'namespace', 'fieldRef': {'apiVersion': 'v1', 'fieldPath': 'metadata.namespace'}}]}}
    )


def reviewed_volumes_and_mounts(spec, allow_default_token):
    volumes = copy.deepcopy(spec.get('volumes', []))
    mounts = copy.deepcopy(next(c for c in spec['containers'] if c['name'] == 'relay').get('volumeMounts', []))
    tokens = [v for v in volumes if default_service_account_volume(v)]
    if allow_default_token and len(tokens) == 1:
        token = tokens[0]
        expected_mount = {'name': token['name'], 'mountPath': '/var/run/secrets/kubernetes.io/serviceaccount', 'readOnly': True}
        if expected_mount in mounts:
            volumes.remove(token)
            mounts.remove(expected_mount)
    for volume in volumes:
        for source in ('secret', 'configMap'):
            if source in volume:
                for field, default in {'defaultMode': 420, 'optional': False}.items():
                    volume[source].setdefault(field, default)
    return (sorted(volumes, key=lambda v: v['name']),
            sorted(mounts, key=lambda m: (m['name'], m['mountPath'])))


parser = argparse.ArgumentParser(description=__doc__)
source = parser.add_mutually_exclusive_group(required=True)
source.add_argument('--context', help='explicit authorized kubectl context (read only)')
source.add_argument('--snapshot', help='JSON with statefulset/configmap/pod API objects')
parser.add_argument('--namespace', required=True)
parser.add_argument('--release', required=True)
args = parser.parse_args()
for value in (args.namespace, args.release):
    if len(value) > 63 or not re.fullmatch(r'[a-z0-9](?:[-a-z0-9]*[a-z0-9])?', value):
        parser.error('namespace and release must be exact Kubernetes DNS labels')
name = args.release + '-clipp-relay'
names = {'statefulset': name, 'configmap': name + '-config', 'pod': name + '-0'}
try:
    if args.context:
        objects = {}
        for kind, resource_name in names.items():
            raw = subprocess.check_output(
                ['kubectl', '--context', args.context, '-n', args.namespace,
                 'get', kind, resource_name, '-o', 'json'],
                stderr=subprocess.DEVNULL, timeout=30,
            )
            objects[kind] = json.loads(raw)
    else:
        with open(args.snapshot, encoding='utf-8') as stream:
            objects = json.load(stream)
    for kind, resource_name in names.items():
        metadata = objects[kind]['metadata']
        if metadata['name'] != resource_name or metadata['namespace'] != args.namespace:
            raise ValueError('snapshot does not match the explicit installation target')
    sts = objects['statefulset']['spec']
    template = sts['template']['spec']
    pod = objects['pod']
    relay = next(c for c in template['containers'] if c['name'] == 'relay')
    running = next(c for c in pod['spec']['containers'] if c['name'] == 'relay')
    config = json.loads(objects['configmap']['data']['config.json'])
    database_mode = config['database']['mode']
    if database_mode not in ('external', 'bundled'):
        raise ValueError('unknown database mode')
except (OSError, ValueError, TypeError, KeyError, StopIteration, subprocess.SubprocessError):
    # API errors and malformed snapshots may contain private input: do not echo them.
    private_failure()

try:
    typed_fields(sts, {'replicas': int})
    validate_pod_fields(template)
    validate_pod_fields(pod['spec'])
    checks = {}
    def check(key, condition):
        checks[key] = 'PASS metadata' if condition else 'FAIL'

    check('external_database_config', database_mode == 'external')
    check('singleton_serving_configuration', sts.get('replicas') == 1 and sts.get('updateStrategy', {}).get('type') == 'OnDelete' and len(template['containers']) == 1)
    security = template.get('securityContext', {})
    container_security = relay.get('securityContext', {})
    effective_security = dict(security, **container_security)
    check('configured_security', (
        effective_security.get('runAsNonRoot') is True
        and effective_security.get('runAsUser', 0) > 0
        and effective_security.get('seccompProfile', {}).get('type') == 'RuntimeDefault'
        and container_security.get('readOnlyRootFilesystem') is True
        and container_security.get('allowPrivilegeEscalation') is False
        and 'ALL' in container_security.get('capabilities', {}).get('drop', [])
        and not container_security.get('capabilities', {}).get('add', [])
        and not container_security.get('privileged', False)
        and not template.get('hostNetwork', False)
        and not template.get('hostPID', False)
        and not template.get('hostIPC', False)
        and all('hostPath' not in volume for volume in template.get('volumes', []))
        and all(not port.get('hostPort') for port in relay.get('ports', []))
    ))
    # API quantities may be canonicalized (1 CPU -> 1000m) by the API server.
    resources = relay.get('resources', {})
    request, limit = resources.get('requests', {}), resources.get('limits', {})
    for cpu in (request.get('cpu'), running.get('resources', {}).get('requests', {}).get('cpu')):
        if cpu is not None and (not isinstance(cpu, str) or len(cpu) > 32 or not re.fullmatch(r'[0-9]+(?:\.[0-9]+)?(?:[num])?', cpu)):
            raise ValueError('invalid CPU quantity')
    check('configured_resource_envelope', (
        request.get('cpu') in ('1', '1000m')
        and limit.get('cpu') in ('2', '2000m')
        and request.get('memory') in ('1Gi', '1024Mi')
        and limit.get('memory') in ('2Gi', '2048Mi')
        and request.get('ephemeral-storage') == '64Mi'
        and limit.get('ephemeral-storage') == '128Mi'
    ))
    check('immutable_image_reference', bool(re.fullmatch(r'[^\s@]+@sha256:[0-9a-f]{64}', relay.get('image', ''))))
    check('pod_matches_template_image_resources_security', (
        running.get('image') == relay.get('image')
        and running.get('resources') == resources
        and running.get('securityContext') == container_security
        and pod['spec'].get('securityContext') == security
    ))
    actual = pod['spec']
    allow_default_token = template.get('automountServiceAccountToken') is True
    check('pod_runtime_declarations_match_template', (
        len(actual['containers']) == 1
        and not actual.get('initContainers', [])
        and not actual.get('ephemeralContainers', [])
        and not actual.get('hostNetwork', False)
        and not actual.get('hostPID', False)
        and not actual.get('hostIPC', False)
        and all('hostPath' not in volume for volume in actual.get('volumes', []))
        and all(not port.get('hostPort') for port in running.get('ports', []))
        and running.get('ports', []) == relay.get('ports', [])
        and running.get('args', []) == relay.get('args', [])
        and running.get('command', []) == relay.get('command', [])
        and reviewed_volumes_and_mounts(actual, allow_default_token) == reviewed_volumes_and_mounts(template, False)
        and all(probe_with_defaults(running.get(key)) == probe_with_defaults(relay.get(key))
                for key in ('startupProbe', 'readinessProbe', 'livenessProbe'))
    ))
    uid = objects['statefulset']['metadata'].get('uid')
    intended_revision = objects['statefulset'].get('status', {}).get('updateRevision')
    if not uid or not intended_revision:
        checks['pod_owner_and_intended_revision'] = 'NOT RUN'
    else:
        check('pod_owner_and_intended_revision', (
            any(owner.get('controller') is True
                and owner.get('kind') == 'StatefulSet'
                and owner.get('apiVersion') == 'apps/v1'
                and owner.get('name') == name
                and owner.get('uid') == uid
                for owner in pod['metadata'].get('ownerReferences', []))
            and pod['metadata'].get('labels', {}).get('controller-revision-hash') == intended_revision
        ))
    check('pod_ready_observation', any(c.get('type') == 'Ready' and c.get('status') == 'True' for c in pod.get('status', {}).get('conditions', [])))
    expected_probes = {'startupProbe': ('/readyz', 5, 2, 120), 'readinessProbe': ('/readyz', 5, 2, 1), 'livenessProbe': ('/livez', 10, 2, 3)}
    for key, (path, period, timeout, failures) in expected_probes.items():
        probe = relay.get(key, {})
        check('configured_' + key, (
            probe.get('httpGet', {}).get('path') == path
            and probe.get('httpGet', {}).get('port') == 'private'
            and probe.get('periodSeconds') == period
            and probe.get('timeoutSeconds') == timeout
            and probe.get('failureThreshold') == failures
            and probe.get('successThreshold', 1) == 1
        ))

    report = {
        'observed_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'source': 'live read-only API metadata' if args.context else 'provided API snapshot',
        'namespace': args.namespace,
        'release': args.release,
        'database_mode': database_mode,
        'relay_image': relay.get('image') if checks['immutable_image_reference'] == 'PASS metadata' else None,
        'configured_cpu_request': request.get('cpu'),
        'observed_pod_cpu_request': running.get('resources', {}).get('requests', {}).get('cpu'),
        'checks': checks,
        'runtime_gates': dict.fromkeys([
            'postgresql_17_18_tls_scram_schema_rejections',
            'actual_uid_readonly_root_seccomp_capabilities',
            'image_architectures_provenance_and_go_only_runtime',
            'process_nofile_and_go_memory_limit',
            'serving_credentials_no_ddl_or_privileged_secret_exposure',
            'f5_oci_nsg_cni_networkpolicy_enforcement',
            'https_discovery_and_relayed_tcp_wss_udp',
            'private_operations_budgets_under_public_pressure',
            'readiness_during_database_outage_and_no_self_dial',
        ], 'NOT RUN'),
        'acceptance_complete': False,
        'limitation': 'Metadata proves declared configuration and a momentary Ready observation only. Separate recorded runtime evidence is required; PASS metadata never resolves ticket 18.',
    }
except (ValueError, TypeError, AttributeError, KeyError, StopIteration):
    private_failure()

print(json.dumps(report, indent=2))
sys.exit(1 if 'FAIL' in checks.values() else 0)

#!/usr/bin/env python3
"""Prepare the operator-approved isolated 300m test chart; never apply it."""
import pathlib, re, shutil, sys
if len(sys.argv) != 3:
    raise SystemExit('Usage: prepare-isolated-test-chart.py SOURCE_CHART EMPTY_DESTINATION')
source, target = (pathlib.Path(value).resolve() for value in sys.argv[1:])
if target == source or target.is_relative_to(source):
    raise SystemExit('The isolated output must be outside the source chart')
if not (source / 'Chart.yaml').is_file():
    raise SystemExit('Source is not a Helm chart')
if target.exists() and (not target.is_dir() or any(target.iterdir())):
    raise SystemExit('Refusing to overwrite a nonempty destination')
shutil.copytree(source, target, dirs_exist_ok=True)
path = target / 'templates/statefulset.yaml'
text = path.read_text()
pattern = r'(          resources:\n            requests:\n              cpu: )"1"'
updated, count = re.subn(pattern, r'\g<1>"300m"', text)
if count != 1:
    raise SystemExit('Canonical relay CPU template changed; review the test profile before continuing')
path.write_text(updated)
# kubelet/CSI fsGroup handling may widen PGDATA permissions on a remount.
# Restore PostgreSQL's strict mode only after the existing claim identity checks.
path = target / 'files/postgres/initialize.sh'
text = path.read_text()
anchor = '  exit 0\nfi\nif [ -e "$data/PG_VERSION" ]'
if text.count(anchor) != 1:
    raise SystemExit('PostgreSQL restart guard changed; review the permission repair')
text = text.replace(anchor, '  chmod 0700 "$data"\n' + anchor, 1)
path.write_text(text)
path = target / 'templates/postgres.yaml'
text = path.read_text()
anchor = '        fsGroup: 999\n'
if text.count(anchor) != 1:
    raise SystemExit('PostgreSQL fsGroup template changed; review the permission repair')
path.write_text(text.replace(anchor, anchor + '        fsGroupChangePolicy: OnRootMismatch\n', 1))
# F5 uses the oldest Ingress when two ordinary Ingresses share a host.
# Explicit Certificates must target the existing application Ingress directly.
path = target / 'templates/certificates.yaml'
text = path.read_text()
for suffix in ('portal', 'wss'):
    anchor = '  name: {{ printf "%s-' + suffix + '" $name | quote }}\nspec:'
    replacement = '  name: {{ printf "%s-' + suffix + '" $name | quote }}\n  annotations:\n    cert-manager.io/issue-temporary-certificate: "true"\n    acme.cert-manager.io/http01-override-ingress-name: {{ printf "%s-' + suffix + '" $name | quote }}\nspec:'
    if text.count(anchor) != 1:
        raise SystemExit('Certificate template changed; review the scoped ACME repair')
    text = text.replace(anchor, replacement, 1)
path.write_text(text)

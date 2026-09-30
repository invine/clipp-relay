#!/usr/bin/env ruby
require 'yaml'
require 'json'

stopped, migrating, serving = ARGV.map { |path| YAML.load_stream(File.read(path)).compact }
find = ->(docs, kind, suffix) { docs.find { |d| d['kind'] == kind && d.dig('metadata', 'name').end_with?(suffix) } }
[stopped, migrating, serving].each do |docs|
  abort 'duplicate resource' unless docs.map { |d| [d['kind'], d.dig('metadata', 'name')] }.uniq.length == docs.length
  abort 'chart generated a Secret' if docs.any? { |d| d['kind'] == 'Secret' }
  db = find.call(docs, 'StatefulSet', '-postgres') or abort 'database StatefulSet missing'
  abort 'database stopped with relay' unless db.dig('spec', 'replicas') == 1
  abort 'database image not pinned' unless db.dig('spec', 'template', 'spec', 'containers', 0, 'image').match?(/@sha256:[0-9a-f]{64}$/)
  abort 'database not separate Pod' unless db.dig('spec', 'template', 'spec', 'containers').length == 1
  abort 'database has no persistent mount' unless db.dig('spec', 'template', 'spec', 'containers', 0, 'volumeMounts').any? { |v| v['name'] == 'data' && v['mountPath'] == '/var/lib/postgresql' }
  abort 'admin credential leaked to serving container' if db.dig('spec', 'template', 'spec', 'containers', 0).to_s.include?('admin-secret')
  abort 'database probe lacks verified TLS' unless db.dig('spec', 'template', 'spec', 'containers', 0, 'readinessProbe', 'exec', 'command').join(' ').include?('PGSSLMODE=verify-full')
  server_config = find.call(docs, 'ConfigMap', '-postgres-scripts') or abort 'PostgreSQL scripts missing'
  abort 'database permits plaintext' unless server_config.dig('data', 'pg_hba.conf').include?('hostnossl all all 0.0.0.0/0 reject')
  abort 'bootstrap lacks privilege restriction' unless server_config.dig('data', 'bootstrap-schema.sql').include?('REVOKE ALL ON SCHEMA public FROM PUBLIC')
  service = find.call(docs, 'Service', '-postgres') or abort 'PostgreSQL service missing'
  abort 'bootstrap cannot reach unready database' unless service.dig('spec', 'publishNotReadyAddresses') == true && service.dig('spec', 'type') == 'ClusterIP'
  config = find.call(docs, 'ConfigMap', '-config') or abort 'relay config missing'
  target = JSON.parse(config.dig('data', 'config.json')).fetch('database')
  abort 'wrong database target' unless target['mode'] == 'bundled' && target['host'] == 'isolated-clipp-relay-postgres.relay-portal-test.svc.cluster.local' && target['port'] == 5432
  relay = find.call(docs, 'StatefulSet', 'clipp-relay') or abort 'relay missing'
  abort 'admin/migration credential reached relay' if relay.to_s.include?('pg-admin-v1') || relay.to_s.include?('clipp-db-migration-v1')
  dbpolicy = find.call(docs, 'NetworkPolicy', '-postgres') or abort 'database policy missing'
  abort 'database policy not isolated' unless dbpolicy.dig('spec', 'policyTypes').sort == %w[Egress Ingress]
end
pvc = find.call(stopped, 'PersistentVolumeClaim', '-postgres') or abort 'new claim missing'
abort 'PVC not retained' unless pvc.dig('metadata', 'annotations', 'helm.sh/resource-policy') == 'keep' && pvc.dig('metadata', 'annotations', 'argocd.argoproj.io/sync-options') == 'Prune=false,Delete=false'
abort 'wrong storage' unless pvc.dig('spec', 'storageClassName') == 'longhorn-retain' && pvc.dig('spec', 'resources', 'requests', 'storage') == '10Gi'
abort 'bootstrap in stopped phase' if stopped.any? { |d| d['kind'] == 'Job' }
job = find.call(migrating, 'Job', '-bootstrap-first') or abort 'explicit bootstrap Job missing'
abort 'bootstrap retried or unbounded' unless job.dig('spec', 'backoffLimit') == 0 && job.dig('spec', 'activeDeadlineSeconds').to_i > 0
abort 'admin credential missing from bootstrap' unless job.to_s.include?('pg-admin-v1')
abort 'bootstrap in serving phase' if serving.any? { |d| d['kind'] == 'Job' }
abort 'PVC changed across phases' unless [migrating, serving].all? { |docs| find.call(docs, 'PersistentVolumeClaim', '-postgres') == pvc }
abort 'relay phase not enforced' unless find.call(stopped, 'StatefulSet', 'clipp-relay').dig('spec', 'replicas') == 0 && find.call(migrating, 'StatefulSet', 'clipp-relay').dig('spec', 'replicas') == 0 && find.call(serving, 'StatefulSet', 'clipp-relay').dig('spec', 'replicas') == 1
puts 'bundled rendered manifest assertions passed'

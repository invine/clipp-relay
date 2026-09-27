#!/usr/bin/env ruby
require 'yaml'
require 'json'

serving = YAML.load_stream(File.read(ARGV.fetch(0))).compact
stopped = YAML.load_stream(File.read(ARGV.fetch(1))).compact
abort 'serving manifest has duplicate kind/name' unless serving.map { |m| [m['kind'], m.dig('metadata', 'name')] }.uniq.length == serving.length
find = ->(docs, kind, suffix) { docs.find { |m| m['kind'] == kind && m.dig('metadata', 'name').end_with?(suffix) } }
state = find.call(serving, 'StatefulSet', 'clipp-relay') or abort 'StatefulSet missing'
abort 'serving replica count' unless state.dig('spec', 'replicas') == 1
abort 'maintenance replica count' unless find.call(stopped, 'StatefulSet', 'clipp-relay').dig('spec', 'replicas') == 0
abort 'network resources changed during maintenance' unless serving.select { |m| %w[Service Ingress NetworkPolicy Role].include?(m['kind']) } == stopped.select { |m| %w[Service Ingress NetworkPolicy Role].include?(m['kind']) }
abort 'unexpected persisted/privileged resource' if serving.any? { |m| %w[Secret PersistentVolumeClaim HorizontalPodAutoscaler PodDisruptionBudget ClusterRole].include?(m['kind']) }
container = state.dig('spec', 'template', 'spec', 'containers').fetch(0)
abort 'serving migration credential mounted' if state.to_s.include?('clipp-db-migration-v1')
abort 'non-root/read-only contract' unless container.dig('securityContext', 'readOnlyRootFilesystem') && container.dig('securityContext', 'allowPrivilegeEscalation') == false && container.dig('securityContext', 'capabilities', 'drop') == ['ALL'] && state.dig('spec', 'template', 'spec', 'securityContext', 'runAsNonRoot')
abort 'resource envelope' unless container.dig('resources', 'requests', 'cpu').to_s == '1' && container.dig('resources', 'limits', 'memory') == '2Gi'
abort 'probe contract' unless container.dig('startupProbe', 'failureThreshold') == 120 && container.dig('startupProbe', 'periodSeconds') == 5 && container.dig('readinessProbe', 'failureThreshold') == 1 && container.dig('livenessProbe', 'periodSeconds') == 10
abort 'subPath present' if state.to_s.include?('subPath')
portal = find.call(serving, 'Ingress', '-portal') or abort 'portal Ingress missing'
wss = find.call(serving, 'Ingress', '-wss') or abort 'WSS Ingress missing'
abort 'host separation' unless portal.dig('spec', 'rules', 0, 'host') == 'portal.example.test' && wss.dig('spec', 'rules', 0, 'host') == 'wss.example.test'
abort 'WSS backend mismatch' unless wss.dig('spec', 'rules', 0, 'http', 'paths', 0, 'backend', 'service', 'name').end_with?('-wss')
abort 'WSS proxy idle budget below reservation lifetime' unless wss.dig('metadata', 'annotations', 'nginx.org/proxy-read-timeout') == '3600s' && wss.dig('metadata', 'annotations', 'nginx.org/proxy-send-timeout') == '3600s'
%w[tcp udp].each do |kind|
  service = find.call(serving, 'Service', "-#{kind}") or abort "#{kind} NLB missing"
  abort 'NodePorts or instant failover' unless service.dig('spec', 'allocateLoadBalancerNodePorts') == false && service.dig('metadata', 'annotations', 'oci-network-load-balancer.oraclecloud.com/is-instant-failover-enabled') == 'false'
  health = JSON.parse(service.dig('metadata', 'annotations', 'oci-network-load-balancer.oraclecloud.com/health-check'))
  abort 'private readyz health' unless health['protocol'] == 'HTTP' && health['urlPath'] == '/readyz' && health['port'] == 8081
end
role = find.call(serving, 'Role', '-watch') or abort 'watch Role missing'
abort 'watch authority too broad' unless role.dig('rules', 0, 'resources') == ['services'] && role.dig('rules', 0, 'resourceNames').sort == %w[isolated-clipp-relay-tcp isolated-clipp-relay-udp] && role.dig('rules', 0, 'verbs').sort == %w[get list watch]
policy = find.call(serving, 'NetworkPolicy', 'clipp-relay') or abort 'NetworkPolicy missing'
abort 'not default deny' unless policy.dig('spec', 'policyTypes').sort == %w[Egress Ingress] && policy.dig('spec', 'ingress').length >= 3 && policy.dig('spec', 'egress').length >= 3
config = find.call(serving, 'ConfigMap', '-config') or abort 'config missing'
settings = JSON.parse(config.dig('data', 'config.json'))
abort 'wrong DB target' unless settings.dig('database', 'mode') == 'external' && settings.dig('database', 'host') == 'db.internal.example.test'
abort 'secret value in config' if config.to_s.include?('client-secret-value')
puts 'rendered manifest assertions passed'

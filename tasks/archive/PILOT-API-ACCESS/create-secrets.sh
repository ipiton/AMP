#!/usr/bin/env bash
# Creates the Secrets for the pilot's basic auth. Prints no secret value and
# refuses to overwrite an existing Secret.
#
#   create-secrets.sh <kube-context>
#
# Run by the owner, see ROLLOUT.md. AMP_NS and MONITORING_NS override the
# namespaces (used to test the script).
set -euo pipefail

CONTEXT="${1:?usage: create-secrets.sh <kube-context>}"
AMP_NS="${AMP_NS:-amp}"
MONITORING_NS="${MONITORING_NS:-sema-monitoring}"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT
umask 077

k() { kubectl --context "${CONTEXT}" "$@"; }

for item in "${AMP_NS}/amp-web-config" "${AMP_NS}/amp-metrics-auth" "${AMP_NS}/amp-operator-auth" \
  "${MONITORING_NS}/amp-prometheus-auth" "${MONITORING_NS}/amp-loki-auth"; do
  if k get secret -n "${item%/*}" "${item#*/}" >/dev/null 2>&1; then
    echo "Secret ${item} already exists, nothing was changed" >&2
    exit 1
  fi
done

echo 'basic_auth_users:' >"${WORK_DIR}/web-config.yml"
for user in prometheus loki operator; do
  openssl rand -hex 24 | tr -d '\n' >"${WORK_DIR}/${user}"
  hash="$(htpasswd -inBC 10 "${user}" <"${WORK_DIR}/${user}" | cut -d: -f2)"
  printf '  %s: %s\n' "${user}" "${hash}" >>"${WORK_DIR}/web-config.yml"
done
# Loki sends this file as the value of "Authorization: Basic <value>".
printf 'loki:%s' "$(cat "${WORK_DIR}/loki")" | base64 | tr -d '\n' >"${WORK_DIR}/authorization"

create() { k create secret generic "$@" >/dev/null; }

# Clients first: a failure below leaves AMP's own configuration untouched.
create -n "${MONITORING_NS}" amp-prometheus-auth --from-file=password="${WORK_DIR}/prometheus"
create -n "${MONITORING_NS}" amp-loki-auth --from-file=authorization="${WORK_DIR}/authorization"
create -n "${AMP_NS}" amp-metrics-auth --from-literal=username=prometheus --from-file=password="${WORK_DIR}/prometheus"
create -n "${AMP_NS}" amp-operator-auth --from-literal=username=operator --from-file=password="${WORK_DIR}/operator"
create -n "${AMP_NS}" amp-web-config --from-file=web-config.yml="${WORK_DIR}/web-config.yml"

echo "created: ${AMP_NS}/{amp-web-config,amp-metrics-auth,amp-operator-auth}, ${MONITORING_NS}/{amp-prometheus-auth,amp-loki-auth}"

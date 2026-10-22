#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version=${1:-1.36.2}
case "$version" in
  1.34.1|1.35.0|1.36.2) ;;
  *) printf 'Versao envtest nao suportada: %s\n' "$version" >&2; exit 1 ;;
esac
mkdir -p .local/envtest
export USE_EXISTING_CLUSTER=false
export KUBECONFIG=/dev/null
KUBEBUILDER_ASSETS=$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@4d452bc0517cf4c6b20e727352f52bb50cbf0df4 use "$version" --bin-dir "$PWD/.local/envtest" -p path)
export KUBEBUILDER_ASSETS
go test -tags=integration -race -count=1 -timeout=5m -run '^TestEnvtestConnectAndSwitchContexts$' ./internal/kube

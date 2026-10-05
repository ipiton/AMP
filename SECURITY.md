# Security Policy

## Supported Versions

AMP is pre-1.0. Security fixes land on `main` and ship in the next release of the
latest `0.x` minor line; older releases are not patched.

| Version                   | Supported          |
| ------------------------- | ------------------ |
| latest `0.x` release      | :white_check_mark: |
| `main`                    | :white_check_mark: |
| older releases (`v0.0.x`) | :x:                |

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues, discussions or pull requests.**

Report them privately through GitHub's private vulnerability reporting:
**[Report a vulnerability](https://github.com/ipiton/AMP/security/advisories/new)**
(repository **Security** tab → **Report a vulnerability**). Only the maintainers can see the report.

AMP is maintained by a single developer, so response times are best effort: we aim to acknowledge a report within 5 business days and to keep you updated in the advisory thread until it is resolved.

Please include as much of the following as you can:

* Type of issue (e.g. authentication bypass, injection, information disclosure)
* Affected component and file paths
* The version, tag or commit where you found it
* Configuration required to reproduce (config.yaml, alertmanager.yml, Helm values — with secrets removed)
* Step-by-step instructions to reproduce
* Proof-of-concept code, if possible
* Impact: what an attacker can do and under which preconditions

## What happens next

* We confirm the problem and determine the affected versions.
* We prepare a fix on a private branch or in a temporary private fork attached to the advisory.
* We release the fix, publish a GitHub Security Advisory (requesting a CVE where applicable) and credit you unless you prefer to stay anonymous.

## Current Security Posture

This section describes what AMP actually provides today, so operators know which protections they get and which ones they must supply themselves.

### Authentication & Authorization

* **Provided:** HTTP basic authentication using the upstream Alertmanager web config format (`--web.config.file`, `basic_auth_users` with bcrypt hashes), with password rotation without restart. Off by default — AMP logs a `WARN` at startup while the API is open. Helm: `webConfig.existingSecret`. See [`docs/CONFIGURATION_GUIDE.md`](docs/CONFIGURATION_GUIDE.md) → *Enable HTTP Authentication*.
* **Not provided:** bearer tokens, API keys, JWT, OAuth/OIDC, per-user roles. Every authenticated user has full access, including silences, `/-/reload` and LLM investigations. For SSO, put an authenticating proxy in front of AMP.
* `webhook.authentication.*` in the application config is **not enforced**; AMP logs a `WARN` if it is enabled.

### Transport Security

* **Not provided:** the AMP server does not terminate TLS; `tls_server_config` in the web config is rejected at startup. Terminate TLS at your ingress controller, load balancer or service mesh.
* **Provided (outbound):** receivers support upstream `http_config.tls_config` (CA, client certificate, server name, insecure skip verify) for notifications AMP sends.

### Network Exposure (Helm chart)

* The Ingress renders only when authentication is configured (`webConfig.existingSecret`) or explicitly delegated to the ingress (`ingress.externalAuth: true`).
* An ingress-only NetworkPolicy for the AMP pods (`networkPolicy.*`), enabled in `values-production.yaml`, off by default. Egress is not restricted.
* `service.type: NodePort` / `LoadBalancer` is not guarded — do not expose the Service directly without authentication.
* See [`helm/amp/README.md`](helm/amp/README.md) → *Network Exposure*.

### Kubernetes Permissions

* No cluster-scoped RBAC. A single namespaced Role with `list secrets` in the target discovery namespace is created only when publishing target discovery is enabled. Redis and backup pods run without a service account token.
* Containers run as non-root (UID 65534) with a read-only root filesystem, no privilege escalation and all capabilities dropped (chart defaults).

### Secrets

* Secrets are read from environment variables, mounted files and Kubernetes Secrets. There is no built-in integration with external secret managers (Vault, cloud KMS); use an operator such as External Secrets to sync them into Kubernetes Secrets.

### Not Provided

* CORS handling and rate limiting on the HTTP API (`rate_limit` in the web config is rejected at startup). Put them in your ingress or proxy if you need them.
* A dedicated audit log. AMP writes structured logs (`log/slog`), but they are not a tamper-evident audit trail.

### Supply Chain

* CI runs on every pull request: `scripts/release-gate.sh` (build, `golangci-lint` with errcheck, govet, staticcheck and others, tests, `-race` on the concurrency-heavy packages, Helm lint/render/RBAC checks) and `govulncheck` for reachable vulnerabilities in dependencies. See [`docs/CI.md`](docs/CI.md).
* Release images are built in GitHub Actions and published to GHCR on `v*` tags.
* Not yet provided: SBOM, provenance attestations, image signing, image vulnerability scanning, automated dependency updates.

## Production Deployment Checklist

- [ ] Enable HTTP authentication (`--web.config.file` / `webConfig.existingSecret`)
- [ ] Terminate TLS in front of AMP (ingress, load balancer or service mesh)
- [ ] Keep the Service internal (`ClusterIP`); expose only through an authenticated Ingress
- [ ] Enable the AMP NetworkPolicy and list only the alert senders and scrapers that need access
- [ ] Scrape `/metrics` with basic auth (or list it in `server.auth.unauthenticated_paths` deliberately)
- [ ] Store passwords and API keys (LLM provider, receivers) in Kubernetes Secrets, not in values files
- [ ] Use TLS (`sslmode=require` or stricter) for an external PostgreSQL
- [ ] Set a Redis/Valkey password and keep it reachable only from AMP
- [ ] Add rate limiting and CORS at the ingress if the API is reachable from browsers or untrusted networks
- [ ] Subscribe to this repository's security advisories (Watch → Custom → Security alerts)

## Responsible Disclosure

We kindly ask that you:

* Give us reasonable time to address the issue before public disclosure (we aim for 90 days at most)
* Make a good faith effort to avoid privacy violations, destruction of data, and interruption or degradation of services
* Only test against deployments you own or have explicit permission to test
* Don't exploit the vulnerability beyond what is necessary to confirm its existence

## Credits

We thank the following people for responsibly disclosing security issues:

* (none yet)

---

**Last Updated**: 2026-10-05

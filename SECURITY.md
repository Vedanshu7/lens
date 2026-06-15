# Security Policy

## Supported versions

| Version | Supported |
|---------|-----------|
| `main` branch | Yes — active |
| Tagged releases | Yes — latest tag only |
| Older releases | No backports |

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Report vulnerabilities privately via [GitHub Security Advisories](https://github.com/Vedanshu7/lens/security/advisories/new).

Include:
- A clear description of the vulnerability
- Steps to reproduce (proof-of-concept or minimal repro if possible)
- The component and file affected
- Your assessment of severity (Critical / High / Medium / Low)

### Response timeline

| Stage | Target |
|-------|--------|
| Acknowledgement | Within 48 hours |
| Triage and severity assessment | Within 5 business days |
| Fix or mitigation plan | Within 30 days for Critical/High |
| Public disclosure | After fix is released; coordinated with reporter |

We aim to credit reporters in the release notes unless they request otherwise.

## Known open security work

The following security improvements are tracked as open issues. They are not CVEs and do not represent current exploits, but they improve the hardening posture of the sidecar:

| Issue | Title |
|-------|-------|
| [#41](https://github.com/Vedanshu7/lens/issues/41) | Add security headers to HTTP API |
| [#42](https://github.com/Vedanshu7/lens/issues/42) | Pin GitHub Actions to commit SHAs |
| [#43](https://github.com/Vedanshu7/lens/issues/43) | Add license compliance scanning |
| [#35](https://github.com/Vedanshu7/lens/issues/35) | Container image vulnerability scanning with Trivy |
| [#36](https://github.com/Vedanshu7/lens/issues/36) | Generate SBOM on every release |
| [#37](https://github.com/Vedanshu7/lens/issues/37) | Secret scanning with Gitleaks |
| [#38](https://github.com/Vedanshu7/lens/issues/38) | Fuzz testing for config parsing and invalidation input |
| [#39](https://github.com/Vedanshu7/lens/issues/39) | SLSA Level 2 build provenance |
| [#40](https://github.com/Vedanshu7/lens/issues/40) | Rate limiting on the HTTP API |
| [#53](https://github.com/Vedanshu7/lens/issues/53) | BindAddr config field ignored — server binds 0.0.0.0 |
| [#56](https://github.com/Vedanshu7/lens/issues/56) | `/api/declare` has no authentication |

## Threat model summary

Lens is designed for deployment inside a trusted private network (Kubernetes cluster, VPC). It is **not** designed to be exposed to the public internet.

- **Token auth (`x-lens-token`)** is the current access control mechanism. It is optional but recommended in production.
- **No TLS** is implemented yet for sidecar-to-sidecar or sidecar-to-dashboard traffic. See [#24](https://github.com/Vedanshu7/lens/issues/24) for mTLS transport support.
- **`/api/declare` is currently unauthenticated** even when a token is configured. See [#56](https://github.com/Vedanshu7/lens/issues/56).
- **The server binds to all interfaces** regardless of `LENS_BIND_ADDR`. See [#53](https://github.com/Vedanshu7/lens/issues/53).

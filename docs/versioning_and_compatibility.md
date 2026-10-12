# Versioning & Compatibility Policy (Requirement C-7)

**English** | [日本語](ja/versioning_and_compatibility.md)

This document establishes versioning standards, backward compatibility guarantees, and version-pinning practices for Tollgate and integrating applications.

---

## 1. Semantic Versioning (SemVer 2.0.0)

Tollgate follows [Semantic Versioning 2.0.0](https://semver.org/):

$$\text{MAJOR}.\text{MINOR}.\text{PATCH}$$

- **MAJOR**: Breaking changes that alter public API schemas, proxy forwarding behaviors, or configuration structures.
- **MINOR**: Backward-compatible new features, added database backends, or non-breaking API additions.
- **PATCH**: Backward-compatible bug fixes, security patches, and internal performance optimizations.

---

## 2. Compatibility Guarantees

### Forward & Backward Compatibility
- **Header Contracts**: Headers injected by Tollgate (`X-Tenant-ID`, `X-Service-ID`, `X-Key-ID`, `X-Key-Prefix`, `X-Request-Id`) maintain stable naming and semantic behavior across all minor releases.
- **Configuration Compatibility**: Renaming or deprecating environment variables requires at least one minor release cycle with fallback support before removal in a major release.

### Breaking Change Protocol
Any breaking change requires:
1. Incrementing the **MAJOR** version number.
2. Publishing migration instructions in `CHANGELOG.md` and `MIGRATION.md`.
3. Clear architectural notice in release tags.

---

## 3. Product Integration & Version Pinning

Production workloads must **strictly pin** Tollgate image and binary tags:

```yaml
# Good: Exact semantic version pinning
image: ghcr.io/kanmon-hq/tollgate:v1.1.2

# Avoid in Production: Unpredictable upstream changes
image: ghcr.io/kanmon-hq/tollgate:latest
```

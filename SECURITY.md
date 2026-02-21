# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it privately by emailing the maintainer or opening a [GitHub security advisory](https://github.com/olgasafonova/mcp-otel-go/security/advisories/new).

Do not open a public issue for security vulnerabilities.

## Scope

This package is an instrumentation middleware. It does not handle authentication, authorization, or transport security. Security concerns specific to this package include:

- Unintended PII leakage through telemetry attributes (error messages, resource URIs)
- Denial of service through high-cardinality metric attributes

The `RedactError` and `RedactURI` config options exist to mitigate PII leakage. See the [README](README.md#privacy-controls) for details.

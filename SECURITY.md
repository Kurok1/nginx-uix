# Security Policy

## Supported versions

Nginx UIX has not published its first stable release yet. Before the formal `v1.0.0` release, no build is supported for production use.

After `v1.0.0` is published, the latest `1.0.x` patch release will receive compatible security, data-integrity, rollback and reliability fixes. Pre-1.0 builds and superseded patch releases are unsupported unless a published security advisory explicitly says otherwise.

## Report a vulnerability privately

Use [GitHub Private Vulnerability Reporting](https://github.com/Kurok1/nginx-uix/security/advisories/new) for suspected vulnerabilities. The repository setting is enabled, so the report and follow-up discussion remain private to the reporter and repository security collaborators until coordinated disclosure.

Do not disclose an unpatched vulnerability in a public Issue, Discussion, pull request, commit, log paste or social channel. Do not submit real passwords, Session cookies, Authorization headers, Cloudflare Tokens, ACME account keys, certificate private keys, production configuration bodies or personal data. Use minimal synthetic fixtures.

Include when available:

- affected version, commit, image digest and platform;
- deployment shape and relevant Nginx version;
- impact and required attacker access;
- minimal reproducible steps or a proof of concept using synthetic data;
- whether credentials, configuration, certificates or availability may be affected;
- suggested mitigation and any disclosure deadline.

Use public Issues for ordinary bugs and support questions only when they contain no undisclosed security impact or sensitive material.

## Response targets

These are response goals, not a warranty:

- acknowledge a private report within three business days;
- provide an initial severity and scope assessment within seven business days;
- send a status update at least every seven days while remediation is active;
- prioritize Critical and High issues, including a safe mitigation when a complete fix needs more time;
- coordinate publication after a fix or agreed mitigation is available.

The maintainers may request more evidence, reject reports that do not describe a security boundary, or adjust disclosure timing when users need time to upgrade. Reporter credit is offered unless the reporter asks to remain anonymous or the report is abusive.

## Security boundaries

High-priority reports include:

- authentication, Session, CSRF or trusted-origin bypass;
- arbitrary command execution or escape from the typed Agent boundary;
- path traversal, unsafe symlink handling or access outside approved roots;
- production configuration corruption, unsafe reload, rollback failure or false success;
- Route Lab escape from its loopback sandbox;
- disclosure of passwords, Tokens, private keys, configuration bodies or sensitive audit data;
- certificate/ACME authorization or cleanup failures that cross tenant or domain boundaries;
- container privilege escalation beyond the documented capability and Unix Socket model.

The project intentionally supports only the single-node Docker deployment and feature scope documented in `PLAN.md`. Requests for Kubernetes, clusters, Nginx Plus, WAF, arbitrary Shell access or Docker Socket management are product requests, not vulnerabilities by themselves.

## Fix and disclosure process

Accepted reports are handled in a private GitHub Security Advisory. Fixes start with a reproducer or failing test, preserve backward-compatible REST API v1 and persisted data unless a security boundary requires otherwise, and rerun the risk-appropriate quality, Docker, upgrade and recovery gates.

An advisory should describe affected versions, impact, mitigation, fixed version, upgrade/rollback instructions and credit. Secrets, private exploit details and user data are removed before publication. A release is not considered fixed until its source commit and immutable artifact digest have been verified.

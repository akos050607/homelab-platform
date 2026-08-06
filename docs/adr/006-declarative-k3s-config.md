# ADR-006 — Declarative K3s configuration

**Context.** The K3s installation failed because passing complex strings (like `--vpn-auth="name=tailscale,joinKey=..."`) via bash environment variables over SSH resulted in mangled escape characters in the `systemd` ExecStart line.
**Decision.** K3s is configured via a declarative `/etc/rancher/k3s/config.yaml` file injected before the installer runs, bypassing CLI flags entirely.
**Alternatives rejected.** Complex bash quoting or nested HEREDOCs. While possible, it remains fragile and unreadable.
**Consequences.** Node provisioning is more reliable, idempotent, and aligns with Infrastructure as Code principles. The installer script simply consumes the YAML file with no argument-parsing risks.

# Contributing

Open Remote Commander accepts focused changes with tests. Read `AGENTS.md` and the relevant repo-local skills before editing privileged code.

## Development gates

```bash
make test
make race
make lint
make vuln
```

Security-sensitive changes should include a regression test or a written explanation of why deterministic testing is not practical. New dependencies need a reason, license check, and vulnerability scan. Avoid large refactors mixed with feature changes.

Use conventional, imperative commit subjects. Keep one logical change per commit where practical.

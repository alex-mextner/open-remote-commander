# Security policy

Open Remote Commander provides remote filesystem and terminal access to paired machines. A vulnerability in authentication, authorization, path policy, relay isolation, or update distribution can have high impact.

## Supported versions

Only the latest tagged release and `main` receive security fixes during the engineering-preview phase.

## Trust model

Tools execute with the permissions of the local user running `orc-agent`. Directory restrictions and future command deny-lists are guardrails, not a sandbox. Use a dedicated account, container, or VM for high-risk automation.

The service is designed so the cloud relay routes calls but does not itself receive an operating-system shell. The database schema stores call metadata and intentionally omits tool arguments and results.

## Reporting

Do not file a public issue for a suspected vulnerability. Use GitHub private vulnerability reporting after the repository is published. Until then, report privately to the repository owner.

Include affected version/commit, prerequisites, reproduction steps, impact, and any suggested mitigation. Do not access data or machines you do not own or have explicit authorization to test.

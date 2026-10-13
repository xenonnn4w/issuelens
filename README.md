# Issue Lens

AI investigations for public GitHub issues and failed GitHub Actions runs.

[Live app](https://issuelens.me)

## Features

- **Issue agent:** investigates bug reports using related issues and commit-pinned code.
- **CI agent:** analyzes failed steps, logs, workflows, and commit changes.
- Source-linked reports, private history, draft review, and Markdown export.
- Bring your own OpenAI key and GitHub token through Connections.

## Stack

Go backend, TypeScript frontend, SQLite, Clerk, and OpenAI. Deployed on AWS EC2 with Docker Compose, Caddy, Cloudflare, and GitHub Actions.

## Local Setup

Requires Go 1.25, Node.js 22.6+, and a C compiler.

Create `backend/.env`:

```dotenv
CLERK_PUBLISHABLE_KEY=pk_test_...
CLERK_ISSUER=https://your-instance.clerk.accounts.dev
OPENAI_API_KEY=
GITHUB_TOKEN=
```

```bash
npm ci --prefix web
make run
```

Open [localhost:8765](http://127.0.0.1:8765). Run checks with `make test check`.

## Limits

Public repositories only. No code execution or automatic GitHub posting. Reports require human review. Without an OpenAI key, runs collect evidence only. The shared AI key allows one run per hour; personal keys bypass this allowance. Some CI logs require a GitHub token with read access.

Connection keys stay in browser-tab storage, not the report database. AI mode sends collected evidence to the provider. CI credential redaction is not exhaustive.

## Deployment

See [infra/.env.example](infra/.env.example). Passing pushes to `main` deploy through GitHub Actions using `DEPLOY_HOST`, `DEPLOY_SSH_KEY`, and `DEPLOY_KNOWN_HOSTS`.

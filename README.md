# Issue Lens

Investigate GitHub issues and failed CI runs with source-linked AI reports.

[Try Issue Lens](https://issuelens.me)

## What It Does

- **Issue Investigator:** reads bug reports, related issues, and repository code to prepare findings and a response draft.
- **CI Failure Investigator:** reads failed GitHub Actions steps, logs, workflow configuration, and commit changes to suggest a diagnosis and verification steps.
- Saves private investigation history, evidence, and reports for each account.
- Supports draft review and Markdown export. Nothing is posted to GitHub automatically.

Choose an agent on **New investigation**, paste its GitHub URL, and review the report. Add your own OpenAI key or GitHub token in **Connections** when needed.

## Stack

Go, TypeScript, SQLite, Clerk, and OpenAI API. Docker Compose and Caddy run the app on AWS EC2 behind Cloudflare. GitHub Actions handles tests and deployment.

Both agents run inside the same Go backend:

```text
Browser -> Go API -> GitHub evidence -> AI tool loop -> Cited report
              |                                         |
            Clerk                                    SQLite
```

## Run Locally

Requires Go 1.25, a C compiler for SQLite, and Node.js 22.6 or newer.

Create `backend/.env` with your Clerk development settings:

```dotenv
CLERK_PUBLISHABLE_KEY=pk_test_...
CLERK_ISSUER=https://your-instance.clerk.accounts.dev
OPENAI_API_KEY=
GITHUB_TOKEN=
```

Provider keys are optional: you can add them through Connections. Without an OpenAI key, runs collect evidence in retrieval-only mode. Some CI logs require a GitHub token with Actions read and Contents read access.

```bash
npm ci --prefix web
make run
```

Open [localhost:8765](http://127.0.0.1:8765). Run `make test check` for the build, TypeScript checks, and Go race tests.

## Limits And Privacy

- Public repositories only. Code is read, never executed; proposed fixes are not verified.
- Searches and log excerpts are bounded. Citations need human review and do not prove a root cause.
- The shared AI key allows one run per hour across the service. Your own key bypasses that allowance; provider and worker limits still apply.
- Connection keys stay in account-scoped browser-tab storage and are not saved with reports. In AI mode, collected evidence is sent to the model provider.
- Common credentials in CI evidence are redacted, but detection is not exhaustive.

## Deploy

Configure the EC2 environment using [infra/.env.example](infra/.env.example), then run:

```bash
bash infra/deploy.sh issuelens-ec2
```

Passing pushes to `main` also deploy through GitHub Actions. The production environment requires `DEPLOY_HOST`, `DEPLOY_SSH_KEY`, and `DEPLOY_KNOWN_HOSTS`. Keep `.env` files out of Git.

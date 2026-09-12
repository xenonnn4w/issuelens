# Issue Lens

Issue Lens investigates public GitHub bug reports and failed GitHub Actions runs. It retrieves commit-pinned evidence and prepares cited findings and a response draft for maintainer review. The backend is written in Go; the browser console is built from strict TypeScript and CSS. The service uses SQLite for its single-instance job history, Docker Compose, Caddy, and GitHub Actions on AWS EC2.

## Run locally

Requires Go 1.25, a C compiler for the SQLite driver, and Node.js 22 or newer for the web build.

```bash
npm ci --prefix web
make test check
make run
```

Open http://127.0.0.1:8765. `LISTEN_ADDR`, `ISSUE_LENS_DB`, and `WEB_ROOT` are optional environment settings. Set `CLERK_PUBLISHABLE_KEY` and `CLERK_ISSUER` for a Clerk development instance to sign in locally; the issuer is its Frontend API URL. Set `OPENAI_API_KEY` to enable the AI investigator and `GITHUB_TOKEN` to raise GitHub API limits. The default model is `gpt-4.1-mini`; `OPENAI_MODEL` and `OPENAI_BASE_URL` configure the server-side provider. Without an OpenAI key the application explicitly uses retrieval-only mode.

Signed-in users can also add their own OpenAI and GitHub keys in **Connections**. These remain in account-scoped browser-tab session storage, are sent with start/recovery requests, and are used only by that in-memory job. Personal OpenAI keys always go to the official OpenAI endpoint. Keys are not stored in SQLite, returned by the API, or included in reports. Issue descriptions and retrieved code are sent to the configured model provider in AI mode. Use a GitHub token with read-only access to public repositories.

## Investigation

1. Paste a specific public issue URL on **New investigation**.
2. The Go GitHub adapter reads the issue and first 20 comments, pins the default branch to a commit SHA, and collects bounded source evidence.
3. The model chooses among `search_issues`, `search_paths`, `search_code`, and `read_file`, then submits a structured report. Every finding must cite an evidence ID actually retrieved. Retrieval-only mode collects evidence without pretending to have performed AI analysis.
4. Evidence and failures are saved. A failed run with evidence can generate a report from saved sources without another GitHub call.
5. The maintainer can edit and approve the response draft, copy it, or download a Markdown report. Issue Lens never posts to GitHub.

Two investigations run concurrently and at most six may be active or queued. Each investigation has a 36-request GitHub budget, 10 model turns, 18 tool calls, and a six-minute timeout. The shared server OpenAI key starts at most one AI investigation or recovery per rolling hour, enforced in SQLite; a visitor's own OpenAI key bypasses that allowance. Retrieval-only runs without a personal key retain a 10-per-UTC-day default cap (`PUBLIC_DAILY_RUN_LIMIT`). A single Go process owns the in-memory job queue; SQLite is not a distributed job broker.

## CI Failure Investigator

Choose **CI failure** on New investigation and paste `https://github.com/owner/repo/actions/runs/123`. Run-attempt and job URLs are accepted; a job URL investigates the run's latest attempt, while an explicit `/attempts/2` URL keeps that attempt. Only completed failed runs in public repositories are supported.

The collector reads metadata for up to 100 jobs and downloads logs for up to three failed jobs. It retains failure windows and the job tail from at most 1 MiB per job, reads the workflow at the run's head commit, and collects patches for up to ten changed files. The AI agent can inspect more pinned files using the existing tools and proposes a diagnosis, targeted changes, and verification commands. It never runs workflows, applies fixes, or posts to GitHub. Retrieval mode collects evidence without AI diagnosis.

GitHub may require a token for log access. Add a fine-grained token with **Actions: Read** and **Contents: Read** for the target public repository in Connections. Deleted or expired logs cannot be recovered. Signed log-storage URLs are fetched without forwarding GitHub credentials and are never saved as evidence links. Common credentials are redacted before logs reach storage or the model, but redaction is not exhaustive. In AI mode, collected workflow logs and changes are sent to the configured model provider. CI runs share the existing queue, authentication, ownership, and shared-key allowance.

## Deployment

`infra/docker-compose.yml` builds the Go API, mounts the static console and a persistent SQLite volume, and places Caddy in front of the API. Production requires `GATEWAY_TOKEN`; the API port is not published. Caddy injects that token. All investigation and usage endpoints require a Clerk session, verified against the production JWKS and issuer, and every run is scoped to its creator. A signed-in user can edit and approve only their own drafts. The application checks hosts and write origins. Static assets are served with `Cache-Control: no-store` so Cloudflare does not mix releases.

Prepare `/opt/issue-lens/infra/.env` from `infra/.env.example` on the EC2 host. Set `CLERK_PUBLISHABLE_KEY` and `CLERK_ISSUER=https://clerk.issuelens.me` for the production instance. The Go API verifies tokens with Clerk's public JWKS and does not need an admin secret key. Keep `.env` out of Git. Deploy manually with:

```bash
bash infra/deploy.sh issuelens-ec2
```

The GitHub Actions workflow typechecks and builds the web package, runs Go tests, and deploys passing `main` pushes. The browser bundle is generated during deployment, not stored in Git. The `production` environment needs `DEPLOY_HOST`, `DEPLOY_SSH_KEY`, and `DEPLOY_KNOWN_HOSTS`. The deploy script synchronizes source without copying local secrets and waits for both services to become healthy.

## Limits

- Only public repositories are supported; repository code is read, never executed.
- Content search covers a bounded group of candidate files, not an exhaustive semantic index or call graph.
- Current default-branch code may differ from the reporter's version. The agent does not reproduce bugs or run tests.
- Citation validation proves a source was observed, not that a claim follows from it. A maintainer must review the report.
- Reports and usage are private to the signed-in Clerk account that created them. Response approval is local metadata, not a verified GitHub identity.
- Historical benchmark scores are not claimed. An issue-time evaluation must exclude future resolutions and compare identical held-out cases.

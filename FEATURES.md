# Issue Lens features

## Web
- Strict TypeScript in `web/src` builds the static console served by Go.
- Console home displays recent investigations and repository evidence.
- Home keeps recent investigations and activity in a compact workspace layout.
- New Investigation has its own page for issue entry and investigation mode.
- Investigation workspace accepts public GitHub issue URLs and tracks progress.
- Reports include findings, evidence citations, questions, and limitations.
- Response review saves edits and records local approval before copying.
- Failed investigations can generate a report from saved evidence without additional GitHub requests.
- Usage displays recorded investigations, GitHub requests, tool calls, and model tokens.
- Connections accepts per-tab OpenAI and GitHub credentials; run-scoped keys never enter saved reports.
- The shared AI key is limited to one run per hour; personal OpenAI keys bypass that allowance.
- Documentation describes configuration, tools, and investigation limits.

## API
- Repository tools search issues, file paths, and bounded code contents at a fixed commit.
- AI investigation validates structured findings against retrieved evidence IDs.
- Retrieval mode collects evidence without a model key.
- The Go API and SQLite retain investigations, evidence, review state, and usage counters.
- Markdown export includes findings, sources, and the reviewed response.

## Infrastructure
- Docker Compose runs an isolated API with persistent storage behind Caddy HTTPS.
- The deployed console and investigations are public, with a daily run cap; response edits and approval require maintainer authentication.
- Deployment sync preserves secrets and stored data in a separate EC2 directory.

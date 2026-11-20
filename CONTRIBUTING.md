# Contributing to KubePilot

Thanks for helping make KubePilot the best way to run game servers on Kubernetes!
Bug reports, translations, templates for new games, docs and code are all welcome.

## Before you start

- **Bugs & ideas** → open an [issue](https://github.com/NycolazSec/KubePilot-Panel/issues/new/choose) first,
  so we can agree on the approach before you spend time on a pull request.
- **Security issues** → never in a public issue: see [SECURITY.md](SECURITY.md).
- Check the [roadmap](ROADMAP.md) to see what is planned and what is up for grabs.

## Development setup

You need Go ≥ 1.26, Node.js ≥ 20 and access to a Kubernetes cluster (a local [k3s](https://k3s.io) or
[kind](https://kind.sigs.k8s.io) cluster is fine).

```sh
./build.sh                                   # typecheck + build the frontend, go vet, go test, build ./kubepilot
./kubepilot -data ./dev-data -listen 127.0.0.1:8099 -secure-cookies=false \
  -kubeconfig ~/.kube/config                 # run the panel locally against your cluster
cd frontend && npm run dev                   # Vite dev server with hot reload, proxies /api to 127.0.0.1:8099
./kubepilot -data ./dev-data user:create --username admin --email admin@example.com --password 'change-me-please' --admin
```

Project layout:

```
backend/    Go: HTTP API, auth, Kubernetes driver (client-go), console hub, files, backups, schedules
frontend/   React + TypeScript + Tailwind, xterm.js console, CodeMirror editor
charts/     Helm chart
deploy/     config example for ./deploy.sh (build from source on k3s)
docs/       logo, banner and documentation
```

## Pull requests

1. Fork the repository and create a branch from `main` (`feat/…`, `fix/…`, `docs/…`).
2. Keep each pull request focused on **one** change, and make sure `./build.sh` passes.
3. Add tests for backend logic when you can (`backend/*_test.go`).
4. Use [Conventional Commits](https://www.conventionalcommits.org) for commit messages:
   `feat: …`, `fix: …`, `docs: …`, `ui: …`, `chore: …`, `ci: …`.
5. Add a line to the `Unreleased` section of [CHANGELOG.md](CHANGELOG.md) for user-visible changes.
6. Open the pull request and fill in the template. CI must be green before merge.

## Translations

KubePilot speaks English and French. Adding a language takes two files:

1. **Interface**: copy `frontend/src/i18n/en.ts` to `frontend/src/i18n/<code>.ts`, translate the values (keep the
   `{placeholders}`), then register it in `frontend/src/i18n/index.tsx` (`catalogs` and `languages`).
   TypeScript refuses to build if a key is missing.
2. **Server messages** (errors, console messages): copy `backend/locales/fr.json` to `backend/locales/<code>.json` and
   translate the values — the keys are the English messages. Keep the `%s` / `%d` verbs in the same order:
   `go test` checks it, and also checks that every message has a translation.

## Guidelines

- **Security first.** Game servers are untrusted: keep them in the `restricted` Pod Security profile, never give the
  panel broader Kubernetes rights than it needs, and route every file path through `os.Root`.
- **Kubernetes-native.** Prefer Kubernetes primitives (StatefulSets, Jobs, Services…) over custom daemons.
- **No new heavy dependencies** without discussing it in an issue first.
- **User-facing text** never goes straight into components or handlers: use `t('key')` in the frontend and English
  messages with `errCode` / `tr` in the backend, so the interface stays available in every language.

## License

KubePilot is licensed under the [GNU AGPL-3.0](LICENSE). By contributing, you agree that your contributions are
licensed under the same terms.

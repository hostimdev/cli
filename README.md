# hostim CLI

Command-line interface for the [Hostim](https://hostim.dev) cloud platform.
Provision and manage projects, apps, databases (MySQL/Postgres/Redis) and
volumes from your terminal or CI pipeline, over the public REST API.

> **Beta.** The Hostim CLI and the public API it uses are in beta. Commands,
> flags, output, and API responses can change between releases. Check the
> release notes before you upgrade.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/hostimdev/cli/main/install.sh | sh
```

Or build from source (Go 1.26+):

```sh
make build && make install
```

## Getting started

```sh
hostim login              # paste an API token created in the dashboard
hostim use my-project     # set the default project
hostim status             # health of every app in the project
```

The target project is resolved as: `-p/--project` flag → `HOSTIM_PROJECT` env →
the project set with `hostim use`. Global flags: `--token`, `--api-url`,
`-o/--output table|json`.

## Deploy (CI-friendly)

`hostim deploy` creates the app if missing (from `--git` or `--docker-image`),
otherwise updates its source and rebuilds — then waits for the build and exits
non-zero if it fails, so it drops straight into a pipeline:

```sh
hostim deploy web \
  --git https://github.com/me/app --branch main \
  --plan small --port 8080 \
  --env LOG_LEVEL=debug --env-file .env

# fire-and-forget
hostim deploy web --docker-image ghcr.io/me/app:latest --no-wait=false
```

## Common commands

```sh
hostim apps ls | get <app> | rebuild <app> | restart <app> | rm <app>
hostim logs web                               # last 100 lines, oldest first
hostim logs web -f                            # stream
hostim logs web --build --since 15m -n 500    # build logs from the last 15m
hostim env set KEY=VALUE --app web            # or --global
hostim env pull --app web --file .env
hostim env push --app web --file .env
hostim domain add example.com --app web
hostim db postgres create main --plan small
hostim db postgres credentials main -o json
hostim volumes create data --plan small --size-mb 5120
hostim regions ls
hostim regions pricing eu-center --for apps
hostim completion zsh > "${fpath[1]}/_hostim"
```

Add `-o json` to any read command for scripting.

## Development

The API client (`api/client.gen.go`) is generated directly from the **live**
public spec at `https://api.hostim.dev/openapi.public.json` — the spec is not
vendored:

```sh
make generate    # curl live spec → oapi-codegen → api/client.gen.go
make test
make lint
```

CI (`.github/workflows/generate.yml`) re-runs `make generate` against the live
API and fails if `api/client.gen.go` has drifted, keeping the committed client
in sync with the deployed API.

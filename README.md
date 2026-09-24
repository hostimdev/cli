# hostim CLI

Command-line interface for the [Hostim](https://hostim.dev) cloud platform.
Create and manage projects, apps, databases (MySQL / Postgres / Redis), volumes
and domains from a terminal or a CI pipeline, over the public REST API at
`https://api.hostim.dev`.

This file is the complete manual. The binary carries the same text: run
`hostim agent` to print it to stdout, so an agent or a script can read the
whole manual without network access.

> **Beta.** The CLI and the public API are in beta. Commands, flags, output and
> API responses can change between releases. Check the release notes before you
> upgrade.

## Contents

- [Install](#install)
- [Log in](#log-in)
- [Configuration](#configuration)
- [Global flags](#global-flags)
- [Output and exit codes](#output-and-exit-codes)
- [Command reference](#command-reference)
  - [login, logout, whoami, use](#login-logout-whoami-use)
  - [projects](#projects)
  - [deploy](#deploy)
  - [apps](#apps)
  - [status, logs, events, overview](#status-logs-events-overview)
  - [env](#env)
  - [domain](#domain)
  - [exec](#exec)
  - [db](#db)
  - [volumes](#volumes)
  - [templates](#templates)
  - [regions](#regions)
  - [completion](#completion)
  - [agent](#agent)
  - [mcp](#mcp)
  - [version](#version)
- [Template files](#template-files)
- [Deploy from CI](#deploy-from-ci)
- [Development](#development)
- [License](#license)

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/hostimdev/cli/main/install.sh | sh
```

The script downloads the release binary for the current OS and architecture
(Linux and macOS, amd64 and arm64) and installs it into `/usr/local/bin`, using
`sudo` when that directory is not writable. When it is not writable and `sudo`
is not installed either — a plain container, for example — the script falls back
to `~/.local/bin` and tells you if that directory is not on your `PATH`. Set
`PREFIX` to choose the location yourself:

```sh
curl -fsSL https://raw.githubusercontent.com/hostimdev/cli/main/install.sh | PREFIX=$HOME/.local sh
```

Some agents and CI policies refuse to pipe a downloaded script into a shell. The
script is not required — fetch the release binary directly:

```sh
VERSION=$(curl -fsSL https://api.github.com/repos/hostimdev/cli/releases/latest | grep -o '"tag_name": *"[^"]*"' | cut -d'"' -f4)
curl -fsSL "https://github.com/hostimdev/cli/releases/download/${VERSION}/hostim_linux_amd64.tar.gz" | tar -xz hostim
install -m 0755 hostim /usr/local/bin/hostim   # or anywhere on your PATH
```

Build from source instead (Go 1.26+):

```sh
git clone https://github.com/hostimdev/cli && cd cli
make build           # writes bin/hostim
make install         # installs into /usr/local/bin (PREFIX to change)
```

Check the install:

```sh
hostim version
```

## Log in

`hostim login` uses a device code. It prints a URL and a short code, you approve
the request in the browser, and the token is saved automatically:

```sh
hostim login
```

`hostim login` polls until you approve and saves the token itself, so there is
no need to write a waiting loop around it or to run it a second time. The
browser step does not need a terminal, so an agent can run `hostim login` and
show you the code. If you already have a token, store it directly instead —
create one in the dashboard at <https://console.hostim.dev>:

```sh
hostim login --token-value "$T"   # non-interactive, for scripts and CI
```

`login` validates the token against the API before saving it to
`~/.config/hostim/config.yml` (mode 0600). In CI, skip `login` and export
`HOSTIM_TOKEN` instead — no file is written.

Most commands act on one project. Set a default once:

```sh
hostim projects ls
hostim use my-project
hostim whoami                     # token source, current project, project count
```

## Configuration

Values are resolved as **flag → environment variable → config file**, with a
built-in default for the API URL.

| Value | Flag | Environment variable | Config key |
|---|---|---|---|
| API token | `--token` | `HOSTIM_TOKEN` | `token` |
| API base URL | `--api-url` | `HOSTIM_API_URL` | `apiUrl` (default `https://api.hostim.dev`) |
| Target project | `-p`, `--project` | `HOSTIM_PROJECT` | `currentProject`, set by `hostim use` |

The config file lives at `~/.config/hostim/config.yml`, or
`$XDG_CONFIG_HOME/hostim/config.yml` when `XDG_CONFIG_HOME` is set:

```yaml
token: ...                 # written by `hostim login`
currentProject: my-project # written by `hostim use`
apiUrl: https://api.hostim.dev  # only written when --api-url was passed
```

Projects and apps can be named by name or by ID everywhere.

## Global flags

Available on every command:

```
--token string      API token (overrides HOSTIM_TOKEN and config)
--api-url string    API base URL (default https://api.hostim.dev)
-p, --project       project to target (overrides HOSTIM_PROJECT and current project)
-o, --output        output format: table or json (default "table")
-h, --help          help for the command
-v, --version       version (root command only)
```

Destructive commands (`rm`, `templates apply`) ask for confirmation and take
`-y` / `--yes` to skip the prompt.

## Output and exit codes

Default output is an aligned text table meant for a human. `-o json` switches
every command to machine output and is the right mode for scripts and agents:

- read commands print the API object or list as indented JSON;
- write commands print a single result object naming what happened, for example
  `{"status":"created","kind":"app","name":"web"}` or
  `{"status":"ok","kind":"env","set":1}`;
- failures print a JSON object on **stderr**, for example
  `{"status":"error","error":"..."}`, and exit non-zero. When a command is
  short of several requirements, the object carries a `"missing"` array listing
  all of them at once.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | any CLI or API error, or an aborted confirmation |
| other | `hostim exec` passes through the remote command's exit status |

## Command reference

Every example below is runnable as written once a token and a project are set.

### login, logout, whoami, use

```sh
hostim login                          # device login: approve a code in the browser
hostim login --token-value "$TOKEN"   # store an existing API token instead
hostim logout                         # remove the stored token
hostim whoami                         # API URL, token source, current project
hostim whoami -o json
hostim use my-project                 # set the default project (verifies it exists)
hostim use my-project --force         # skip the check (offline)
hostim use --clear                    # unset the default project
```

### projects

```sh
hostim projects ls                            # aliases: project, proj; ls | list
hostim projects get my-project
hostim projects create my-project --region eu-center
hostim projects rm my-project -y
hostim projects export -p prod -f prod.yml    # template YAML for `templates apply`
hostim projects export -p prod -o json        # template as JSON on stdout
```

`--region` is required on create; see `hostim regions ls` for the values.

`projects export` writes a desired-state template, not a backup: volume and
database contents (data) are not exported. Costs, IDs, built-in domains and
unused source blocks are stripped, and env var values are exported RAW — the
file holds secrets (passwords, API keys), so store and share it like a
password file. Docker registry passwords and git tokens are not readable
through the API and are left out (registry usernames are kept); set them
again after applying.
Redeploy with `hostim templates apply -f prod.yml --new-project prod-clone`.

### deploy

`hostim deploy <app>` creates the app when it does not exist (from `--git` or
`--docker-image`) and otherwise updates its source and triggers a rebuild. It
waits for the build and exits non-zero if the build fails, so it drops straight
into a pipeline. `hostim apps deploy` is the same command.

```sh
# first deploy from a git repo
hostim deploy web \
  --git https://github.com/me/app --branch main \
  --plan sa-1-1 --port 8080 \
  --health-check-path /healthz \
  --env LOG_LEVEL=debug --env-file .env

# redeploy: source is unchanged, just rebuild
hostim deploy web

# deploy a docker image and do not wait
hostim deploy web --docker-image ghcr.io/me/app:latest --wait=false

# private registry
hostim deploy web --docker-image me/app:1.2.3 \
  --registry registry.example.com --docker-user ci --docker-pass "$REG_PASS"

# private git repo
hostim deploy web --git https://github.com/me/private --git-token "$GH_TOKEN"

# mount a volume and attach a domain on create
hostim deploy web --docker-image nginx --plan sa-1-1 --port 80 \
  --volume data:/var/lib/app --domain app.example.com
```

Flags:

```
--git string                 git repository URL
--branch string              git branch
--dockerfile string          path to the Dockerfile in the repo
--git-token string           token for private git repos
--docker-image string        docker image reference
--registry string            docker registry
--docker-user string         docker registry username
--docker-pass string         docker registry password
--plan string                app plan (required when creating)
--port int                   HTTP port (on create)
--public                     expose the app publicly (on create) (default true)
--replicas int               number of replicas (on create) (default 1)
--domain stringArray         custom domain (repeatable, on create)
--volume stringArray         mount a volume as name:mountPath (repeatable)
--env stringArray            environment variable as KEY=VALUE (repeatable, merges)
--env-file string            read environment variables from a .env file (merges)
--command string             override the container command (empty value restores the image default)
--health-check-path string   HTTP path for the readiness probe (empty value disables it)
--wait                       wait for the build to finish (default true)
--timeout duration           max time to wait for the build (default 15m0s)
--poll-interval duration     status poll interval (default 3s)
```

Plan IDs are per resource kind and look like `sa-1-1` (shared app, 1 core, 1 GB
RAM). List the ones a region offers with `hostim regions pricing <region>
--for apps`.

`deploy` handles one app. An app that also needs a database, a Redis or a
volume is deployed as a template — see [templates](#templates).

### apps

```sh
hostim apps ls                    # aliases: app; ls | list
hostim apps get web
hostim apps status                # health table for every app in the project
hostim apps status web
hostim apps rebuild web           # rebuild from the current source
hostim apps restart web           # restart the running containers
hostim apps rm web -y             # aliases: delete, remove
```

### status, logs, events, overview

```sh
hostim status                          # every app in the project
hostim status web                      # one app: build + runtime status
hostim status -o json

hostim logs web                        # last 100 container lines, oldest first
hostim logs web -f                     # stream new lines
hostim logs web -n 500                 # 1-1000 lines
hostim logs web --since 15m            # only lines newer than a duration
hostim logs web --build --since 1h     # build logs (git-built apps only)

hostim events web                      # status history: why the app changed state
hostim events web -f                   # keep printing new events
hostim events web -n 20

hostim overview                        # every resource in every project + cost
hostim overview my-project             # alias: resources
```

`status` shows the current value; `events` shows how it got there, which is
where to look when an app went unhealthy and came back. `--build` works for apps
built from git; an app that runs a prebuilt docker image has no build logs.

### env

Environment variables belong either to one app (`-a/--app`) or to the whole
project (`--global`). Apps see the global set plus their own.

```sh
hostim env get -a web
hostim env get --global -o json
hostim env set KEY=VALUE OTHER=2 -a web       # merges with existing vars
hostim env set SENTRY_DSN=... --global
hostim env rm KEY OTHER -a web
hostim env pull -a web --file .env            # write to a file (- for stdout)
hostim env push -a web --file .env            # load from a file, merging
hostim env push -a web --file .env --replace  # replace the whole set
```

Changing environment variables restarts the app.

### domain

```sh
hostim domain add app.example.com -a web    # prints the DNS record to create
hostim domain status web                    # domains + DNS/certificate state
hostim domain rm app.example.com -a web -y
```

Add the printed record at the DNS provider, then re-run `hostim domain status`
until it reports the domain as active; the certificate is issued automatically.

### exec

Runs a command inside an app's container, or opens an interactive shell when no
command is given. The connection goes through the project's SSH bastion, so the
project has to authorize your public SSH key. The first `hostim exec` against a
project offers to add the key for you; pass `-y` to add it without the prompt,
which is what a script or a CI job needs.

```sh
hostim exec web                                   # interactive shell
hostim exec web -- rails c                        # one-off command
hostim exec web -- sh -c 'ls -la /var/www | head'
hostim exec web -- cat /tmp/app.log               # read a file the app does not log
cat dump.sql | hostim exec --stdin db -- psql app # pipe local data in
hostim exec web -i ~/.ssh/id_ed25519 -- env       # pick the SSH key
hostim exec -y web -- ./migrate.sh                 # no prompts, for CI
```

Aliases: `shell`, `ssh`. The exit status of the remote command becomes the exit
status of `hostim exec`.

### db

Three engines, the same command shape: `hostim db mysql|postgres|redis
ls|get|create|credentials|status|rm`. `pg` is an alias for `postgres`.

```sh
hostim db postgres ls
hostim db postgres create main --plan sp-1
hostim db postgres get main
hostim db postgres status main
hostim db postgres credentials main -o json   # hostname, port, username, password, database
hostim db postgres rm main -y

hostim db mysql ls
hostim db mysql create app --plan sm-1
hostim db mysql get app
hostim db mysql status app
hostim db mysql credentials app -o json
hostim db mysql rm app -y

hostim db redis ls
hostim db redis create cache --plan sr-1
hostim db redis get cache
hostim db redis status cache
hostim db redis credentials cache -o json
hostim db redis rm cache -y
```

`--plan` is required on create. Plan IDs differ per engine — `sp-*` for
Postgres, `sm-*` for MySQL, `sr-*` for Redis — and `hostim regions pricing
<region> --for postgres|mysql|redis` lists them with their storage and price.

Postgres extensions:

```sh
hostim db postgres extensions ls              # what a database can request (alias: ext)
hostim db postgres extensions add main postgis pg_trgm
```

Load a plain-SQL dump (`pg_dump --format=plain`). It goes through the project's
SSH bastion into `psql`. SQL errors are printed and counted but do not stop the
import, because dumps from other hosts carry a few harmless ones (event
triggers, extension owners, unknown settings). It exits non-zero only when psql
cannot run the dump at all, for example when it cannot connect. Like `exec`, it
offers to authorize your SSH key; `-y` adds it without asking.

```sh
hostim db postgres import main -f dump.sql
hostim db postgres import main -y < dump.sql
```

Read the credentials into an app's environment in one step:

```sh
hostim db postgres credentials main -o json \
  | jq -r '"DATABASE_URL=postgres://\(.username):\(.password)@\(.hostname):\(.port)/\(.database)"' \
  | xargs hostim env set -a web
```

### volumes

```sh
hostim volumes ls                                   # aliases: volume, vol
hostim volumes get data
hostim volumes create data --plan vol-1
hostim volumes rm data -y
```

The plan sets the size (`vol-1` is 5 GB); list plans with `hostim regions
pricing <region> --for volume`.

Mount a volume on an app with `hostim deploy <app> --volume data:/var/lib/app`.

### templates

A template is one YAML description of a whole stack — volumes, databases, Redis
and apps — that `apply` creates in dependency order, waiting for each resource.

```sh
hostim templates ls                        # curated templates (aliases: template, tpl)
hostim templates show freshrss

# deploy a curated template into the current project
hostim templates apply --id freshrss

# deploy into a brand new project
hostim templates apply --id freshrss --new-project blog --region eu-center

# save it as YAML, edit, then deploy the edited file
hostim templates apply --id freshrss --save stack.yml
hostim templates validate -f stack.yml     # offline check, no API calls
hostim templates apply -f stack.yml -y

# convert a docker-compose file into a template
hostim templates apply --compose docker-compose.yml --save stack.yml
hostim templates apply --compose docker-compose.yml

# a file holding a list of templates: pick one entry
hostim templates apply -f templates.yml --id freshrss
```

`apply` prints the resources it will create and asks for confirmation
(`-y` skips it). If any resource already exists in the target project it aborts
before creating anything, so it never overwrites a configured or scaled
resource; `--skip-existing` creates only what is missing.

Custom domains from the template are attached only after all resources are up:
a domain already held by another project (for example the project a template
was exported from) is reported per-domain and the rest of the apply still
succeeds — add it later with `hostim domain add <domain> --app <app>`.

Flags for `apply`:

```
--id string                curated template ID, or the entry to pick out of a -f list
-f, --file string          local template YAML file to deploy
--compose string           docker-compose file to convert and deploy
--save string              write the resolved template to a YAML file instead of deploying
--new-project string       create a new project with this name before deploying
--region string            region for --new-project (defaults to the only region when there is one)
--skip-existing            skip resources that already exist instead of aborting
--wait                     wait for each resource to become ready (default true)
--timeout duration         max time to wait per resource (default 15m0s)
--poll-interval duration   status poll interval (default 3s)
-y, --yes                  skip the confirmation prompt
```

### regions

```sh
hostim regions ls                                  # aliases: region
hostim regions get eu-center
hostim regions pricing eu-center                   # app plans (default)
hostim regions pricing eu-center --for postgres    # apps, mysql, postgres, redis, volume
```

Use this to find valid `--plan` and `--region` values before creating anything.

### completion

```sh
hostim completion bash > /etc/bash_completion.d/hostim
hostim completion zsh > "${fpath[1]}/_hostim"
hostim completion fish > ~/.config/fish/completions/hostim.fish
hostim completion powershell | Out-String | Invoke-Expression
```

### agent

```sh
hostim agent            # print this manual to stdout
hostim agent > CLI.md
```

The binary embeds this README, so the manual an agent reads is exactly the
manual shipped with the installed version. Feed it to a coding agent before it
writes Hostim commands, and the agent stops guessing flags.

For agents that load skills (Claude Code and others), the same knowledge ships
as a skill: [`skills/hostim/SKILL.md`](skills/hostim/SKILL.md). Put it at
`.claude/skills/hostim/SKILL.md` in your repository, or let the installer do it:

```sh
curl -fsSL https://hostim.dev/agent.sh | sh
```

That installs the CLI, writes the skill and points `AGENTS.md` at it.

### mcp

```sh
hostim mcp                      # Model Context Protocol server on stdio, read-only
hostim mcp --allow-write        # also expose tools that create, change and delete
```

`hostim mcp` speaks the Model Context Protocol on stdin/stdout, so a coding
agent can inspect and provision Hostim resources as tools. It is the same
binary, the same token and the same public API as every other command.

Read-only tools are always available: `list_projects`, `list_apps`, `get_app`,
`get_app_status`, `get_app_logs`, `get_app_events`, `list_databases`,
`get_database_credentials`, `list_volumes`, `list_regions`, `list_templates`
and `list_region_plans`. Tools that create, change or delete anything —
projects, apps, databases, volumes, env vars and domains — are only registered
with `--allow-write`.

Point an MCP client at it. For Claude Desktop, in
`claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "hostim": {
      "command": "hostim",
      "args": ["mcp", "--allow-write"],
      "env": { "HOSTIM_TOKEN": "your-api-token" }
    }
  }
}
```

Cursor, VS Code and other MCP clients use the same `command` + `args` shape.

Claude Desktop can also install it without the CLI: download
[`hostim.mcpb`](https://github.com/hostimdev/cli/releases/latest/download/hostim.mcpb)
from the latest release and open it. Claude Desktop asks for the token and
whether to allow changes.
Because the token comes from the same place as every other command, you can
also leave `env` out and rely on `hostim login`'s saved token.

### version

```sh
hostim version          # hostim version v1.2.3
hostim --version        # same thing
```

## Template files

A template file is either one template object or a list of them. Field names
match the API's JSON (camelCase). Minimal example:

```yaml
id: my-stack
name: My stack
description: A web app with a Postgres database and a volume
components:
  volumes:
    - name: data
      plan: vol-1
  postgres:
    - name: main
      plan: sp-1
  mysql: []
  redis: []
  apps:
    - name: web
      plan: sa-1-1
      public: true
      replicas: 1
      httpPort: 8080
      healthCheckPath: /healthz
      domains: []
      deploymentSource:
        type: docker
        docker:
          image: ghcr.io/me/app:latest
      envVars:
        - name: LOG_LEVEL
          value: info
      volumeMounts:
        - name: data
          mountPath: /var/lib/app
```

A git-based app uses this source instead:

```yaml
      deploymentSource:
        type: git
        git:
          url: https://github.com/me/app
          branch: main
          dockerfilepath: Dockerfile
```

`hostim templates validate -f <file>` checks names, plans and deployment
sources offline, before any resource is created.

### Placeholders in envVars

Two kinds of placeholder are resolved for you, so a template never has to carry
a hostname or a hand-written secret.

`GENERATE_ME_<n>` is replaced by a fresh random secret of `<n>` characters when
the template is applied. It is expanded by the CLI, so the value that reaches
the API is already the real secret:

```yaml
      envVars:
        - name: APP_SECRET
          value: GENERATE_ME_32
```

`$(NAME)` references another variable present in the container. These are
expanded at container start, not by the CLI, so `hostim env get` still shows the
literal `$(...)` text — that is expected, and the app sees the resolved value:

- `$(BUILTIN_DOMAIN)` — the app's built-in hostname, without a scheme
  (`myapp-abc123.hostim.app`).
- Managed databases in the same project publish connection variables prefixed
  with the database's name, upper-cased and with `-` turned into `_`. A Postgres
  named `main` gives `$(MAIN_POSTGRES_HOST)`, `$(MAIN_POSTGRES_PORT)`,
  `$(MAIN_POSTGRES_DATABASE)`, `$(MAIN_POSTGRES_USER)`,
  `$(MAIN_POSTGRES_PASSWORD)`. MySQL uses `_MYSQL_` with the same five fields;
  Redis uses `$(<NAME>_REDIS_HOST)`, `$(<NAME>_REDIS_PORT)`,
  `$(<NAME>_REDIS_DB)` and `$(<NAME>_REDIS_PASSWORD)`.

```yaml
  postgres:
    - name: main
      plan: sp-1
  apps:
    - name: web
      envVars:
        - name: DATABASE_URL
          value: postgresql://$(MAIN_POSTGRES_USER):$(MAIN_POSTGRES_PASSWORD)@$(MAIN_POSTGRES_HOST):$(MAIN_POSTGRES_PORT)/$(MAIN_POSTGRES_DATABASE)
        - name: BASE_URL
          value: https://$(BUILTIN_DOMAIN)
        - name: SESSION_SECRET
          value: GENERATE_ME_32
```

## Deploy from CI

GitHub Actions:

```yaml
- name: Deploy
  env:
    HOSTIM_TOKEN: ${{ secrets.HOSTIM_TOKEN }}
    HOSTIM_PROJECT: my-project
  run: |
    curl -fsSL https://raw.githubusercontent.com/hostimdev/cli/main/install.sh | sh
    hostim deploy web \
      --docker-image ghcr.io/${{ github.repository }}:${{ github.sha }} \
      --plan sa-1-1 --port 8080 -o json
```

The step fails when the build fails, because `deploy` waits and exits non-zero.
There is also a ready-made GitHub Action at
<https://github.com/hostimdev/action>.

Any other CI works the same way: export `HOSTIM_TOKEN` and `HOSTIM_PROJECT`,
install the binary, run `hostim deploy` with `-o json`.

## Development

The API client (`api/client.gen.go`) is generated from the **live** public spec
at `https://api.hostim.dev/openapi.public.json` — the spec is not vendored:

```sh
make generate    # curl live spec → oapi-codegen → api/client.gen.go
make build
make test
make lint
```

CI (`.github/workflows/generate.yml`) re-runs `make generate` against the live
API and fails if `api/client.gen.go` has drifted, keeping the committed client
in sync with the deployed API.

This README is the single source for the CLI documentation: it is embedded in
the binary and printed by `hostim agent`. Edit it here, nowhere else.

## License

MIT. See [LICENSE](LICENSE).

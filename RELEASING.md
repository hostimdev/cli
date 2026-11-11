# Releasing the CLI

Maintainer notes. Not in `README.md`, because the README is embedded in the binary and
printed by `hostim agent`.

## Release channels

`.github/workflows/release.yml` runs on every push to `main`. Besides the GitHub release,
the MCPB bundle and the MCP registry entry, it publishes to two package channels.

### Homebrew

- GoReleaser `homebrew_casks` pushes a cask to `hostimdev/homebrew-tap`.
- It pushes over SSH with a deploy key, stored in the `HOMEBREW_TAP_KEY` secret on `hostimdev/cli`.
- Users install with `brew install hostimdev/tap/hostim`.
- Smoke-test the install on Linux with `docker run --rm -it homebrew/brew:latest`.

### npm

- Package `hostim`, owned by npm user `pv1337`.
- It is a Node shim in `npm/` that downloads the release binary on first run.
- Published with npm trusted publishing (GitHub OIDC). There is no npm token.

npm trusted-publisher configs created after 2026-09-03 allow only `stage publish` by
default. A plain `npm publish` then fails with `403 OIDC permission denied for this action`.
Fix it with (needs the owner's 2FA):

```
npm trust github hostim --repo hostimdev/cli --file release.yml --allow-publish
```

Check the config with `npm trust list hostim`.

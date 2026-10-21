#!/usr/bin/env node
// `npx hostim` entry point. Downloads the release binary that matches this
// package's version on first run, checks it against the release checksums,
// caches it and runs it. No postinstall script, so it also works with
// `npm install --ignore-scripts`.
"use strict";

const { spawnSync } = require("child_process");
const crypto = require("crypto");
const fs = require("fs");
const os = require("os");
const path = require("path");

const { version } = require("../package.json");
const REPO = "hostimdev/cli";

const goos = { linux: "linux", darwin: "darwin", win32: "windows" }[process.platform];
const goarch = { x64: "amd64", arm64: "arm64" }[process.arch];
if (!goos || !goarch) {
  console.error(`hostim: unsupported platform ${process.platform}/${process.arch}`);
  process.exit(1);
}

const exe = goos === "windows" ? "hostim.exe" : "hostim";
const archive = `hostim_${goos}_${goarch}.${goos === "windows" ? "zip" : "tar.gz"}`;
const cacheRoot =
  process.env.HOSTIM_NPM_CACHE ||
  (goos === "windows"
    ? path.join(process.env.LOCALAPPDATA || os.homedir(), "hostim", "npm")
    : path.join(process.env.XDG_CACHE_HOME || path.join(os.homedir(), ".cache"), "hostim", "npm"));
const dir = path.join(cacheRoot, version);
const bin = path.join(dir, exe);

async function download(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`GET ${url}: HTTP ${res.status}`);
  return Buffer.from(await res.arrayBuffer());
}

async function install() {
  const base = `https://github.com/${REPO}/releases/download/v${version}`;
  const [sums, data] = await Promise.all([
    download(`${base}/checksums.txt`).then(String),
    download(`${base}/${archive}`),
  ]);
  const line = sums.split("\n").find((l) => l.trim().endsWith(`  ${archive}`));
  const want = line && line.split(/\s+/)[0];
  const got = crypto.createHash("sha256").update(data).digest("hex");
  if (want !== got) throw new Error(`checksum mismatch for ${archive}`);

  // Extract into a temp dir next to the cache, then rename, so two concurrent
  // first runs never see a half-written binary.
  fs.mkdirSync(cacheRoot, { recursive: true });
  const tmp = fs.mkdtempSync(path.join(cacheRoot, `.${version}-`));
  try {
    fs.writeFileSync(path.join(tmp, archive), data);
    // System tar: GNU/bsdtar on Linux and macOS, bsdtar (reads zip too) on Windows 10+.
    const r = spawnSync("tar", ["-xf", archive, exe], { cwd: tmp, stdio: "inherit" });
    if (r.status !== 0) throw new Error(`tar failed extracting ${archive}`);
    fs.chmodSync(path.join(tmp, exe), 0o755);
    fs.rmSync(path.join(tmp, archive));
    try {
      fs.renameSync(tmp, dir);
    } catch (e) {
      if (!fs.existsSync(bin)) throw e; // lost the race to another run: fine
    }
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

(async () => {
  if (!fs.existsSync(bin)) {
    try {
      await install();
    } catch (e) {
      console.error(`hostim: could not install v${version}: ${e.message}`);
      process.exit(1);
    }
  }
  const r = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
  if (r.error) {
    console.error(`hostim: ${r.error.message}`);
    process.exit(1);
  }
  if (r.signal) process.kill(process.pid, r.signal);
  process.exit(r.status);
})();

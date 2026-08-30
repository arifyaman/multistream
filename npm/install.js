'use strict';

// postinstall: download the multistream binary for this platform from the
// GitHub release, verify its SHA-256, and place it next to the CLI shim.
// Then download the bundled runtime (ffmpeg + mediamtx) from the dedicated
// runtime release, so a fresh machine works with no system dependencies.

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const https = require('https');
const { URL } = require('url');
const { spawnSync } = require('child_process');

const pkg = require(path.join(__dirname, 'package.json'));
const key = `${process.platform}-${process.arch}`;
const spec = (pkg.binaries || {})[key];
const binName = process.platform === 'win32' ? 'multistream.exe' : 'multistream';
const binPath = path.join(__dirname, 'bin', binName);

function fetchBuffer(url) {
  return new Promise((resolve, reject) => {
    const req = https.get(new URL(url), (res) => {
      if ([301, 302, 307, 308].includes(res.statusCode) && res.headers.location) {
        res.resume();
        return fetchBuffer(new URL(res.headers.location, url).toString()).then(resolve, reject);
      }
      if (res.statusCode !== 200) {
        res.resume();
        return reject(new Error(`HTTP ${res.statusCode} for ${url}`));
      }
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => resolve(Buffer.concat(chunks)));
      res.on('error', reject);
    });
    req.on('error', reject);
    req.setTimeout(120000, () => req.destroy(new Error('download timed out')));
  });
}

// installRuntime downloads the bundled runtime tarball for this platform,
// verifies it, and extracts it to vendor/runtime (bin/ffmpeg, bin/mediamtx,
// licenses, VERSIONS). The CLI shim exposes vendor/runtime/bin to the binary
// via $MULTISTREAM_RUNTIME_DIR.
function installRuntime(spec) {
  const vendor = path.join(__dirname, 'vendor');
  const runtimeDir = path.join(vendor, 'runtime');
  const tarball = path.join(vendor, 'runtime-download.tar.gz');
  return (async () => {
    const data = await fetchBuffer(spec.url);
    const sha256 = crypto.createHash('sha256').update(data).digest('hex');
    if (sha256 !== spec.sha256) {
      throw new Error(`runtime sha256 mismatch: got ${sha256}, want ${spec.sha256}`);
    }
    fs.mkdirSync(vendor, { recursive: true });
    fs.writeFileSync(tarball, data);
    try {
      // Re-run on every install so a new runtime release is picked up.
      fs.rmSync(runtimeDir, { recursive: true, force: true });
      fs.mkdirSync(runtimeDir, { recursive: true });
      // The tarball has one top-level dir (<os>_<arch>/); strip it so the
      // layout is platform-independent.
      const r = spawnSync('tar', ['-xzf', tarball, '--strip-components=1', '-C', runtimeDir], { stdio: 'pipe' });
      if (r.status !== 0) {
        throw new Error(`extracting the runtime failed: ${r.stderr.toString().trim()}`);
      }
      for (const name of ['ffmpeg', 'mediamtx']) {
        const bin = path.join(runtimeDir, 'bin', name);
        if (process.platform !== 'win32' && fs.existsSync(bin)) {
          fs.chmodSync(bin, 0o755);
        }
      }
    } finally {
      fs.rmSync(tarball, { force: true });
    }
    console.log(`[multistream] installed bundled runtime (ffmpeg + mediamtx) to ${runtimeDir}`);
  })();
}

(async () => {
  if (!spec) {
    console.warn(`[multistream] no prebuilt binary for ${key}; install manually from the GitHub release`);
    return;
  }
  try {
    console.log(`[multistream] downloading v${pkg.version} (${key}) ...`);
    const data = await fetchBuffer(spec.url);
    const sha256 = crypto.createHash('sha256').update(data).digest('hex');
    if (sha256 !== spec.sha256) {
      throw new Error(`sha256 mismatch: got ${sha256}, want ${spec.sha256}`);
    }
    fs.mkdirSync(path.dirname(binPath), { recursive: true });
    fs.writeFileSync(binPath, data);
    fs.chmodSync(binPath, 0o755);
    console.log(`[multistream] installed ${binPath}`);

    const runtimeSpec = (pkg.runtime || {}).platforms && pkg.runtime.platforms[key];
    if (process.env.MULTISTREAM_SKIP_RUNTIME) {
      console.warn('[multistream] skipping bundled runtime (MULTISTREAM_SKIP_RUNTIME set); the daemon will use system ffmpeg/mediamtx from PATH');
    } else if (runtimeSpec) {
      await installRuntime(runtimeSpec);
    } else {
      console.warn(`[multistream] no bundled runtime for ${key}; the daemon will use system ffmpeg/mediamtx from PATH`);
    }
  } catch (err) {
    console.error(`[multistream] postinstall failed: ${err.message}`);
    process.exit(1);
  }
})();

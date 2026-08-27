// Build the npm package.json for a release from the SHA256SUMS file.
// Usage: node make-package.mjs <version> <path-to-SHA256SUMS>
// The resulting package.json is written to stdout.

import { readFileSync } from 'node:fs';

const [version, sumsPath] = process.argv.slice(2);
if (!version || !sumsPath) {
  console.error('usage: make-package.mjs <version> <SHA256SUMS>');
  process.exit(2);
}

const base = `https://github.com/xlip/multiStream/releases/download/v${version}`;

// "<os>_<arch>" (asset suffix) -> npm platform key
const map = {
  linux_amd64: 'linux-x64',
  linux_arm64: 'linux-arm64',
  darwin_amd64: 'darwin-x64',
  darwin_arm64: 'darwin-arm64',
  windows_amd64: 'win32-x64',
  windows_arm64: 'win32-arm64',
};

const binaries = {};
for (const line of readFileSync(sumsPath, 'utf8').trim().split('\n')) {
  const [sha256, name] = line.trim().split(/\s+/);
  for (const [suffix, key] of Object.entries(map)) {
    if (name === `multistream_${version}_${suffix}` || name === `multistream_${version}_${suffix}.exe`) {
      binaries[key] = { url: `${base}/${name}`, sha256 };
    }
  }
}

const pkg = {
  name: '@xlip/multistream',
  version,
  description: 'CLI status monitor for the multistream RTMP re-broadcast chain (OBS -> mediamtx -> platforms)',
  license: 'MIT',
  repository: 'github:xlip/multistream',
  os: ['linux', 'darwin', 'win32'],
  cpu: ['x64', 'arm64'],
  engines: { node: '>=18' },
  bin: { multistream: 'bin/multistream.js' },
  scripts: { postinstall: 'node install.js' },
  files: ['install.js', 'bin/multistream.js', 'README.md'],
  binaries,
};

console.log(JSON.stringify(pkg, null, 2));

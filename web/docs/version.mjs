import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

// Which build this is: the version in package.json, the commit it was built
// from, and when (UTC, to the second). The page shows it (`virtual:version`)
// and /version.json answers it, so what is live can be told without reading
// a bundle.
//
// The commit is BUILD_COMMIT when the deploy hands it over: the image is built
// on the server, from a copy with no .git. Otherwise it is git's, with a +
// when this folder has changes not yet committed. With neither, there is none.
// web/console has the same file.
export function readVersion(dir) {
  const { version } = JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8'))
  let commit = process.env.BUILD_COMMIT || null
  if (!commit) {
    const head = git(dir, 'rev-parse', '--short', 'HEAD')
    if (head) commit = head + (git(dir, 'status', '--porcelain', '--', '.') ? '+' : '')
  }
  return { version, commit, built: new Date().toISOString().replace(/\.\d+Z$/, 'Z') }
}

function git(dir, ...args) {
  try {
    return execFileSync('git', args, { cwd: dir, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim()
  } catch {
    return ''
  }
}

export function buildVersion(dir) {
  const id = 'virtual:version'
  const build = readVersion(dir)
  return {
    name: 'build-version',
    resolveId: (source) => (source === id ? '\0' + id : null),
    load: (source) => (source === '\0' + id ? `export default ${JSON.stringify(build)}` : null),
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: 'version.json', source: JSON.stringify(build, null, 2) + '\n' })
    },
  }
}

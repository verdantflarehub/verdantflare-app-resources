import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, dirname, basename, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'

test('synthetic release binds navigation, dashboard and original asset bytes', async t => {
  const root = await mkdtemp(join(tmpdir(), 'resources-release-unit-'))
  t.after(() => {
    assert.equal(dirname(resolve(root)), resolve(tmpdir()))
    assert.ok(basename(root).startsWith('resources-release-unit-'))
    return rm(root, { recursive: true, force: true })
  })
  const frontend = join(root, 'frontend'), dist = join(frontend, 'dist')
  await mkdir(dist, { recursive: true })
  await writeFile(join(root, 'VERSION'), '0.1.0\n')
  await writeFile(join(dist, 'index.html'), '<!doctype html><p>unit fixture</p>')
  const script = fileURLToPath(new URL('../scripts/package-dashboard.mjs', import.meta.url))
  const run = () => spawnSync(process.execPath, [script], { cwd: frontend, encoding: 'utf8' })
  assert.equal(run().status, 0)
  const index = await readFile(join(dist, 'asset-index.json'))
  const dashboard = JSON.parse(await readFile(join(dist, 'dashboard-declaration.json'), 'utf8'))
  const navigation = JSON.parse(await readFile(join(dist, 'navigation-declaration.json'), 'utf8'))
  const digest = 'sha256:' + createHash('sha256').update(index).digest('hex')
  assert.equal(dashboard.dashboard.artifactDigest, digest)
  assert.equal(navigation.artifactDigest, digest)
  assert.equal(navigation.appId, 'resources')
  assert.equal(dashboard.dashboard.minimumSdkVersion, '1.1.0')
  assert.deepEqual(Object.keys(JSON.parse(index).files), ['index.html'])
  assert.equal(run().status, 0)
  assert.deepEqual(await readFile(join(dist, 'asset-index.json')), index)
  await writeFile(join(dist, 'private.json'), '{}')
  assert.notEqual(run().status, 0)
})

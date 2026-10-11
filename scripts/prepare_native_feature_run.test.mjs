import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {execFileSync, spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

const script = fileURLToPath(new URL('./prepare_native_feature_run.mjs', import.meta.url));

function fixture(t, reject = false) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'tekroo-run-preparation-'));
  t.after(() => fs.rmSync(root, {recursive: true, force: true}));
  const repository = path.join(root, 'repository');
  const deployment = path.join(root, 'deployment');
  fs.mkdirSync(repository);
  fs.mkdirSync(path.join(deployment, 'config'), {recursive: true});
  for (const args of [['init', '-b', 'main'], ['config', 'user.email', 'fixture@local'], ['config', 'user.name', 'Fixture']]) execFileSync('git', args, {cwd: repository, stdio: 'ignore'});
  fs.writeFileSync(path.join(repository, 'README.md'), 'fixture\n');
  execFileSync('git', ['add', 'README.md'], {cwd: repository});
  execFileSync('git', ['commit', '-m', 'fixture'], {cwd: repository, stdio: 'ignore'});
  const baseline = execFileSync('git', ['rev-parse', 'HEAD'], {cwd: repository, encoding: 'utf8'}).trim();
  const candidate = path.join(root, 'candidate.json');
  const policy = path.join(root, 'policy.json');
  const cli = path.join(root, 'fixture-tekroo.mjs');
  const config = {execution_backend: 'native', profiles: [{native_settings: {response_mode: 'json_schema_actions'}}], workspaces: [{workspace_id: 'editor-1'}], mongo: {}, native: {}, worker: {}};
  fs.writeFileSync(candidate, JSON.stringify(config));
  fs.writeFileSync(policy, '{}');
  fs.writeFileSync(path.join(deployment, 'config', 'feature-request-r38.json'), JSON.stringify({description: 'unchanged fixture input'}));
  // This is a process-wiring double, not role/qualification evidence. The Go
  // integration tests separately exercise the real authenticated CLI.
  fs.writeFileSync(cli, `#!${process.execPath}
import fs from 'node:fs';
import path from 'node:path';
const args = process.argv.slice(2);
if (args[0] !== 'prepare-role-versions') throw new Error('wrong CLI command');
const flags = Object.fromEntries(Array.from({length: 4}, (_, i) => [args[1+i*2], args[2+i*2]]));
if (flags['--version-policy'] !== ${JSON.stringify(policy)} || flags['--run-id'] !== 'r999') throw new Error('missing policy/run identity');
if (fs.existsSync(${JSON.stringify(path.join(deployment, 'workspaces-r999'))})) throw new Error('worktrees started before resolution');
${reject ? "throw new Error('no qualified version matches');" : ''}
const config = JSON.parse(fs.readFileSync(flags['--config']));
const out = flags['--output']; fs.mkdirSync(out);
config.organization = {role_lock_file: path.join(out, 'role-lock.json'), role_lock_digest: 'a'.repeat(64), role_lock_run_id: flags['--run-id']};
const configFile = path.join(out, 'prepared-config.json');
fs.writeFileSync(configFile, JSON.stringify(config));
console.log(JSON.stringify({run_id: flags['--run-id'], config_file: configFile, lock_file: config.organization.role_lock_file, lock_digest: config.organization.role_lock_digest}));
`, {mode: 0o700});
  return {root, deployment, args: [script, candidate, repository, deployment, 'r999', baseline, policy, cli]};
}

test('native preparation consumes the frozen config before creating worktrees', t => {
  const f = fixture(t);
  const result = spawnSync(process.execPath, f.args, {encoding: 'utf8'});
  assert.equal(result.status, 0, result.stderr);
  const receipt = JSON.parse(result.stdout);
  assert.equal(receipt.activated, false);
  assert.equal(receipt.role_lock.run_id, 'r999');
  const config = JSON.parse(fs.readFileSync(receipt.config_path));
  assert.equal(config.organization.role_lock_run_id, 'r999');
  assert.equal(config.worker.start_paused, true);
  assert.equal(config.mongo.database, 'tekroo_teams_v4_phase10_prod_r999');
  assert.equal(fs.existsSync(config.workspaces[0].working_directory), true);
  const again = spawnSync(process.execPath, f.args, {encoding: 'utf8'});
  assert.notEqual(again.status, 0);
  assert.match(again.stderr, /Target already exists/);
});

test('resolution failure creates no worktrees, run config, feature or activation', t => {
  const f = fixture(t, true);
  const result = spawnSync(process.execPath, f.args, {encoding: 'utf8'});
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /no qualified version matches/);
  for (const relative of ['workspaces-r999', 'evidence-r999', 'config/tekrood.r999.native.json', 'config/feature-request-r999.json']) assert.equal(fs.existsSync(path.join(f.deployment, relative)), false);
});

test('native preparation cannot silently omit the central policy', t => {
  const f = fixture(t);
  const result = spawnSync(process.execPath, f.args.slice(0, 6), {encoding: 'utf8'});
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /central version policy/);
  assert.equal(fs.existsSync(path.join(f.deployment, 'workspaces-r999')), false);
});

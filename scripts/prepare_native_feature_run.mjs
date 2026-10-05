// Prepare, but do not activate, a new native run. Historical databases,
// workspaces, and the active configuration are never overwritten.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { execFileSync } from 'node:child_process';

const [candidatePath, repository, deployment, run, baseline] = process.argv.slice(2);
if (!candidatePath || !path.isAbsolute(repository) || !path.isAbsolute(deployment) || !/^r[0-9]+$/.test(run) || !/^[0-9a-f]{40}$/.test(baseline)) throw new Error('Expected candidate config, repository, deployment, rNUMBER, baseline SHA');
const config = JSON.parse(fs.readFileSync(candidatePath, 'utf8'));
if (config.execution_backend !== 'native' || config.profiles.some(p => !p.native_settings || p.native_settings.response_mode !== 'json_schema_actions')) throw new Error('Candidate is not the qualified schema-action configuration');
if (config.profiles.some(p => p.role_fqrn !== 'operator' && (!p.qualification || p.qualification.status !== 'PASS'))) throw new Error('Unqualified automatic role');
const git = (...args) => execFileSync('git', args, { cwd: repository, encoding: 'utf8', timeout: 120000 }).trim();
if (git('rev-parse', 'HEAD') !== baseline || git('rev-parse', 'main') !== baseline) throw new Error('Repository/main does not match the proposed baseline');
if (git('diff', '--name-only') || git('diff', '--cached', '--name-only')) throw new Error('Tracked source is dirty');
const workspaceRoot = path.join(deployment, `workspaces-${run}`);
const evidenceRoot = path.join(deployment, `evidence-${run}`);
const configPath = path.join(deployment, 'config', `tekrood.${run}.native.json`);
const featurePath = path.join(deployment, 'config', `feature-request-${run}.json`);
for (const target of [workspaceRoot, evidenceRoot, configPath, featurePath]) if (fs.existsSync(target)) throw new Error(`Target already exists: ${target}`);
const priorRequest = JSON.parse(fs.readFileSync(path.join(deployment, 'config', 'feature-request-r38.json'), 'utf8'));
fs.mkdirSync(workspaceRoot, { mode: 0o700 });
for (const workspace of config.workspaces) {
  workspace.working_directory = path.join(workspaceRoot, workspace.workspace_id);
  workspace.branch = `tekroo-phase10-prod-${run}/${workspace.workspace_id}`;
  workspace.baseline_sha = baseline;
  git('worktree', 'add', '-b', workspace.branch, workspace.working_directory, baseline);
  const head = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: workspace.working_directory, encoding: 'utf8' }).trim();
  if (head !== baseline) throw new Error(`Workspace baseline mismatch: ${workspace.workspace_id}`);
}
fs.mkdirSync(evidenceRoot, { mode: 0o700 });
config.mongo.database = `tekroo_teams_v4_phase10_prod_${run}`;
config.teams_database_identity = config.mongo.database;
config.evidence_root = evidenceRoot;
config.deployment_identity_digest = crypto.createHash('sha256').update(`native:${run}:${baseline}`).digest('hex');
config.native.owner = `tekrood-production-native-${run}`;
config.worker.start_paused = true;
const bytes = JSON.stringify(config, null, 2) + '\n';
fs.writeFileSync(configPath, bytes, { flag: 'wx', mode: 0o600 });
priorRequest.idempotency_key = `phase10-prod-agent-alias-${run}`;
fs.writeFileSync(featurePath, JSON.stringify(priorRequest, null, 2) + '\n', { flag: 'wx', mode: 0o600 });
const receipt = { run, baseline, execution_backend: config.execution_backend, database: config.mongo.database, config_path: configPath, config_sha256: crypto.createHash('sha256').update(bytes).digest('hex'), feature_request: featurePath, workspace_count: config.workspaces.length, evidence_root: evidenceRoot, activated: false };
fs.writeFileSync(path.join(deployment, 'config', `preparation-${run}.json`), JSON.stringify(receipt, null, 2)+'\n', { flag:'wx', mode:0o600 });
console.log(JSON.stringify(receipt, null, 2));

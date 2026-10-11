// Qualify only the verification scheduling patch in the current host environment.
// No Teams dispatch, model requests, deployment restart, or feature acceptance.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import cp from 'node:child_process';
import { fileURLToPath } from 'node:url';

const ownedFiles = [
  'agentruntime/runtime.go', 'agentruntime/parallel_reads.go', 'agentruntime/parallel_reads_test.go', 'agentruntime/parallel_effects_test.go',
  'agenttools/turn_adapter.go', 'agenttools/isolated_go_tests.go', 'agenttools/verification_parallel_test.go',
  'internal/verification/queue.go', 'internal/verification/selection.go', 'internal/verification/selection_test.go',
  'adapters/nativeagent/session.go', 'adapters/nativeagent/session_test.go',
  'adapters/operationalruntime/candidate_workspace.go', 'cmd/tekroo/local_init.go', 'cmd/tekroo/launch_agent_test.go',
];
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const run = (cmd, args, cwd) => cp.execFileSync(cmd, args, { cwd, encoding: 'utf8', maxBuffer: 32 << 20 });
const xml = value => value.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
const script = fileURLToPath(import.meta.url);

if (process.argv[2] === '--collect') {
  const directory = process.argv[3], destination = process.argv[4];
  if (!directory || !destination) throw new Error('Usage: --collect qualification-directory destination');
  const receipt = JSON.parse(fs.readFileSync(path.join(directory, 'receipt.json')));
  fs.mkdirSync(destination, { recursive: false });
  for (const file of ['regression.jsonl', 'regression.stderr', 'source.json', 'started.json', 'qualification.plist', 'launchctl-start.txt', 'service.stdout', 'service.stderr']) {
    fs.copyFileSync(path.join(directory, file), path.join(destination, file), fs.constants.COPYFILE_EXCL);
  }
  receipt.original_raw_path = receipt.raw_path;
  receipt.raw_path = path.join(destination, 'regression.jsonl');
  receipt.stderr_path = path.join(destination, 'regression.stderr');
  if (hash(fs.readFileSync(receipt.raw_path)) !== receipt.raw_sha256) throw new Error('Retained output digest mismatch');
  fs.writeFileSync(path.join(destination, 'receipt.json'), JSON.stringify(receipt, null, 2), { flag: 'wx' });
  console.log(JSON.stringify({ label: 'OBSERVED', retained: destination, exit_code: receipt.exit_code, wall_seconds: receipt.wall_seconds, passed: receipt.passed.length, skipped: receipt.skipped.length, failed: receipt.failed.length }));
} else if (process.argv[2] === '--worker') {
  const directory = process.argv[3];
  const manifest = JSON.parse(fs.readFileSync(path.join(directory, 'source.json')));
  const source = path.join(directory, 'source');
  const argv = ['test', '-json', '-count=1', '-p=2', '-parallel=2', './...'];
  const started = new Date();
  const rawPath = path.join(directory, 'regression.jsonl');
  const stderrPath = path.join(directory, 'regression.stderr');
  const raw = fs.createWriteStream(rawPath, { flags: 'wx' });
  const stderr = fs.createWriteStream(stderrPath, { flags: 'wx' });
  const child = cp.spawn('/opt/homebrew/bin/go', argv, {
    cwd: source, detached: true,
    env: { ...process.env, PATH: '/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin', CI: '1', GOMAXPROCS: '2', GIT_TERMINAL_PROMPT: '0', GIT_PAGER: 'cat', PAGER: 'cat' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.stdout.pipe(raw); child.stderr.pipe(stderr);
  fs.writeFileSync(path.join(directory, 'started.json'), JSON.stringify({ started_at: started, pid: child.pid, source, command: ['/opt/homebrew/bin/go', ...argv] }, null, 2));
  const timeout = setTimeout(() => process.kill(-child.pid, 'SIGKILL'), 600000);
  const finished = new Promise((resolve, reject) => { child.on('error', reject); child.on('close', (code, signal) => resolve({ code, signal })); });
  const result = await finished;
  clearTimeout(timeout);
  for (const stream of [raw, stderr]) {
    if (!stream.writableFinished) await new Promise(resolve => stream.on('finish', resolve));
  }
  const ended = new Date();
  const events = fs.readFileSync(rawPath, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
  const passed = events.filter(e => e.Action === 'pass' && e.Test).map(e => ({ package: e.Package, test: e.Test, elapsed: e.Elapsed }));
  const skipped = events.filter(e => e.Action === 'skip' && e.Test).map(e => ({ package: e.Package, test: e.Test }));
  const failed = events.filter(e => e.Action === 'fail');
  const packages = events.filter(e => e.Action === 'pass' && !e.Test).map(e => ({ package: e.Package, elapsed: e.Elapsed }));
  const dirtyAfter = run('/usr/bin/git', ['status', '--porcelain'], source).trim();
  const receipt = {
    label: 'OBSERVED', diagnostic_not_team_attempt: true, source_manifest: manifest,
    scheduling: 'launchd ProcessType Interactive; Go -p=2 -parallel=2 GOMAXPROCS=2',
    started_at: started, finished_at: ended, wall_seconds: (ended - started) / 1000,
    exit_code: result.code, signal: result.signal, raw_path: rawPath,
    raw_sha256: hash(fs.readFileSync(rawPath)), stderr_path: stderrPath,
    passed, skipped, failed, packages, dirty_after: dirtyAfter,
  };
  fs.writeFileSync(path.join(directory, 'receipt.json'), JSON.stringify(receipt, null, 2));
  process.exitCode = result.code === 0 && failed.length === 0 && dirtyAfter === '' ? 0 : 1;
} else {
  const repository = process.argv[2];
  if (!repository || process.platform !== 'darwin') throw new Error('Usage: node qualify_verification_service.mjs /absolute/repository (macOS)');
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'tekroo-verification-service-'));
  const source = path.join(directory, 'source'); fs.mkdirSync(source);
  const baseline = run('/usr/bin/git', ['rev-parse', 'HEAD'], repository).trim();
  const archive = cp.execFileSync('/usr/bin/git', ['archive', baseline], { cwd: repository, maxBuffer: 64 << 20 });
  cp.execFileSync('/usr/bin/tar', ['-x', '-C', source], { input: archive });
  const files = ownedFiles.map(file => {
    const data = fs.readFileSync(path.join(repository, file));
    fs.mkdirSync(path.dirname(path.join(source, file)), { recursive: true });
    fs.copyFileSync(path.join(repository, file), path.join(source, file));
    return { file, sha256: hash(data) };
  });
  for (const args of [['init', '-q'], ['add', '--', '.'], ['-c', 'user.name=Verification Fixture', '-c', 'user.email=verification@example.invalid', 'commit', '-qm', 'exact qualification sources']]) run('/usr/bin/git', args, source);
  const manifest = { baseline, files, qualification_script_sha256: hash(fs.readFileSync(script)), excluded_workspace_changes: run('/usr/bin/git', ['status', '--porcelain'], repository).trim().split('\n').filter(line => !ownedFiles.some(file => line.slice(3) === file)) };
  fs.writeFileSync(path.join(directory, 'source.json'), JSON.stringify(manifest, null, 2));
  const label = 'com.tekroo.verification-qualification-' + crypto.randomUUID();
  const plist = path.join(directory, 'qualification.plist');
  const content = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>${label}</string>
<key>ProgramArguments</key><array><string>${xml(process.execPath)}</string><string>${xml(script)}</string><string>--worker</string><string>${xml(directory)}</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><false/>
<key>ProcessType</key><string>Interactive</string>
<key>StandardOutPath</key><string>${xml(path.join(directory, 'service.stdout'))}</string>
<key>StandardErrorPath</key><string>${xml(path.join(directory, 'service.stderr'))}</string>
</dict></plist>`;
  fs.writeFileSync(plist, content);
  run('/bin/launchctl', ['bootstrap', `gui/${process.getuid()}`, plist]);
  const serviceTarget = `gui/${process.getuid()}/${label}`;
  fs.writeFileSync(path.join(directory, 'launchctl-start.txt'), run('/bin/launchctl', ['print', serviceTarget]));
  console.log(JSON.stringify({ label: 'OBSERVED', directory, service_target: serviceTarget, receipt: path.join(directory, 'receipt.json'), source_manifest: path.join(directory, 'source.json') }));
}

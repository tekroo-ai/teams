import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const sha256 = (bytes) => crypto.createHash("sha256").update(bytes).digest("hex");
const read = (relative) => fs.readFileSync(path.join(root, relative));
const parse = (relative) => JSON.parse(read(relative));
const fail = (message) => { throw new Error(message); };

const manifestBytes = read("manifest.json");
const detached = read("manifest.sha256").toString("utf8").trim().split(/\s+/)[0];
if (sha256(manifestBytes) !== detached) fail("detached manifest digest mismatch");

const manifest = JSON.parse(manifestBytes);
if (manifest.contract?.identity !== "tekroo.event-export.contracts/0.1.0") fail("contract identity mismatch");
if (manifest.contract?.status !== "CANDIDATE_NOT_ACCEPTED") fail("unexpected contract status");
if (manifest.source?.identity !== "tekroo.kernel.contracts/0.7.0") fail("source identity mismatch");
if (manifest.source?.manifestSHA256 !== "e2b9b5224a860a3eaa07451cf48fb5ac16a440b22b8dd592ff1662c1cff67f16") fail("source manifest mismatch");

const inventory = new Set();
for (const file of manifest.files ?? []) {
  if (inventory.has(file.path)) fail(`duplicate inventory path: ${file.path}`);
  inventory.add(file.path);
  const bytes = read(file.path);
  if (bytes.length !== file.bytes) fail(`byte length mismatch: ${file.path}`);
  if (sha256(bytes) !== file.sha256) fail(`file digest mismatch: ${file.path}`);
}
for (const required of [
  "compatibility/teams-kernel-0.7.0.json",
  "fixtures/conformance.json",
  "invariants/invariants.json",
  "runner/protocol.json",
  "runner/validate-package.mjs",
  "schemas/event-export.schema.json"
]) if (!inventory.has(required)) fail(`missing inventory path: ${required}`);

const invariants = parse("invariants/invariants.json");
const fixtures = parse("fixtures/conformance.json");
const protocol = parse("runner/protocol.json");
const compatibility = parse("compatibility/teams-kernel-0.7.0.json");
const schema = parse("schemas/event-export.schema.json");
if (invariants.invariants?.length !== 12) fail("invariant count mismatch");
if (fixtures.fixtures?.length !== 10) fail("fixture count mismatch");
if (protocol.delivery !== "AT_LEAST_ONCE" || protocol.historyLoss !== "RESYNC_REQUIRED") fail("protocol recovery semantics mismatch");
if (protocol.initialConvergence !== "OPEN_LIVE_BEFORE_BOUNDED_BACKLOG") fail("initial convergence mismatch");
if (compatibility.organizationalSemanticsChanged !== false || compatibility.outboxSemanticsChanged !== false) fail("compatibility mutation mismatch");
if (schema.$defs?.response?.properties?.status?.enum?.includes("RESYNC_REQUIRED") !== true) fail("schema lacks resync status");

process.stdout.write(JSON.stringify({
  status: "PASS",
  contractIdentity: manifest.contract.identity,
  manifestSHA256: detached,
  fileCount: manifest.files.length,
  invariantCount: invariants.invariants.length,
  fixtureCount: fixtures.fixtures.length
}) + "\n");

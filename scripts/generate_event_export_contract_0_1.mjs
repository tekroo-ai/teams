import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const root = path.join(repo, "CONTRACTS/tekroo.event-export.contracts/0.1.0");
const manifestPath = path.join(root, "manifest.json");
const detachedPath = path.join(root, "manifest.sha256");
if (fs.existsSync(manifestPath) || fs.existsSync(detachedPath)) throw new Error("event-export contract is already sealed");

const relativeFiles = [
  "compatibility/teams-kernel-0.7.0.json",
  "fixtures/conformance.json",
  "invariants/invariants.json",
  "runner/protocol.json",
  "runner/validate-package.mjs",
  "schemas/event-export.schema.json"
];
const sha256 = (bytes) => crypto.createHash("sha256").update(bytes).digest("hex");
const files = relativeFiles.map((relative) => {
  const bytes = fs.readFileSync(path.join(root, relative));
  return {path: relative, bytes: bytes.length, sha256: sha256(bytes)};
});
const manifest = {
  schemaVersion: "1.0.0",
  contract: {identity: "tekroo.event-export.contracts/0.1.0", name: "tekroo.event-export.contracts", version: "0.1.0", status: "CANDIDATE_NOT_ACCEPTED"},
  source: {identity: "tekroo.kernel.contracts/0.7.0", manifestSHA256: "e2b9b5224a860a3eaa07451cf48fb5ac16a440b22b8dd592ff1662c1cff67f16"},
  authority: {decisionAuthority: "Principal", implementationAuthority: "AUTHORIZED_CANDIDATE", productionAuthority: "NONE"},
  semantics: {access: "AUTHENTICATED_AUTHORIZED_READ_ONLY", delivery: "AT_LEAST_ONCE", deduplicationIdentity: "event_id", cursorRole: "OPERATIONAL_ONLY", initialConvergence: "OPEN_LIVE_BEFORE_BOUNDED_BACKLOG", historyLoss: "RESYNC_REQUIRED"},
  counts: {files: files.length, invariants: 12, fixtures: 10, schemas: 1},
  files,
  canonicalization: {textEncoding: "UTF-8", byteOrderMark: false, lineEnding: "LF", byteDigest: "SHA-256", manifestDigest: "detached manifest.sha256"},
  inventoryBoundary: "files lists every normative payload asset; manifest.json is identified by detached manifest.sha256 and manifest.sha256 is not self-listed."
};
const bytes = Buffer.from(JSON.stringify(manifest, null, 2) + "\n");
fs.writeFileSync(manifestPath, bytes, {flag: "wx"});
const digest = sha256(bytes);
fs.writeFileSync(detachedPath, `${digest}  manifest.json\n`, {flag: "wx"});
process.stdout.write(`SEALED tekroo.event-export.contracts/0.1.0 ${digest} ${files.length}\n`);

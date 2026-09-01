#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.9.0");
const target = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.10.0");
const oldIdentity = "tekroo.kernel.contracts/0.9.0";
const newIdentity = "tekroo.kernel.contracts/0.10.0";

const sha256 = (value) => crypto.createHash("sha256").update(value).digest("hex");
const sortValue = (value) => Array.isArray(value) ? value.map(sortValue) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortValue(value[key])])) : value;
const canonical = (value) => JSON.stringify(sortValue(value));

function writeJson(relative, value) {
  const absolute = path.join(target, relative);
  fs.mkdirSync(path.dirname(absolute), { recursive: true });
  fs.writeFileSync(absolute, `${JSON.stringify(sortValue(value), null, 2)}\n`);
}

function walk(directory, prefix = "") {
  const values = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) values.push(...walk(absolute, relative));
    else values.push(relative);
  }
  return values.sort();
}

const uuid = { type: "string", pattern: "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$" };
const digest = { type: "string", pattern: "^[0-9a-f]{64}$" };
const actor = { type: "string", pattern: "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])-[1-9][0-9]{0,19}$" };
const text = (maximum = 4096) => ({ type: "string", minLength: 1, maxLength: maximum });

const federationSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `${newIdentity}/schemas/federation.schema.json`,
  title: "Tekroo Teams exact federation contract",
  $defs: {
    aliasBinding: {
      type: "object", additionalProperties: false,
      required: ["schema_version", "name", "revision", "route_id", "route_revision", "deployment_identity", "actor_fqn"],
      properties: { schema_version: { const: "1.0.0" }, name: { type: "string", pattern: "^[a-z0-9](?:[a-z0-9.-]{0,62}[a-z0-9])?$" }, revision: { type: "integer", minimum: 1 }, route_id: uuid, route_revision: { type: "integer", minimum: 1 }, deployment_identity: digest, actor_fqn: actor },
    },
    trustGrant: {
      type: "object", additionalProperties: false,
      required: ["schema_version", "peer_id", "deployment_identity", "key_id", "key_epoch", "public_key", "not_before", "not_after", "status"],
      properties: { schema_version: { const: "1.0.0" }, peer_id: text(256), deployment_identity: digest, key_id: text(256), key_epoch: { type: "integer", minimum: 1 }, public_key: text(4096), not_before: { type: "string", format: "date-time" }, not_after: { type: "string", format: "date-time" }, status: { enum: ["ACTIVE", "REVOKED"] } },
    },
    route: {
      type: "object", additionalProperties: false,
      required: ["schema_version", "route_id", "revision", "source_deployment", "destination_deployment", "source_actor", "destination_actor", "message_types", "purposes", "key_id", "endpoint", "status"],
      properties: { schema_version: { const: "1.0.0" }, route_id: uuid, revision: { type: "integer", minimum: 1 }, source_deployment: digest, destination_deployment: digest, source_actor: actor, destination_actor: actor, message_types: { type: "array", minItems: 1, maxItems: 64, uniqueItems: true, items: text(256) }, purposes: { type: "array", minItems: 1, maxItems: 5, uniqueItems: true, items: { enum: ["REQUEST", "HANDOFF", "EVIDENCE", "RESPONSE", "NOTIFICATION"] } }, key_id: text(256), endpoint: { type: "string", format: "uri", maxLength: 4096 }, status: { enum: ["ACTIVE", "DISABLED"] }, allow_insecure_loopback_for_test: { type: "boolean", default: false } },
    },
    signedEnvelope: {
      type: "object", additionalProperties: false,
      required: ["schema_version", "delivery_id", "replay_id", "route_id", "route_revision", "source_deployment", "destination_deployment", "source_actor", "destination_actor", "key_id", "key_epoch", "issued_at", "expires_at", "message_sha256", "message", "route_trace", "signature"],
      properties: { schema_version: { const: "1.0.0" }, delivery_id: uuid, replay_id: uuid, route_id: uuid, route_revision: { type: "integer", minimum: 1 }, source_deployment: digest, destination_deployment: digest, source_actor: actor, destination_actor: actor, key_id: text(256), key_epoch: { type: "integer", minimum: 1 }, issued_at: { type: "string", format: "date-time" }, expires_at: { type: "string", format: "date-time" }, alias_name: { type: "string", maxLength: 64 }, alias_revision: { type: "integer", minimum: 1 }, message_sha256: digest, message: { type: "object" }, route_trace: { type: "array", minItems: 1, maxItems: 16, uniqueItems: true, items: digest }, signature: text(4096) },
    },
    deliveryReceipt: {
      type: "object", additionalProperties: false,
      required: ["schema_version", "delivery_id", "replay_id", "route_id", "route_revision", "message_id", "message_sha256", "accepted_at", "outcome"],
      properties: { schema_version: { const: "1.0.0" }, delivery_id: uuid, replay_id: uuid, route_id: uuid, route_revision: { type: "integer", minimum: 1 }, message_id: uuid, message_sha256: digest, accepted_at: { type: "string", format: "date-time" }, outcome: { const: "ACCEPTED" } },
    },
  },
};

const fixtures = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  fixtures: [
    ["P7-FED-001", "ALIAS", "NORMATIVE_EXAMPLE", "an alias resolves to one exact actor, deployment, route, and revision before signing"],
    ["P7-FED-002", "ALIAS", "BOUNDARY_NEGATIVE", "wildcard, ambiguous, stale, or remotely supplied alias routing is rejected"],
    ["P7-FED-003", "SIGNED_INGRESS", "NORMATIVE_EXAMPLE", "an exact authorized Ed25519 envelope appends one organizational message"],
    ["P7-FED-004", "SIGNED_INGRESS", "BOUNDARY_NEGATIVE", "unknown, revoked, premature, expired, or signature-invalid keys fail before append"],
    ["P7-FED-005", "ROUTE_AUTHORITY", "BOUNDARY_NEGATIVE", "signature validity cannot widen exact source, destination, type, or purpose authority"],
    ["P7-FED-006", "REPLAY", "NORMATIVE_EXAMPLE", "duplicate delivery returns the retained receipt and never appends a second message"],
    ["P7-FED-007", "REPLAY", "BOUNDARY_NEGATIVE", "replay ID reuse with different bytes fails closed"],
    ["P7-FED-008", "CLOCK", "BOUNDARY_NEGATIVE", "future, stale, or expired envelopes fail closed"],
    ["P7-FED-009", "DIGEST", "BOUNDARY_NEGATIVE", "payload mutation or nonmatching message digest fails closed"],
    ["P7-FED-010", "ROUTE_TRACE", "BOUNDARY_NEGATIVE", "a deployment may occur only once in a finite route trace"],
    ["P7-FED-011", "TRANSPORT", "BOUNDARY_NEGATIVE", "production routes require HTTPS and cannot expose operator or MCP tools"],
    ["P7-FED-012", "AUTHORITY", "BOUNDARY_NEGATIVE", "federated receipt cannot launch a role, invoke a model, reset a budget, or mutate SMA"],
    ["P7-FED-013", "LINEAGE", "NORMATIVE_EXAMPLE", "DAG, budget, lifecycle, scope, causation, and progress lineage survive transport unchanged"],
    ["P7-FED-014", "OUTBOUND", "NORMATIVE_EXAMPLE", "uncertain outbound delivery reconciles by exact delivery identity before retry"],
    ["P7-FED-015", "OPERATOR", "NORMATIVE_EXAMPLE", "operator HTTP, MCP, and CLI inspect and send without direct database access"],
  ].map(([fixtureId, kind, classification, assertion]) => ({ fixtureId, kind, classification, assertion })),
};

const invariants = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  invariants: [
    { invariantId: "P7-INV-001", statement: "Aliases are revisioned convenience names and never durable actor identities.", testIds: ["P7-FED-001", "P7-FED-002"] },
    { invariantId: "P7-INV-002", statement: "Signature verification and exact route authorization are both required before append.", testIds: ["P7-FED-003", "P7-FED-004", "P7-FED-005"] },
    { invariantId: "P7-INV-003", statement: "Replay identity and content digest make inbound acceptance exactly-once and conflict-detecting.", testIds: ["P7-FED-006", "P7-FED-007", "P7-FED-009"] },
    { invariantId: "P7-INV-004", statement: "Federation time windows and route traces are finite and fail closed.", testIds: ["P7-FED-008", "P7-FED-010"] },
    { invariantId: "P7-INV-005", statement: "Production federation uses a dedicated HTTPS transport surface.", testIds: ["P7-FED-011"] },
    { invariantId: "P7-INV-006", statement: "Federated messages carry information but no local execution or memory authority.", testIds: ["P7-FED-012"] },
    { invariantId: "P7-INV-007", statement: "Organizational DAG and budget lineage is invariant across federation.", testIds: ["P7-FED-013"] },
    { invariantId: "P7-INV-008", statement: "Outbound uncertainty is reconciled by exact delivery identity through the supported operator path.", testIds: ["P7-FED-014", "P7-FED-015"] },
  ],
};
const traceability = { schemaVersion: "1.0.0", contractIdentity: newIdentity, requirements: invariants.invariants.map((item) => ({ requirementId: item.invariantId.replace("INV", "REQ"), kind: "DECISION", statement: item.statement, testIds: item.testIds })) };

fs.rmSync(target, { recursive: true, force: true });
fs.cpSync(source, target, { recursive: true });
for (const relative of walk(target)) {
  if (["manifest.json", "manifest.sha256"].includes(relative)) continue;
  const absolute = path.join(target, relative);
  const raw = fs.readFileSync(absolute, "utf8");
  fs.writeFileSync(absolute, raw.split(oldIdentity).join(newIdentity).split('version === "0.9.0"').join('version === "0.10.0"'));
}
fs.rmSync(path.join(target, "manifest.json"), { force: true });
fs.rmSync(path.join(target, "manifest.sha256"), { force: true });
writeJson("schemas/federation.schema.json", federationSchema);
writeJson("fixtures/federation.json", fixtures);
writeJson("invariants/federation-invariants.json", invariants);
writeJson("traceability/federation-traceability.json", traceability);
writeJson("compatibility/from-0.9.0.json", {
  schemaVersion: "1.0.0", contractName: "tekroo.kernel.contracts", contractVersion: "0.10.0",
  predecessor: { contractName: "tekroo.kernel.contracts", contractVersion: "0.9.0", manifestSha256: sha256(fs.readFileSync(path.join(source, "manifest.json"))) },
  compatibility: "ADDITIVE", directions: { reader: "COMPATIBLE", writer: "SUCCESSOR_IDENTITY_REQUIRED", adapter: "NOT_REQUIRED" },
  additions: ["exact revisioned aliases", "Ed25519 peer trust grants", "exact signed cross-team routes", "replay-safe ingress receipts", "dedicated federation transport"],
  preserved: ["all 0.9.0 organizational runtime, DAG, budget, execution, evidence, human, operator, and SMA boundaries"],
});

const validatorPath = path.join(target, "runner/validate-package.mjs");
let validator = fs.readFileSync(validatorPath, "utf8");
validator = validator.replace('manifest.contract.version === "0.9.0"', 'manifest.contract.version === "0.10.0"');
validator = validator.replace('const compatibility = readJson("compatibility/from-0.8.0.json");', 'const compatibility = readJson("compatibility/from-0.9.0.json");');
validator = validator.replace('compatibility.predecessor?.contractVersion === "0.8.0" && compatibility.contractVersion === "0.9.0"', 'compatibility.predecessor?.contractVersion === "0.9.0" && compatibility.contractVersion === "0.10.0"');
validator = validator.replace('check("phase6-organization-schema"', 'check("phase7-federation-schema", listedPayloadFiles.includes("schemas/federation.schema.json"));\nconst federationFixtures = readJson("fixtures/federation.json");\nconst federationInvariants = readJson("invariants/federation-invariants.json");\nconst federationTraceability = readJson("traceability/federation-traceability.json");\nconst federationTestIds = new Set([...federationFixtures.fixtures.map((item) => item.fixtureId), ...federationInvariants.invariants.map((item) => item.invariantId)]);\ncheck("phase7-federation-fixture-cardinality", federationFixtures.fixtures.length === 15);\ncheck("phase7-federation-invariant-cardinality", federationInvariants.invariants.length === 8);\ncheck("phase7-federation-traceability", federationTraceability.requirements.every((item) => item.testIds.length > 0 && item.testIds.every((id) => federationTestIds.has(id))));\ncheck("phase6-organization-schema"');
fs.writeFileSync(validatorPath, validator);

const sourceManifest = JSON.parse(fs.readFileSync(path.join(source, "manifest.json"), "utf8"));
const sourceByPath = new Map(sourceManifest.files.map((entry) => [entry.path, entry]));
const specialRoles = new Map([
  ["schemas/federation.schema.json", "federation-schemas"], ["fixtures/federation.json", "federation-fixtures"],
  ["invariants/federation-invariants.json", "federation-invariants"], ["traceability/federation-traceability.json", "federation-traceability"],
  ["compatibility/from-0.9.0.json", "compatibility"],
]);
const paths = walk(target).filter((value) => !["manifest.json", "manifest.sha256"].includes(value));
const files = paths.map((relative) => {
  const bytes = fs.readFileSync(path.join(target, relative));
  const json = relative.endsWith(".json") ? JSON.parse(bytes.toString("utf8")) : null;
  const sourceEntry = sourceByPath.get(relative);
  return { path: relative, role: specialRoles.get(relative) ?? sourceEntry?.role ?? "contract-asset", mediaType: json ? "application/json" : sourceEntry?.mediaType ?? "text/javascript", bytes: bytes.length, sha256: sha256(bytes), canonicalJsonSha256: json ? sha256(canonical(json)) : null, dependencies: sourceEntry?.dependencies ?? [] };
});
const manifest = {
  ...sourceManifest,
  contract: { name: "tekroo.kernel.contracts", version: "0.10.0", status: "FROZEN_CANDIDATE" },
  generatedAt: "2026-09-01T00:00:00Z",
  files,
  counts: { ...sourceManifest.counts, schemas: sourceManifest.counts.schemas + 1, fixtures: sourceManifest.counts.fixtures + fixtures.fixtures.length, invariants: sourceManifest.counts.invariants + invariants.invariants.length, traceabilityRequirements: sourceManifest.counts.traceabilityRequirements + traceability.requirements.length },
  knownLimitations: ["Contract structure does not by itself qualify Go implementation or deployment.", "Public-network deployment, cloud trust broker, billing, wildcard routing, and v3 data migration are not authorized."],
  sourceLineage: [
    { path: "CONTRACTS/tekroo.kernel.contracts/0.9.0/manifest.json", sha256: sha256(fs.readFileSync(path.join(source, "manifest.json"))) },
    { path: "docs/architecture/122-phase-7-federated-exact-routing-plan.md", sha256: sha256(fs.readFileSync(path.join(root, "docs/architecture/122-phase-7-federated-exact-routing-plan.md"))) },
    { path: "OUTPUT/phase-6/step-1/feature-preservation-ledger.json", sha256: sha256(fs.readFileSync(path.join(root, "OUTPUT/phase-6/step-1/feature-preservation-ledger.json"))) },
    { path: "scripts/generate_phase7_contract_0_10.mjs", sha256: sha256(fs.readFileSync(fileURLToPath(import.meta.url))) },
  ],
};
writeJson("manifest.json", manifest);
const manifestBytes = fs.readFileSync(path.join(target, "manifest.json"));
fs.writeFileSync(path.join(target, "manifest.sha256"), `${sha256(manifestBytes)}  manifest.json\n`);
process.stdout.write(`${newIdentity} ${sha256(manifestBytes)} ${files.length}\n`);

#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const reportPath = process.argv[2];
if (!reportPath) throw new Error("usage: node validate-package.mjs <report-path>");

function sha256(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
}

function sortValue(value) {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortValue(value[key])]));
  }
  return value;
}

function canonical(value) {
  return JSON.stringify(sortValue(value));
}

function readJson(relativePath) {
  return JSON.parse(fs.readFileSync(path.join(packageRoot, relativePath), "utf8"));
}

function walk(directory, prefix = "") {
  const result = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) result.push(...walk(absolute, relative));
    else result.push(relative);
  }
  return result.sort();
}

const errors = [];
const checks = [];
function check(name, condition, detail = null) {
  checks.push({ name, status: condition ? "PASS" : "FAIL", detail });
  if (!condition) errors.push({ name, detail });
}

const manifestBytes = fs.readFileSync(path.join(packageRoot, "manifest.json"));
const manifest = JSON.parse(manifestBytes.toString("utf8"));
const detached = fs.readFileSync(path.join(packageRoot, "manifest.sha256"), "utf8").trim().split(/\s+/)[0];
check("detached-manifest-digest", detached === sha256(manifestBytes));
check("contract-identity", manifest.contract.name === "tekroo.kernel.contracts" && manifest.contract.version === "0.5.0");

const actualPayloadFiles = walk(packageRoot).filter((file) => !["manifest.json", "manifest.sha256"].includes(file));
const listedPayloadFiles = manifest.files.map((file) => file.path).sort();
check("payload-file-inventory", canonical(actualPayloadFiles) === canonical(listedPayloadFiles), {
  actual: actualPayloadFiles,
  listed: listedPayloadFiles,
});

for (const file of manifest.files) {
  const absolute = path.join(packageRoot, file.path);
  const bytes = fs.readFileSync(absolute);
  check(`byte-digest:${file.path}`, sha256(bytes) === file.sha256);
  check(`byte-size:${file.path}`, bytes.length === file.bytes);
  if (file.mediaType === "application/json") {
    const parsed = JSON.parse(bytes.toString("utf8"));
    check(`canonical-digest:${file.path}`, sha256(canonical(parsed)) === file.canonicalJsonSha256);
  }
}

const catalogue = readJson("catalogue/kernel-catalogue.json");
const payloadSchemas = readJson("schemas/payloads.schema.json");
const typeIds = catalogue.entries.map((entry) => entry.typeId);
check("catalogue-unique-type-ids", new Set(typeIds).size === typeIds.length);
for (const entry of catalogue.entries) {
  const fragment = entry.payloadSchema.split("#/$defs/")[1];
  check(`payload-schema:${entry.typeId}`, Boolean(fragment && payloadSchemas.$defs[fragment]));
}

const fixtureFiles = manifest.files.filter((file) => file.role === "fixtures").map((file) => file.path);
const fixtures = fixtureFiles.flatMap((file) => readJson(file).fixtures);
const fixtureIds = fixtures.map((fixture) => fixture.fixtureId);
check("fixture-unique-ids", new Set(fixtureIds).size === fixtureIds.length);
const activeCommands = catalogue.entries.filter((entry) => entry.kind === "COMMAND" && entry.lifecycle === "ACTIVE");
for (const command of activeCommands) {
  const coverage = fixtures.filter((fixture) => fixture.kind === "CATALOGUE_COMMAND" && fixture.when.commandType === command.typeId);
  check(`positive-fixture:${command.typeId}`, coverage.some((fixture) => fixture.classification === "NORMATIVE_EXAMPLE"));
  check(`negative-fixture:${command.typeId}`, coverage.some((fixture) => fixture.classification === "BOUNDARY_NEGATIVE"));
}

const invariants = readJson("invariants/invariants.json");
const invariantIds = invariants.invariants.map((item) => item.invariantId);
const testIds = new Set([...fixtureIds, ...invariantIds]);
check("invariant-unique-ids", new Set(invariantIds).size === invariantIds.length);

const traceability = readJson("traceability/traceability.json");
const requirementIds = traceability.requirements.map((item) => item.requirementId);
check("traceability-unique-requirements", new Set(requirementIds).size === requirementIds.length);
for (const requirement of traceability.requirements) {
  check(`requirement-has-tests:${requirement.requirementId}`, requirement.testIds.length > 0);
  for (const testId of requirement.testIds) {
    check(`requirement-test-resolves:${requirement.requirementId}:${testId}`, testIds.has(testId));
  }
}
for (const testId of testIds) {
  check(`test-backlink:${testId}`, traceability.requirements.some((item) => item.testIds.includes(testId)));
}

const decisions = traceability.requirements.filter((item) => item.kind === "DECISION");
const negatives = traceability.requirements.filter((item) => item.kind === "NEGATIVE_REQUIREMENT");
const prohibitions = traceability.requirements.filter((item) => item.kind === "PROHIBITED_INTERPRETATION");
check("decision-trace-count", decisions.length === 13, decisions.length);
check("negative-requirement-trace-count", negatives.length === 14, negatives.length);
check("prohibition-trace-nonempty", prohibitions.length > 0, prohibitions.length);

const compatibility = readJson("compatibility/from-0.4.0.json");
check("successor-compatibility-record", compatibility.predecessor?.contractVersion === "0.4.0" && compatibility.contractVersion === "0.5.0" && compatibility.directions?.adapter === "REQUIRED");
const protocol = readJson("runner/protocol.json");
check("runner-protocol", protocol.protocolVersion === "1.0.0" && protocol.resultClasses.includes("PASS"));

const profileNames = manifest.profiles.map((profile) => profile.name);
check("profile-set", canonical(profileNames) === canonical([
  "contract-structure",
  "core-hermetic",
  "mongo-integration",
  "synthesized-merge",
  "provider-e2e",
]));

const report = {
  schemaVersion: "1.0.0",
  reportType: "CONTRACT_STRUCTURE",
  contractIdentity: `${manifest.contract.name}/${manifest.contract.version}`,
  manifestSha256: sha256(manifestBytes),
  generatedAt: "2026-08-11T00:00:00Z",
  profile: "contract-structure",
  status: errors.length === 0 ? "PASS" : "FAIL",
  counts: {
    checks: checks.length,
    passed: checks.filter((item) => item.status === "PASS").length,
    failed: errors.length,
    catalogueEntries: catalogue.entries.length,
    fixtures: fixtures.length,
    invariants: invariants.invariants.length,
    traceabilityRequirements: traceability.requirements.length,
  },
  checks,
  errors,
  implementationProfiles: {
    coreHermeticImplementation: "NOT_RUN",
    mongoIntegration: "NOT_RUN",
    synthesizedMerge: "NOT_RUN",
    providerE2E: "NOT_RUN",
  },
  qualificationBoundary: "PASS qualifies contract-package structure and reference integrity only; no Tekroo implementation or production system is qualified.",
};
report.reportSha256 = sha256(canonical(report));
fs.mkdirSync(path.dirname(path.resolve(reportPath)), { recursive: true });
fs.writeFileSync(path.resolve(reportPath), `${JSON.stringify(sortValue(report), null, 2)}\n`);
process.stdout.write(`${report.status} ${report.counts.checks} ${report.reportSha256}\n`);
if (report.status !== "PASS") process.exitCode = 1;

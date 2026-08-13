import fs from "node:fs";
import crypto from "node:crypto";

const path = process.argv[2] ?? "investigations/openhands-q1/preregistration.json";
const value = JSON.parse(fs.readFileSync(path, "utf8"));
const fail = message => {
  process.stderr.write(`${message}\n`);
  process.exit(1);
};

if (value.investigationId !== "OPENHANDS-Q1-MODEL-CAPABILITY-VERTICAL-SLICE") fail("unexpected investigation identity");
if (value.status !== "FROZEN_CANDIDATE" || value.executionState !== "NOT_STARTED" || value.executionAuthorized !== false || value.productionAuthorized !== false) fail("unsafe candidate authority state");
if (value.contractIdentity !== "tekroo.kernel.contracts/0.6.0") fail("unexpected contract identity");
if (!Array.isArray(value.scenarios) || value.scenarios.length !== 20) fail("scenario count must be 20");
const ids = value.scenarios.map(item => item.id);
if (new Set(ids).size !== ids.length) fail("duplicate scenario id");
for (let index = 0; index < ids.length; index++) {
  const expected = `OHQ1-${String(index + 1).padStart(3, "0")}-`;
  if (!ids[index].startsWith(expected)) fail(`scenario order mismatch at ${index}`);
  if (!value.scenarios[index].category || !value.scenarios[index].expected) fail(`incomplete scenario ${ids[index]}`);
}
if (!Array.isArray(value.absoluteStopConditions) || value.absoluteStopConditions.length < 8) fail("stop conditions incomplete");
if (!Array.isArray(value.preexecutionRequirements) || value.preexecutionRequirements.length < 10) fail("preexecution requirements incomplete");
if (value.frozenInputs.modelProfileDigests.length !== 0 || value.frozenInputs.runtimeIdentityDigests.length !== 0) fail("candidate unexpectedly binds executable identities");
const canonical = input => Array.isArray(input)
  ? input.map(canonical)
  : input && typeof input === "object"
    ? Object.fromEntries(Object.keys(input).sort().map(key => [key, canonical(input[key])]))
    : input;
const claimedDigest = value.preregistrationSha256;
value.preregistrationSha256 = "";
const observedDigest = crypto.createHash("sha256").update(JSON.stringify(canonical(value))).digest("hex");
if (claimedDigest !== observedDigest) fail(`preregistration digest mismatch: ${observedDigest}`);
process.stdout.write(JSON.stringify({ status: "PASS", scenarios: ids.length, executionAuthorized: false }) + "\n");

#!/usr/bin/env python3
"""Concrete, bound disposable live actions for the final-P2 S2 runner.

No action is reachable through the offline qualifier. A live Runner first
consumes its single-use authority, then LocalRawPorts invokes this exact file.
The file accepts only the frozen definition and returns raw receipts; it
contains no scientific predicate.
"""

from __future__ import annotations

import argparse
from copy import deepcopy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time
from typing import Any, Mapping


ACTIONS = frozenset({
    "start_sma", "stop_sma", "start_bridge", "stop_bridge", "start_stub", "stop_stub",
    "start_intake_proxy", "stop_intake_proxy", "start_transport_guard", "stop_transport_guard",
    "configure_bridge_fault", "configure_model_fault", "configure_service_mode",
    "configure_capture_allowlist", "set_startup_configuration", "prepare_workspaces", "seed_partition",
    "promote_memories", "reconcile_persisted", "reconcile_exact_event", "cleanup_owned",
    "inventory_owned",
})


def _sha(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as stream:
        while chunk := stream.read(1024 * 1024): value.update(chunk)
    return value.hexdigest()


def _tree_sha(root: Path) -> str:
    entries = [
        [str(path.relative_to(root)), _sha(path)]
        for path in sorted(root.rglob("*"))
        if path.is_file() and "__pycache__" not in path.parts and path.suffix != ".pyc"
    ]
    return hashlib.sha256(json.dumps(entries, separators=(",", ":"), ensure_ascii=False).encode()).hexdigest()


def _load(path: Path) -> Mapping[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if value.get("recordType") != "SMA_S2_FINAL_P2_FROZEN_LIVE_LAUNCH_DEFINITION" or set(value.get("actions", [])) != ACTIONS:
        raise ValueError("wrong or incomplete frozen launch definition")
    for binding in value.get("boundFiles", []):
        file = Path(binding["path"])
        if not file.is_file() or _sha(file) != binding["sha256"]: raise ValueError(f"bound live file drift: {file}")
    for binding in value.get("boundTrees", []):
        root = Path(binding["path"])
        if not root.is_dir() or _tree_sha(root) != binding["sha256"]: raise ValueError(f"bound live tree drift: {root}")
    return value


class Engine:
    def __init__(self, definition: Mapping[str, Any]):
        self.d = definition
        self.root = Path(definition["disposable"]["workspaceRoot"])
        self.runtime = self.root / "runtime"
        self.state_path = self.runtime / "action-state.json"
        self.runtime.mkdir(parents=True, exist_ok=True)
        helper = Path(definition["environment"]["smaRoot"]) / "scripts/wp5e_restart_recovery.py"
        spec = importlib.util.spec_from_file_location("s2_final_p2_live_support", helper)
        if spec is None or spec.loader is None: raise RuntimeError("cannot load bound SMA launch support")
        self.support = importlib.util.module_from_spec(spec); spec.loader.exec_module(self.support)
        self.support.SMA_LABEL = definition["disposable"]["launchLabel"]

    def run_subprocess(
            self, command: list[str], cwd: Path, timeout_seconds: int
    ) -> subprocess.CompletedProcess[str]:
        """The only promotion-process seam; offline qualification replaces it."""
        return subprocess.run(
            command, cwd=cwd, capture_output=True, text=True,
            timeout=timeout_seconds, check=False)

    def promote_memory(self, memory_id: str) -> Mapping[str, Any]:
        fixture = self.d["promotionFixture"]
        project = Path(fixture["projectDirectory"])
        command = [
            fixture["javaExecutable"],
            f'-Ds2.database={self.d["disposable"]["mongoDatabase"]}',
            f'-Ds2.semanticCollection={self.d["disposable"]["semanticCollection"]}',
            f'-Ds2.episodicCollection={self.d["disposable"]["episodicCollection"]}',
            f"-Ds2.memoryId={memory_id}",
            "-cp", f'{fixture["fixtureJar"]}:{fixture["smaJar"]}',
            fixture["mainClass"],
        ]
        completed = self.run_subprocess(
            command, project, int(fixture["timeoutSeconds"]))
        stdout = completed.stdout or ""
        stderr = completed.stderr or ""
        if completed.returncode != 0:
            raise RuntimeError(
                "deterministic product promotion failed: "
                f"exit={completed.returncode} stdoutSha256="
                f"{hashlib.sha256(stdout.encode()).hexdigest()} stderrSha256="
                f"{hashlib.sha256(stderr.encode()).hexdigest()}")
        receipt = None
        for line in reversed(stdout.splitlines()):
            try:
                candidate = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(candidate, dict):
                receipt = candidate
                break
        if receipt is None:
            raise RuntimeError("deterministic product promotion emitted no JSON receipt")
        expected = {
            "recordType": "SMA_S2_DETERMINISTIC_PRODUCT_PROMOTION_RECEIPT",
            "memoryId": memory_id,
            "state": "consistent",
            "reasoningEligible": True,
            "generativeModelCalls": 0,
        }
        if any(receipt.get(key) != value for key, value in expected.items()):
            raise RuntimeError("deterministic product promotion receipt mismatch")
        if int(receipt.get("embeddingInvocations", 0)) < 1:
            raise RuntimeError("promotion did not exercise the embedding boundary")
        return {
            "workingDirectory": str(project),
            "argvSha256": hashlib.sha256(
                json.dumps(command, separators=(",", ":")).encode()).hexdigest(),
            "exitCode": completed.returncode,
            "stdoutLength": len(stdout.encode()),
            "stdoutSha256": hashlib.sha256(stdout.encode()).hexdigest(),
            "stderrLength": len(stderr.encode()),
            "stderrSha256": hashlib.sha256(stderr.encode()).hexdigest(),
            "productPromotion": receipt,
        }

    def default_state(self) -> dict[str, Any]:
        return {
            "capture": False,
            "retrieval": False,
            "allowlist": [str(self.root / "__deny_all_capture__")],
            "modelFault": "SUCCESS",
            "bridgeFault": "OK",
            "stubPid": None,
            "bridgeFaultPid": None,
            "intakeProxyPid": None,
            "transportGuardPid": None,
        }

    def state(self) -> dict[str, Any]:
        if self.state_path.is_file(): return json.loads(self.state_path.read_text(encoding="utf-8"))
        return self.default_state()

    def save(self, value: Mapping[str, Any]) -> None:
        temporary = self.state_path.with_suffix(".tmp")
        temporary.write_text(json.dumps(value, sort_keys=True, separators=(",", ":")), encoding="utf-8")
        os.replace(temporary, self.state_path)

    def env(self, state: Mapping[str, Any]) -> dict[str, str]:
        d = self.d; disposable = d["disposable"]; environment = d["environment"]
        return {
            "HOME": "/Users/paul", "PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
            "SMA_OPENHANDS_API_KEY_FILE": environment["sessionKeyFile"], "SMA_AGENT_ID": "openhands:final-p2-closure:default",
            "SMA_COGNITIVE_API": environment["cognitionApi"],
            "SMA_COGNITIVE_BASE_URL": environment["cognitionBaseUrl"],
            "SMA_COGNITIVE_MODEL": environment["cognitionModel"],
            "SMA_CANARY_PARTITION": "sma-s2-final-p2:no-background-replay",
            "SMA_MONGO_URI": environment["mongoUri"], "SMA_MONGO_DATABASE": disposable["mongoDatabase"],
            "SMA_QDRANT_SEMANTIC_COLLECTION": disposable["semanticCollection"], "SMA_QDRANT_EPISODIC_COLLECTION": disposable["episodicCollection"],
            "SMA_CONSOLIDATION_LOOP_MS": "60000", "SMA_STARTUP_HEALTH_CHECK_RETRIES": "5", "SMA_STARTUP_HEALTH_CHECK_BACKOFF_MS": "1000",
            "SMA_GRACEFUL_SHUTDOWN_ENABLED": "true", "SMA_GRACEFUL_SHUTDOWN_TIMEOUT_MS": "15000",
            "SMA_OPENHANDS_BASE_URL": d["loopback"]["openhandsCaptureProxy"], "SMA_OPENHANDS_BRIDGE_HOST": "127.0.0.1", "SMA_OPENHANDS_BRIDGE_PORT": str(d["ports"]["smaBridge"]),
            "SMA_OPENHANDS_CAPTURE_ENABLED": str(bool(state["capture"])).lower(), "SMA_OPENHANDS_RETRIEVAL_ENABLED": str(bool(state["retrieval"])).lower(),
            "SMA_OPENHANDS_WORKSPACE_ALLOWLIST": ",".join(state["allowlist"]),
        }

    def stop_sma(self) -> Mapping[str, Any]:
        return self.support.bootout_sma() if self.support.launch_identity(self.support.SMA_LABEL)["loaded"] else {"alreadyStopped": True}

    def start_sma(self) -> Mapping[str, Any]:
        self.stop_sma(); state = self.state()
        if state["capture"] and not state["allowlist"]:
            raise RuntimeError("refusing to start capture with an empty workspace allowlist")
        plist = self.runtime / f"{self.support.SMA_LABEL}.plist"
        self.support.write_plist(plist, self.env(state), self.runtime / "sma.out.log", self.runtime / "sma.err.log")
        return self.support.bootstrap_sma(plist)

    def set_startup_configuration(self, arguments: list[str]) -> Mapping[str, Any]:
        if not arguments:
            raise RuntimeError("startup configuration requires a service mode")
        mode = arguments[0]
        if mode not in {"CAPTURE_AND_RETRIEVAL", "CAPTURE_ONLY", "RETRIEVAL_ONLY", "STOPPED"}:
            raise RuntimeError(f"unsupported startup service mode: {mode}")
        root = self.root.resolve()
        allowlist = [str(Path(value).resolve()) for value in arguments[1:]]
        if any(not Path(value).is_relative_to(root) for value in allowlist):
            raise RuntimeError("startup allowlist must remain under the disposable workspace root")
        if not allowlist:
            allowlist = [str(root / "__deny_all_capture__")]
        state = self.state()
        state.update({
            "capture": mode in {"CAPTURE_AND_RETRIEVAL", "CAPTURE_ONLY"},
            "retrieval": mode in {"CAPTURE_AND_RETRIEVAL", "RETRIEVAL_ONLY"},
            "allowlist": allowlist,
            "modelFault": "SUCCESS",
            "bridgeFault": "OK",
        })
        self.save(state)
        return {"configuredWhileStopped": True, "mode": mode, "allowlist": allowlist}

    def stop_pid(self, field: str) -> Mapping[str, Any]:
        state = self.state(); pid = state.get(field)
        if pid:
            try: os.kill(int(pid), signal.SIGTERM)
            except ProcessLookupError: pass
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                try: os.kill(int(pid), 0)
                except ProcessLookupError: break
                time.sleep(0.05)
            else: raise RuntimeError(f"owned process did not terminate: {field}:{pid}")
        state[field] = None; self.save(state); return {"stoppedPid": pid, "terminationConfirmed": True}

    def reset_stub_evidence(self) -> None:
        evidence = self.d["evidenceFiles"]
        for path in (Path(evidence["stubRaw"]), Path(evidence["stubTerminal"]), Path(evidence["operationalLog"]), self.runtime / "stub-ready.json"):
            try: path.unlink()
            except FileNotFoundError: pass
        for key in ("stubRaw", "stubTerminal", "operationalLog"):
            path = Path(evidence[key]); path.parent.mkdir(parents=True, exist_ok=True); path.touch(mode=0o600, exist_ok=False)

    def start_stub(self) -> Mapping[str, Any]:
        self.stop_pid("stubPid"); self.reset_stub_evidence(); d = self.d; evidence = d["evidenceFiles"]
        command = [d["environment"]["livePython"], str(Path(d["environment"]["teamsRoot"]) / "scripts/sma_s2_deterministic_model_stub_candidate_3.py"), "--host", "127.0.0.1", "--port", str(d["ports"]["stub"]), "--raw-journal", evidence["stubRaw"], "--terminal-journal", evidence["stubTerminal"], "--operational-journal", evidence["operationalLog"], "--ready-file", str(self.runtime / "stub-ready.json")]
        process = subprocess.Popen(command, cwd=d["environment"]["teamsRoot"], stdout=self.runtime.joinpath("stub.out.log").open("ab"), stderr=self.runtime.joinpath("stub.err.log").open("ab"), start_new_session=True)
        ready_path = self.runtime / "stub-ready.json"; deadline = time.monotonic() + 10
        while time.monotonic() < deadline and not ready_path.is_file():
            if process.poll() is not None: raise RuntimeError(f"stub exited before readiness: {process.returncode}")
            time.sleep(0.05)
        if not ready_path.is_file(): raise RuntimeError("stub readiness deadline exceeded")
        ready = json.loads(ready_path.read_text(encoding="utf-8"))
        if ready.get("pid") != process.pid or ready.get("port") != d["ports"]["stub"]: raise RuntimeError("stub readiness identity mismatch")
        state = self.state(); state["stubPid"] = process.pid; self.save(state); return {"pid": process.pid, "ready": ready, "argvSha256": hashlib.sha256(json.dumps(command).encode()).hexdigest()}

    def _wait_ready(self, process: subprocess.Popen[bytes], ready_path: Path, expected_port: int) -> Mapping[str, Any]:
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline and not ready_path.is_file():
            if process.poll() is not None: raise RuntimeError(f"bound helper exited before readiness: {process.returncode}")
            time.sleep(0.05)
        if not ready_path.is_file(): raise RuntimeError("bound helper readiness deadline exceeded")
        ready = json.loads(ready_path.read_text(encoding="utf-8"))
        if ready.get("pid") != process.pid or ready.get("port") != expected_port: raise RuntimeError("bound helper readiness identity mismatch")
        return ready

    def start_intake_proxy(self) -> Mapping[str, Any]:
        self.stop_pid("intakeProxyPid")
        d = self.d; journal = Path(d["evidenceFiles"]["captureAudit"]); ready_path = self.runtime / "capture-audit-proxy-ready.json"
        for path in (journal, ready_path):
            try: path.unlink()
            except FileNotFoundError: pass
        journal.parent.mkdir(parents=True, exist_ok=True); journal.touch(mode=0o600, exist_ok=False)
        script = Path(d["environment"]["teamsRoot"]) / "scripts/sma_s2_openhands_capture_audit_proxy_v1.py"
        command = [d["environment"]["livePython"], str(script), "--host", "127.0.0.1", "--port", str(d["ports"]["openhandsCaptureProxy"]), "--upstream", d["loopback"]["openhands"], "--journal", str(journal), "--ready-file", str(ready_path)]
        process = subprocess.Popen(command, cwd=d["environment"]["teamsRoot"], stdout=self.runtime.joinpath("capture-audit-proxy.out.log").open("ab"), stderr=self.runtime.joinpath("capture-audit-proxy.err.log").open("ab"), start_new_session=True)
        ready = self._wait_ready(process, ready_path, d["ports"]["openhandsCaptureProxy"])
        state = self.state(); state["intakeProxyPid"] = process.pid; self.save(state)
        return {"pid": process.pid, "ready": ready, "journal": str(journal), "argvSha256": hashlib.sha256(json.dumps(command).encode()).hexdigest()}

    def start_transport_guard(self) -> Mapping[str, Any]:
        self.stop_pid("transportGuardPid")
        d = self.d; ready_path = self.runtime / "transport-guard-ready.json"
        try: ready_path.unlink()
        except FileNotFoundError: pass
        script = Path(d["environment"]["teamsRoot"]) / "scripts/sma_s2_unused_port_guard_v1.py"
        command = [d["environment"]["livePython"], str(script), "--host", "127.0.0.1", "--port", str(d["ports"]["transportFailure"]), "--ready-file", str(ready_path)]
        process = subprocess.Popen(command, cwd=d["environment"]["teamsRoot"], stdout=self.runtime.joinpath("transport-guard.out.log").open("ab"), stderr=self.runtime.joinpath("transport-guard.err.log").open("ab"), start_new_session=True)
        ready = self._wait_ready(process, ready_path, d["ports"]["transportFailure"])
        if ready.get("listening") is not False or ready.get("connectRefused") is not True: raise RuntimeError("transport-failure guard did not prove fail-closed state")
        state = self.state(); state["transportGuardPid"] = process.pid; self.save(state)
        return {"pid": process.pid, "ready": ready, "argvSha256": hashlib.sha256(json.dumps(command).encode()).hexdigest()}

    def inventory(self) -> Mapping[str, Any]:
        state = self.state(); launch = self.support.launch_identity(self.support.SMA_LABEL)
        def active(pid: Any) -> bool:
            if not pid: return False
            try: os.kill(int(pid), 0); return True
            except (ProcessLookupError, PermissionError): return False
        from pymongo import MongoClient
        client = MongoClient(self.d["environment"]["mongoUri"], serverSelectionTimeoutMS=5000)
        mongo_present = self.d["disposable"]["mongoDatabase"] in client.list_database_names(); client.close()
        import urllib.request, urllib.error
        qdrant_present: dict[str, bool] = {}
        for name in (self.d["disposable"]["semanticCollection"], self.d["disposable"]["episodicCollection"]):
            try: urllib.request.urlopen(self.d["loopback"]["qdrant"] + "/collections/" + name, timeout=5); qdrant_present[name] = True
            except urllib.error.HTTPError as exc: qdrant_present[name] = exc.code != 404
        workspaces = [str(path) for path in self.root.glob("SMA-S2-*") if path.is_dir()]
        def journal_ids(path: str) -> set[str]:
            file = Path(path)
            if not file.is_file(): return set()
            return {str(json.loads(line).get("requestId")) for line in file.read_text(encoding="utf-8").splitlines() if line}
        raw_ids = journal_ids(self.d["evidenceFiles"]["stubRaw"]); terminal_ids = journal_ids(self.d["evidenceFiles"]["stubTerminal"])
        return {"processState": {"sma": "RUNNING" if launch["loaded"] else "STOPPED", "bridge": "RUNNING" if launch["loaded"] else "STOPPED", "stub": "RUNNING" if active(state.get("stubPid")) else "STOPPED", "intakeProxy": "RUNNING" if active(state.get("intakeProxyPid")) else "STOPPED", "transportGuard": "RUNNING" if active(state.get("transportGuardPid")) else "STOPPED"}, "workspaces": workspaces, "activeRequests": sorted(raw_ids - terminal_ids), "mongoDatabasePresent": mongo_present, "semanticCollectionPresent": qdrant_present[self.d["disposable"]["semanticCollection"]], "episodicCollectionPresent": qdrant_present[self.d["disposable"]["episodicCollection"]]}

    def run(self, action: str, arguments: list[str]) -> Mapping[str, Any]:
        state = self.state()
        if action == "start_sma": return self.start_sma()
        if action == "stop_sma": return self.stop_sma()
        if action == "start_bridge": return self.start_sma() if not self.support.launch_identity(self.support.SMA_LABEL)["loaded"] else {"alreadyRunning": True, "health": self.support.bridge_health()}
        if action == "stop_bridge": return self.stop_sma()
        if action == "start_stub": return self.start_stub()
        if action == "stop_stub": return self.stop_pid("stubPid")
        if action == "start_intake_proxy": return self.start_intake_proxy()
        if action == "stop_intake_proxy": return self.stop_pid("intakeProxyPid")
        if action == "start_transport_guard": return self.start_transport_guard()
        if action == "stop_transport_guard": return self.stop_pid("transportGuardPid")
        if action == "configure_model_fault": state["modelFault"] = arguments[0]; self.save(state); return {"mode": arguments[0], "promptControlled": True}
        if action == "configure_service_mode":
            state["capture"] = arguments[0] in {"CAPTURE_AND_RETRIEVAL", "CAPTURE_ONLY"}; state["retrieval"] = arguments[0] in {"CAPTURE_AND_RETRIEVAL", "RETRIEVAL_ONLY"}; self.save(state); return self.start_sma()
        if action == "configure_capture_allowlist": state["allowlist"] = arguments; self.save(state); return self.start_sma()
        if action == "set_startup_configuration": return self.set_startup_configuration(arguments)
        if action == "prepare_workspaces":
            for value in arguments: Path(value).mkdir(parents=True, exist_ok=True)
            return {"workspaces": arguments}
        if action == "configure_bridge_fault":
            state["bridgeFault"] = arguments[0]; self.save(state)
            if arguments[0] == "OK": self.stop_pid("bridgeFaultPid"); return self.start_sma()
            self.stop_sma()
            if arguments[0] == "BRIDGE_DOWN": return {"mode": arguments[0], "listener": None}
            mode = {"TIMEOUT": "timeout", "MALFORMED": "malformed", "HTTP_503": "non2xx"}[arguments[0]]
            fixture = Path(self.d["environment"]["teamsRoot"]) / "scripts/sma_s2_bridge_fault_fixture_v2.py"
            command = [self.d["environment"]["livePython"], str(fixture), "--host", "127.0.0.1", "--port", str(self.d["ports"]["smaBridge"]), "--mode", mode, "--journal", self.d["evidenceFiles"]["operationalLog"]]
            process = subprocess.Popen(command, cwd=self.d["environment"]["teamsRoot"], stdout=self.runtime.joinpath("bridge-fault.out.log").open("ab"), stderr=self.runtime.joinpath("bridge-fault.err.log").open("ab"), start_new_session=True)
            state = self.state(); state["bridgeFaultPid"] = process.pid; self.save(state); return {"mode": mode, "pid": process.pid}
        if action == "seed_partition":
            workspace, profile, memory_id, text, memory_state = arguments
            template = self.d["seedTemplate"]
            truth_path = Path(template["path"]); manifest_path = Path(template["manifestPath"])
            if _sha(truth_path) != template["sha256"] or _sha(manifest_path) != template["manifestSha256"]: raise RuntimeError("bound seed truth or manifest drift")
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            if manifest.get("artifacts", {}).get(truth_path.name, {}).get("sha256") != template["sha256"]: raise RuntimeError("seed truth is not bound by product-truth manifest")
            from bson import json_util
            truth = json_util.loads(truth_path.read_text(encoding="utf-8"))
            document = deepcopy(truth["rawMemoryDocument"]); document["_id"] = memory_id
            document["agent_id"] = f"openhands:{hashlib.sha256(workspace.encode()).hexdigest()[:24]}:{profile}"; document["state"] = "raw"; document["reasoning_eligible"] = False
            # Preserve the product's initial-canonical invariant. ReplayWorker
            # bootstraps only version-0 memories with blank canonical text and
            # no semantic reference; the deterministic fixture interprets the
            # retained event surface summary.
            document["canonical"]["text"] = ""; document["event"]["surface_summary"] = text
            from pymongo import MongoClient
            client = MongoClient(self.d["environment"]["mongoUri"]); client[self.d["disposable"]["mongoDatabase"]]["memories"].replace_one({"_id": memory_id}, document, upsert=True); client.close()
            promotion = None
            if memory_state == "consistent":
                promotion = self.promote_memory(memory_id)
            return {"memoryId": memory_id, "documentSha256": hashlib.sha256(json.dumps(document, sort_keys=True, default=str).encode()).hexdigest(), "promotion": promotion}
        if action == "promote_memories":
            from pymongo import MongoClient
            client = MongoClient(self.d["environment"]["mongoUri"]); rows = list(client[self.d["disposable"]["mongoDatabase"]]["memories"].find({}, {"_id": 1})); client.close()
            receipts = [self.promote_memory(str(row["_id"])) for row in rows]
            return {"promotions": receipts}
        if action == "reconcile_persisted": return {"restart": self.start_sma(), "runnerPerformsBoundedExactKeyStabilization": True}
        if action == "reconcile_exact_event":
            conversation_id, event_id = arguments
            from pymongo import MongoClient
            selector = {"origin.openhands_provenance.conversation_id": conversation_id, "origin.openhands_provenance.event_id": event_id}
            client = MongoClient(self.d["environment"]["mongoUri"]); collection = client[self.d["disposable"]["mongoDatabase"]]["memories"]
            before = collection.count_documents(selector)
            if before != 1: client.close(); raise RuntimeError("exact event was not captured once before duplicate reconciliation")
            audit_path = Path(self.d["evidenceFiles"]["captureAudit"])
            audit_start = len(audit_path.read_text(encoding="utf-8").splitlines()) if audit_path.is_file() else 0
            restart = self.start_sma()
            deadline = time.monotonic() + 50
            cycle_receipt = None
            expected_path = f"/api/conversations/{conversation_id}/events/search"
            while time.monotonic() < deadline:
                rows = [json.loads(line) for line in audit_path.read_text(encoding="utf-8").splitlines()[audit_start:] if line] if audit_path.is_file() else []
                cycle_receipt = next((row for row in rows if row.get("recordType") == "SMA_S2_OPENHANDS_CAPTURE_AUDIT_PROXY_RECEIPT" and row.get("method") == "GET" and str(row.get("path", "")).startswith(expected_path) and row.get("status") == 200 and event_id in row.get("eventIds", [])), None)
                if cycle_receipt is not None: break
                time.sleep(0.1)
            if cycle_receipt is None: client.close(); raise RuntimeError("no successful product capture-cycle receipt for exact event")
            counts = []
            for _ in range(3): counts.append(collection.count_documents(selector)); time.sleep(0.1)
            client.close()
            if counts != [1, 1, 1]: raise RuntimeError("duplicate reconciliation changed exact capture cardinality")
            return {"conversationId": conversation_id, "eventId": event_id, "before": before, "stableAfterRestart": counts, "restart": restart, "captureCycleReceipt": cycle_receipt, "captureCycleReceiptSha256": hashlib.sha256(json.dumps(cycle_receipt, sort_keys=True, separators=(",", ":")).encode()).hexdigest()}
        if action == "inventory_owned": return self.inventory()
        if action == "cleanup_owned":
            stop_errors: list[str] = []
            stop_operations = (
                ("bridge-fault", lambda: self.stop_pid("bridgeFaultPid")),
                ("stub", lambda: self.stop_pid("stubPid")),
                ("sma", self.stop_sma),
                ("intake-proxy", lambda: self.stop_pid("intakeProxyPid")),
                ("transport-guard", lambda: self.stop_pid("transportGuardPid")),
            )
            for name, operation in stop_operations:
                try:
                    operation()
                except Exception as exc:
                    stop_errors.append(f"{name}:{type(exc).__name__}")
            self.save(self.default_state())
            from pymongo import MongoClient
            client = MongoClient(self.d["environment"]["mongoUri"]); client.drop_database(self.d["disposable"]["mongoDatabase"]); client.close()
            import urllib.request, urllib.error
            for name in (self.d["disposable"]["semanticCollection"], self.d["disposable"]["episodicCollection"]):
                try: urllib.request.urlopen(urllib.request.Request(self.d["loopback"]["qdrant"] + "/collections/" + name, method="DELETE"), timeout=10)
                except urllib.error.HTTPError as exc:
                    if exc.code != 404: raise
            for path in self.root.glob("SMA-S2-*"):
                if path.is_dir(): shutil.rmtree(path)
            inventory = self.inventory()
            for path in (Path(self.d["evidenceFiles"]["stubRaw"]), Path(self.d["evidenceFiles"]["stubTerminal"]), Path(self.d["evidenceFiles"]["operationalLog"]), Path(self.d["evidenceFiles"]["captureAudit"]), self.runtime / "stub-ready.json", self.runtime / "capture-audit-proxy-ready.json", self.runtime / "transport-guard-ready.json"):
                try: path.unlink()
                except FileNotFoundError: pass
            expected_process_state = {name: "STOPPED" for name in inventory["processState"]}
            clean = (
                inventory["processState"] == expected_process_state
                and not inventory["workspaces"]
                and not inventory["activeRequests"]
                and not inventory["mongoDatabasePresent"]
                and not inventory["semanticCollectionPresent"]
                and not inventory["episodicCollectionPresent"]
            )
            if not clean:
                raise RuntimeError(
                    "owned cleanup incomplete; stopErrors=" + ",".join(stop_errors))
            return {**inventory, "stopWarnings": stop_errors}
        raise ValueError(f"unimplemented bound action: {action}")


def main() -> int:
    parser = argparse.ArgumentParser(); parser.add_argument("--definition", required=True); parser.add_argument("action", choices=sorted(ACTIONS)); parser.add_argument("arguments", nargs="*"); args = parser.parse_args()
    result = Engine(_load(Path(args.definition).resolve())).run(args.action, list(args.arguments))
    print(json.dumps(result, sort_keys=True, separators=(",", ":"), default=str))
    return 0


if __name__ == "__main__": raise SystemExit(main())

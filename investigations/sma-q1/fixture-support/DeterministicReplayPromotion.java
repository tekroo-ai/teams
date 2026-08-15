package ai.tekroo.sma.qualification;

import ai.tekroo.sma.config.SemanticMemoryConfig;
import ai.tekroo.sma.domain.Memory;
import ai.tekroo.sma.domain.MemoryInterpretation;
import ai.tekroo.sma.domain.MemoryState;
import ai.tekroo.sma.domain.PropositionPolarity;
import ai.tekroo.sma.domain.ReplayOrigin;
import ai.tekroo.sma.domain.ReplayTrigger;
import ai.tekroo.sma.mongo.MongoBootstrap;
import ai.tekroo.sma.mongo.MongoContradictionEdgeRepository;
import ai.tekroo.sma.mongo.MongoInterpretationLogRepository;
import ai.tekroo.sma.mongo.MongoMemoryRepository;
import ai.tekroo.sma.mongo.MongoRawArtifactRepository;
import ai.tekroo.sma.mongo.MongoRetrievalEventRepository;
import ai.tekroo.sma.mongo.MongoTier1EdgeRepository;
import ai.tekroo.sma.replay.LifecyclePolicy;
import ai.tekroo.sma.replay.ReplayCandidate;
import ai.tekroo.sma.replay.ReplayWorker;
import ai.tekroo.sma.vector.OllamaEmbeddingService;
import ai.tekroo.sma.vector.QdrantVectorStore;
import io.qdrant.client.QdrantClient;
import io.qdrant.client.QdrantGrpcClient;

import java.time.Clock;
import java.time.Instant;
import java.nio.charset.StandardCharsets;
import java.util.List;

/**
 * V5 fixture-only driver around production persistence, replay, lifecycle,
 * embedding, and vector components. The cognitive candidate is deterministic
 * fixture input; no cognition model is invoked during corpus preparation.
 */
public final class DeterministicReplayPromotion {
    private DeterministicReplayPromotion() {
    }

    public static void main(String[] args) throws Exception {
        if (args.length != 4) {
            throw new IllegalArgumentException(
                    "expected database, semantic collection, episodic collection, and memory ID; canonical text is stdin");
        }
        String databaseName = args[0];
        String semanticCollection = args[1];
        String episodicCollection = args[2];
        String memoryId = args[3];
        String canonicalText = new String(System.in.readAllBytes(), StandardCharsets.UTF_8);
        if (canonicalText.isBlank()) {
            throw new IllegalArgumentException("canonical fixture text must not be blank");
        }
        SemanticMemoryConfig config = config(semanticCollection, episodicCollection);

        try (MongoBootstrap mongo = new MongoBootstrap(
                "mongodb://127.0.0.1:27017", databaseName, config);
             QdrantClient qdrant = new QdrantClient(
                     QdrantGrpcClient.newBuilder(
                             config.vector().qdrantHost(),
                             config.vector().qdrantGrpcPort(),
                             false).build())) {
            mongo.initialize();
            MongoMemoryRepository memories = mongo.memoryRepository();
            ReplayWorker worker = new ReplayWorker(
                    config,
                    new LifecyclePolicy(config),
                    memories,
                    new MongoInterpretationLogRepository(mongo.database(), config),
                    new MongoContradictionEdgeRepository(mongo.database(), config),
                    new MongoTier1EdgeRepository(mongo.database(), config),
                    new MongoRetrievalEventRepository(mongo.database(), config),
                    new MongoRawArtifactRepository(mongo.database(), config),
                    (memory, context, rawArtifact, origin) -> deterministicCandidate(
                            config, memory, context, canonicalText),
                    ignored -> { },
                    Clock.systemUTC(),
                    OllamaEmbeddingService.create(config.vector()),
                    new QdrantVectorStore(qdrant, config.vector()));

            Memory current = memories.findById(memoryId).orElseThrow();
            if (current.state() != MemoryState.RAW || current.reasoningEligible()) {
                throw new IllegalStateException("fixture memory must begin raw and ineligible");
            }
            int startingReplayCount = current.convergence().replayCount();
            while (current.state() == MemoryState.RAW
                    && current.convergence().replayCount() < 12) {
                worker.replay(new ReplayCandidate(current, ReplayOrigin.ENDOGENOUS));
                current = memories.findById(memoryId).orElseThrow();
                if (!canonicalText.equals(current.canonical().text())) {
                    throw new IllegalStateException("production replay did not retain deterministic fixture text");
                }
            }
            if (current.state() != MemoryState.CONSISTENT || !current.reasoningEligible()) {
                throw new IllegalStateException("fixture memory did not reach consistent eligibility");
            }
            long transitions = current.stateHistory().stream()
                    .filter(entry -> entry.from() == MemoryState.RAW
                            && entry.to() == MemoryState.CONSISTENT)
                    .count();
            if (transitions != 1) {
                throw new IllegalStateException("fixture promotion transition cardinality mismatch");
            }
            System.out.printf(
                    "deterministic-replay-promotion start=%d finish=%d state=consistent eligible=true%n",
                    startingReplayCount, current.convergence().replayCount());
        }
    }

    private static ReplayWorker.CognitiveEngineOutput deterministicCandidate(
            SemanticMemoryConfig config,
            Memory memory,
            ReplayWorker.ContextBundle context,
            String canonicalText
    ) {
        int cycle = memory.convergence().replayCount() + 1;
        Instant now = Instant.now();
        Memory.Proposition proposition = new Memory.Proposition(
                "prop_" + memory.id() + "_fixture",
                canonicalText,
                PropositionPolarity.POSITIVE,
                false,
                1.0);
        ReplayWorker.CognitiveCandidate candidate = new ReplayWorker.CognitiveCandidate(
                "interp_" + memory.id() + "_fixture_" + cycle,
                memory.id(),
                cycle,
                now,
                config.cognitiveEngine().modelName(),
                ReplayTrigger.MANUAL,
                new MemoryInterpretation.InputContext(
                        context.memories().stream().map(Memory::id).toList(), List.of(), null),
                new MemoryInterpretation.PriorState(
                        memory.canonical().interpretationVersion(),
                        memory.convergence().lastSemanticDelta()),
                new MemoryInterpretation.Interpretation(canonicalText, List.of(proposition)),
                List.of(proposition.id()),
                List.of(),
                List.of(),
                List.of(),
                new MemoryInterpretation.Stability(cycle > 1, false, "deterministic_fixture"),
                false);
        return new ReplayWorker.CognitiveEngineOutput(candidate);
    }

    private static SemanticMemoryConfig config(
            String semanticCollection,
            String episodicCollection
    ) {
        SemanticMemoryConfig base = SemanticMemoryConfig.defaults();
        SemanticMemoryConfig.VectorConfig vector = base.vector();
        SemanticMemoryConfig.VectorConfig selectedVector = new SemanticMemoryConfig.VectorConfig(
                vector.ollamaBaseUrl(),
                vector.embeddingModelName(),
                vector.embeddingDimension(),
                vector.maxInputTokens(),
                episodicCollection,
                semanticCollection,
                vector.qdrantHost(),
                vector.qdrantGrpcPort());
        return new SemanticMemoryConfig(
                base.collections(),
                base.lifecycle(),
                base.hysteresis(),
                base.pressure(),
                base.replay(),
                base.retrieval(),
                base.edges(),
                base.training(),
                base.entities(),
                base.consolidation(),
                base.selfSelection(),
                selectedVector,
                base.cognitiveEngine(),
                base.observerEdges(),
                base.openhands(),
                base.service(),
                base.summaryBudgetBySource());
    }
}

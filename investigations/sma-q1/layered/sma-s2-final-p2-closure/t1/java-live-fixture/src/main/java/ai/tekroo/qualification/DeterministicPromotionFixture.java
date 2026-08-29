package ai.tekroo.qualification;

import ai.tekroo.sma.config.SemanticMemoryConfig;
import ai.tekroo.sma.domain.Memory;
import ai.tekroo.sma.domain.MemoryInterpretation;
import ai.tekroo.sma.domain.MemoryState;
import ai.tekroo.sma.domain.ReplayOrigin;
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
import com.google.gson.JsonObject;
import io.qdrant.client.QdrantClient;
import io.qdrant.client.QdrantGrpcClient;

import java.time.Clock;
import java.time.Instant;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Promotes one pre-inserted raw fixture through the production ReplayWorker,
 * repositories, embedding boundary, and Qdrant store without a generative
 * cognitive-model call. The only injected boundary is deterministic cognition.
 */
public final class DeterministicPromotionFixture {
    private DeterministicPromotionFixture() { }

    public static void main(String[] args) throws Exception {
        String database = required("s2.database");
        String semanticCollection = required("s2.semanticCollection");
        String episodicCollection = required("s2.episodicCollection");
        String memoryId = required("s2.memoryId");
        SemanticMemoryConfig config = config(semanticCollection, episodicCollection);
        AtomicInteger cognitiveInvocations = new AtomicInteger();
        AtomicInteger embeddingInvocations = new AtomicInteger();

        try (MongoBootstrap mongo = new MongoBootstrap(
                "mongodb://127.0.0.1:27017", database, config);
             QdrantClient qdrant = new QdrantClient(
                     QdrantGrpcClient.newBuilder(
                             config.vector().qdrantHost(),
                             config.vector().qdrantGrpcPort(), false).build())) {
            mongo.initialize();
            MongoMemoryRepository memories = mongo.memoryRepository();
            var actualEmbedding = OllamaEmbeddingService.create(config.vector());
            ReplayWorker worker = new ReplayWorker(
                    config,
                    new LifecyclePolicy(config),
                    memories,
                    new MongoInterpretationLogRepository(mongo.database(), config),
                    new MongoContradictionEdgeRepository(mongo.database(), config),
                    new MongoTier1EdgeRepository(mongo.database(), config),
                    new MongoRetrievalEventRepository(mongo.database(), config),
                    new MongoRawArtifactRepository(mongo.database(), config),
                    (locked, context, raw, origin) -> {
                        cognitiveInvocations.incrementAndGet();
                        return new ReplayWorker.CognitiveEngineOutput(
                                new ReplayWorker.CognitiveCandidate(
                                        "s2-fixture-" + locked.id() + "-" + cognitiveInvocations.get(),
                                        locked.id(),
                                        1,
                                        Instant.now(),
                                        "sma-s2-deterministic-cognitive-fixture",
                                        null, null, null,
                                        new MemoryInterpretation.Interpretation(
                                                locked.event().surfaceSummary(), List.of()),
                                        List.of(), List.of(), List.of(), List.of(),
                                        null, false));
                    },
                    ignored -> { },
                    Clock.systemUTC(),
                    text -> {
                        embeddingInvocations.incrementAndGet();
                        return actualEmbedding.embed(text);
                    },
                    new QdrantVectorStore(qdrant, config.vector()));

            Memory current = memories.findById(memoryId).orElseThrow();
            if (current.state() != MemoryState.RAW || current.reasoningEligible()) {
                throw new IllegalStateException("fixture must begin raw and ineligible");
            }
            int startingReplayCount = current.convergence().replayCount();
            while (current.state() == MemoryState.RAW
                    && current.convergence().replayCount() < startingReplayCount + 12) {
                worker.replay(new ReplayCandidate(current, ReplayOrigin.ENDOGENOUS));
                current = memories.findById(memoryId).orElseThrow();
            }
            if (current.state() != MemoryState.CONSISTENT
                    || !current.reasoningEligible()
                    || current.embeddings().semanticRef() == null) {
                throw new IllegalStateException("deterministic product replay did not produce a retrievable consistent memory");
            }

            JsonObject receipt = new JsonObject();
            receipt.addProperty("recordType", "SMA_S2_DETERMINISTIC_PRODUCT_PROMOTION_RECEIPT");
            receipt.addProperty("memoryId", memoryId);
            receipt.addProperty("state", current.state().name().toLowerCase());
            receipt.addProperty("reasoningEligible", current.reasoningEligible());
            receipt.addProperty("startingReplayCount", startingReplayCount);
            receipt.addProperty("endingReplayCount", current.convergence().replayCount());
            receipt.addProperty("deterministicCognitiveInvocations", cognitiveInvocations.get());
            receipt.addProperty("generativeModelCalls", 0);
            receipt.addProperty("embeddingInvocations", embeddingInvocations.get());
            receipt.addProperty("semanticRef", current.embeddings().semanticRef().value());
            System.out.println(receipt);
        }
    }

    private static SemanticMemoryConfig config(
            String semanticCollection, String episodicCollection) {
        SemanticMemoryConfig base = SemanticMemoryConfig.defaults();
        SemanticMemoryConfig.VectorConfig vector = base.vector();
        SemanticMemoryConfig.VectorConfig selected = new SemanticMemoryConfig.VectorConfig(
                vector.ollamaBaseUrl(), vector.embeddingModelName(),
                vector.embeddingDimension(), vector.maxInputTokens(),
                episodicCollection, semanticCollection,
                vector.qdrantHost(), vector.qdrantGrpcPort());
        return new SemanticMemoryConfig(
                base.collections(), base.lifecycle(), base.hysteresis(),
                base.pressure(), base.replay(), base.retrieval(), base.edges(),
                base.training(), base.entities(), base.consolidation(),
                base.selfSelection(), selected, base.cognitiveEngine(),
                base.observerEdges(), base.openhands(), base.service(),
                base.summaryBudgetBySource());
    }

    private static String required(String name) {
        String value = System.getProperty(name);
        if (value == null || value.isBlank()) {
            throw new IllegalStateException(name + " is required");
        }
        return value;
    }
}

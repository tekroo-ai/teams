package ai.tekroo.sma.openhands;

import ai.tekroo.sma.config.SemanticMemoryConfig;
import ai.tekroo.sma.domain.EmbeddingRef;
import ai.tekroo.sma.domain.Memory;
import ai.tekroo.sma.domain.MemoryState;
import ai.tekroo.sma.domain.RetrievalEvent;
import ai.tekroo.sma.replay.ReplayWorker;
import ai.tekroo.sma.retrieval.RetrievalDisclosureRepository;
import ai.tekroo.sma.retrieval.SmaRetrievalService;
import ai.tekroo.sma.vector.EmbeddingService;
import ai.tekroo.sma.vector.QdrantVectorStore;
import ai.tekroo.sma.vector.SimilarMemory;
import ai.tekroo.sma.vector.VectorCollection;
import ai.tekroo.sma.vector.VectorStorePort;
import com.google.common.util.concurrent.Futures;
import com.google.common.util.concurrent.ListenableFuture;
import com.google.gson.Gson;
import com.google.gson.GsonBuilder;
import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import com.mongodb.MongoClientSettings;
import com.sun.net.httpserver.Headers;
import com.sun.net.httpserver.HttpContext;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import com.sun.net.httpserver.HttpPrincipal;
import io.qdrant.client.QdrantClient;
import io.qdrant.client.grpc.Points.PointStruct;
import io.qdrant.client.grpc.Points.UpdateResult;
import org.bson.Document;
import org.bson.conversions.Bson;
import org.bson.json.JsonMode;
import org.bson.json.JsonWriterSettings;

import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLParameters;
import javax.net.ssl.SSLSession;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.lang.reflect.Constructor;
import java.lang.reflect.Method;
import java.net.Authenticator;
import java.net.CookieHandler;
import java.net.InetSocketAddress;
import java.net.ProxySelector;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpHeaders;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Executor;

/**
 * Executes frozen final-P2 product code without starting a service or making a
 * network call. Test doubles sit below the real intake, replay-update builder,
 * vector adapter, retrieval service, and bridge-handler orchestration.
 */
public final class FinalP2ProductTruthProbe {
    private static final Gson GSON = new GsonBuilder().disableHtmlEscaping().create();
    private static final Instant FIXED_INSTANT = Instant.parse("2026-08-28T12:00:00Z");
    private static final String WORKSPACE = "/tmp/sma-s2-product-truth";
    private static final JsonWriterSettings EXTENDED_JSON = JsonWriterSettings.builder()
            .outputMode(JsonMode.EXTENDED)
            .build();

    private FinalP2ProductTruthProbe() {
    }

    public static void main(String[] args) throws Exception {
        if (args.length == 3 && "--classify-hook".equals(args[0])) {
            classifyProductHook(Path.of(args[1]), Path.of(args[2]));
            return;
        }
        if (args.length != 2) {
            throw new IllegalArgumentException(
                    "usage: <openhands-fixture-json> <output-json> | "
                            + "--classify-hook <hook-receipt-json> <output-json>");
        }
        JsonObject openHandsFixtures = JsonParser.parseString(
                Files.readString(Path.of(args[0]), StandardCharsets.UTF_8)
        ).getAsJsonObject();

        IntakeResult intake = runActualIntake(openHandsFixtures);
        if (intake.capturedMemories().size() != 3) {
            throw new IllegalStateException(
                    "actual intake captured " + intake.capturedMemories().size() + " events, expected 3");
        }
        Memory rawMemory = intake.capturedMemories().get("eligible_user_task");
        if (rawMemory == null) {
            throw new IllegalStateException("eligible user task was not captured");
        }
        Document rawDocument = memoryDocument(rawMemory);

        RecordingQdrantClient recordingClient = new RecordingQdrantClient();
        SemanticMemoryConfig.VectorConfig vectorConfig = new SemanticMemoryConfig.VectorConfig(
                "http://127.0.0.1:1", "not-invoked", 3, 16,
                "sma_s2_truth_episodic", "sma_s2_truth_semantic", "127.0.0.1", 1);
        QdrantVectorStore qdrant = new QdrantVectorStore(recordingClient, vectorConfig);
        EmbeddingRef episodicRef = qdrant.upsert(
                VectorCollection.EPISODIC, rawMemory.id(),
                new float[]{0.1f, 0.2f, 0.3f}, rawMemory.agentId());
        EmbeddingRef semanticRef = qdrant.upsert(
                VectorCollection.SEMANTIC, rawMemory.id(),
                new float[]{0.4f, 0.5f, 0.6f}, rawMemory.agentId());

        Memory.Canonical replayCanonical = new Memory.Canonical(
                rawMemory.event().surfaceSummary(), List.of(), 1, FIXED_INSTANT, 1);
        Bson canonicalUpdate = memoryUpdate(
                "canonicalUpdate",
                new Class<?>[]{Memory.Canonical.class, EmbeddingRef.class, Instant.class},
                replayCanonical, semanticRef, FIXED_INSTANT);
        Memory.StateHistoryEntry promotionEntry = new Memory.StateHistoryEntry(
                MemoryState.RAW, MemoryState.CONSISTENT, FIXED_INSTANT,
                "replay_converged", 1);
        Bson promotionUpdate = memoryUpdate(
                "forwardToConsistentUpdate",
                new Class<?>[]{Memory.StateHistoryEntry.class, int.class, double.class, Instant.class},
                promotionEntry, 1, 0.0d, FIXED_INSTANT);

        CapturingRetrievalRepository retrievalRepository = new CapturingRetrievalRepository();
        RetrievalDisclosureRepository disclosureRepository = event ->
                RetrievalDisclosureRepository.AppendResult.APPENDED;
        OpenHandsBridgeServer.SemanticRetriever semanticRetriever =
                (query, limit, agentId, taskRef, cancelled) -> {
                    Memory eligibleView = retrievalEligibleView(
                            rawMemory, agentId, replayCanonical, semanticRef);
                    SmaRetrievalService retrievalService = new SmaRetrievalService(
                            new RetrievalVectorPort(eligibleView.id()),
                            (EmbeddingService) text -> new float[]{0.4f, 0.5f, 0.6f},
                            new InMemoryStore(eligibleView),
                            retrievalRepository,
                            disclosureRepository,
                            SemanticMemoryConfig.defaults());
                    return retrievalService.retrieveSemantic(
                            query, limit, agentId, taskRef, cancelled);
                };

        JsonObject conversation = new JsonObject();
        JsonObject workspace = new JsonObject();
        workspace.addProperty("working_dir", WORKSPACE);
        conversation.add("workspace", workspace);
        JsonObject profile = new JsonObject();
        profile.addProperty("agent_profile_id", "truth-profile");
        conversation.add("launched_agent_profile", profile);
        FakeOpenHandsClient openHandsClient = new FakeOpenHandsClient(conversation.toString());
        CapturingResponseSender responseSender = new CapturingResponseSender();

        OpenHandsBridgeServer bridge = new OpenHandsBridgeServer(
                semanticRetriever,
                openHandsClient,
                "127.0.0.1:0",
                "http://openhands.invalid/",
                true,
                "not-used",
                List.of(WORKSPACE),
                responseSender);
        int boundPort = bridge.boundPort();
        JsonObject bridgeRequest = new JsonObject();
        bridgeRequest.addProperty("session_id", "conv-product-truth");
        bridgeRequest.addProperty("working_dir", WORKSPACE);
        bridgeRequest.addProperty("prompt", "Implement the product-bound fixture.");
        ProbeExchange exchange = new ProbeExchange(bridgeRequest.toString());
        Method contextHandler = OpenHandsBridgeServer.class.getDeclaredMethod("contextHandler");
        contextHandler.setAccessible(true);
        ((HttpHandler) contextHandler.invoke(bridge)).handle(exchange);
        bridge.close();

        if (responseSender.statusCode != 200 || responseSender.body == null) {
            throw new IllegalStateException("actual bridge handler did not return a 200 response");
        }
        JsonObject bridgeResponse = JsonParser.parseString(responseSender.body).getAsJsonObject();
        if (retrievalRepository.events.size() != 1) {
            throw new IllegalStateException("actual handler path did not emit one retrieval event");
        }
        RetrievalEvent retrievalEvent = retrievalRepository.events.get(0);
        Document retrievalDocument = retrievalDocument(retrievalEvent);
        String traceId = bridgeResponse.get("trace_id").getAsString();
        if (!traceId.equals(retrievalDocument.getString("task_ref"))) {
            throw new IllegalStateException("handler-generated trace did not reach retrieval task_ref");
        }

        JsonObject output = new JsonObject();
        output.addProperty("schemaVersion", "2.0.0-dev");
        output.addProperty("recordType", "FINAL_P2_SMA_PRODUCT_TRUTH");
        output.addProperty("authoritativePositiveSource", "BOUND_FINAL_P2_IMPLEMENTATION");
        output.add("intake", intake.receipt());
        output.add("rawMemoryDocument", parseDocument(rawDocument));

        JsonObject replay = new JsonObject();
        replay.addProperty("authoritativeSource", "MemoryUpdateBuilders invoked by ReplayWorker");
        replay.add("canonicalUpdate", parseBson(canonicalUpdate));
        replay.add("promotionToConsistentUpdate", parseBson(promotionUpdate));
        replay.addProperty("episodicReferencePersistence", "QDRANT_ONLY_RETURN_IGNORED_BY_REPLAY_WORKER");
        replay.addProperty("semanticReferencePersistence", "MONGO_EMBEDDINGS_SEMANTIC_REF");
        replay.addProperty("probeAuthoredPromotedMongoDocument", false);
        output.add("replayPersistenceContracts", replay);

        output.add("qdrantWrites", recordingClient.receipts());
        output.add("retrievalEventDocument", parseDocument(retrievalDocument));
        output.add("bridgeResponse", bridgeResponse);

        JsonObject retrievalBoundary = new JsonObject();
        retrievalBoundary.addProperty(
                "memoryStore", "IN_MEMORY_TEST_DOUBLE_BELOW_ACTUAL_RETRIEVAL_SERVICE");
        retrievalBoundary.addProperty("eligibilityFieldsDerivedFromActualReplayUpdates", true);
        retrievalBoundary.addProperty("serializedAsAuthoritativeMongoDocument", false);
        retrievalBoundary.addProperty(
                "authoritativeOutputs", "RETRIEVAL_EVENT_MAPPER_AND_BRIDGE_HANDLER_RESPONSE");
        output.add("retrievalFixtureBoundary", retrievalBoundary);

        JsonObject lifecycle = new JsonObject();
        lifecycle.addProperty("serverStartCalled", false);
        lifecycle.addProperty("handlerInvokedDirectly", true);
        lifecycle.addProperty("loopbackSocketBound", true);
        lifecycle.addProperty("boundPort", boundPort);
        lifecycle.addProperty("acceptedNetworkConnections", false);
        lifecycle.addProperty("fakeOpenHandsRequests", openHandsClient.requestCount);
        lifecycle.addProperty("responseStatus", responseSender.statusCode);
        lifecycle.addProperty("ownedExecutorsTerminated", bridge.ownedExecutorsTerminated());
        output.add("bridgeLifecycle", lifecycle);

        JsonObject join = new JsonObject();
        join.addProperty("bridgeTraceId", traceId);
        join.addProperty("retrievalTaskRef", retrievalDocument.getString("task_ref"));
        join.addProperty(
                "retrievalMemoryId",
                retrievalDocument.getList("results", Document.class).get(0).getString("memory_id"));
        join.addProperty("mongoRawMemoryId", rawDocument.getString("_id"));
        Document provenance = rawDocument
                .get("origin", Document.class)
                .get("openhands_provenance", Document.class);
        join.addProperty("mongoConversationId", provenance.getString("conversation_id"));
        join.addProperty("mongoEventId", provenance.getString("event_id"));
        join.addProperty("mongoSequence", provenance.getInteger("sequence"));
        join.addProperty(
                "memorySequencePosition",
                rawDocument.get("event", Document.class).getInteger("sequence_position"));
        join.addProperty("qdrantSemanticPointId", semanticRef.value());
        join.addProperty("qdrantEpisodicPointId", episodicRef.value());
        join.addProperty("mongoEpisodicReferenceExpected", false);
        output.add("evidenceJoin", join);

        Path outputPath = Path.of(args[1]);
        Files.createDirectories(outputPath.getParent());
        Files.writeString(
                outputPath, GSON.toJson(canonicalize(output)) + "\n", StandardCharsets.UTF_8);
    }

    private static void classifyProductHook(Path hookReceiptPath, Path outputPath)
            throws Exception {
        JsonObject hookReceipt = JsonParser.parseString(
                Files.readString(hookReceiptPath, StandardCharsets.UTF_8)).getAsJsonObject();
        JsonObject fixture = new JsonObject();
        JsonArray events = new JsonArray();
        JsonObject record = new JsonObject();
        record.addProperty("label", "ineligible_hook");
        record.add(
                "event",
                hookReceipt.getAsJsonObject("persistedHookExecutionEvent").deepCopy());
        events.add(record);
        fixture.add("events", events);
        IntakeResult result = runActualIntake(fixture);
        if (!result.capturedMemories().isEmpty()) {
            throw new IllegalStateException("actual intake captured HookExecutionEvent");
        }
        JsonObject output = new JsonObject();
        output.addProperty("schemaVersion", "2.0.0-dev");
        output.addProperty("recordType", "FINAL_P2_HOOK_INTAKE_DISCRIMINATOR");
        output.addProperty("authoritativePositiveSource", "BOUND_FINAL_P2_IMPLEMENTATION");
        output.add("intake", result.receipt());
        Files.createDirectories(outputPath.getParent());
        Files.writeString(
                outputPath, GSON.toJson(canonicalize(output)) + "\n", StandardCharsets.UTF_8);
    }

    private static IntakeResult runActualIntake(JsonObject fixtures) throws Exception {
        SemanticMemoryConfig.OpenHandsBridgeConfig config =
                new SemanticMemoryConfig.OpenHandsBridgeConfig(
                        "http://127.0.0.1:1", "127.0.0.1", 1,
                        false, false, List.of(), 60);
        OpenHandsEventIntakeService.MemoryRepository noStore =
                new OpenHandsEventIntakeService.MemoryRepository() {
                    @Override
                    public boolean exists(String memoryId) {
                        return false;
                    }

                    @Override
                    public void insert(Memory memory) {
                        throw new IllegalStateException("probe must not persist");
                    }
                };
        OpenHandsEventIntakeService intake = new OpenHandsEventIntakeService(
                config, noStore, HttpClient.newHttpClient(), null,
                Clock.fixed(FIXED_INSTANT, ZoneOffset.UTC));

        Class<?> metadataType = Class.forName(
                "ai.tekroo.sma.openhands.OpenHandsEventIntakeService$ConversationMetadata");
        Constructor<?> metadataConstructor = metadataType.getDeclaredConstructor(
                String.class, String.class, String.class, String.class, String.class);
        metadataConstructor.setAccessible(true);
        Object conversation = metadataConstructor.newInstance(
                "conv-product-truth", null, WORKSPACE, "truth-profile", "deterministic-stub");
        Method buildMemory = OpenHandsEventIntakeService.class.getDeclaredMethod(
                "buildMemory", metadataType, JsonObject.class, int.class);
        buildMemory.setAccessible(true);

        Map<String, Memory> captured = new LinkedHashMap<>();
        JsonArray decisions = new JsonArray();
        int position = 0;
        for (JsonElement item : fixtures.getAsJsonArray("events")) {
            JsonObject record = item.getAsJsonObject();
            String label = record.get("label").getAsString();
            JsonObject event = record.getAsJsonObject("event");
            @SuppressWarnings("unchecked")
            Optional<Memory> memory = (Optional<Memory>) buildMemory.invoke(
                    intake, conversation, event, position++);
            JsonObject decision = new JsonObject();
            decision.addProperty("label", label);
            decision.addProperty("eventId", event.get("id").getAsString());
            decision.addProperty("eventKind", event.get("kind").getAsString());
            decision.addProperty("captured", memory.isPresent());
            if (memory.isPresent()) {
                captured.put(label, memory.get());
                decision.addProperty("memoryId", memory.get().id());
                decision.addProperty("agentId", memory.get().agentId());
                decision.add("mongoDocument", parseDocument(memoryDocument(memory.get())));
            }
            decisions.add(decision);
        }
        JsonObject receipt = new JsonObject();
        receipt.addProperty("evaluatedEvents", decisions.size());
        receipt.addProperty("capturedEvents", captured.size());
        receipt.add("decisions", decisions);
        return new IntakeResult(Map.copyOf(captured), receipt);
    }

    private static Memory retrievalEligibleView(
            Memory raw,
            String agentId,
            Memory.Canonical canonical,
            EmbeddingRef semanticRef) {
        return new Memory(
                raw.id(), agentId, raw.schemaVersion(), raw.docVersion() + 2,
                MemoryState.CONSISTENT, true, true,
                List.of(new Memory.StateHistoryEntry(
                        MemoryState.RAW, MemoryState.CONSISTENT, FIXED_INSTANT,
                        "replay_converged", 1)),
                raw.regressionCount(), raw.lastRegressionAt(), false,
                raw.origin(), raw.event(), canonical, raw.convergence(), raw.pressure(),
                raw.weights(), raw.identityRelevance(), raw.policyRelevance(),
                raw.selfSelection(), raw.consolidation(), raw.processing(),
                new Memory.Embeddings(null, semanticRef), raw.relations(), raw.archival(),
                raw.createdAt(), FIXED_INSTANT);
    }

    private static Bson memoryUpdate(
            String methodName,
            Class<?>[] parameterTypes,
            Object... arguments) throws Exception {
        Class<?> type = Class.forName("ai.tekroo.sma.mongo.MemoryUpdateBuilders");
        Method method = type.getDeclaredMethod(methodName, parameterTypes);
        method.setAccessible(true);
        return (Bson) method.invoke(null, arguments);
    }

    private static Document memoryDocument(Memory memory) throws Exception {
        return invokeMapper("ai.tekroo.sma.mongo.MemoryDocumentMapper", "toDocument", Memory.class, memory);
    }

    private static Document retrievalDocument(RetrievalEvent event) throws Exception {
        return invokeMapper("ai.tekroo.sma.mongo.RetrievalEventDocumentMapper", "toDocument", RetrievalEvent.class, event);
    }

    private static Document invokeMapper(
            String className, String methodName, Class<?> inputType, Object input) throws Exception {
        Class<?> mapperType = Class.forName(className);
        Constructor<?> constructor = mapperType.getDeclaredConstructor();
        constructor.setAccessible(true);
        Object mapper = constructor.newInstance();
        Method method = mapperType.getDeclaredMethod(methodName, inputType);
        method.setAccessible(true);
        return (Document) method.invoke(mapper, input);
    }

    private static JsonElement parseDocument(Document document) {
        return JsonParser.parseString(document.toJson(EXTENDED_JSON));
    }

    private static JsonElement parseBson(Bson bson) {
        return JsonParser.parseString(
                bson.toBsonDocument(Document.class, MongoClientSettings.getDefaultCodecRegistry())
                        .toJson(EXTENDED_JSON));
    }

    private static JsonElement canonicalize(JsonElement element) {
        if (element.isJsonArray()) {
            JsonArray array = new JsonArray();
            for (JsonElement child : element.getAsJsonArray()) {
                array.add(canonicalize(child));
            }
            return array;
        }
        if (!element.isJsonObject()) {
            return element.deepCopy();
        }
        JsonObject object = new JsonObject();
        element.getAsJsonObject().entrySet().stream()
                .sorted(Map.Entry.comparingByKey())
                .forEach(entry -> object.add(entry.getKey(), canonicalize(entry.getValue())));
        return object;
    }

    private record IntakeResult(Map<String, Memory> capturedMemories, JsonObject receipt) {
    }

    private static final class RecordingQdrantClient extends QdrantClient {
        private final List<WriteReceipt> writes = new ArrayList<>();

        RecordingQdrantClient() {
            super(null);
        }

        @Override
        public ListenableFuture<UpdateResult> upsertAsync(
                String collectionName, List<PointStruct> points) {
            for (PointStruct point : points) {
                writes.add(new WriteReceipt(collectionName, point));
            }
            return Futures.immediateFuture(UpdateResult.getDefaultInstance());
        }

        JsonArray receipts() {
            JsonArray receipts = new JsonArray();
            writes.stream().sorted(Comparator.comparing(WriteReceipt::collectionName))
                    .forEach(write -> {
                        JsonObject receipt = new JsonObject();
                        receipt.addProperty("collection", write.collectionName());
                        receipt.addProperty("pointId", write.point().getId().getUuid());
                        JsonObject payload = new JsonObject();
                        write.point().getPayloadMap().entrySet().stream()
                                .sorted(Map.Entry.comparingByKey())
                                .forEach(entry -> payload.addProperty(
                                        entry.getKey(), entry.getValue().getStringValue()));
                        receipt.add("payload", payload);
                        receipts.add(receipt);
                    });
            return receipts;
        }
    }

    private record WriteReceipt(String collectionName, PointStruct point) {
    }

    private static final class CapturingRetrievalRepository
            implements ReplayWorker.RetrievalEventRepository {
        private final List<RetrievalEvent> events = new ArrayList<>();

        @Override
        public void write(RetrievalEvent retrievalEvent) {
            events.add(retrievalEvent);
        }
    }

    private static final class InMemoryStore implements SmaRetrievalService.MemoryStore {
        private final Memory memory;

        private InMemoryStore(Memory memory) {
            this.memory = memory;
        }

        @Override
        public Optional<Memory> findById(String memoryId) {
            return memory.id().equals(memoryId) ? Optional.of(memory) : Optional.empty();
        }

        @Override
        public List<Memory> findByIds(List<String> memoryIds) {
            return memoryIds.contains(memory.id()) ? List.of(memory) : List.of();
        }

        @Override
        public void boostRecurrencePressure(String memoryId, double increment) {
            if (!memory.id().equals(memoryId)) throw new IllegalArgumentException(memoryId);
        }

        @Override
        public void dischargeReplayDebt(String memoryId, double factor) {
            if (!memory.id().equals(memoryId)) throw new IllegalArgumentException(memoryId);
        }
    }

    private static final class RetrievalVectorPort implements VectorStorePort {
        private final String memoryId;

        private RetrievalVectorPort(String memoryId) {
            this.memoryId = memoryId;
        }

        @Override
        public EmbeddingRef upsert(
                VectorCollection collection, String memoryId, float[] vector, String agentId) {
            throw new UnsupportedOperationException();
        }

        @Override
        public StoredVector findByRef(
                VectorCollection collection, EmbeddingRef ref, String memoryId, String agentId) {
            throw new UnsupportedOperationException();
        }

        @Override
        public List<SimilarMemory> findSimilar(
                VectorCollection collection, float[] queryVector, int limit, String agentId) {
            return List.of(new SimilarMemory(memoryId, 0.99f));
        }

        @Override
        public void delete(String memoryId) {
            throw new UnsupportedOperationException();
        }
    }

    private static final class CapturingResponseSender implements OpenHandsBridgeServer.ResponseSender {
        private int statusCode;
        private String body;

        @Override
        public boolean send(HttpExchange exchange, int statusCode, String jsonBody) {
            this.statusCode = statusCode;
            this.body = jsonBody;
            return true;
        }
    }

    private static final class ProbeExchange extends HttpExchange {
        private final Headers requestHeaders = new Headers();
        private final Headers responseHeaders = new Headers();
        private InputStream requestBody;
        private OutputStream responseBody = new ByteArrayOutputStream();
        private int responseCode = -1;

        private ProbeExchange(String body) {
            byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
            this.requestBody = new ByteArrayInputStream(bytes);
            requestHeaders.add("Content-Length", Integer.toString(bytes.length));
            requestHeaders.add("Content-Type", "application/json");
        }

        @Override public Headers getRequestHeaders() { return requestHeaders; }
        @Override public Headers getResponseHeaders() { return responseHeaders; }
        @Override public URI getRequestURI() { return URI.create("/v1/openhands/context"); }
        @Override public String getRequestMethod() { return "POST"; }
        @Override public HttpContext getHttpContext() { return null; }
        @Override public void close() { }
        @Override public InputStream getRequestBody() { return requestBody; }
        @Override public OutputStream getResponseBody() { return responseBody; }
        @Override public void sendResponseHeaders(int code, long length) { responseCode = code; }
        @Override public InetSocketAddress getRemoteAddress() { return new InetSocketAddress("127.0.0.1", 1); }
        @Override public int getResponseCode() { return responseCode; }
        @Override public InetSocketAddress getLocalAddress() { return new InetSocketAddress("127.0.0.1", 0); }
        @Override public String getProtocol() { return "HTTP/1.1"; }
        @Override public Object getAttribute(String name) { return null; }
        @Override public void setAttribute(String name, Object value) { }
        @Override public void setStreams(InputStream input, OutputStream output) {
            requestBody = input;
            responseBody = output;
        }
        @Override public HttpPrincipal getPrincipal() { return null; }
    }

    private static final class FakeOpenHandsClient extends HttpClient {
        private final String responseBody;
        private int requestCount;

        private FakeOpenHandsClient(String responseBody) {
            this.responseBody = responseBody;
        }

        @Override public Optional<CookieHandler> cookieHandler() { return Optional.empty(); }
        @Override public Optional<Duration> connectTimeout() { return Optional.of(Duration.ofSeconds(1)); }
        @Override public Redirect followRedirects() { return Redirect.NEVER; }
        @Override public Optional<ProxySelector> proxy() { return Optional.empty(); }
        @Override public SSLContext sslContext() { return null; }
        @Override public SSLParameters sslParameters() { return new SSLParameters(); }
        @Override public Optional<Authenticator> authenticator() { return Optional.empty(); }
        @Override public Version version() { return Version.HTTP_1_1; }
        @Override public Optional<Executor> executor() { return Optional.empty(); }

        @Override
        @SuppressWarnings("unchecked")
        public <T> HttpResponse<T> send(
                HttpRequest request, HttpResponse.BodyHandler<T> responseBodyHandler) {
            requestCount++;
            if (!request.uri().getPath().contains("/api/conversations/conv-product-truth")) {
                throw new IllegalStateException("unexpected product request " + request.uri());
            }
            return new FixedResponse<>(request, (T) responseBody);
        }

        @Override
        public <T> CompletableFuture<HttpResponse<T>> sendAsync(
                HttpRequest request, HttpResponse.BodyHandler<T> responseBodyHandler) {
            try {
                return CompletableFuture.completedFuture(send(request, responseBodyHandler));
            } catch (RuntimeException exception) {
                return CompletableFuture.failedFuture(exception);
            }
        }

        @Override
        public <T> CompletableFuture<HttpResponse<T>> sendAsync(
                HttpRequest request,
                HttpResponse.BodyHandler<T> responseBodyHandler,
                HttpResponse.PushPromiseHandler<T> pushPromiseHandler) {
            return sendAsync(request, responseBodyHandler);
        }
    }

    private record FixedResponse<T>(HttpRequest request, T body) implements HttpResponse<T> {
        @Override public int statusCode() { return 200; }
        @Override public Optional<HttpResponse<T>> previousResponse() { return Optional.empty(); }
        @Override public HttpHeaders headers() { return HttpHeaders.of(Map.of(), (a, b) -> true); }
        @Override public Optional<SSLSession> sslSession() { return Optional.empty(); }
        @Override public URI uri() { return request.uri(); }
        @Override public HttpClient.Version version() { return HttpClient.Version.HTTP_1_1; }
    }
}

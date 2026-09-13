package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "tekroo:", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(arguments) > 0 && arguments[0] == "init-local" {
		return runLocalInit(arguments[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("tekroo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "absolute path to tekrood JSON configuration")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	remaining := flags.Args()
	if *configPath == "" || len(remaining) == 0 {
		return usageError()
	}
	config, err := operationalruntime.LoadProductionConfig(*configPath)
	if err != nil {
		return err
	}
	tokenPath := config.Operator.BearerTokenFile
	tokenRaw, err := os.ReadFile(tokenPath)
	if err != nil {
		return fmt.Errorf("read operator token: %w", err)
	}
	token := strings.TrimSpace(string(tokenRaw))
	timeout, err := time.ParseDuration(config.Operator.OperationTimeout)
	if err != nil || timeout <= 0 || len(token) < 32 {
		return errors.New("operator client configuration is invalid")
	}
	client, err := newOperatorClient("http://"+config.Operator.Address, token, timeout, config.Operator.MaximumBodyBytes)
	if err != nil {
		return err
	}
	command := remaining[0]
	operands := remaining[1:]
	var method, path string
	var body []byte
	requestTimeout := timeout
	switch command {
	case "health":
		method, path, err = noOperand(http.MethodGet, "/health", operands)
	case "status":
		method, path, err = noOperand(http.MethodGet, "/v1/status", operands)
	case "pause":
		method, path, err = noOperand(http.MethodPost, "/v1/pause", operands)
	case "resume":
		method, path, err = noOperand(http.MethodPost, "/v1/resume", operands)
	case "stop":
		method, path, err = noOperand(http.MethodPost, "/v1/stop", operands)
	case "task":
		method, path, err = identityRead("/v1/tasks/", operands)
	case "story":
		method, path, err = identityRead("/v1/stories/", operands)
	case "invocation":
		method, path, err = identityRead("/v1/invocations/", operands)
	case "wait":
		method, path, body, requestTimeout, err = eventWaitOperation(operands)
	case "roles":
		method, path, err = noOperand(http.MethodGet, "/v1/roles", operands)
	case "libraries":
		method, path, err = noOperand(http.MethodGet, "/v1/libraries", operands)
	case "library-sync":
		method, path, err = noOperand(http.MethodPost, "/v1/libraries/sync", operands)
	case "diagnostics":
		method, path, err = noOperand(http.MethodGet, "/v1/diagnostics", operands)
	case "federation":
		method, path, err = noOperand(http.MethodGet, "/v1/federation", operands)
	case "federation-alias":
		if len(operands) != 1 || operands[0] == "" || strings.Contains(operands[0], "/") {
			err = usageError()
		} else {
			method, path = http.MethodGet, "/v1/federation/aliases/"+url.PathEscape(operands[0])
		}
	case "federation-send":
		method, path = http.MethodPost, "/v1/federation/messages"
		body, err = loadFederatedMessage(operands, stdin, config.Operator.MaximumBodyBytes)
	case "feature":
		method, path = http.MethodPost, "/v1/features"
		body, err = loadFeature(operands, stdin, config.Operator.MaximumBodyBytes)
	case "feature-status":
		method, path, err = identityRead("/v1/features/", operands)
	case "feature-clarify":
		method, path, body, err = loadFeatureClarification(operands, stdin, config.Operator.MaximumBodyBytes)
	case "feature-plan":
		method, path, body, err = loadFeaturePlan(operands, stdin, config.Operator.MaximumBodyBytes)
	case "feature-replan":
		method, path, body, err = loadFeatureReplan(operands, stdin, config.Operator.MaximumBodyBytes)
	case "feature-accept":
		method, path, body, err = featureAccept(operands)
	case "feature-release":
		method, path, body, err = loadFeatureRelease(operands, stdin, config.Operator.MaximumBodyBytes)
	case "human-register":
		method, path = http.MethodPost, "/v1/humans"
		body, err = loadHumanRegistration(operands, stdin, config.Operator.MaximumBodyBytes)
	case "human-ask":
		method, path = http.MethodPost, "/v1/human-questions"
		body, err = loadHumanQuestion(operands, stdin, config.Operator.MaximumBodyBytes)
	case "human-notifications":
		method, path, err = humanNotificationsRead(operands)
	case "human-respond":
		method, path = http.MethodPost, "/v1/human-responses"
		body, err = loadHumanResponse(operands, stdin, config.Operator.MaximumBodyBytes)
	case "human-interaction":
		method, path, err = identityRead("/v1/human-interactions/", operands)
	case "role":
		method, path, err = roleOperation(operands)
	case "inbox":
		method, path, err = actorRead("/v1/roles/", "/inbox", operands)
	case "message":
		method, path = http.MethodPost, "/v1/messages"
		body, err = loadMessage(operands, stdin, config.Operator.MaximumBodyBytes)
	case "message-status":
		method, path, err = identityRead("/v1/messages/", operands)
	case "message-trace":
		method, path, err = identityRead("/v1/message-threads/", operands)
	case "deadletters":
		method, path, err = deadLetterRead(operands)
	case "deadletter-repair":
		method, path, body, err = loadDeadLetterRepair(operands, stdin, config.Operator.MaximumBodyBytes)
	case "submit":
		method, path = http.MethodPost, "/v1/commands"
		body, _, err = loadCommand(operands, stdin, config.Operator.MaximumBodyBytes)
	case "cancel":
		method, path, body, err = loadCancellation(operands, stdin, config.Operator.MaximumBodyBytes)
	case "retry-planning":
		method, path, body, err = loadPlanningRecovery(operands, stdin, config.Operator.MaximumBodyBytes)
	case "retry-task":
		method, path, body, err = loadTaskRecovery(operands, stdin, config.Operator.MaximumBodyBytes)
	default:
		err = usageError()
	}
	if err != nil {
		return err
	}
	client.http.Timeout = requestTimeout
	response, err := client.request(context.Background(), method, path, body)
	if err != nil {
		return err
	}
	if _, err := stdout.Write(response); err != nil {
		return err
	}
	if len(response) == 0 || response[len(response)-1] != '\n' {
		_, err = io.WriteString(stdout, "\n")
	}
	return err
}

func eventWaitOperation(operands []string) (string, string, []byte, time.Duration, error) {
	if len(operands) < 4 {
		return "", "", nil, 0, usageError()
	}
	request := operationalruntime.EventWaitRequest{
		Aggregate: kernel.AggregateRef{Kind: kernel.AggregateKind(operands[0]), ID: kernel.UUIDv7(operands[1])},
	}
	var wait time.Duration
	for index := 2; index < len(operands); {
		if index+1 >= len(operands) {
			return "", "", nil, 0, usageError()
		}
		switch operands[index] {
		case "--after-revision":
			value, err := strconv.ParseUint(operands[index+1], 10, 64)
			if err != nil {
				return "", "", nil, 0, usageError()
			}
			request.AfterRevision = value
		case "--timeout":
			value, err := time.ParseDuration(operands[index+1])
			if err != nil || value <= 0 || value > operationalruntime.MaximumEventWait {
				return "", "", nil, 0, usageError()
			}
			wait = value
			request.TimeoutMillis = uint64(value / time.Millisecond)
		case "--event-type":
			request.EventTypes = append(request.EventTypes, operands[index+1])
		default:
			return "", "", nil, 0, usageError()
		}
		index += 2
	}
	if wait == 0 || !request.Valid() {
		return "", "", nil, 0, usageError()
	}
	body, err := json.Marshal(request)
	if err != nil {
		return "", "", nil, 0, err
	}
	return http.MethodPost, "/v1/events/wait", body, wait + 5*time.Second, nil
}

func roleOperation(operands []string) (string, string, error) {
	if len(operands) != 2 || !kernel.ActorFQN(operands[0]).Valid() {
		return "", "", usageError()
	}
	switch operands[1] {
	case "start", "stop", "restart", "pause", "resume":
		return http.MethodPost, "/v1/roles/" + operands[0] + "/" + operands[1], nil
	default:
		return "", "", usageError()
	}
}

func featureAccept(operands []string) (string, string, []byte, error) {
	if len(operands) != 3 || !kernel.UUIDv7(operands[0]).Valid() || operands[2] == "" || len(operands[2]) > 4096 {
		return "", "", nil, usageError()
	}
	revision, err := strconv.ParseUint(operands[1], 10, 64)
	if err != nil || revision == 0 {
		return "", "", nil, usageError()
	}
	body, err := json.Marshal(map[string]any{"expected_revision": revision, "no_release_reason": operands[2]})
	if err != nil {
		return "", "", nil, err
	}
	return http.MethodPost, "/v1/features/" + operands[0] + "/accept", body, nil
}

func actorRead(prefix, suffix string, operands []string) (string, string, error) {
	if len(operands) != 1 || !kernel.ActorFQN(operands[0]).Valid() {
		return "", "", usageError()
	}
	return http.MethodGet, prefix + operands[0] + suffix, nil
}

func deadLetterRead(operands []string) (string, string, error) {
	if len(operands) == 0 {
		return http.MethodGet, "/v1/dead-letters", nil
	}
	if len(operands) != 1 || !kernel.ActorFQN(operands[0]).Valid() {
		return "", "", usageError()
	}
	return http.MethodGet, "/v1/dead-letters?recipient=" + url.QueryEscape(operands[0]), nil
}

func loadDeadLetterRepair(operands []string, stdin io.Reader, maximum int64) (string, string, []byte, error) {
	if len(operands) != 2 || !kernel.UUIDv7(operands[0]).Valid() {
		return "", "", nil, usageError()
	}
	raw, err := loadDocument(operands[1:], stdin, maximum)
	if err != nil {
		return "", "", nil, err
	}
	var successor organization.OrganizationalMessage
	if err := strictDocument(raw, &successor); err != nil || successor.Validate() != nil {
		return "", "", nil, errors.New("dead-letter successor document is invalid")
	}
	return http.MethodPost, "/v1/dead-letters/" + operands[0] + "/repair", raw, nil
}

func loadMessage(operands []string, stdin io.Reader, maximum int64) ([]byte, error) {
	raw, err := loadDocument(operands, stdin, maximum)
	if err != nil {
		return nil, err
	}
	var message organization.OrganizationalMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil || message.Validate() != nil {
		return nil, errors.New("message document is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("message document contains trailing JSON content")
	}
	return raw, nil
}

func loadFederatedMessage(operands []string, stdin io.Reader, maximum int64) ([]byte, error) {
	if len(operands) != 2 || operands[0] == "" || strings.ContainsAny(operands[0], "/*?") {
		return nil, usageError()
	}
	raw, err := loadDocument(operands[1:], stdin, maximum)
	if err != nil {
		return nil, err
	}
	var message organization.OrganizationalMessage
	if err := strictDocument(raw, &message); err != nil || message.SchemaVersion != organization.OrganizationalMessageSchemaVersion || !message.ID.Valid() || !message.Sender.Valid() || !message.SenderExecution.Valid() {
		return nil, errors.New("federated message document is invalid")
	}
	return json.Marshal(map[string]any{"alias": operands[0], "message": message})
}

func loadFeature(operands []string, stdin io.Reader, maximum int64) ([]byte, error) {
	raw, err := loadDocument(operands, stdin, maximum)
	if err != nil {
		return nil, err
	}
	var input organization.FeatureRequestInput
	if err := strictDocument(raw, &input); err != nil || input.Validate() != nil {
		return nil, errors.New("feature request document is invalid")
	}
	return raw, nil
}

func loadHumanRegistration(operands []string, stdin io.Reader, maximum int64) ([]byte, error) {
	raw, err := loadDocument(operands, stdin, maximum)
	if err != nil {
		return nil, err
	}
	var input organization.HumanParticipantRegistration
	if err := strictDocument(raw, &input); err != nil || !input.Valid() {
		return nil, errors.New("human registration document is invalid")
	}
	return raw, nil
}

func loadHumanQuestion(operands []string, stdin io.Reader, maximum int64) ([]byte, error) {
	raw, err := loadDocument(operands, stdin, maximum)
	if err != nil {
		return nil, err
	}
	var input organization.HumanQuestionRequest
	if err := strictDocument(raw, &input); err != nil {
		return nil, errors.New("human question document is invalid")
	}
	return raw, nil
}

func loadHumanResponse(operands []string, stdin io.Reader, maximum int64) ([]byte, error) {
	raw, err := loadDocument(operands, stdin, maximum)
	if err != nil {
		return nil, err
	}
	var input organization.HumanResponseInput
	if err := strictDocument(raw, &input); err != nil || !input.Valid() {
		return nil, errors.New("human response document is invalid")
	}
	return raw, nil
}

func humanNotificationsRead(operands []string) (string, string, error) {
	if len(operands) == 0 || len(operands) == 1 && operands[0] == "open" {
		return http.MethodGet, "/v1/human-notifications?open_only=true", nil
	}
	if len(operands) == 1 && operands[0] == "all" {
		return http.MethodGet, "/v1/human-notifications?open_only=false", nil
	}
	return "", "", usageError()
}

func loadFeaturePlan(operands []string, stdin io.Reader, maximum int64) (string, string, []byte, error) {
	if len(operands) != 2 || !kernel.UUIDv7(operands[0]).Valid() {
		return "", "", nil, usageError()
	}
	raw, err := loadDocument(operands[1:], stdin, maximum)
	if err != nil {
		return "", "", nil, err
	}
	var input struct {
		ExpectedRevision uint64                   `json:"expected_revision"`
		Plan             organization.FeaturePlan `json:"plan"`
	}
	if err := strictDocument(raw, &input); err != nil || input.ExpectedRevision == 0 {
		return "", "", nil, errors.New("feature plan document is invalid")
	}
	return http.MethodPost, "/v1/features/" + operands[0] + "/plan", raw, nil
}

func loadFeatureClarification(operands []string, stdin io.Reader, maximum int64) (string, string, []byte, error) {
	if len(operands) != 2 || !kernel.UUIDv7(operands[0]).Valid() {
		return "", "", nil, usageError()
	}
	raw, err := loadDocument(operands[1:], stdin, maximum)
	if err != nil {
		return "", "", nil, err
	}
	var input organization.FeatureClarificationResponseInput
	if err := strictDocument(raw, &input); err != nil || !input.Valid() {
		return "", "", nil, errors.New("feature clarification response document is invalid")
	}
	return http.MethodPost, "/v1/features/" + operands[0] + "/clarifications", raw, nil
}

func loadFeatureReplan(operands []string, stdin io.Reader, maximum int64) (string, string, []byte, error) {
	if len(operands) != 2 || !kernel.UUIDv7(operands[0]).Valid() {
		return "", "", nil, usageError()
	}
	raw, err := loadDocument(operands[1:], stdin, maximum)
	if err != nil {
		return "", "", nil, err
	}
	var input operationalruntime.FeatureReplanRequest
	if err := strictDocument(raw, &input); err != nil || !input.Valid() {
		return "", "", nil, errors.New("feature replan document is invalid")
	}
	return http.MethodPost, "/v1/features/" + operands[0] + "/replan", raw, nil
}

func loadFeatureRelease(operands []string, stdin io.Reader, maximum int64) (string, string, []byte, error) {
	if len(operands) != 2 || !kernel.UUIDv7(operands[0]).Valid() {
		return "", "", nil, usageError()
	}
	raw, err := loadDocument(operands[1:], stdin, maximum)
	if err != nil {
		return "", "", nil, err
	}
	var input organization.FeatureAcceptanceInput
	if err := strictDocument(raw, &input); err != nil || input.Mode != organization.FeatureAcceptanceCode || input.ExpectedRevision == 0 {
		return "", "", nil, errors.New("feature release document is invalid")
	}
	return http.MethodPost, "/v1/features/" + operands[0] + "/release", raw, nil
}

func strictDocument(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("document contains trailing JSON content")
	}
	return nil
}

func loadDocument(operands []string, stdin io.Reader, maximum int64) ([]byte, error) {
	if len(operands) != 1 {
		return nil, usageError()
	}
	var source io.Reader
	if operands[0] == "-" {
		source = stdin
	} else {
		file, err := os.Open(operands[0])
		if err != nil {
			return nil, err
		}
		defer file.Close()
		source = file
	}
	raw, err := io.ReadAll(io.LimitReader(source, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, errors.New("input file is unreadable or exceeds configured limit")
	}
	return raw, nil
}

type operatorClient struct {
	baseURL string
	token   string
	http    *http.Client
	maximum int64
}

func newOperatorClient(baseURL, token string, timeout time.Duration, maximum int64) (*operatorClient, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Hostname() == "" || parsed.Port() == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !loopbackHost(parsed.Hostname()) {
		return nil, errors.New("operator endpoint must be loopback")
	}
	if len(token) < 32 || timeout <= 0 || maximum <= 0 || maximum > 1<<20 {
		return nil, errors.New("operator client configuration is invalid")
	}
	return &operatorClient{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: &http.Client{Timeout: timeout}, maximum: maximum}, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func (client *operatorClient) request(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	if int64(len(body)) > client.maximum {
		return nil, errors.New("request body exceeds configured limit")
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, client.maximum+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > client.maximum {
		return nil, errors.New("response body exceeds configured limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("operator request failed with HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func noOperand(method, path string, operands []string) (string, string, error) {
	if len(operands) != 0 {
		return "", "", usageError()
	}
	return method, path, nil
}

func identityRead(prefix string, operands []string) (string, string, error) {
	if len(operands) != 1 || !kernel.UUIDv7(operands[0]).Valid() {
		return "", "", usageError()
	}
	return http.MethodGet, prefix + operands[0], nil
}

func loadCancellation(operands []string, stdin io.Reader, maximum int64) (string, string, []byte, error) {
	if len(operands) != 2 || !kernel.UUIDv7(operands[0]).Valid() {
		return "", "", nil, usageError()
	}
	raw, err := loadDocument(operands[1:], stdin, maximum)
	if err != nil {
		return "", "", nil, err
	}
	var input operationalruntime.CancellationRequest
	if err := strictDocument(raw, &input); err != nil || !input.Valid() {
		return "", "", nil, errors.New("cancellation request is invalid")
	}
	return http.MethodPost, "/v1/invocations/" + operands[0] + "/cancel", raw, nil
}

func loadPlanningRecovery(operands []string, stdin io.Reader, maximum int64) (string, string, []byte, error) {
	if len(operands) != 2 || !kernel.UUIDv7(operands[0]).Valid() {
		return "", "", nil, usageError()
	}
	raw, err := loadDocument(operands[1:], stdin, maximum)
	if err != nil {
		return "", "", nil, err
	}
	var input operationalruntime.PlanningRecoveryRequest
	if err := strictDocument(raw, &input); err != nil || !input.Valid() {
		return "", "", nil, errors.New("planning recovery request is invalid")
	}
	return http.MethodPost, "/v1/invocations/" + operands[0] + "/retry-planning", raw, nil
}

func loadTaskRecovery(operands []string, stdin io.Reader, maximum int64) (string, string, []byte, error) {
	if len(operands) != 2 || !kernel.UUIDv7(operands[0]).Valid() {
		return "", "", nil, usageError()
	}
	raw, err := loadDocument(operands[1:], stdin, maximum)
	if err != nil {
		return "", "", nil, err
	}
	var input operationalruntime.TaskRecoveryRequest
	if err := strictDocument(raw, &input); err != nil || !input.Valid() {
		return "", "", nil, errors.New("task recovery request is invalid")
	}
	return http.MethodPost, "/v1/invocations/" + operands[0] + "/retry-task", raw, nil
}

func loadCommand(operands []string, stdin io.Reader, maximum int64) ([]byte, kernel.KernelCommand, error) {
	raw, err := loadDocument(operands, stdin, maximum)
	if err != nil {
		return nil, kernel.KernelCommand{}, err
	}
	var command kernel.KernelCommand
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		return nil, kernel.KernelCommand{}, fmt.Errorf("decode command: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, kernel.KernelCommand{}, errors.New("command contains trailing JSON content")
	}
	return raw, command, nil
}

func usageError() error {
	return errors.New("usage: tekroo init-local [OPTIONS] | tekroo -config CONFIG health|status|diagnostics|federation|federation-alias NAME|federation-send ALIAS FILE|-|pause|resume|stop|feature FILE|-|feature-status ID|feature-clarify ID FILE|-|feature-plan ID FILE|-|feature-replan ID FILE|-|feature-accept ID REVISION NO_RELEASE_REASON|feature-release ID FILE|-|human-register FILE|-|human-ask FILE|-|human-notifications [open|all]|human-respond FILE|-|human-interaction ID|roles|libraries|library-sync|role ACTOR start|stop|restart|pause|resume|inbox ACTOR|message FILE|-|message-status ID|message-trace THREAD_ID|deadletters [ACTOR]|deadletter-repair ID FILE|-|task ID|story ID|invocation ID|wait KIND ID --timeout DURATION [--after-revision REVISION] [--event-type TYPE]...|submit FILE|-|cancel INVOCATION_ID FILE|-|retry-planning INVOCATION_ID FILE|-|retry-task INVOCATION_ID FILE|-")
}

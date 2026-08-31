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
	"strings"
	"time"

	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/kernel"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "teamsctl:", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("teamsctl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "absolute path to teamsd JSON configuration")
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
	case "submit":
		method, path = http.MethodPost, "/v1/commands"
		body, _, err = loadCommand(operands, stdin, config.Operator.MaximumBodyBytes)
	case "cancel":
		method, path = http.MethodPost, "/v1/commands"
		body, err = loadCancellation(operands, stdin, config.Operator.MaximumBodyBytes)
	default:
		err = usageError()
	}
	if err != nil {
		return err
	}
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

func loadCancellation(operands []string, stdin io.Reader, maximum int64) ([]byte, error) {
	if len(operands) != 2 || !kernel.UUIDv7(operands[0]).Valid() {
		return nil, usageError()
	}
	raw, command, err := loadCommand(operands[1:], stdin, maximum)
	if err != nil {
		return nil, err
	}
	if command.CommandType != "tekroo.command.work-invocation.request-cancellation" || command.Target.Kind != kernel.AggregateWorkInvocation || command.Target.ID != kernel.UUIDv7(operands[0]) {
		return nil, errors.New("cancellation command does not match the exact invocation")
	}
	return raw, nil
}

func loadCommand(operands []string, stdin io.Reader, maximum int64) ([]byte, kernel.KernelCommand, error) {
	if len(operands) != 1 {
		return nil, kernel.KernelCommand{}, usageError()
	}
	var source io.Reader
	if operands[0] == "-" {
		source = stdin
	} else {
		file, err := os.Open(operands[0])
		if err != nil {
			return nil, kernel.KernelCommand{}, err
		}
		defer file.Close()
		source = file
	}
	raw, err := io.ReadAll(io.LimitReader(source, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, kernel.KernelCommand{}, errors.New("command file is unreadable or exceeds configured limit")
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
	return errors.New("usage: teamsctl -config /absolute/path/teamsd.json health|status|pause|resume|stop|task ID|story ID|invocation ID|submit FILE|-|cancel INVOCATION_ID FILE|-")
}

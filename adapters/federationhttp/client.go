package federationhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tekroo-ai/teams/organization"
)

type Client struct {
	httpClient *http.Client
	maxBody    int64
}

func NewClient(httpClient *http.Client, maximumBodyBytes int64) (*Client, error) {
	if httpClient == nil || httpClient.Timeout <= 0 || maximumBodyBytes <= 0 || maximumBodyBytes > DefaultMaximumBodyBytes {
		return nil, ErrInvalidConfiguration
	}
	return &Client{httpClient: httpClient, maxBody: maximumBodyBytes}, nil
}

// Send performs at most two identical POSTs. The second POST is not a new
// delivery attempt: it reconciles an uncertain first result using the same
// signed delivery and replay identities, which the receiver handles
// idempotently.
func (client *Client) Send(ctx context.Context, route organization.FederationRoute, envelope organization.FederatedEnvelope) (organization.FederationDeliveryReceipt, error) {
	if client == nil || client.httpClient == nil || route.Validate() != nil || envelope.RouteID != route.RouteID || envelope.RouteRevision != route.Revision || envelope.DestinationDeployment != route.DestinationDeployment {
		return organization.FederationDeliveryReceipt{}, ErrInvalidConfiguration
	}
	raw, err := json.Marshal(envelope)
	if err != nil || int64(len(raw)) > client.maxBody {
		return organization.FederationDeliveryReceipt{}, ErrInvalidConfiguration
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		receipt, retry, sendErr := client.sendOnce(ctx, route.Endpoint, raw, envelope)
		if sendErr == nil {
			return receipt, nil
		}
		last = sendErr
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return organization.FederationDeliveryReceipt{}, last
}

func (client *Client) sendOnce(ctx context.Context, endpoint string, raw []byte, envelope organization.FederatedEnvelope) (organization.FederationDeliveryReceipt, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return organization.FederationDeliveryReceipt{}, false, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return organization.FederationDeliveryReceipt{}, true, err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, client.maxBody+1)
	body, err := io.ReadAll(limited)
	if err != nil || int64(len(body)) > client.maxBody {
		return organization.FederationDeliveryReceipt{}, true, errors.Join(ErrInvalidConfiguration, err)
	}
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return organization.FederationDeliveryReceipt{}, response.StatusCode >= 500, fmt.Errorf("federation ingress returned HTTP %d", response.StatusCode)
	}
	var receipt organization.FederationDeliveryReceipt
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil || !receipt.Valid() || receipt.DeliveryID != envelope.DeliveryID || receipt.ReplayID != envelope.ReplayID || receipt.RouteID != envelope.RouteID || receipt.RouteRevision != envelope.RouteRevision || receipt.MessageSHA256 != envelope.MessageSHA256 {
		return organization.FederationDeliveryReceipt{}, false, ErrInvalidConfiguration
	}
	return receipt, false, nil
}

func NewBoundedHTTPClient(timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &http.Client{Timeout: timeout}, nil
}

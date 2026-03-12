/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HostResponse represents the response from the inventory service
type HostResponse struct {
	Hosts []Host `json:"nodes"`
}

// Host represents a single host from the inventory
// TODO: temporary strictly for OpenStack Ironic nodes
type Host struct {
	NodeId         string         `json:"uuid"`
	HostClass      string         `json:"resource_class"`
	ProvisionState string         `json:"provision_state"` // available, active, etc
	Extra          map[string]any `json:"extra"`           // contains matchType and clusterId
}

type InventoryClient struct {
	httpClient    *http.Client
	inventoryURL  *url.URL
	managementURL *url.URL
	authToken     string
}

func NewInventoryClient(httpClient *http.Client, inventoryURL *url.URL, managementURL *url.URL, authToken string) *InventoryClient {
	return &InventoryClient{
		httpClient:    httpClient,
		inventoryURL:  inventoryURL,
		managementURL: managementURL,
		authToken:     authToken,
	}
}

// getHostsOptions holds configuration for GetHosts
type getHostsOptions struct {
	hostClass *string
	matchType *string
	count     int
}

// GetHostsOption is a function that configures getHostsOptions
type GetHostsOption func(*getHostsOptions)

// WithHostClass sets the host class filter
func WithHostClass(hostClass string) GetHostsOption {
	return func(o *getHostsOptions) {
		o.hostClass = &hostClass
	}
}

// WithMatchType sets the match type filter
func WithMatchType(matchType string) GetHostsOption {
	return func(o *getHostsOptions) {
		o.matchType = &matchType
	}
}

// WithCount sets the maximum number of hosts to return
func WithCount(count int) GetHostsOption {
	return func(o *getHostsOptions) {
		if count < 0 {
			count = 0
		}
		o.count = count
	}
}

// GetHosts retrieves hosts from the inventory service
func (c *InventoryClient) GetHosts(
	ctx context.Context,
	clusterId string,
	opts ...GetHostsOption,
) ([]Host, error) {
	options := &getHostsOptions{}
	for _, opt := range opts {
		opt(options)
	}
	log := logf.FromContext(ctx).V(1)
	ctx = logf.IntoContext(ctx, log)

	inventoryURL := *c.inventoryURL
	inventoryURL.Path = "/v1/nodes/detail"
	query := url.Values{}
	// TODO: set query values for pluggable bare metal adapter
	inventoryURL.RawQuery = query.Encode()

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, inventoryURL.String(), nil)
	if err != nil {
		log.Error(err, "Failed to create NewRequestWithContext", "method", "getHosts")
		return nil, err
	}

	httpRequest.Header.Set("X-Auth-Token", c.authToken)
	httpRequest.Header.Set("X-OpenStack-Ironic-API-Version", "1.69")

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		log.Error(err, "Failed to perform request", "method", "getHosts")
		return nil, err
	}
	defer func() {
		err := response.Body.Close()
		if err != nil {
			log.Error(err, "Failed to close connection", "method", "getHosts")
		}
	}()

	if response.StatusCode != http.StatusOK {
		message, err := io.ReadAll(response.Body)
		if err != nil {
			log.Error(err, "Failed to read response body", "method", "getHosts")
			return nil, err
		}
		err = errors.New(string(message))
		return nil, err
	}

	hostResponse := HostResponse{}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&hostResponse); err != nil {
		log.Error(err, "Failed to decode response body", "method", "getHosts")
		return nil, err
	}

	// assume filters don't work on the inventory
	hosts := []Host{}
	for _, host := range hostResponse.Hosts {
		if options.hostClass != nil && host.HostClass != *options.hostClass {
			continue
		}

		hostMatchType := ""
		hostClusterId := ""
		if host.Extra != nil {
			if mt, ok := host.Extra["matchType"].(string); ok {
				hostMatchType = mt
			}
			if cid, ok := host.Extra["clusterId"].(string); ok {
				hostClusterId = cid
			}
		}

		if options.matchType != nil && hostMatchType != *options.matchType {
			continue
		}

		if hostClusterId != clusterId {
			continue
		}

		hosts = append(hosts, host)

		if options.count > 0 && len(hosts) >= options.count {
			break
		}
	}

	log.Info("Successfully queried for hosts", "hosts", hosts)

	return hosts, nil
}

// PatchInventoryHostClusterId updates the cluster ID for a host in the inventory
func (c *InventoryClient) PatchInventoryHostClusterId(
	ctx context.Context,
	nodeId string,
	clusterId string,
) error {
	log := logf.FromContext(ctx).V(1)

	managementURL := *c.managementURL
	managementURL.Path = "/v1/nodes/" + nodeId

	patchBody := []map[string]string{
		{
			"op":    "replace",
			"path":  "/extra/clusterId",
			"value": clusterId,
		},
	}

	bodyBytes, err := json.Marshal(patchBody)
	if err != nil {
		log.Error(err, "Failed to marshal request body", "method", "patchInventoryHostClusterId")
		return err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPatch, managementURL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		log.Error(err, "Failed to create NewRequestWithContext", "method", "patchInventoryHostClusterId")
		return err
	}

	httpRequest.Header.Set("X-Auth-Token", c.authToken)
	httpRequest.Header.Set("X-OpenStack-Ironic-API-Version", "1.69")
	httpRequest.Header.Set("Content-Type", "application/json-patch+json")

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		log.Error(err, "Failed to perform request", "method", "patchInventoryHostClusterId")
		return err
	}
	defer func() {
		err := response.Body.Close()
		if err != nil {
			log.Error(err, "Failed to close connection", "method", "patchInventoryHostClusterId")
		}
	}()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, err := io.ReadAll(response.Body)
		if err != nil {
			log.Error(err, "Failed to read response body", "method", "patchInventoryHostClusterId")
			return err
		}
		err = errors.New(string(message))
		return err
	}

	log.Info("Successfully patched host", "host", nodeId)

	return nil
}

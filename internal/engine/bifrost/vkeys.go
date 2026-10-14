package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// managedDescription marks the engine objects remora owns.
const managedDescription = "managed by remora"

// VirtualKey is a front-door virtual key. Value (the engine's own key, sent upstream in x-bf-vk) is set by the
// engine and never reaches a user.
type VirtualKey struct {
	ID, Name, Value string
	Active          bool
}

type virtualKeyBody struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	IsActive          bool   `json:"is_active"`
	AllowAllProviders bool   `json:"allow_all_providers"`
}

type virtualKeyAnswer struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Value    secretValue `json:"value"`
	IsActive bool        `json:"is_active"`
}

func (a virtualKeyAnswer) toVirtualKey() VirtualKey {
	return VirtualKey{ID: a.ID, Name: a.Name, Value: string(a.Value), Active: a.IsActive}
}

// secretValue reads the engine's secret field in both forms it answers: a plain string (the virtual key table's
// own encoding, used by every governance answer) and the {"value":".."} object of its generic secret type.
type secretValue string

func (s *secretValue) UnmarshalJSON(data []byte) error {
	var plain string
	if err := json.Unmarshal(data, &plain); err == nil {
		*s = secretValue(plain)
		return nil
	}
	var obj struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("bifrost: decode secret value: %w", err)
	}
	*s = secretValue(obj.Value)
	return nil
}

func virtualKeyWire(v VirtualKey) virtualKeyBody {
	return virtualKeyBody{Name: v.Name, Description: managedDescription, IsActive: v.Active, AllowAllProviders: true}
}

func virtualKeyPath(id string) string {
	return "/api/governance/virtual-keys/" + url.PathEscape(id)
}

// ListVirtualKeys returns every virtual key.
func (c *Client) ListVirtualKeys(ctx context.Context) ([]VirtualKey, error) {
	var out struct {
		VirtualKeys []virtualKeyAnswer `json:"virtual_keys"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/governance/virtual-keys", nil, &out); err != nil {
		return nil, err
	}
	vks := make([]VirtualKey, 0, len(out.VirtualKeys))
	for _, a := range out.VirtualKeys {
		vks = append(vks, a.toVirtualKey())
	}
	return vks, nil
}

// CreateVirtualKey adds a virtual key and returns it with the engine's ID and Value.
func (c *Client) CreateVirtualKey(ctx context.Context, v VirtualKey) (VirtualKey, error) {
	var out struct {
		VirtualKey *virtualKeyAnswer `json:"virtual_key"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/governance/virtual-keys", virtualKeyWire(v), &out); err != nil {
		return VirtualKey{}, err
	}
	if out.VirtualKey == nil || out.VirtualKey.ID == "" || out.VirtualKey.Value == "" {
		return VirtualKey{}, fmt.Errorf("bifrost: create virtual key: answer has no id or value")
	}
	return out.VirtualKey.toVirtualKey(), nil
}

// UpdateVirtualKey sets a virtual key's name and active flag.
func (c *Client) UpdateVirtualKey(ctx context.Context, v VirtualKey) error {
	if err := requireID("virtual key id", v.ID); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, virtualKeyPath(v.ID), virtualKeyWire(v), nil)
}

// DeleteVirtualKey removes a virtual key.
func (c *Client) DeleteVirtualKey(ctx context.Context, id string) error {
	if err := requireID("virtual key id", id); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, virtualKeyPath(id), nil, nil)
}

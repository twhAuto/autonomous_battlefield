package members

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type GroupWriter interface {
	Replace(context.Context, []string) error
}
type Cloudflare struct {
	Config Config
	Client *http.Client
}

// Replace touches only the configured dedicated group, never Access policies or sessions.
func (c *Cloudflare) Replace(ctx context.Context, emails []string) error {
	include := []any{}
	exclude := []any{}
	for _, email := range emails {
		include = append(include, map[string]any{"email": map[string]string{"email": email}})
	}
	if len(include) == 0 {
		include = append(include, map[string]any{"everyone": map[string]any{}})
		exclude = append(exclude, map[string]any{"everyone": map[string]any{}})
	}
	raw, _ := json.Marshal(map[string]any{"name": "Battlefield managed Players", "include": include, "exclude": exclude, "require": []any{}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "https://api.cloudflare.com/client/v4/accounts/"+c.Config.AccountID+"/access/groups/"+c.Config.GroupID, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("Cloudflare request configuration invalid")
	}
	req.Header.Set("Authorization", "Bearer "+c.Config.Token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	if c.Client != nil {
		*client = *c.Client
	}
	// Never forward the credential through a redirect, including same-host redirects.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Cloudflare request failed; retry pending")
	}
	defer resp.Body.Close()
	var result struct {
		Success bool `json:"success"`
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Cloudflare returned HTTP %d", resp.StatusCode)
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || !result.Success {
		return fmt.Errorf("Cloudflare did not confirm the group update")
	}
	return nil
}

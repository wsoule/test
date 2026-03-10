package analytics

import (
	"log"

	"github.com/posthog/posthog-go"
)

// Client wraps the PostHog client with nil-safe methods.
// A zero-value Client is a no-op (safe to use without initialization).
type Client struct {
	ph posthog.Client
}

// New creates a PostHog analytics client. Returns a no-op client if apiKey is empty.
func New(apiKey string) *Client {
	if apiKey == "" {
		return &Client{}
	}
	ph, err := posthog.NewWithConfig(apiKey, posthog.Config{
		Endpoint: "https://us.i.posthog.com",
	})
	if err != nil {
		log.Printf("analytics: failed to init posthog: %v", err)
		return &Client{}
	}
	return &Client{ph: ph}
}

// Close flushes pending events and closes the client.
func (c *Client) Close() {
	if c.ph != nil {
		c.ph.Close()
	}
}

// Capture enqueues an event asynchronously. Safe to call on a no-op client.
func (c *Client) Capture(distinctID, event string, props map[string]interface{}) {
	if c.ph == nil {
		return
	}
	p := posthog.NewProperties()
	for k, v := range props {
		p.Set(k, v)
	}
	_ = c.ph.Enqueue(posthog.Capture{
		DistinctId: distinctID,
		Event:      event,
		Properties: p,
	})
}

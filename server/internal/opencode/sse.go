package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/moonstream-labs/tmux-agents/internal/state"
)

// sseEvent represents a single Server-Sent Event from OpenCode.
type sseEvent struct {
	Type string
	Data string
}

// eventPayload is the common envelope for OpenCode SSE events.
type eventPayload struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type sessionProps struct {
	SessionID string `json:"sessionID"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Directory string `json:"directory"`
	// session.updated nests session data under "info"
	Info struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Directory string `json:"directory"`
		Slug      string `json:"slug"`
	} `json:"info"`
}

// resolvedTitle returns the title from the top level or from info.
func (p *sessionProps) resolvedTitle() string {
	if p.Title != "" {
		return p.Title
	}
	if p.Info.Title != "" {
		return p.Info.Title
	}
	return p.Info.Slug
}

func (p *sessionProps) resolvedDir() string {
	if p.Directory != "" {
		return p.Directory
	}
	return p.Info.Directory
}

func (p *sessionProps) resolvedID() string {
	return coalesce(p.SessionID, p.ID, p.Info.ID)
}

type sessionStatusProps struct {
	SessionID string `json:"sessionID"`
	Status    struct {
		Type string `json:"type"`
	} `json:"status"`
}

type permissionProps struct {
	RequestID string `json:"requestID"`
	SessionID string `json:"sessionID"`
}

type messagePartProps struct {
	SessionID string `json:"sessionID"`
}

// apiSession is the response from GET /session.
type apiSession struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Directory string `json:"directory"`
}

// Client manages the SSE connection to a single OpenCode instance.
type Client struct {
	port       int
	store      *Store
	reconciler *state.Reconciler
	httpClient *http.Client
}

func NewClient(port int, store *Store, reconciler *state.Reconciler) *Client {
	return &Client{
		port:       port,
		store:      store,
		reconciler: reconciler,
		httpClient: &http.Client{Timeout: 0}, // SSE needs no timeout
	}
}

// Run connects to the OpenCode SSE stream and processes events until ctx is cancelled.
// Retries on connection failure with exponential backoff.
func (c *Client) Run(ctx context.Context) {
	// Fetch initial session metadata.
	c.fetchSessions(ctx)

	backoff := time.Second
	maxBackoff := 30 * time.Second

	consecutiveFailures := 0
	maxConsecutiveFailures := 10 // give up after ~5 minutes of failures

	for {
		err := c.connect(ctx)
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			consecutiveFailures++
			if consecutiveFailures >= maxConsecutiveFailures {
				log.Printf("opencode[%d]: giving up after %d consecutive failures", c.port, consecutiveFailures)
				return
			}
			log.Printf("opencode[%d]: SSE error: %v (retry %d/%d in %v)", c.port, err, consecutiveFailures, maxConsecutiveFailures, backoff)
		} else {
			// Connection was established then dropped — reset failure count.
			consecutiveFailures = 0
			backoff = time.Second
			log.Printf("opencode[%d]: SSE connection closed (retry in %v)", c.port, backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Client) connect(ctx context.Context) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/event", c.port)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// Reset backoff on successful connection.
	log.Printf("opencode[%d]: SSE connected", c.port)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentEvent sseEvent

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = event boundary.
			if currentEvent.Data != "" {
				c.handleEvent(ctx, &currentEvent)
			}
			currentEvent = sseEvent{}
			continue
		}

		if after, ok := strings.CutPrefix(line, "event: "); ok {
			currentEvent.Type = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			currentEvent.Data = after
		} else if strings.HasPrefix(line, ":") {
			// Comment line (heartbeat), ignore.
			continue
		}
	}

	return scanner.Err()
}

func (c *Client) handleEvent(ctx context.Context, evt *sseEvent) {
	var payload eventPayload
	if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
		// Some events may not have the envelope format — try type from the event field.
		payload.Type = evt.Type
		payload.Properties = json.RawMessage(evt.Data)
	}

	// Use the SSE event type if available, fall back to payload type.
	eventType := evt.Type
	if eventType == "" {
		eventType = payload.Type
	}

	changed := false

	// Log notable events for debugging (skip heartbeats and high-frequency deltas).
	switch eventType {
	case "server.connected", "server.heartbeat", "message.part.delta":
		// skip logging
	default:
		log.Printf("opencode[%d]: event=%s", c.port, eventType)
	}

	switch eventType {
	case "session.idle":
		var props sessionProps
		json.Unmarshal(payload.Properties, &props)
		sid := props.resolvedID()
		if sid != "" {
			changed = c.store.SetSessionState(c.port, sid, state.StateIdle)
		}

	case "session.status":
		var props sessionStatusProps
		json.Unmarshal(payload.Properties, &props)
		if props.SessionID != "" {
			st := mapOpenCodeStatus(props.Status.Type)
			changed = c.store.SetSessionState(c.port, props.SessionID, st)
		}

	case "session.created":
		var props sessionProps
		json.Unmarshal(payload.Properties, &props)
		sid := props.resolvedID()
		if sid != "" {
			changed = c.store.UpsertSession(c.port, sid, props.resolvedTitle(), props.resolvedDir())
			log.Printf("opencode[%d]: session.created sid=%s title=%q dir=%q", c.port, sid, props.resolvedTitle(), props.resolvedDir())
		}

	case "session.updated":
		var props sessionProps
		json.Unmarshal(payload.Properties, &props)
		sid := props.resolvedID()
		if sid != "" {
			changed = c.store.UpsertSession(c.port, sid, props.resolvedTitle(), props.resolvedDir())
			log.Printf("opencode[%d]: session.updated sid=%s title=%q dir=%q", c.port, sid, props.resolvedTitle(), props.resolvedDir())
		}

	case "session.deleted":
		var props sessionProps
		json.Unmarshal(payload.Properties, &props)
		sid := props.resolvedID()
		if sid != "" {
			c.store.RemoveSession(c.port, sid)
			changed = true
		}

	case "session.error":
		var props sessionProps
		json.Unmarshal(payload.Properties, &props)
		sid := props.resolvedID()
		if sid != "" {
			changed = c.store.SetSessionState(c.port, sid, state.StateIdle)
		}

	case "permission.asked":
		var props permissionProps
		json.Unmarshal(payload.Properties, &props)
		if props.SessionID != "" {
			changed = c.store.SetSessionState(c.port, props.SessionID, state.StatePermission)
		}

	case "permission.replied":
		var props permissionProps
		json.Unmarshal(payload.Properties, &props)
		if props.SessionID != "" {
			// Permission resolved — check actual status via API.
			c.refreshSessionStatus(ctx, props.SessionID)
			changed = true
		}

	case "message.part.updated":
		// Only escalate to running — don't override idle set by session.idle/session.status.
		// The SSE stream can deliver part updates slightly after the idle signal.
		var props messagePartProps
		json.Unmarshal(payload.Properties, &props)
		if props.SessionID != "" {
			changed = c.store.SetSessionStateIf(c.port, props.SessionID, state.StateRunning, state.StateIdle)
		}

	case "server.connected":
		// Initial connection event — refresh all sessions.
		c.fetchSessions(ctx)
		changed = true
	}

	if changed {
		c.reconciler.Reconcile()
	}
}

// fetchSessions queries GET /session to populate the instance's session map.
func (c *Client) fetchSessions(ctx context.Context) {
	url := fmt.Sprintf("http://127.0.0.1:%d/session", c.port)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("opencode[%d]: failed to fetch sessions: %v", c.port, err)
		return
	}
	defer resp.Body.Close()

	var sessions []apiSession
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		log.Printf("opencode[%d]: failed to decode sessions: %v", c.port, err)
		return
	}

	for _, s := range sessions {
		c.store.UpsertSession(c.port, s.ID, s.Title, s.Directory)
	}
	log.Printf("opencode[%d]: fetched %d sessions", c.port, len(sessions))
}

// refreshSessionStatus checks GET /session/status to update all session states.
func (c *Client) refreshSessionStatus(ctx context.Context, _ string) {
	url := fmt.Sprintf("http://127.0.0.1:%d/session/status", c.port)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var statuses map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return
	}

	for sid, raw := range statuses {
		// Status can be a string or an object with a "status" field.
		var statusStr string
		if err := json.Unmarshal(raw, &statusStr); err != nil {
			var statusObj struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(raw, &statusObj); err == nil {
				statusStr = statusObj.Status
			}
		}

		st := mapOpenCodeStatus(statusStr)
		c.store.SetSessionState(c.port, sid, st)
	}
}

func mapOpenCodeStatus(s string) state.SessionState {
	switch s {
	case "idle":
		return state.StateIdle
	case "busy":
		return state.StateRunning
	case "retry":
		return state.StateRunning
	default:
		return state.StateUnknown
	}
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

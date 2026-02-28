package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// linearProvider implements TaskProvider for Linear using the GraphQL API.
type linearProvider struct {
	apiKey   string
	username string
	client   *http.Client

	// Cached viewer ID (resolved on first "My Issues" fetch).
	viewerID string
}

func newLinearProvider() *linearProvider {
	return &linearProvider{
		apiKey:   os.Getenv("LINEAR_API_KEY"),
		username: os.Getenv("LINEAR_USERNAME"),
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *linearProvider) Name() string   { return "Linear" }
func (p *linearProvider) Icon() string   { return "◆" }
func (p *linearProvider) Tabs() []string { return []string{"My Issues", "Projects"} }

func (p *linearProvider) Fetch(tab int, group string) (*TaskResult, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("LINEAR_API_KEY not set — add it to ~/.cogent/settings or .cogent/.env")
	}

	switch tab {
	case 0: // My Issues
		return p.fetchMyIssues()
	case 1: // Projects
		if group == "" {
			return p.fetchProjects()
		}
		return p.fetchProjectIssues(group)
	}
	return &TaskResult{}, nil
}

// ─── GraphQL helpers ────────────────────────────────────────────────────────

type gqlRequest struct {
	Query string `json:"query"`
}

func (p *linearProvider) gql(query string) (map[string]any, error) {
	body, err := json.Marshal(gqlRequest{Query: query})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", "https://api.linear.app/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Linear API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading Linear API response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Linear API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing Linear API response: %w", err)
	}

	// Check for GraphQL errors
	if errs, ok := result["errors"].([]any); ok && len(errs) > 0 {
		if first, ok := errs[0].(map[string]any); ok {
			if msg, ok := first["message"].(string); ok {
				return nil, fmt.Errorf("Linear API error: %s", msg)
			}
		}
		return nil, fmt.Errorf("Linear API returned errors")
	}

	data, ok := result["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Linear API response missing data field")
	}
	return data, nil
}

// ─── Viewer resolution ──────────────────────────────────────────────────────

// resolveViewerID returns the user ID for "My Issues". Uses LINEAR_USERNAME if
// set (fuzzy match), otherwise falls back to the authenticated user (viewer).
func (p *linearProvider) resolveViewerID() (string, error) {
	if p.viewerID != "" {
		return p.viewerID, nil
	}

	if p.username != "" {
		// Look up by username
		data, err := p.gql(`{ users { nodes { id name displayName } } }`)
		if err != nil {
			return "", err
		}
		target := strings.ToLower(p.username)
		if users := jsonPath[[]any](data, "users", "nodes"); users != nil {
			for _, u := range users {
				um, _ := u.(map[string]any)
				name, _ := um["name"].(string)
				display, _ := um["displayName"].(string)
				if strings.Contains(strings.ToLower(name), target) ||
					strings.Contains(strings.ToLower(display), target) {
					p.viewerID, _ = um["id"].(string)
					return p.viewerID, nil
				}
			}
		}
		return "", fmt.Errorf("no Linear user matching %q", p.username)
	}

	// Fall back to viewer (API key owner)
	data, err := p.gql(`{ viewer { id } }`)
	if err != nil {
		return "", err
	}
	if viewer := jsonPath[map[string]any](data, "viewer"); viewer != nil {
		p.viewerID, _ = viewer["id"].(string)
	}
	if p.viewerID == "" {
		return "", fmt.Errorf("could not determine Linear user ID")
	}
	return p.viewerID, nil
}

// ─── Fetch implementations ──────────────────────────────────────────────────

func (p *linearProvider) fetchMyIssues() (*TaskResult, error) {
	userID, err := p.resolveViewerID()
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`{
		issues(
			first: 50
			filter: {
				assignee: { id: { eq: "%s" } }
				state: { type: { nin: ["canceled", "completed"] } }
			}
			orderBy: updatedAt
		) {
			nodes {
				identifier title
				state { name }
				priorityLabel
				assignee { name }
				labels { nodes { name } }
				description url
			}
		}
	}`, userID)

	data, err := p.gql(query)
	if err != nil {
		return nil, err
	}

	return &TaskResult{Items: parseIssueNodes(data, "issues")}, nil
}

func (p *linearProvider) fetchProjects() (*TaskResult, error) {
	data, err := p.gql(`{
		projects(first: 50) {
			nodes {
				id name state
				issues { nodes { id } }
			}
		}
	}`)
	if err != nil {
		return nil, err
	}

	var groups []TaskGroup
	if nodes := jsonPath[[]any](data, "projects", "nodes"); nodes != nil {
		for _, n := range nodes {
			nm, _ := n.(map[string]any)
			id, _ := nm["id"].(string)
			name, _ := nm["name"].(string)
			state, _ := nm["state"].(string)
			count := 0
			if issues := jsonPath[[]any](nm, "issues", "nodes"); issues != nil {
				count = len(issues)
			}
			groups = append(groups, TaskGroup{
				Key:    id,
				Name:   name,
				Status: mapProjectState(state),
				Count:  count,
			})
		}
	}

	return &TaskResult{Groups: groups}, nil
}

func (p *linearProvider) fetchProjectIssues(projectID string) (*TaskResult, error) {
	query := fmt.Sprintf(`{
		issues(
			first: 50
			filter: {
				project: { id: { eq: "%s" } }
				state: { type: { nin: ["canceled", "completed"] } }
			}
			orderBy: updatedAt
		) {
			nodes {
				identifier title
				state { name }
				priorityLabel
				assignee { name }
				labels { nodes { name } }
				description url
			}
		}
	}`, projectID)

	data, err := p.gql(query)
	if err != nil {
		return nil, err
	}

	return &TaskResult{Items: parseIssueNodes(data, "issues")}, nil
}

// ─── Response parsing ───────────────────────────────────────────────────────

// parseIssueNodes extracts TaskItems from a GraphQL response containing an
// issues connection at the given top-level key.
func parseIssueNodes(data map[string]any, key string) []TaskItem {
	nodes := jsonPath[[]any](data, key, "nodes")
	if nodes == nil {
		return nil
	}

	var items []TaskItem
	for _, n := range nodes {
		nm, ok := n.(map[string]any)
		if !ok {
			continue
		}
		item := TaskItem{
			ID:    jsonStr(nm, "identifier"),
			Title: jsonStr(nm, "title"),
		}
		if state := jsonPath[map[string]any](nm, "state"); state != nil {
			item.Status, _ = state["name"].(string)
		}
		item.Priority = mapPriorityLabel(jsonStr(nm, "priorityLabel"))
		if assignee := jsonPath[map[string]any](nm, "assignee"); assignee != nil {
			item.Assignee, _ = assignee["name"].(string)
		}
		item.Description = jsonStr(nm, "description")
		item.URL = jsonStr(nm, "url")

		if labels := jsonPath[[]any](nm, "labels", "nodes"); labels != nil {
			for _, l := range labels {
				if lm, ok := l.(map[string]any); ok {
					if name, ok := lm["name"].(string); ok {
						item.Labels = append(item.Labels, name)
					}
				}
			}
		}

		items = append(items, item)
	}
	return items
}

// mapPriorityLabel normalizes Linear's priority labels to the standard set
// used by the task modal's styling helpers.
func mapPriorityLabel(label string) string {
	switch strings.ToLower(label) {
	case "urgent":
		return "Urgent"
	case "high":
		return "High"
	case "medium":
		return "Medium"
	case "low":
		return "Low"
	default:
		return label
	}
}

// mapProjectState maps Linear's project state enum to display labels that
// align with the task modal's statusStyled helper.
func mapProjectState(state string) string {
	switch strings.ToLower(state) {
	case "started":
		return "In Progress"
	case "planned":
		return "Todo"
	case "completed":
		return "Done"
	case "canceled":
		return "Backlog"
	case "paused":
		return "Backlog"
	case "backlog":
		return "Backlog"
	default:
		return state
	}
}

// ─── JSON helpers ───────────────────────────────────────────────────────────

// jsonStr extracts a string value from a map.
func jsonStr(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// jsonPath traverses nested maps and slices to extract a typed value.
// Example: jsonPath[[]any](data, "issues", "nodes") navigates
// data["issues"]["nodes"] and asserts the result as []any.
func jsonPath[T any](m map[string]any, keys ...string) T {
	var zero T
	var current any = m
	for _, k := range keys {
		cm, ok := current.(map[string]any)
		if !ok {
			return zero
		}
		current = cm[k]
	}
	result, _ := current.(T)
	return result
}

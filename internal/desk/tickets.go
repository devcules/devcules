package desk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Ticket is one row of the Tickets widget, whatever the source. The
// frontend renders Key, Summary, Status and opens URL on click.
type Ticket struct {
	Key      string `json:"key"`
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
	Updated  string `json:"updated,omitempty"`
	URL      string `json:"url"`
}

// ErrNoTicketSource tells the widget to open the Sources drawer instead of
// showing an error, mirroring news' ErrNoKey.
var ErrNoTicketSource = errors.New("no tickets: set a site and token in Desk sources")

// TicketQuery describes one configured ticket source. Exactly one Provider.
type TicketQuery struct {
	// Provider: "jira" | "asana" | "linear".
	Provider string
	// Site is the host for jira (https://you.atlassian.net); unused otherwise.
	Site string
	// Email + Token: jira basic auth.
	Email string
	// Token is the API token (any provider) or personal access token.
	Token string
	// JQL is jira's query; Project is Asana's project gid / Linear's team key.
	JQL     string
	Project string
	// Limit rows, default 20.
	Limit int
}

// Tickets fetches the configured source into the shared Ticket shape.
func Tickets(ctx context.Context, hc *http.Client, q TicketQuery) ([]Ticket, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	switch strings.ToLower(strings.TrimSpace(q.Provider)) {
	case "jira", "":
		return jiraTickets(ctx, hc, q)
	case "asana":
		return asanaTickets(ctx, hc, q)
	case "linear":
		return linearTickets(ctx, hc, q)
	default:
		return nil, fmt.Errorf("tickets: unknown provider %q (want jira, asana or linear)", q.Provider)
	}
}

func jiraTickets(ctx context.Context, hc *http.Client, q TicketQuery) ([]Ticket, error) {
	site := strings.TrimRight(strings.TrimSpace(q.Site), "/")
	if site == "" || q.Token == "" {
		return nil, ErrNoTicketSource
	}
	jql := strings.TrimSpace(q.JQL)
	if jql == "" {
		jql = "assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC"
	}
	u := site + "/rest/api/3/search?" + url.Values{
		"jql":        {jql},
		"maxResults": {fmt.Sprint(q.Limit)},
		"fields":     {"summary,status,priority,issuetype,updated"},
	}.Encode()
	var raw struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
				Status  struct {
					Name     string `json:"name"`
					Category struct {
						Key string `json:"key"`
					} `json:"statusCategory"`
				} `json:"status"`
				Priority  *struct{ Name string } `json:"priority"`
				IssueType *struct{ Name string } `json:"issuetype"`
				Updated   string                 `json:"updated"`
			} `json:"fields"`
		} `json:"issues"`
	}
	hdr := map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(q.Email+":"+q.Token)),
		"Accept":        "application/json",
	}
	if err := getJSON(ctx, hc, u, hdr, &raw); err != nil {
		return nil, fmt.Errorf("jira: %w", err)
	}
	out := make([]Ticket, 0, len(raw.Issues))
	for _, i := range raw.Issues {
		t := Ticket{Key: i.Key, Summary: i.Fields.Summary, Status: i.Fields.Status.Name, Updated: i.Fields.Updated, URL: site + "/browse/" + i.Key}
		if i.Fields.Priority != nil {
			t.Priority = i.Fields.Priority.Name
		}
		out = append(out, t)
	}
	return out, nil
}

func asanaTickets(ctx context.Context, hc *http.Client, q TicketQuery) ([]Ticket, error) {
	if q.Token == "" {
		return nil, ErrNoTicketSource
	}
	vals := url.Values{
		"assignee":        {"me"},
		"workspace":       {q.Project}, // Asana wants a workspace gid; Project field carries it.
		"completed_since": {"now"},
		"opt_fields":      {"name,permalink_url,completed,due_on,modified_at,memberships.section.name,priority"},
		"limit":           {fmt.Sprint(q.Limit)},
	}
	u := "https://app.asana.com/api/1.0/tasks?" + vals.Encode()
	var raw struct {
		Data []struct {
			GID          string `json:"gid"`
			Name         string `json:"name"`
			PermalinkURL string `json:"permalink_url"`
			Completed    bool   `json:"completed"`
			ModifiedAt   string `json:"modified_at"`
			Memberships  []struct {
				Section struct {
					Name string `json:"name"`
				} `json:"section"`
			} `json:"memberships"`
		} `json:"data"`
	}
	hdr := map[string]string{"Authorization": "Bearer " + q.Token, "Accept": "application/json"}
	if err := getJSON(ctx, hc, u, hdr, &raw); err != nil {
		return nil, fmt.Errorf("asana: %w", err)
	}
	out := make([]Ticket, 0, len(raw.Data))
	for _, t := range raw.Data {
		if t.Completed {
			continue
		}
		status := ""
		if len(t.Memberships) > 0 {
			status = t.Memberships[0].Section.Name
		}
		out = append(out, Ticket{Key: t.GID, Summary: t.Name, Status: status, Updated: t.ModifiedAt, URL: t.PermalinkURL})
	}
	return out, nil
}

func linearTickets(ctx context.Context, hc *http.Client, q TicketQuery) ([]Ticket, error) {
	if q.Token == "" {
		return nil, ErrNoTicketSource
	}
	// Linear's GraphQL: issues assigned to the viewer, open states first.
	gql := `{"query":"query($first:Int!){viewer{assignedIssues(first:$first,filter:{state:{type:{nin:[\"completed\",\"canceled\"]}}},orderBy:updatedAt){nodes{identifier title url state{name} priorityLabel updatedAt}}}}","variables":{"first":` + fmt.Sprint(q.Limit) + `}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.linear.app/graphql", strings.NewReader(gql))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", q.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("linear: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("linear: HTTP %d", resp.StatusCode)
	}
	var raw struct {
		Data struct {
			Viewer struct {
				AssignedIssues struct {
					Nodes []struct {
						Identifier    string                `json:"identifier"`
						Title         string                `json:"title"`
						URL           string                `json:"url"`
						State         struct{ Name string } `json:"state"`
						PriorityLabel string                `json:"priorityLabel"`
						UpdatedAt     string                `json:"updatedAt"`
					} `json:"nodes"`
				} `json:"assignedIssues"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("linear: decode: %w", err)
	}
	if len(raw.Errors) > 0 {
		return nil, fmt.Errorf("linear: %s", raw.Errors[0].Message)
	}
	out := make([]Ticket, 0, len(raw.Data.Viewer.AssignedIssues.Nodes))
	for _, n := range raw.Data.Viewer.AssignedIssues.Nodes {
		out = append(out, Ticket{Key: n.Identifier, Summary: n.Title, Status: n.State.Name, Priority: n.PriorityLabel, Updated: n.UpdatedAt, URL: n.URL})
	}
	return out, nil
}

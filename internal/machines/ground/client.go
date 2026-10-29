package ground

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client grounds one frame against SAM 3.1 through serve's model
// router (/llm/meta/v1/responses, per-launch token) — never a
// provider key and never the public Meta endpoint.
type Client struct {
	// Router is the model router's base URL, e.g. http://127.0.0.1:7659.
	Router string
	// LaunchToken authenticates the call through the router.
	LaunchToken string
	// HTTP is the transport; tests swap it. Nil means http.DefaultClient
	// with a timeout.
	HTTP interface {
		Do(req *http.Request) (*http.Response, error)
	}
	deadline time.Duration
}

// New builds a client for one launch token.
func New(router, launchToken string) *Client {
	return &Client{Router: strings.TrimSuffix(router, "/"), LaunchToken: launchToken, deadline: 45 * time.Second}
}

// request is the Responses body Ground sends.
type request struct {
	Model  string    `json:"model"`
	Input  []msg     `json:"input"`
	Stream bool      `json:"stream"`
	Meta   *metadata `json:"metadata,omitempty"`
}
type msg struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []part `json:"content"`
}
type part struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}
type metadata struct {
	MaskEncoding string `json:"mask_encoding"`
}

// responsesAnswer is the non-streaming reply: SAM's segmentation
// arrives in the output_text lane of the one output message.
type responsesAnswer struct {
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Ground finds every object matching the noun phrase in the frame and
// returns the matches with their computed click points. A phrase with
// no match is a valid outcome: it returns an empty slice, not an
// error, exactly as the model's contract says.
func (c *Client) Ground(ctx context.Context, frame image.Image, phrase string) ([]Match, error) {
	if strings.TrimSpace(phrase) == "" {
		return nil, fmt.Errorf("ground: the prompt is empty: name one object, like \u201cred Pay button\u201d")
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: c.deadline}
	}
	ctx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, frame); err != nil {
		return nil, fmt.Errorf("ground: cannot encode the frame: %w", err)
	}
	body := request{
		Model: "sam-3.1",
		Input: []msg{{Type: "message", Role: "user", Content: []part{
			{Type: "input_text", Text: phrase},
			{Type: "input_image", ImageURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBuf.Bytes())},
		}}},
		Stream: false,
		Meta:   &metadata{MaskEncoding: "one_bit"},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ground: cannot build the request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Router+"/llm/meta/v1/responses", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("ground: cannot build the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.LaunchToken)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ground: SAM call failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("ground: SAM answered %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var ans responsesAnswer
	if err := json.NewDecoder(res.Body).Decode(&ans); err != nil {
		return nil, fmt.Errorf("ground: SAM's answer does not parse: %w", err)
	}
	if ans.Error != nil {
		return nil, fmt.Errorf("ground: SAM rejected the request: %s", ans.Error.Message)
	}
	var lane strings.Builder
	for _, o := range ans.Output {
		for _, p := range o.Content {
			if p.Type == "output_text" {
				lane.WriteString(p.Text)
			}
		}
	}
	return parse(lane.String())
}

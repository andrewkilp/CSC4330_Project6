// Package telephony implements explicit server-side Twilio REST operations.
// Constructors never dial. Start must only be invoked behind the durable gate.
package telephony

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

type Config struct{ AccountSID, AuthToken, FromNumber string }
type Client struct {
	cfg      Config
	endpoint string
	http     *http.Client
}

var _ providers.Telephony = (*Client)(nil)
var accountSID = regexp.MustCompile(`^AC[0-9a-fA-F]{32}$`)
var callSID = regexp.MustCompile(`^CA[0-9a-fA-F]{32}$`)
var e164 = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

func New(cfg Config) (*Client, error) {
	if !accountSID.MatchString(cfg.AccountSID) || !e164.MatchString(cfg.FromNumber) || strings.TrimSpace(cfg.AuthToken) == "" || len(cfg.AuthToken) > 1024 || strings.ContainsAny(cfg.AuthToken, "\r\n") {
		return nil, providers.ErrInvalid
	}
	return &Client{cfg: cfg, endpoint: "https://api.twilio.com", http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Start(ctx context.Context, dial providers.Dial) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := dial.Validate(); err != nil {
		return "", err
	}
	form := url.Values{"To": {dial.Destination}, "From": {c.cfg.FromNumber}, "Url": {dial.Callbacks.Voice}, "Method": {"POST"}, "StatusCallback": {dial.Callbacks.Status}, "StatusCallbackMethod": {"POST"}, "StatusCallbackEvent": {"initiated", "ringing", "answered", "completed"}, "TimeLimit": {"180"}, "Record": {"false"}, "Timeout": {"30"}}
	data, err := c.post(ctx, "/2010-04-01/Accounts/"+c.cfg.AccountSID+"/Calls.json", form)
	if err != nil {
		return "", err
	}
	var result struct {
		SID        string `json:"sid"`
		AccountSID string `json:"account_sid"`
	}
	if json.Unmarshal(data, &result) != nil || !callSID.MatchString(result.SID) || result.AccountSID != c.cfg.AccountSID {
		return "", providers.ErrUnavailable
	}
	return result.SID, nil
}

func (c *Client) End(ctx context.Context, sid string) error {
	if !callSID.MatchString(sid) {
		return providers.ErrInvalid
	}
	_, err := c.post(ctx, "/2010-04-01/Accounts/"+c.cfg.AccountSID+"/Calls/"+sid+".json", url.Values{"Status": {"completed"}})
	return err
}

func (c *Client) post(ctx context.Context, path string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, providers.ErrUnavailable
	}
	req.SetBasicAuth(c.cfg.AccountSID, c.cfg.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, providers.ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if res.StatusCode == http.StatusNotFound && strings.Contains(path, "/Calls/") {
			return nil, providers.ErrNotFound
		}
		return nil, providers.ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 32*1024+1))
	if err != nil || len(body) > 32*1024 {
		return nil, providers.ErrUnavailable
	}
	return body, nil
}

// Media is deliberately unavailable until #21 supplies its authenticated bridge.
func (c *Client) Media(context.Context, string, func(providers.MediaEvent) error) (providers.MediaStream, error) {
	return nil, providers.ErrUnavailable
}

package agent

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Client talks to the SWIM server. When Pin is set, the server's leaf
// certificate must hash to it (works with the lab's self-signed cert and
// refuses anything else, including a "valid" cert from a MITM proxy).
type Client struct {
	Server string
	hc     *http.Client

	mu   sync.Mutex
	skew time.Duration // server clock − local clock, from the Date header
}

// Now is local time corrected by the server's clock. Command validity windows
// are checked against it, so a PC with a wrong clock still works (seen in the
// lab: a VM whose RTC was 7 h off).
func (c *Client) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.skew)
}

func (c *Client) Skew() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.skew
}

func (c *Client) noteDate(resp *http.Response) {
	if d, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
		c.mu.Lock()
		c.skew = time.Until(d).Round(time.Second)
		c.mu.Unlock()
	}
}

func NewClient(server, pin string) *Client {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if pin != "" {
		want := strings.ToLower(strings.TrimSpace(pin))
		tc.InsecureSkipVerify = true // replaced by the pin check below
		tc.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("server sent no certificate")
			}
			sum := sha256.Sum256(raw[0])
			if hex.EncodeToString(sum[:]) != want {
				return fmt.Errorf("server certificate does not match pin (got %x)", sum[:6])
			}
			return nil
		}
	}
	return &Client{
		Server: strings.TrimRight(server, "/"),
		// timeout must exceed the 25 s long-poll wait
		hc: &http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{
			TLSClientConfig: tc, Proxy: http.ProxyFromEnvironment, IdleConnTimeout: 90 * time.Second}},
	}
}

type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s (HTTP %d): %s", e.Code, e.Status, e.Message)
}

func (c *Client) PostJSON(path, bearer string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.do(http.MethodPost, path, bearer, bytes.NewReader(b), out)
}

func (c *Client) GetJSON(path, bearer string, out any) error {
	return c.do(http.MethodGet, path, bearer, nil, out)
}

func (c *Client) do(method, path, bearer string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, c.Server+path, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("SWIM-Agent-Version", Version)
	req.Header.Set("User-Agent", "swim-agent/"+Version)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	c.noteDate(resp)
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return &APIError{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Download streams path to dst over the pinned connection and returns its SHA-256.
func (c *Client) Download(path, dst string) (string, int64, error) {
	hc := *c.hc
	hc.Timeout = 15 * time.Minute
	resp, err := hc.Get(c.Server + path)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("download %s: HTTP %d", path, resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 200<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return hex.EncodeToString(h.Sum(nil)), n, err
}

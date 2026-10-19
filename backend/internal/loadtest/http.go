package loadtest

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// executor performs requests against one target.
type executor interface {
	// preflight prepares the target and sends one request; an error means
	// the target is unusable and the run must not start.
	preflight(ctx context.Context) error
	do(ctx context.Context, seq int64) outcome
	close()
}

func newTransport(workers int) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          workers,
		MaxIdleConnsPerHost:   workers,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
	}
}

type httpExec struct {
	client  *http.Client
	method  string
	url     template
	body    template
	hasBody bool
	host    string
	headers http.Header
	// warmup makes preflight retry, until its deadline, connections that are
	// accepted and then dropped. That is what a sandbox port published by
	// Docker does while the service behind it (an EC2 instance's web server,
	// say) is still starting. Refused connections still fail at once: nothing
	// is listening at all.
	warmup bool
}

// newHTTPExec builds an HTTP executor. With a connection, its headers and
// authentication apply under the target's headers and its TLS settings
// apply to a dedicated transport.
func newHTTPExec(t *model.HTTPTarget, rawURL string, workers int, conn *model.Connection) (*httpExec, error) {
	method := strings.ToUpper(strings.TrimSpace(t.Method))
	if method == "" {
		method = http.MethodGet
	}
	tr := newTransport(workers)
	headers := http.Header{}
	if conn != nil {
		headers = connHeaders(conn)
		if conn.InsecureSkipVerify {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit per-connection opt-in
		}
	}
	h := &httpExec{
		client: &http.Client{
			Transport: tr,
			// Redirects are reported as their own status codes.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		method:  method,
		url:     compileTemplate(rawURL),
		body:    compileTemplate(t.Body),
		hasBody: t.Body != "",
		headers: headers,
	}
	u, err := url.Parse(h.url.render(1))
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q: need an absolute http(s) URL", rawURL)
	}
	for k, v := range t.Headers {
		h.headers.Set(k, v)
	}
	if host := h.headers.Get("Host"); host != "" {
		h.host = host
		h.headers.Del("Host")
	}
	return h, nil
}

func (h *httpExec) preflight(ctx context.Context) error {
	for {
		o, err := h.send(ctx, 0)
		if err == nil {
			return nil
		}
		retryable := o.errKind == errConnectionReset || o.errKind == errConnectionClosed
		if !h.warmup || !retryable {
			return fmt.Errorf("%s %s (%s): %w", h.method, h.url.render(0), o.errKind, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s %s (%s, still not answering): %w", h.method, h.url.render(0), o.errKind, err)
		case <-time.After(time.Second):
		}
	}
}

func (h *httpExec) do(ctx context.Context, seq int64) outcome {
	o, _ := h.send(ctx, seq)
	return o
}

func (h *httpExec) send(ctx context.Context, seq int64) (outcome, error) {
	var body io.Reader
	if h.hasBody {
		body = strings.NewReader(h.body.render(seq))
	}
	req, err := http.NewRequestWithContext(ctx, h.method, h.url.render(seq), body)
	if err != nil {
		return outcome{errKind: errInvalidPayload}, err
	}
	req.Header = h.headers.Clone()
	if h.host != "" {
		req.Host = h.host
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return outcome{errKind: classify(err)}, err
	}
	n, err := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	o := outcome{status: strconv.Itoa(resp.StatusCode), bytes: n}
	switch {
	case err != nil:
		o.errKind = classify(err)
	case resp.StatusCode >= 500:
		o.errKind = errHTTP5xx
	case resp.StatusCode >= 400:
		o.errKind = errHTTP4xx
	}
	return o, nil
}

func (h *httpExec) close() { h.client.CloseIdleConnections() }

// classify maps a transport error to an error kind.
func classify(err error) string {
	var ne net.Error
	var dns *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return errTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return errConnectionRefused
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return errConnectionReset
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return errConnectionClosed
	case errors.As(err, &dns):
		return errDNS
	default:
		return errNetwork
	}
}

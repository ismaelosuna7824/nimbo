package endpoints

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// ociKey signs the daemon's OCI requests; floci-oci parses signatures for
// the tenancy but never verifies them, so a per-process key does.
var ociKey = sync.OnceValues(func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 2048) })

// ociSignHeaders returns the headers of an OCI HTTP signature (draft
// cavage, rsa-sha256) for a request: date, (request-target) and host, plus
// the body headers for POST/PUT/PATCH.
func ociSignHeaders(method, rawURL, contentType, body, keyID string, key *rsa.PrivateKey, now time.Time) (map[string]string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	date := now.UTC().Format(http.TimeFormat)
	headers := map[string]string{"date": date}
	names := []string{"date", "(request-target)", "host"}
	values := []string{date, strings.ToLower(method) + " " + u.RequestURI(), u.Host}
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		sum := sha256.Sum256([]byte(body))
		headers["content-type"] = contentType
		headers["x-content-sha256"] = base64.StdEncoding.EncodeToString(sum[:])
		names = append(names, "content-length", "content-type", "x-content-sha256")
		values = append(values, strconv.Itoa(len(body)), contentType, headers["x-content-sha256"])
	}
	lines := make([]string, len(names))
	for i, n := range names {
		lines[i] = n + ": " + values[i]
	}
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, err
	}
	headers["authorization"] = fmt.Sprintf(`Signature version="1",keyId=%q,algorithm="rsa-sha256",headers=%q,signature=%q`,
		keyID, strings.Join(names, " "), base64.StdEncoding.EncodeToString(sig))
	return headers, nil
}

// ociKeyID is the keyId of the daemon's signatures: the workflow's tenancy
// selects its resources.
func (b *builder) ociKeyID() string {
	ids := sandbox.SandboxIdentities()
	return b.ociTenancy() + "/" + ids.OCIUser + "/" + ids.OCIFingerprint
}

func (b *builder) ociHeaders(method, u, contentType, body string) (map[string]string, error) {
	key, err := ociKey()
	if err != nil {
		return nil, err
	}
	return ociSignHeaders(method, u, contentType, body, b.ociKeyID(), key, time.Now())
}

// ociRequest runs one signed request and reports the answer as is.
func (b *builder) ociRequest(ctx context.Context, method, u, contentType, body string) model.TryResult {
	h, err := b.ociHeaders(method, u, contentType, body)
	if err != nil {
		return model.TryResult{Error: err.Error()}
	}
	return doHTTP(ctx, method, u, h, body)
}

// ociCall runs one signed JSON request and decodes a 2xx answer into out.
func (b *builder) ociCall(ctx context.Context, method, u, body string, out any) error {
	h, err := b.ociHeaders(method, u, "application/json", body)
	if err != nil {
		return err
	}
	var rd io.Reader
	if method != http.MethodGet {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := tryClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct{ Code, Message string }
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			return fmt.Errorf("HTTP %d %s: %s", resp.StatusCode, e.Code, e.Message)
		}
		return fmt.Errorf("HTTP %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// unb64 decodes base64 text, keeping undecodable input as is.
func unb64(s string) string {
	if d, err := base64.StdEncoding.DecodeString(s); err == nil {
		return string(d)
	}
	return s
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func (b *builder) tryOCI(ctx context.Context, e *entry, action, input string) (model.TryResult, error) {
	t := e.t
	switch e.Kind + "/" + action {
	case KindBucket + "/" + ActList:
		return b.ociRequest(ctx, http.MethodGet, t.url+"?limit=50&fields=name,size,timeCreated", "", ""), nil
	case KindBucket + "/" + ActPut:
		var in struct {
			Key  string `json:"key"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal([]byte(input), &in); err != nil || in.Key == "" {
			return model.TryResult{Error: `Input must be {"key": "<object name>", "body": "<text>"}.`}, nil
		}
		return b.ociRequest(ctx, http.MethodPut, t.url+"/"+url.PathEscape(in.Key), "text/plain; charset=utf-8", in.Body), nil
	case KindBucket + "/" + ActGet:
		return b.ociRequest(ctx, http.MethodGet, t.url+"/"+url.PathEscape(strings.TrimSpace(input)), "", ""), nil

	case KindQueue + "/" + ActSend:
		return b.ociRequest(ctx, http.MethodPost, t.url, "application/json", mustJSON(map[string]any{"messages": []map[string]string{{"content": input}}})), nil
	case KindQueue + "/" + ActReceive:
		return b.ociRequest(ctx, http.MethodGet, t.url+"?limit=10&visibilityInSeconds=0&timeoutInSeconds=0", "", ""), nil

	case KindStream + "/" + ActPut:
		body := mustJSON(map[string]any{"messages": []map[string]string{{"key": b64("nimbo"), "value": b64(input)}}})
		return b.ociRequest(ctx, http.MethodPost, t.url+"/messages", "application/json", body), nil
	case KindStream + "/" + ActReceive:
		return b.ociReadStream(ctx, t)

	case KindKey + "/" + ActEncrypt:
		body := mustJSON(map[string]string{"keyId": t.arn, "plaintext": b64(input)})
		return b.ociRequest(ctx, http.MethodPost, t.url+ociKMSAPI+"/encrypt", "application/json", body), nil
	case KindKey + "/" + ActDecrypt:
		ct := strings.TrimSpace(input)
		if ct == "" {
			return model.TryResult{Error: "Paste a ciphertext from Encrypt."}, nil
		}
		var out struct {
			Plaintext    string `json:"plaintext"`
			KeyVersionID string `json:"keyVersionId"`
		}
		if err := b.ociCall(ctx, http.MethodPost, t.url+ociKMSAPI+"/decrypt", mustJSON(map[string]string{"keyId": t.arn, "ciphertext": ct}), &out); err != nil {
			return model.TryResult{}, err
		}
		return jsonResult("200 OK", map[string]string{"plaintext": unb64(out.Plaintext), "keyVersionId": out.KeyVersionID}), nil

	case KindSecret + "/" + ActGet:
		var out struct {
			VersionNumber int      `json:"versionNumber"`
			Stages        []string `json:"stages"`
			Content       struct {
				ContentType string `json:"contentType"`
				Content     string `json:"content"`
			} `json:"secretBundleContent"`
		}
		if err := b.ociCall(ctx, http.MethodGet, t.url, "", &out); err != nil {
			return model.TryResult{}, err
		}
		value := out.Content.Content
		if out.Content.ContentType == "" || out.Content.ContentType == "BASE64" {
			value = unb64(value)
		}
		return jsonResult("200 OK", map[string]any{"value": value, "versionNumber": out.VersionNumber, "stages": out.Stages}), nil

	case KindFunction + "/" + ActInvoke:
		return b.ociRequest(ctx, http.MethodPost, t.url, "application/json", input), nil

	case KindKubernetes + "/" + ActGet:
		return b.ociRequest(ctx, http.MethodPost, t.url, "application/json", "{}"), nil
	}
	return model.TryResult{}, badRequest("action %q is not available for %s", action, e.Kind)
}

// ociReadStream reads every partition from its oldest message and shows the
// latest 10 messages, values decoded.
func (b *builder) ociReadStream(ctx context.Context, t target) (model.TryResult, error) {
	type message struct {
		Partition string    `json:"partition"`
		Offset    int64     `json:"offset"`
		Timestamp time.Time `json:"timestamp"`
		Key       string    `json:"key,omitempty"`
		Value     string    `json:"value"`
	}
	var all []message
	for p := range max(t.parts, 1) {
		// Page from the oldest message, keeping the tail (up to 2000 messages per partition).
		cursor := map[string]any{"partition": strconv.Itoa(p), "type": "TRIM_HORIZON"}
		var tail []message
		for range 20 {
			var cur struct {
				Value string `json:"value"`
			}
			if err := b.ociCall(ctx, http.MethodPost, t.url+"/cursors", mustJSON(cursor), &cur); err != nil {
				return model.TryResult{}, err
			}
			var msgs []message
			if err := b.ociCall(ctx, http.MethodGet, t.url+"/messages?limit=100&cursor="+url.QueryEscape(cur.Value), "", &msgs); err != nil {
				return model.TryResult{}, err
			}
			tail = append(tail, msgs...)
			tail = tail[max(0, len(tail)-10):]
			if len(msgs) < 100 {
				break
			}
			cursor = map[string]any{"partition": strconv.Itoa(p), "type": "AFTER_OFFSET", "offset": msgs[len(msgs)-1].Offset}
		}
		all = append(all, tail...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Timestamp.Before(all[j].Timestamp) })
	all = all[max(0, len(all)-10):]
	for i := range all {
		all[i].Key, all[i].Value = unb64(all[i].Key), unb64(all[i].Value)
	}
	if len(all) == 0 {
		return jsonResult("200 OK", map[string]any{"messages": []message{}, "note": "The stream has no messages yet."}), nil
	}
	return jsonResult("200 OK", map[string]any{"messages": all}), nil
}

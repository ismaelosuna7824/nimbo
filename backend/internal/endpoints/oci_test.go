package endpoints

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

const (
	ociBase      = "http://127.0.0.1:45003"
	tOCITenancy  = "ocid1.tenancy.oc1..flocilocaltenancy0000000000000000000000000000000000000000"
	tOCIQueue    = "ocid1.queue.oc1.iad.q1"
	tOCIStream   = "ocid1.stream.oc1.iad.s1"
	tOCIKey      = "ocid1.key.oc1.iad.k1"
	tOCISecret   = "ocid1.vaultsecret.oc1.iad.x1"
	tOCIFunction = "ocid1.fnfunc.oc1.iad.f1"
	tOCICluster  = "ocid1.cluster.oc1.iad.c1"
)

// ociFixture mirrors the state floci-oci 0.5.0 + oracle/oci 9.0.0 produced.
func ociFixture(base string) (*model.Workflow, *model.DeployedState, Sandbox) {
	wf := &model.Workflow{ID: "wf-oci", Name: "OCI"}
	st := &model.DeployedState{WorkflowID: "wf-oci", Resources: []model.DeployedResource{
		res("oci_identity_compartment.app", map[string]any{"compartment_id": tOCITenancy, "id": "ocid1.compartment.oc1..c", "name": "nimbo-app"}),
		res("oci_identity_policy.app", map[string]any{"compartment_id": tOCITenancy, "id": "ocid1.policy.oc1..p", "name": "nimbo-app-policy"}),
		res("oci_objectstorage_bucket.assets", map[string]any{"compartment_id": tOCITenancy, "id": "n/floci-local/b/nimbo-assets", "name": "nimbo-assets", "namespace": "floci-local"}),
		res("oci_objectstorage_preauthrequest.share", map[string]any{"access_type": "AnyObjectReadWrite", "bucket": "nimbo-assets", "namespace": "floci-local", "name": "share-bucket",
			"full_path": "/p/TOKEN/n/floci-local/b/nimbo-assets/o/", "time_expires": "2030-01-01T00:00:00Z"}),
		res("oci_queue_queue.orders", map[string]any{"compartment_id": tOCITenancy, "display_name": "orders", "id": tOCIQueue, "messages_endpoint": "http://localhost:4599"}),
		res("oci_streaming_stream.events", map[string]any{"compartment_id": tOCITenancy, "name": "events", "id": tOCIStream, "partitions": float64(2), "messages_endpoint": "http://localhost:4599"}),
		res("oci_kms_vault.vault", map[string]any{"compartment_id": tOCITenancy, "display_name": "app-vault", "id": "ocid1.vault.oc1.iad.v1", "crypto_endpoint": "http://localhost:4599", "management_endpoint": "http://localhost:4599"}),
		res("oci_kms_key.key", map[string]any{"compartment_id": tOCITenancy, "display_name": "app-key", "id": tOCIKey, "vault_id": "ocid1.vault.oc1.iad.v1",
			"key_shape": []any{map[string]any{"algorithm": "AES", "length": float64(32)}}, "management_endpoint": "http://localhost:4599"}),
		res("oci_kms_key.sign", map[string]any{"compartment_id": tOCITenancy, "display_name": "sign-key", "id": "ocid1.key.oc1.iad.k2",
			"key_shape": []any{map[string]any{"algorithm": "ECDSA", "length": float64(32)}}}),
		res("oci_vault_secret.db", map[string]any{"compartment_id": tOCITenancy, "secret_name": "db-password", "id": tOCISecret, "vault_id": "ocid1.vault.oc1.iad.v1"}),
		res("oci_functions_application.app", map[string]any{"compartment_id": tOCITenancy, "display_name": "hello-app", "id": "ocid1.fnapp.oc1.iad.a1"}),
		res("oci_functions_function.hello", map[string]any{"compartment_id": tOCITenancy, "display_name": "hello", "id": tOCIFunction, "image": "fnproject/fn-test-utils:latest", "invoke_endpoint": "http://localhost:4599"}),
		res("oci_containerengine_cluster.k8s", map[string]any{"compartment_id": tOCITenancy, "name": "dev-cluster", "id": tOCICluster, "kubernetes_version": "v1.30.1",
			"endpoints": []any{map[string]any{"kubernetes": "https://127.0.0.1:6443", "private_endpoint": "10.0.0.10:6443"}}}),
	}}
	sb := Sandbox{Bases: map[model.Cloud]string{model.CloudOCI: base}, Running: map[model.Cloud]bool{model.CloudOCI: true}}
	return wf, st, sb
}

func TestOCIBuild(t *testing.T) {
	info := Build(ociFixture(ociBase))
	want := map[string]struct {
		kind       string
		supporting bool
		label      string
		actions    []string
	}{
		"oci_identity_compartment.app":           {KindGeneric, true, "nimbo-app", nil},
		"oci_identity_policy.app":                {KindGeneric, true, "nimbo-app-policy", nil},
		"oci_objectstorage_bucket.assets":        {KindBucket, false, "nimbo-assets", []string{ActList, ActPut, ActGet}},
		"oci_objectstorage_preauthrequest.share": {KindGeneric, true, "share-bucket", nil},
		"oci_queue_queue.orders":                 {KindQueue, false, "orders", []string{ActSend, ActReceive}},
		"oci_streaming_stream.events":            {KindStream, false, "events", []string{ActPut, ActReceive}},
		"oci_kms_vault.vault":                    {KindGeneric, true, "app-vault", nil},
		"oci_kms_key.key":                        {KindKey, false, "app-key", []string{ActEncrypt, ActDecrypt}},
		"oci_kms_key.sign":                       {KindKey, false, "sign-key", nil},
		"oci_vault_secret.db":                    {KindSecret, false, "db-password", []string{ActGet}},
		"oci_functions_application.app":          {KindGeneric, true, "hello-app", nil},
		"oci_functions_function.hello":           {KindFunction, false, "hello", []string{ActInvoke}},
		"oci_containerengine_cluster.k8s":        {KindKubernetes, false, "dev-cluster", []string{ActGet}},
	}
	if len(info.Resources) != len(want) {
		t.Fatalf("got %d resources, want %d", len(info.Resources), len(want))
	}
	for addr, w := range want {
		r := byAddr(t, info, addr)
		if r.Kind != w.kind || r.Supporting != w.supporting || r.Label != w.label {
			t.Errorf("%s: kind=%s supporting=%v label=%q, want %s %v %q", addr, r.Kind, r.Supporting, r.Label, w.kind, w.supporting, w.label)
		}
		var ids []string
		for _, a := range r.Actions {
			ids = append(ids, a.ID)
		}
		if strings.Join(ids, ",") != strings.Join(w.actions, ",") {
			t.Errorf("%s: actions %v, want %v", addr, ids, w.actions)
		}
		if w.kind != KindGeneric && r.Summary == "" {
			t.Errorf("%s: no summary", addr)
		}
		for _, f := range r.Fields {
			if strings.Contains(f.Value, "localhost:4599") {
				t.Errorf("%s: field %s points inside the sandbox: %s", addr, f.Label, f.Value)
			}
		}
		for _, s := range r.Snippets {
			if strings.Contains(s.Code, "localhost:4599") {
				t.Errorf("%s: snippet %s points inside the sandbox", addr, s.Label)
			}
		}
	}

	bucket := byAddr(t, info, "oci_objectstorage_bucket.assets")
	if got := field(bucket, "List objects URL"); got != ociBase+"/n/floci-local/b/nimbo-assets/o" {
		t.Errorf("list URL %q", got)
	}
	if got := field(bucket, "Pre-authenticated URL (share-bucket)"); got != ociBase+"/p/TOKEN/n/floci-local/b/nimbo-assets/o/" {
		t.Errorf("PAR URL %q", got)
	}
	if c := snippet(t, bucket, "curl"); !strings.Contains(c, ociBase+"/p/TOKEN/n/floci-local/b/nimbo-assets/o/shared.txt") {
		t.Errorf("curl snippet lacks the PAR upload:\n%s", c)
	}
	if c := snippet(t, bucket, "OCI CLI"); !strings.Contains(c, "--profile NIMBO --endpoint "+ociBase) {
		t.Errorf("CLI snippet:\n%s", c)
	}
	if c := snippet(t, bucket, "Python"); !strings.Contains(c, `endpoint = "`+ociBase+`"`) || !strings.Contains(c, `"tenancy": "`+tOCITenancy+`"`) {
		t.Errorf("Python snippet:\n%s", c)
	}
	if c := snippet(t, bucket, "JavaScript"); !strings.Contains(c, `import * as objectstorage from "oci-objectstorage";`) || !strings.Contains(c, "npm install oci-common oci-objectstorage") {
		t.Errorf("JavaScript snippet:\n%s", c)
	}
	if got := field(byAddr(t, info, "oci_queue_queue.orders"), "Messages URL"); got != ociBase+"/20210201/queues/"+tOCIQueue+"/messages" {
		t.Errorf("queue messages URL %q", got)
	}
	if got := field(byAddr(t, info, "oci_functions_function.hello"), "Invoke URL"); got != ociBase+"/20181201/functions/"+tOCIFunction+"/actions/invoke" {
		t.Errorf("invoke URL %q", got)
	}
	k8s := byAddr(t, info, "oci_containerengine_cluster.k8s")
	if field(k8s, "Kubernetes API") != "https://127.0.0.1:6443" || !strings.Contains(snippet(t, k8s, "OCI CLI + kubectl"), "KUBECONFIG=./kubeconfig kubectl get nodes") {
		t.Errorf("cluster card: %+v", k8s)
	}
	if w := byAddr(t, info, "oci_kms_key.sign").Warning; !strings.Contains(w, "AES") {
		t.Errorf("ECDSA key warning %q", w)
	}

	if len(info.Sandbox) != 1 {
		t.Fatalf("sandbox access: %+v", info.Sandbox)
	}
	sa := info.Sandbox[0]
	ids := sandbox.SandboxIdentities()
	for _, s := range []string{"[NIMBO]", "tenancy=" + tOCITenancy, "user=" + ids.OCIUser, "fingerprint=" + ids.OCIFingerprint, "OCI_CLI_ENDPOINT=" + ociBase, "OCI_CLI_PROFILE=NIMBO"} {
		if !strings.Contains(sa.EnvSnippet, s) {
			t.Errorf("env snippet lacks %q:\n%s", s, sa.EnvSnippet)
		}
	}
	if sa.URL != ociBase || sa.Region != "us-ashburn-1" {
		t.Errorf("sandbox access %+v", sa)
	}
}

// Resources in another tenancy can't be reached unsigned: no curl, no
// Postman folder, and signatures carry that tenancy.
func TestOCIOtherTenancy(t *testing.T) {
	wf, st, sb := ociFixture(ociBase)
	other := "ocid1.tenancy.oc1..mine"
	for i := range st.Resources {
		if st.Resources[i].Values["compartment_id"] == tOCITenancy {
			st.Resources[i].Values["compartment_id"] = other
		}
	}
	info := Build(wf, st, sb)
	for _, r := range info.Resources {
		for _, s := range r.Snippets {
			if s.Label == "curl" {
				t.Errorf("%s has a curl snippet for tenancy %s", r.Address, other)
			}
		}
	}
	if !strings.Contains(info.Sandbox[0].EnvSnippet, "tenancy="+other) {
		t.Errorf("env snippet:\n%s", info.Sandbox[0].EnvSnippet)
	}
	col := Postman(wf, st, sb)
	for _, it := range col.Item {
		if it.Name == "OCI services (sandbox)" {
			t.Errorf("Postman has an unsigned OCI folder for tenancy %s", other)
		}
	}
	if b := newBuilder(wf, st, sb); !strings.HasPrefix(b.ociKeyID(), other+"/") {
		t.Errorf("keyId %q", b.ociKeyID())
	}
}

func TestOCIPostman(t *testing.T) {
	col := Postman(ociFixture(ociBase))
	var folder *PostmanItem
	for i := range col.Item {
		if col.Item[i].Name == "OCI services (sandbox)" {
			folder = &col.Item[i]
		}
	}
	if folder == nil {
		t.Fatalf("no OCI folder in %+v", col.Item)
	}
	var names []string
	for _, it := range folder.Item {
		names = append(names, it.Name)
		if !strings.HasPrefix(it.Request.URL.Raw, "{{ociBaseUrl}}/") {
			t.Errorf("%s: URL %s", it.Name, it.Request.URL.Raw)
		}
	}
	for _, n := range []string{"nimbo-assets: upload hello.txt", "orders: send message", "events: create cursor", "app-key: encrypt (base64 plaintext)", "db-password: get secret bundle (base64 content)", "hello: invoke", "dev-cluster: get kubeconfig"} {
		if !strings.Contains(strings.Join(names, "\n"), n) {
			t.Errorf("no %q in %v", n, names)
		}
	}
	for _, n := range names {
		if strings.HasPrefix(n, "sign-key") {
			t.Errorf("ECDSA key got requests: %s", n)
		}
	}
	found := false
	for _, v := range col.Variable {
		found = found || v.Key == "ociBaseUrl" && v.Value == ociBase
	}
	if !found {
		t.Errorf("variables %+v", col.Variable)
	}
}

var ociSigRE = regexp.MustCompile(`^Signature version="1",keyId="([^"]+)",algorithm="rsa-sha256",headers="([^"]+)",signature="([^"]+)"$`)

// verifyOCISignature checks r's OCI signature with pub (the emulator only
// parses it; the test verifies it).
func verifyOCISignature(r *http.Request, body []byte, pub *rsa.PublicKey) (string, error) {
	m := ociSigRE.FindStringSubmatch(r.Header.Get("Authorization"))
	if m == nil {
		return "", ociErr("malformed authorization: " + r.Header.Get("Authorization"))
	}
	var lines []string
	for _, h := range strings.Fields(m[2]) {
		var v string
		switch h {
		case "(request-target)":
			v = strings.ToLower(r.Method) + " " + r.URL.RequestURI()
		case "host":
			v = r.Host
		case "content-length":
			v = r.Header.Get("Content-Length")
			if v == "" {
				v = "0"
			}
		case "x-content-sha256":
			sum := sha256.Sum256(body)
			if r.Header.Get(h) != base64.StdEncoding.EncodeToString(sum[:]) {
				return "", ociErr("x-content-sha256 mismatch")
			}
			v = r.Header.Get(h)
		default:
			v = r.Header.Get(h)
		}
		lines = append(lines, h+": "+v)
	}
	sig, _ := base64.StdEncoding.DecodeString(m[3])
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return "", err
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		if m[2] != "date (request-target) host content-length content-type x-content-sha256" {
			return "", ociErr("body headers not signed: " + m[2])
		}
	}
	return m[1], nil
}

type ociErr string

func (e ociErr) Error() string { return string(e) }

func TestOCISignHeaders(t *testing.T) {
	key, err := ociKey()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"a":1}`
	h, err := ociSignHeaders(http.MethodPost, "http://127.0.0.1:1/20210201/queues/q/messages?x=1", "application/json", body, "t/u/f", key, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/20210201/queues/q/messages?x=1", strings.NewReader(body))
	for k, v := range h {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Length", "7")
	if id, err := verifyOCISignature(req, []byte(body), &key.PublicKey); err != nil || id != "t/u/f" {
		t.Fatalf("verify: %v (keyId %q)", err, id)
	}
	if h["date"] != "Thu, 01 Jan 1970 00:00:00 GMT" {
		t.Errorf("date %q", h["date"])
	}
}

// TestOCITry runs every action against a fake floci-oci that verifies the
// signatures and answers like the real one.
func TestOCITry(t *testing.T) {
	key, err := ociKey()
	if err != nil {
		t.Fatal(err)
	}
	ids := sandbox.SandboxIdentities()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		kid, err := verifyOCISignature(r, body, &key.PublicKey)
		if err != nil || kid != tOCITenancy+"/"+ids.OCIUser+"/"+ids.OCIFingerprint {
			http.Error(w, "bad signature: "+kid+" "+ociErrText(err), http.StatusUnauthorized)
			return
		}
		calls = append(calls, r.Method+" "+r.URL.Path+" "+string(body))
		w.Header().Set("Content-Type", "application/json")
		switch p := r.URL.Path; {
		case p == "/n/floci-local/b/nimbo-assets/o":
			io.WriteString(w, `{"objects":[{"name":"hello.txt"}]}`)
		case p == "/n/floci-local/b/nimbo-assets/o/hello.txt" && r.Method == http.MethodPut:
		case p == "/n/floci-local/b/nimbo-assets/o/hello.txt":
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "Hello from Nimbo")
		case strings.HasSuffix(p, "/queues/"+tOCIQueue+"/messages"):
			io.WriteString(w, `{"messages":[{"id":1,"content":"hi"}]}`)
		case strings.HasSuffix(p, "/streams/"+tOCIStream+"/cursors"):
			var c struct{ Partition string }
			json.Unmarshal(body, &c)
			io.WriteString(w, `{"value":"cur`+c.Partition+`"}`)
		case strings.HasSuffix(p, "/streams/"+tOCIStream+"/messages") && r.Method == http.MethodGet:
			if r.URL.Query().Get("cursor") == "cur1" {
				io.WriteString(w, `[{"partition":"1","offset":0,"timestamp":"2026-10-18T06:42:19Z","key":"bmltYm8=","value":"c2Vjb25k"}]`)
				return
			}
			io.WriteString(w, `[{"partition":"0","offset":0,"timestamp":"2026-10-18T06:42:18Z","key":"bmltYm8=","value":"Zmlyc3Q="}]`)
		case strings.HasSuffix(p, "/streams/"+tOCIStream+"/messages"):
			io.WriteString(w, `{"failures":0,"entries":[{"partition":"0","offset":1}]}`)
		case p == "/20180608/encrypt":
			io.WriteString(w, `{"ciphertext":"CT","keyId":"`+tOCIKey+`"}`)
		case p == "/20180608/decrypt":
			io.WriteString(w, `{"plaintext":"SGVsbG8gZnJvbSBOaW1ibw==","keyVersionId":"kv1"}`)
		case p == "/20190301/secretbundles/"+tOCISecret:
			io.WriteString(w, `{"versionNumber":1,"stages":["CURRENT"],"secretBundleContent":{"contentType":"BASE64","content":"czNjcjN0"}}`)
		case p == "/20181201/functions/"+tOCIFunction+"/actions/invoke":
			w.Write(body)
		case p == "/20180222/clusters/"+tOCICluster+"/kubeconfig/content":
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "apiVersion: v1\nkind: Config\n")
		default:
			http.Error(w, `{"code":"NotFound"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	wf, st, sb := ociFixture(srv.URL)
	cases := []struct {
		addr, action, input, want string
	}{
		{"oci_objectstorage_bucket.assets", ActList, "", `"hello.txt"`},
		{"oci_objectstorage_bucket.assets", ActPut, ociPutDefault, ""},
		{"oci_objectstorage_bucket.assets", ActGet, "hello.txt", "Hello from Nimbo"},
		{"oci_queue_queue.orders", ActSend, "hi", `"id": 1`},
		{"oci_queue_queue.orders", ActReceive, "", `"content": "hi"`},
		{"oci_streaming_stream.events", ActPut, "hello", `"failures": 0`},
		{"oci_streaming_stream.events", ActReceive, "", `"value": "second"`},
		{"oci_kms_key.key", ActEncrypt, "Hello from Nimbo", `"ciphertext": "CT"`},
		{"oci_kms_key.key", ActDecrypt, "CT", `"plaintext": "Hello from Nimbo"`},
		{"oci_vault_secret.db", ActGet, "", `"value": "s3cr3t"`},
		{"oci_functions_function.hello", ActInvoke, `{"name":"Nimbo"}`, `"name": "Nimbo"`},
		{"oci_containerengine_cluster.k8s", ActGet, "", "kind: Config"},
	}
	for _, c := range cases {
		res, err := Try(context.Background(), wf, st, sb, model.TryRequest{NodeID: c.addr, Action: c.action, Input: c.input})
		if err != nil || !res.OK || !strings.Contains(res.Body, c.want) {
			t.Errorf("%s %s: err=%v result=%+v", c.addr, c.action, err, res)
		}
	}
	joined := strings.Join(calls, "\n")
	for _, w := range []string{
		"PUT /n/floci-local/b/nimbo-assets/o/hello.txt Hello from Nimbo",
		`POST /20210201/queues/` + tOCIQueue + `/messages {"messages":[{"content":"hi"}]}`,
		`POST /20180418/streams/` + tOCIStream + `/messages {"messages":[{"key":"bmltYm8=","value":"aGVsbG8="}]}`,
		`POST /20180418/streams/` + tOCIStream + `/cursors {"partition":"1","type":"TRIM_HORIZON"}`,
		`POST /20180608/encrypt {"keyId":"` + tOCIKey + `","plaintext":"SGVsbG8gZnJvbSBOaW1ibw=="}`,
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("no call %q in\n%s", w, joined)
		}
	}
	if res, _ := Try(context.Background(), wf, st, sb, model.TryRequest{NodeID: "oci_kms_key.key", Action: ActDecrypt, Input: " "}); res.Error == "" {
		t.Errorf("decrypt without ciphertext: %+v", res)
	}
}

func ociErrText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

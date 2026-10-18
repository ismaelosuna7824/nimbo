package endpoints

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const gcpBase = "http://127.0.0.1:48602"

// gcpFixture mirrors the Terraform state of a floci-gcp deployment.
func gcpFixture(base string) (*model.Workflow, *model.DeployedState, Sandbox) {
	p := "projects/floci-local"
	schema := `[{"mode":"NULLABLE","name":"name","type":"STRING"},{"mode":"NULLABLE","name":"count","type":"INTEGER"}]`
	ca := base64.StdEncoding.EncodeToString([]byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"))
	st := &model.DeployedState{WorkflowID: "gwf", Resources: []model.DeployedResource{
		res("google_storage_bucket.files", map[string]any{"name": "floci-local-files", "id": "floci-local-files", "project": "floci-local", "url": "gs://floci-local-files"}),
		res("google_pubsub_topic.events", map[string]any{"name": "events", "id": p + "/topics/events", "project": "floci-local"}),
		res("google_pubsub_topic.lonely", map[string]any{"name": "lonely", "id": p + "/topics/lonely", "project": "floci-local"}),
		res("google_pubsub_subscription.worker", map[string]any{"name": "events-worker", "id": p + "/subscriptions/events-worker", "topic": p + "/topics/events", "ack_deadline_seconds": float64(20), "project": "floci-local"}),
		res("google_secret_manager_secret.api_key", map[string]any{"secret_id": "api-key", "id": p + "/secrets/api-key", "name": p + "/secrets/api-key", "project": "floci-local"}),
		res("google_secret_manager_secret_version.api_key", map[string]any{"secret": p + "/secrets/api-key", "id": p + "/secrets/api-key/versions/1", "secret_data": "s3cr3t-value"}),
		res("google_kms_key_ring.ring", map[string]any{"name": "app-ring", "id": p + "/locations/us-central1/keyRings/app-ring", "location": "us-central1"}),
		res("google_kms_crypto_key.key", map[string]any{"name": "app-key", "id": p + "/locations/us-central1/keyRings/app-ring/cryptoKeys/app-key", "key_ring": p + "/locations/us-central1/keyRings/app-ring", "purpose": "ENCRYPT_DECRYPT"}),
		res("google_cloud_run_v2_service.web", map[string]any{"name": "web", "location": "us-central1", "id": p + "/locations/us-central1/services/web", "uri": "http://web-1efb2c36c23f.us-central1.run.localhost.floci.io:4588",
			"template": []any{map[string]any{"containers": []any{map[string]any{"image": "mendhak/http-https-echo:latest"}}}}}),
		res("google_cloudfunctions2_function.fn", map[string]any{"name": "hello-fn", "location": "us-central1", "id": p + "/locations/us-central1/functions/hello-fn", "state": "ACTIVE",
			"build_config": []any{map[string]any{"runtime": "nodejs20", "entry_point": "hello"}}}),
		res("google_sql_database_instance.db", map[string]any{"name": "app-db", "id": "app-db", "project": "floci-local", "database_version": "POSTGRES_15", "connection_name": "floci-local:us-central1:app-db", "first_ip_address": "192.168.171.5"}),
		res("google_sql_database.appdb", map[string]any{"name": "appdb", "instance": "app-db"}),
		res("google_sql_user.app", map[string]any{"name": "app", "instance": "app-db", "password": "app-password"}),
		res("google_bigquery_dataset.analytics", map[string]any{"dataset_id": "analytics", "id": p + "/datasets/analytics", "project": "floci-local", "location": "US"}),
		res("google_bigquery_table.events", map[string]any{"dataset_id": "analytics", "table_id": "events", "id": p + "/datasets/analytics/tables/events", "project": "floci-local", "schema": schema}),
		res("google_cloud_scheduler_job.tick", map[string]any{"name": "tick", "region": "us-central1", "id": p + "/locations/us-central1/jobs/tick", "schedule": "*/5 * * * *", "time_zone": "Etc/UTC", "state": "ENABLED",
			"pubsub_target": []any{map[string]any{"topic_name": p + "/topics/events"}}}),
		res("google_container_cluster.gke", map[string]any{"name": "app-gke", "location": "us-central1", "endpoint": "localhost:6550", "master_version": "1.30.5-gke.1014001",
			"master_auth": []any{map[string]any{"cluster_ca_certificate": ca}}}),
		res("google_service_account.app", map[string]any{"account_id": "app-runner", "name": p + "/serviceAccounts/app-runner@floci-local.iam.gserviceaccount.com", "email": "app-runner@floci-local.iam.gserviceaccount.com"}),
		res("google_project_iam_member.app_pubsub", map[string]any{"id": "floci-local/roles/pubsub.publisher/serviceAccount:x", "role": "roles/pubsub.publisher"}),
		res("google_compute_network.vpc", map[string]any{"name": "app-vpc", "id": p + "/global/networks/app-vpc", "self_link": "https://www.googleapis.com/compute/v1/projects/floci-local/global/networks/app-vpc"}),
		res("google_compute_firewall.web", map[string]any{"name": "allow-web", "id": p + "/global/firewalls/allow-web"}),
	}}
	sb := Sandbox{Bases: map[model.Cloud]string{model.CloudGCP: base}, Running: map[model.Cloud]bool{model.CloudGCP: true}}
	return &model.Workflow{ID: "gwf", Name: "GCP"}, st, sb
}

func TestBuildGCP(t *testing.T) {
	info := Build(gcpFixture(gcpBase))

	if len(info.Sandbox) != 1 || info.Sandbox[0].Cloud != model.CloudGCP {
		t.Fatalf("sandbox access %+v", info.Sandbox)
	}
	env := info.Sandbox[0].EnvSnippet
	for _, want := range []string{"STORAGE_EMULATOR_HOST=" + gcpBase, "PUBSUB_EMULATOR_HOST=127.0.0.1:48602", "GOOGLE_CLOUD_PROJECT=floci-local", "CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE=" + gcpBase + "/"} {
		if !strings.Contains(env, want) {
			t.Errorf("env snippet lacks %q:\n%s", want, env)
		}
	}

	bk := byAddr(t, info, "google_storage_bucket.files")
	if bk.Kind != KindBucket || field(bk, "List objects URL") != gcpBase+"/storage/v1/b/floci-local-files/o" || hasAction(bk, ActPut) == nil {
		t.Fatalf("bucket %+v", bk)
	}
	if js := snippet(t, bk, "JavaScript"); !strings.Contains(js, `apiEndpoint: "`+gcpBase+`"`) {
		t.Errorf("storage JS snippet:\n%s", js)
	}
	if py := snippet(t, bk, "Python"); !strings.Contains(py, `client_options={"api_endpoint": "`+gcpBase+`"}`) {
		t.Errorf("storage Python snippet:\n%s", py)
	}
	if g := snippet(t, bk, "gcloud"); !strings.Contains(g, "CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE="+gcpBase+"/\n") || !strings.Contains(g, "gcloud storage ls gs://floci-local-files") {
		t.Errorf("gcloud snippet:\n%s", g)
	}

	topic := byAddr(t, info, "google_pubsub_topic.events")
	if field(topic, "Subscriptions") != "events-worker" || topic.Warning != "" {
		t.Fatalf("topic %+v", topic)
	}
	if lonely := byAddr(t, info, "google_pubsub_topic.lonely"); lonely.Warning == "" {
		t.Error("a topic without subscriptions warns that messages are dropped")
	}

	sub := byAddr(t, info, "google_pubsub_subscription.worker")
	if sub.Kind != KindQueue || sub.Supporting || field(sub, "Pull URL") != gcpBase+"/v1/projects/floci-local/subscriptions/events-worker:pull" || hasAction(sub, ActReceive) == nil {
		t.Fatalf("subscription %+v", sub)
	}
	if js := snippet(t, sub, "JavaScript"); !strings.Contains(js, `port: 48602`) || !strings.Contains(js, "createInsecure()") {
		t.Errorf("subscription JS snippet:\n%s", js)
	}

	sec := byAddr(t, info, "google_secret_manager_secret.api_key")
	if sec.Kind != KindSecret || sec.Label != "api-key" || field(sec, "Versions") != "1" || sec.Warning != "" {
		t.Fatalf("secret %+v", sec)
	}
	if field(sec, "Access URL") != gcpBase+"/v1/projects/floci-local/secrets/api-key/versions/latest:access" {
		t.Errorf("access URL %q", field(sec, "Access URL"))
	}

	key := byAddr(t, info, "google_kms_crypto_key.key")
	if key.Kind != KindKey || hasAction(key, ActEncrypt) == nil || hasAction(key, ActDecrypt) == nil {
		t.Fatalf("kms key %+v", key)
	}
	if g := snippet(t, key, "gcloud"); !strings.Contains(g, "--keyring=app-ring --key=app-key") || !strings.Contains(g, "--location=us-central1") {
		t.Errorf("kms gcloud snippet:\n%s", g)
	}

	run := byAddr(t, info, "google_cloud_run_v2_service.web")
	wantURL := gcpBase + "/run/v2/projects/floci-local/locations/us-central1/services/web/"
	if run.Kind != KindWebService || field(run, "URL") != wantURL || len(run.Endpoints) != 1 || run.Endpoints[0].URL != wantURL || hasAction(run, ActRequest) == nil {
		t.Fatalf("cloud run %+v", run)
	}
	if got := field(run, "Service URL"); got != "http://web-1efb2c36c23f.us-central1.run.localhost.floci.io:48602" {
		t.Errorf("service URL %q", got)
	}
	if field(run, "Image") != "mendhak/http-https-echo:latest" {
		t.Errorf("image %q", field(run, "Image"))
	}

	fn := byAddr(t, info, "google_cloudfunctions2_function.fn")
	if fn.Kind != KindFunction || fn.Warning == "" || field(fn, "Runtime") != "nodejs20" || hasAction(fn, ActGet) == nil || hasAction(fn, ActInvoke) != nil {
		t.Fatalf("function %+v", fn)
	}

	db := byAddr(t, info, "google_sql_database_instance.db")
	if db.Kind != KindDatabase || db.Warning == "" || field(db, "Port") != "5432" || field(db, "Databases") != "appdb" || field(db, "Users") != "app" ||
		field(db, "Connection name") != "floci-local:us-central1:app-db" {
		t.Fatalf("cloud sql %+v", db)
	}
	if psql := snippet(t, db, "psql (in the sandbox)"); !strings.Contains(psql, "docker exec -it floci-gcp-nimbo-gwf-cloudsql-floci-local-app-db psql -U app -d appdb") {
		t.Errorf("psql snippet:\n%s", psql)
	}
	for _, r := range info.Resources {
		if r.Type == "google_sql_database" || r.Type == "google_sql_user" || r.Type == "google_secret_manager_secret_version" {
			t.Errorf("%s is folded into its parent, not listed", r.Address)
		}
	}

	ds := byAddr(t, info, "google_bigquery_dataset.analytics")
	if q := hasAction(ds, ActQuery); ds.Kind != KindDatabase || q == nil || q.Default != "SELECT * FROM `analytics.events` LIMIT 10" {
		t.Fatalf("dataset %+v", ds)
	}
	tb := byAddr(t, info, "google_bigquery_table.events")
	if put := hasAction(tb, ActPut); tb.Kind != KindTable || put == nil || !strings.Contains(put.Default, `"count": 1`) || field(tb, "Columns") != "name STRING, count INTEGER" {
		t.Fatalf("table %+v", tb)
	}

	job := byAddr(t, info, "google_cloud_scheduler_job.tick")
	if job.Supporting || hasAction(job, ActStart) == nil || field(job, "Publishes to") != "projects/floci-local/topics/events" {
		t.Fatalf("scheduler job %+v", job)
	}

	gke := byAddr(t, info, "google_container_cluster.gke")
	if gke.Kind != KindKubernetes || field(gke, "API server") != "https://localhost:6550" || hasAction(gke, ActList) == nil {
		t.Fatalf("gke %+v", gke)
	}
	if kc := snippet(t, gke, "kubectl"); !strings.Contains(kc, "server: https://localhost:6550") || !strings.Contains(kc, "token: fake") {
		t.Errorf("kubectl snippet:\n%s", kc)
	}

	for _, addr := range []string{"google_service_account.app", "google_project_iam_member.app_pubsub", "google_compute_network.vpc", "google_compute_firewall.web", "google_kms_key_ring.ring"} {
		if r := byAddr(t, info, addr); !r.Supporting || r.Summary == "" {
			t.Errorf("%s: supporting=%v summary=%q", addr, r.Supporting, r.Summary)
		}
	}
}

// fakeGCP answers the floci-gcp REST calls the Try executor makes.
func fakeGCP(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/projects/floci-local/subscriptions/events-worker:pull":
			io.WriteString(w, `{"receivedMessages":[{"ackId":"a1","message":{"messageId":"1","data":"`+base64.StdEncoding.EncodeToString([]byte("hi"))+`"}}]}`)
		case "POST /v1/projects/floci-local/subscriptions/events-worker:acknowledge":
			if !strings.Contains(string(body), `"a1"`) {
				w.WriteHeader(http.StatusBadRequest)
			}
			io.WriteString(w, `{}`)
		case "GET /v1/projects/floci-local/secrets/api-key/versions/latest:access":
			io.WriteString(w, `{"name":"projects/floci-local/secrets/api-key/versions/1","payload":{"data":"`+base64.StdEncoding.EncodeToString([]byte("s3cr3t"))+`"}}`)
		case "POST /v1/projects/floci-local/locations/us-central1/keyRings/app-ring/cryptoKeys/app-key:encrypt":
			var in struct{ Plaintext string }
			json.Unmarshal(body, &in)
			io.WriteString(w, `{"ciphertext":"X`+in.Plaintext+`"}`)
		case "POST /v1/projects/floci-local/locations/us-central1/keyRings/app-ring/cryptoKeys/app-key:decrypt":
			var in struct{ Ciphertext string }
			json.Unmarshal(body, &in)
			io.WriteString(w, `{"plaintext":"`+strings.TrimPrefix(in.Ciphertext, "X")+`"}`)
		case "POST /bigquery/v2/projects/floci-local/queries":
			io.WriteString(w, `{"schema":{"fields":[{"name":"name","type":"STRING"},{"name":"count","type":"INTEGER"}]},"rows":[{"f":[{"v":"hello"},{"v":"1"}]}],"totalRows":"1"}`)
		case "GET /bigquery/v2/projects/floci-local/datasets/analytics/tables/events/data":
			io.WriteString(w, `{"rows":[{"f":[{"v":"hello"},{"v":"1"}]}],"totalRows":"1"}`)
		case "POST /bigquery/v2/projects/floci-local/datasets/analytics/tables/events/insertAll":
			io.WriteString(w, `{"kind":"bigquery#tableDataInsertAllResponse"}`)
		case "POST /v1/projects/floci-local/locations/us-central1/jobs/tick:run":
			io.WriteString(w, `{"name":"projects/floci-local/locations/us-central1/jobs/tick","state":"ENABLED"}`)
		case "GET /run/v2/projects/floci-local/locations/us-central1/services/web/items":
			io.WriteString(w, `{"path":"/items"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"`+r.Method+" "+r.URL.Path+`"}`)
		}
	}))
}

func TestTryGCP(t *testing.T) {
	srv := fakeGCP(t)
	defer srv.Close()
	wf, st, sb := gcpFixture(srv.URL)
	try := func(addr, action, input string) model.TryResult {
		t.Helper()
		res, err := Try(context.Background(), wf, st, sb, model.TryRequest{NodeID: addr, Action: action, Input: input})
		if err != nil {
			t.Fatalf("%s %s: %v", addr, action, err)
		}
		if !res.OK {
			t.Fatalf("%s %s: %+v", addr, action, res)
		}
		return res
	}
	if res := try("google_pubsub_subscription.worker", ActReceive, ""); !strings.Contains(res.Body, `"data": "hi"`) {
		t.Errorf("pull: %s", res.Body)
	}
	if res := try("google_secret_manager_secret.api_key", ActGet, ""); res.Body != "s3cr3t" {
		t.Errorf("secret: %q", res.Body)
	}
	enc := try("google_kms_crypto_key.key", ActEncrypt, "hello")
	if dec := try("google_kms_crypto_key.key", ActDecrypt, enc.Body); dec.Body != "hello" {
		t.Errorf("encrypt %q, decrypt %q", enc.Body, dec.Body)
	}
	if res := try("google_bigquery_dataset.analytics", ActQuery, "SELECT 1"); !strings.Contains(res.Body, `"count": "1"`) || !strings.Contains(res.Body, `"name": "hello"`) {
		t.Errorf("query: %s", res.Body)
	}
	if res := try("google_bigquery_table.events", ActScan, ""); !strings.Contains(res.Body, `"name": "hello"`) {
		t.Errorf("rows: %s", res.Body)
	}
	try("google_bigquery_table.events", ActPut, `{"name":"x","count":2}`)
	try("google_cloud_scheduler_job.tick", ActStart, "")
	res, err := Try(context.Background(), wf, st, sb, model.TryRequest{NodeID: "google_cloud_run_v2_service.web", Action: ActRequest, Method: "GET", Path: "/items"})
	if err != nil || !res.OK || !strings.Contains(res.Body, `"/items"`) {
		t.Fatalf("cloud run request: %+v %v", res, err)
	}
	if res, _ := Try(context.Background(), wf, st, sb, model.TryRequest{NodeID: "google_kms_crypto_key.key", Action: ActDecrypt}); res.OK || res.Error == "" {
		t.Errorf("decrypt without ciphertext: %+v", res)
	}
}

func TestPostmanGCP(t *testing.T) {
	col := Postman(gcpFixture(gcpBase))
	var gcpVar bool
	for _, v := range col.Variable {
		gcpVar = gcpVar || v.Key == "gcpBaseUrl" && v.Value == gcpBase
	}
	if !gcpVar {
		t.Fatalf("variables %+v", col.Variable)
	}
	names := map[string]PostmanItem{}
	for _, it := range col.Item {
		names[it.Name] = it
	}
	if run, ok := names["web"]; !ok || len(run.Item) != 2 || run.Item[0].Request.URL.Raw != "{{gcpBaseUrl}}/run/v2/projects/floci-local/locations/us-central1/services/web/" {
		t.Fatalf("cloud run folder %+v", run)
	}
	svc, ok := names["Google Cloud services (sandbox)"]
	if !ok {
		t.Fatalf("no Google Cloud folder in %+v", col.Item)
	}
	var reqs []string
	for _, it := range svc.Item {
		reqs = append(reqs, it.Request.Method+" "+it.Request.URL.Raw)
	}
	all := strings.Join(reqs, "\n")
	for _, want := range []string{
		"POST {{gcpBaseUrl}}/v1/projects/floci-local/topics/events:publish",
		"POST {{gcpBaseUrl}}/v1/projects/floci-local/subscriptions/events-worker:pull",
		"GET {{gcpBaseUrl}}/v1/projects/floci-local/secrets/api-key/versions/latest:access",
		"POST {{gcpBaseUrl}}/v1/projects/floci-local/locations/us-central1/keyRings/app-ring/cryptoKeys/app-key:encrypt",
		"POST {{gcpBaseUrl}}/bigquery/v2/projects/floci-local/queries",
		"POST {{gcpBaseUrl}}/upload/storage/v1/b/floci-local-files/o?uploadType=media&name=hello.txt",
		"POST {{gcpBaseUrl}}/v1/projects/floci-local/locations/us-central1/jobs/tick:run",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in\n%s", want, all)
		}
	}
}

package endpoints

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Try action IDs only Google Cloud resources offer.
const (
	ActQuery = "query"
)

// gcpLocation is the default region of GCP examples.
const gcpLocation = "us-central1"

// gcpResources builds every Google Cloud resource floci-gcp serves; the
// rest falls through to generic cards.
func (b *builder) gcpResources() {
	b.gcpBuckets()
	b.pubsubTopics()
	b.pubsubSubscriptions()
	b.gcpSecrets()
	b.kmsKeys()
	b.cloudRunServices()
	b.cloudFunctions()
	b.sqlInstances()
	b.bigqueryDatasets()
	b.bigqueryTables()
	b.schedulerJobs()
	b.gkeClusters()
	b.gcpSupporting()
}

// ---------------------------------------------------------------------------
// Sandbox access

func (b *builder) gcpSandboxAccess(base string) model.SandboxAccess {
	project := b.gcpProject()
	host := strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
	return model.SandboxAccess{
		Cloud: model.CloudGCP, URL: base, Region: gcpLocation,
		Credentials: []model.AccessField{
			{Label: "Project", Value: project},
			{Label: "Access token", Value: "fake", Hint: "Any bearer token works, or none at all; the sandbox doesn't check credentials."},
			{Label: "Emulator host", Value: host, Hint: "PUBSUB_EMULATOR_HOST and gRPC clients (plaintext) use host:port without http://."},
		},
		EnvSnippet: strings.Join([]string{
			"export GOOGLE_CLOUD_PROJECT=" + project,
			"export STORAGE_EMULATOR_HOST=" + base,
			"export PUBSUB_EMULATOR_HOST=" + host,
			"# gcloud CLI (one override per API):",
			"echo fake > /tmp/nimbo-gcloud-token",
			"export CLOUDSDK_CORE_PROJECT=" + project + " CLOUDSDK_AUTH_ACCESS_TOKEN_FILE=/tmp/nimbo-gcloud-token",
			"export CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE=" + base + "/ CLOUDSDK_API_ENDPOINT_OVERRIDES_PUBSUB=" + base + "/",
			"export CLOUDSDK_API_ENDPOINT_OVERRIDES_SECRETMANAGER=" + base + "/ CLOUDSDK_API_ENDPOINT_OVERRIDES_CLOUDKMS=" + base + "/",
			"export CLOUDSDK_API_ENDPOINT_OVERRIDES_CLOUDSCHEDULER=" + base + "/ CLOUDSDK_API_ENDPOINT_OVERRIDES_SQL=" + base + "/",
		}, "\n"),
		Notes: []string{
			"Every Google Cloud API of the sandbox answers on this one address: REST (curl, Postman, fetch) and gRPC (official client libraries) alike.",
			"Cloud Storage and Pub/Sub client libraries (Node.js, Python, Go, Java) switch to the sandbox by themselves when STORAGE_EMULATOR_HOST and PUBSUB_EMULATOR_HOST are set.",
			"Other client libraries take the address in code: Node.js gRPC clients { apiEndpoint: \"" + hostOnly(host) + "\", port: " + portOnly(host) + ", sslCreds: grpc.credentials.createInsecure() }; Python clients credentials=AnonymousCredentials(), transport=\"rest\", client_options={\"api_endpoint\": \"" + base + "\"}. Each resource below shows the exact code.",
			"REST paths: Cloud Storage /storage/v1/ (uploads /upload/storage/v1/), Pub/Sub, Secret Manager, KMS, IAM and Cloud Scheduler /v1/, Cloud Run and Cloud Functions /v2/, Cloud SQL /sql/v1beta4/, BigQuery /bigquery/v2/, GKE /container/v1/, Compute Engine /compute/v1/.",
			"This address stays the same when you stop and start the sandbox.",
		},
	}
}

func hostOnly(hostPort string) string {
	h, _, _ := strings.Cut(hostPort, ":")
	return h
}

func portOnly(hostPort string) string {
	_, p, _ := strings.Cut(hostPort, ":")
	return p
}

// ---------------------------------------------------------------------------
// Names and snippet helpers

// gcpProjectOf is the project of r, else the sandbox's.
func (b *builder) gcpProjectOf(r *resource) string {
	if p := str(r, "project"); p != "" {
		return p
	}
	return b.gcpProject()
}

// gcpPath is the "projects/…" resource name of r: its ID when Terraform
// stores the full name, else built from the project and parts.
func (b *builder) gcpPath(r *resource, parts ...string) string {
	if id := str(r, "id"); strings.HasPrefix(id, "projects/") {
		return id
	}
	for _, p := range parts {
		if p == "" {
			return ""
		}
	}
	return "projects/" + b.gcpProjectOf(r) + "/" + strings.Join(parts, "/")
}

// gcpHostPort is the sandbox's host and port for gRPC client libraries.
func (b *builder) gcpHostPort() (host, port string) {
	u, err := url.Parse(b.gcp)
	if err != nil {
		return "127.0.0.1", ""
	}
	return u.Hostname(), u.Port()
}

// jsGCP renders an ES module using Google Cloud client libraries.
func jsGCP(pkgs, body string) string {
	return "// Node.js 18+: npm i " + pkgs + "\n// save as try.mjs and run: node try.mjs\n" + strings.TrimSpace(body) + "\n"
}

// jsGRPC renders the client options of a Node.js gRPC client for the sandbox.
func (b *builder) jsGRPC() string {
	host, port := b.gcpHostPort()
	return fmt.Sprintf("{\n  apiEndpoint: %s,\n  port: %s,\n  sslCreds: grpc.credentials.createInsecure(),\n}", jsStr(host), port)
}

// pyGCP renders a Python script using Google Cloud client libraries.
func pyGCP(pkgs, body string) string {
	return "# pip install " + pkgs + "\n" + strings.TrimSpace(body) + "\n"
}

// pyREST renders the arguments of a Python client using the REST transport
// against the sandbox.
func (b *builder) pyREST() string {
	return fmt.Sprintf("credentials=AnonymousCredentials(),\n    transport=\"rest\",\n    client_options={\"api_endpoint\": %s},", jsStr(b.gcp))
}

// gcloud renders gcloud CLI commands with the sandbox overrides of api.
func (b *builder) gcloud(api, project string, cmds ...string) string {
	return "# gcloud CLI: point it at the sandbox (any token works)\n" +
		"echo fake > /tmp/nimbo-gcloud-token\n" +
		"export CLOUDSDK_CORE_PROJECT=" + shq(project) + " CLOUDSDK_AUTH_ACCESS_TOKEN_FILE=/tmp/nimbo-gcloud-token\n" +
		"export CLOUDSDK_API_ENDPOINT_OVERRIDES_" + api + "=" + shq(b.gcp+"/") + "\n\n" +
		strings.Join(cmds, "\n")
}

// ---------------------------------------------------------------------------
// Cloud Storage

func (b *builder) gcpBuckets() {
	for _, bk := range b.byType["google_storage_bucket"] {
		name := str(bk, "name")
		if name == "" {
			continue
		}
		project := b.gcpProjectOf(bk)
		list := b.gcp + "/storage/v1/b/" + url.PathEscape(name) + "/o"
		upload := b.gcp + "/upload/storage/v1/b/" + url.PathEscape(name) + "/o?uploadType=media&name=hello.txt"
		download := list + "/hello.txt?alt=media"
		e := b.add(bk, KindBucket, "A Cloud Storage bucket for files (objects). Upload, list and download them with the JSON API URLs below or a Cloud Storage client library.",
			target{cloud: model.CloudGCP, name: name})
		e.field("Bucket name", name, "")
		e.field("gs:// URL", "gs://"+name, "How gcloud and Google tools name the bucket.")
		e.field("List objects URL", list, "GET it to list objects; add /<name>?alt=media to download one.")
		e.field("Upload URL", upload, "POST the file content; name= sets the object name.")
		e.snippet("curl", "bash", strings.Join([]string{
			"curl -X POST " + shq(upload) + " \\\n  -H 'Content-Type: text/plain' --data-binary 'Hello from Nimbo'",
			"curl " + shq(list),
			"curl " + shq(download),
		}, "\n\n"))
		e.snippet("gcloud", "bash", b.gcloud("STORAGE", project,
			"echo 'Hello from Nimbo' > hello.txt",
			"gcloud storage cp hello.txt "+shq("gs://"+name+"/hello.txt"),
			"gcloud storage ls "+shq("gs://"+name),
			"gcloud storage cat "+shq("gs://"+name+"/hello.txt")))
		e.snippet("JavaScript", "javascript", jsGCP("@google-cloud/storage", fmt.Sprintf(`
import { Storage } from "@google-cloud/storage";

const storage = new Storage({ apiEndpoint: %s, projectId: %s });
const bucket = storage.bucket(%s);

await bucket.file("hello.txt").save("Hello from Nimbo");
const [files] = await bucket.getFiles();
console.log(files.map((f) => f.name));
const [data] = await bucket.file("hello.txt").download();
console.log(data.toString());`, jsStr(b.gcp), jsStr(project), jsStr(name))))
		e.snippet("Python", "python", pyGCP("google-cloud-storage", fmt.Sprintf(`
from google.auth.credentials import AnonymousCredentials
from google.cloud import storage

client = storage.Client(
    project=%s,
    credentials=AnonymousCredentials(),
    client_options={"api_endpoint": %s},
)
bucket = client.bucket(%s)

bucket.blob("hello.txt").upload_from_string("Hello from Nimbo")
print([blob.name for blob in client.list_blobs(bucket)])
print(bucket.blob("hello.txt").download_as_text())`, jsStr(project), jsStr(b.gcp), jsStr(name))))
		e.action(model.TryAction{ID: ActList, Label: "List files", Description: "Show the first 50 objects.", Input: "none"})
		e.action(model.TryAction{ID: ActPut, Label: "Upload a file", Description: "Store body as an object named key.", Input: "json", Default: s3PutDefault, Writes: true})
		e.action(model.TryAction{ID: ActGet, Label: "Download a file", Description: "Show the content of the object with this name.", Input: "text", Default: "hello.txt"})
	}
}

// ---------------------------------------------------------------------------
// Pub/Sub

func (b *builder) pubsubTopics() {
	for _, tp := range b.byType["google_pubsub_topic"] {
		path := b.gcpPath(tp, "topics", str(tp, "name"))
		if path == "" {
			continue
		}
		project := b.gcpProjectOf(tp)
		name := str(tp, "name")
		publish := b.gcp + "/v1/" + path + ":publish"
		e := b.add(tp, KindTopic, "A Pub/Sub topic. Every message you publish is delivered to each of its subscriptions.",
			target{cloud: model.CloudGCP, name: name, url: path})
		e.field("Topic", path, "The full topic name client libraries and gcloud accept.")
		e.field("Publish URL", publish, "POST {\"messages\":[{\"data\":\"<base64>\"}]} to it.")
		var subs []string
		for _, s := range b.byType["google_pubsub_subscription"] {
			if t := str(s, "topic"); t == path || t == name {
				subs = append(subs, str(s, "name"))
			}
		}
		if len(subs) > 0 {
			e.field("Subscriptions", strings.Join(subs, ", "), "Each receives a copy of every message.")
		} else {
			e.Warning = "This topic has no subscription yet: published messages are dropped. Add a google_pubsub_subscription to receive them."
		}
		data := base64.StdEncoding.EncodeToString([]byte(helloMessage))
		body := `{"messages":[{"data":"` + data + `"}]}`
		e.snippet("curl", "bash", "# data is base64: "+helloMessage+"\ncurl -X POST "+shq(publish)+" \\\n  -H 'Content-Type: application/json' \\\n  -d "+shq(body))
		e.snippet("gcloud", "bash", b.gcloud("PUBSUB", project,
			"gcloud pubsub topics publish "+shq(name)+" --message="+shq(helloMessage)))
		e.snippet("JavaScript", "javascript", jsGCP("@google-cloud/pubsub", fmt.Sprintf(`
import { PubSub } from "@google-cloud/pubsub";

// Or set PUBSUB_EMULATOR_HOST=%s and use new PubSub().
const pubsub = new PubSub({ projectId: %s, apiEndpoint: %s, emulatorMode: true });
const id = await pubsub.topic(%s).publishMessage({ json: %s });
console.log("published message", id);`, b.gcpHostPortString(), jsStr(project), jsStr(b.gcpHostPortString()), jsStr(name), jsLit(helloMessage))))
		e.snippet("Python", "python", pyGCP("google-cloud-pubsub", fmt.Sprintf(`
import json
import os

os.environ["PUBSUB_EMULATOR_HOST"] = %s  # the client then talks to the sandbox
from google.cloud import pubsub_v1

publisher = pubsub_v1.PublisherClient()
future = publisher.publish(%s, json.dumps(%s).encode())
print("published message", future.result())`, jsStr(b.gcpHostPortString()), jsStr(path), pyLit(helloMessage))))
		e.action(model.TryAction{ID: ActPublish, Label: "Publish a message", Description: "Send this text to the topic's subscriptions.", Input: "text", Default: helloMessage, Writes: true})
	}
}

func (b *builder) gcpHostPortString() string {
	h, p := b.gcpHostPort()
	return h + ":" + p
}

func (b *builder) pubsubSubscriptions() {
	for _, s := range b.byType["google_pubsub_subscription"] {
		path := b.gcpPath(s, "subscriptions", str(s, "name"))
		if path == "" {
			continue
		}
		project := b.gcpProjectOf(s)
		name := str(s, "name")
		topic := str(s, "topic")
		pull := b.gcp + "/v1/" + path + ":pull"
		ack := b.gcp + "/v1/" + path + ":acknowledge"
		e := b.add(s, KindQueue, "A Pub/Sub subscription: a mailbox that receives a copy of every message published to its topic. Your code pulls messages from it and acknowledges each one it handled.",
			target{cloud: model.CloudGCP, name: name, url: path})
		e.field("Subscription", path, "The full subscription name client libraries and gcloud accept.")
		e.field("Topic", topic, "Publish to this topic to fill the subscription.")
		e.field("Pull URL", pull, "POST {\"maxMessages\":10} to it.")
		e.field("Acknowledge URL", ack, "POST {\"ackIds\":[…]} with the ackId of each handled message.")
		if d := str(s, "ack_deadline_seconds"); d != "" {
			e.field("Ack deadline", d+" s", "Time to acknowledge a pulled message.")
		}
		for _, pc := range list(s, "push_config") {
			if ep := mstr(pc, "push_endpoint"); ep != "" {
				e.field("Push endpoint", ep, "Messages are POSTed to this URL instead of waiting to be pulled.")
				e.Warning = "This is a push subscription: Pub/Sub delivers messages to its push endpoint, so pulling returns nothing."
			}
		}
		e.snippet("curl", "bash", strings.Join([]string{
			"# Pull up to 10 messages (data is base64)",
			"RES=$(curl -s -X POST " + shq(pull) + " \\\n  -H 'Content-Type: application/json' -d '{\"maxMessages\":10}')",
			"echo \"$RES\"",
			"# Acknowledge the handled messages by their ackId",
			"ACK=$(echo \"$RES\" | python3 -c 'import json,sys; print(json.dumps({\"ackIds\": [m[\"ackId\"] for m in json.load(sys.stdin).get(\"receivedMessages\", [])]}))')",
			"curl -X POST " + shq(ack) + " \\\n  -H 'Content-Type: application/json' -d \"$ACK\"",
		}, "\n"))
		e.snippet("gcloud", "bash", b.gcloud("PUBSUB", project,
			"gcloud pubsub subscriptions pull "+shq(name)+" --auto-ack --limit=10"))
		e.snippet("JavaScript", "javascript", jsGCP("@google-cloud/pubsub @grpc/grpc-js", fmt.Sprintf(`
import grpc from "@grpc/grpc-js";
import { v1 } from "@google-cloud/pubsub";

const subscriber = new v1.SubscriberClient(%s);
const subscription = %s;

const [res] = await subscriber.pull({ subscription, maxMessages: 10 });
for (const m of res.receivedMessages) console.log(m.message.data.toString());
const ackIds = res.receivedMessages.map((m) => m.ackId);
if (ackIds.length) await subscriber.acknowledge({ subscription, ackIds });`, b.jsGRPC(), jsStr(path))))
		e.snippet("Python", "python", pyGCP("google-cloud-pubsub", fmt.Sprintf(`
import os

os.environ["PUBSUB_EMULATOR_HOST"] = %s  # the client then talks to the sandbox
from google.cloud import pubsub_v1

subscriber = pubsub_v1.SubscriberClient()
subscription = %s

res = subscriber.pull(subscription=subscription, max_messages=10)
for m in res.received_messages:
    print(m.message.data.decode())
ack_ids = [m.ack_id for m in res.received_messages]
if ack_ids:
    subscriber.acknowledge(subscription=subscription, ack_ids=ack_ids)`, jsStr(b.gcpHostPortString()), jsStr(path))))
		e.action(model.TryAction{ID: ActReceive, Label: "Pull messages", Description: "Pull up to 10 waiting messages and acknowledge them, so they are removed from the subscription.", Input: "none", Writes: true})
	}
}

// ---------------------------------------------------------------------------
// Secret Manager

func (b *builder) gcpSecrets() {
	for _, s := range b.byType["google_secret_manager_secret"] {
		path := b.gcpPath(s, "secrets", str(s, "secret_id"))
		if path == "" {
			continue
		}
		project := b.gcpProjectOf(s)
		id := first(s, "secret_id")
		access := b.gcp + "/v1/" + path + "/versions/latest:access"
		e := b.add(s, KindSecret, "A secret (password, API key, …) with numbered versions. Your code reads the latest version by name; in the sandbox you can look at its value.",
			target{cloud: model.CloudGCP, name: id, url: path})
		e.Label = b.label(s, "secret_id")
		e.field("Secret", path, "The full secret name client libraries accept.")
		e.field("Access URL", access, "GET it for the latest value (payload.data is base64).")
		versions := 0
		for _, v := range b.byType["google_secret_manager_secret_version"] {
			if str(v, "secret") == path || str(v, "secret") == id {
				b.done[v] = true
				versions++
			}
		}
		if versions == 0 {
			e.Warning = "This secret has no version yet, so there is no value to read. Add a google_secret_manager_secret_version (or use Add a version below)."
		} else {
			e.field("Versions", strconv.Itoa(versions), "Created by Terraform (google_secret_manager_secret_version).")
		}
		e.snippet("curl", "bash", "curl "+shq(access)+"\n\n# Just the value:\ncurl -s "+shq(access)+" | python3 -c 'import base64,json,sys; print(base64.b64decode(json.load(sys.stdin)[\"payload\"][\"data\"]).decode())'")
		e.snippet("gcloud", "bash", b.gcloud("SECRETMANAGER", project,
			"gcloud secrets versions access latest --secret="+shq(id)))
		e.snippet("JavaScript", "javascript", jsGCP("@google-cloud/secret-manager @grpc/grpc-js", fmt.Sprintf(`
import grpc from "@grpc/grpc-js";
import { SecretManagerServiceClient } from "@google-cloud/secret-manager";

const client = new SecretManagerServiceClient(%s);
const [version] = await client.accessSecretVersion({ name: %s });
console.log(version.payload.data.toString());`, b.jsGRPC(), jsStr(path+"/versions/latest"))))
		e.snippet("Python", "python", pyGCP("google-cloud-secret-manager", fmt.Sprintf(`
from google.auth.credentials import AnonymousCredentials
from google.cloud import secretmanager

client = secretmanager.SecretManagerServiceClient(
    %s
)
version = client.access_secret_version(name=%s)
print(version.payload.data.decode())`, b.pyREST(), jsStr(path+"/versions/latest"))))
		e.action(model.TryAction{ID: ActGet, Label: "Show the value", Description: "Read the latest version's value (sandbox data only).", Input: "none"})
		e.action(model.TryAction{ID: ActPut, Label: "Add a version", Description: "Store this text as the secret's new latest value.", Input: "text", Default: "new-secret-value", Writes: true})
	}
}

// ---------------------------------------------------------------------------
// Cloud KMS

func (b *builder) kmsKeys() {
	for _, k := range b.byType["google_kms_crypto_key"] {
		path := str(k, "id")
		if !strings.HasPrefix(path, "projects/") {
			if ring := str(k, "key_ring"); strings.HasPrefix(ring, "projects/") && str(k, "name") != "" {
				path = ring + "/cryptoKeys/" + str(k, "name")
			} else {
				continue
			}
		}
		purpose := orDefault(str(k, "purpose"), "ENCRYPT_DECRYPT")
		e := b.add(k, KindKey, "An encryption key in Cloud KMS. Your code sends data to KMS to encrypt or decrypt it; the key itself never leaves KMS.",
			target{cloud: model.CloudGCP, name: str(k, "name"), url: path})
		e.field("Key", path, "The full key name client libraries and gcloud accept.")
		e.field("Purpose", purpose, "")
		ring := str(k, "key_ring")
		location := gcpLocation
		if p := strings.Split(path, "/"); len(p) >= 8 {
			ring, location = p[5], p[3]
		}
		if purpose != "ENCRYPT_DECRYPT" {
			e.Summary = "A " + purpose + " key in Cloud KMS. The Try actions below cover symmetric (ENCRYPT_DECRYPT) keys only."
			continue
		}
		encrypt := b.gcp + "/v1/" + path + ":encrypt"
		decrypt := b.gcp + "/v1/" + path + ":decrypt"
		e.field("Encrypt URL", encrypt, "POST {\"plaintext\":\"<base64>\"} to it.")
		e.field("Decrypt URL", decrypt, "POST {\"ciphertext\":\"<base64 from encrypt>\"} to it.")
		plain := base64.StdEncoding.EncodeToString([]byte("Hello from Nimbo"))
		e.snippet("curl", "bash", strings.Join([]string{
			"# Encrypt (plaintext is base64 of \"Hello from Nimbo\")",
			"CT=$(curl -s -X POST " + shq(encrypt) + " \\\n  -H 'Content-Type: application/json' -d '{\"plaintext\":\"" + plain + "\"}' \\\n  | python3 -c 'import json,sys; print(json.load(sys.stdin)[\"ciphertext\"])')",
			"echo \"ciphertext: $CT\"",
			"# Decrypt it (plaintext comes back base64)",
			"curl -s -X POST " + shq(decrypt) + " \\\n  -H 'Content-Type: application/json' -d \"{\\\"ciphertext\\\":\\\"$CT\\\"}\" \\\n  | python3 -c 'import base64,json,sys; print(base64.b64decode(json.load(sys.stdin)[\"plaintext\"]).decode())'",
		}, "\n"))
		e.snippet("gcloud", "bash", b.gcloud("CLOUDKMS", b.gcpProjectOf(k),
			"echo -n 'Hello from Nimbo' > plain.txt",
			fmt.Sprintf("gcloud kms encrypt --location=%s --keyring=%s --key=%s \\\n  --plaintext-file=plain.txt --ciphertext-file=secret.enc", shq(location), shq(ring), shq(str(k, "name"))),
			fmt.Sprintf("gcloud kms decrypt --location=%s --keyring=%s --key=%s \\\n  --ciphertext-file=secret.enc --plaintext-file=-", shq(location), shq(ring), shq(str(k, "name")))))
		e.snippet("JavaScript", "javascript", jsGCP("@google-cloud/kms @grpc/grpc-js", fmt.Sprintf(`
import grpc from "@grpc/grpc-js";
import { KeyManagementServiceClient } from "@google-cloud/kms";

const client = new KeyManagementServiceClient(%s);
const name = %s;

const [encrypted] = await client.encrypt({ name, plaintext: Buffer.from("Hello from Nimbo") });
console.log(Buffer.from(encrypted.ciphertext).toString("base64"));
const [decrypted] = await client.decrypt({ name, ciphertext: encrypted.ciphertext });
console.log(Buffer.from(decrypted.plaintext).toString());`, b.jsGRPC(), jsStr(path))))
		e.snippet("Python", "python", pyGCP("google-cloud-kms", fmt.Sprintf(`
import base64

from google.auth.credentials import AnonymousCredentials
from google.cloud import kms

client = kms.KeyManagementServiceClient(
    %s
)
name = %s

encrypted = client.encrypt(name=name, plaintext=b"Hello from Nimbo")
print(base64.b64encode(encrypted.ciphertext).decode())
print(client.decrypt(name=name, ciphertext=encrypted.ciphertext).plaintext.decode())`, b.pyREST(), jsStr(path))))
		e.action(model.TryAction{ID: ActEncrypt, Label: "Encrypt", Description: "Encrypt this text; the result is base64 ciphertext.", Input: "text", Default: "Hello from Nimbo"})
		e.action(model.TryAction{ID: ActDecrypt, Label: "Decrypt", Description: "Paste a ciphertext from Encrypt to get the text back.", Input: "text"})
	}
}

// ---------------------------------------------------------------------------
// Cloud Run

func (b *builder) cloudRunServices() {
	for _, s := range b.byType["google_cloud_run_v2_service"] {
		path := b.gcpPath(s, "locations", str(s, "location"), "services", str(s, "name"))
		if path == "" {
			continue
		}
		base := b.gcp + "/run/v2/" + path
		e := b.add(s, KindWebService, "A Cloud Run service: your container answers HTTP requests on its own address. Call it from your browser, curl, Postman or your app.",
			target{cloud: model.CloudGCP, name: str(s, "name"), url: path, httpBase: base})
		e.field("URL", base+"/", "Any path after it reaches your container. After the sandbox restarts it answers 503 until the service is deployed again (the emulator doesn't restart Cloud Run containers).")
		if u := b.sandboxHostURL(str(s, "uri")); u != "" {
			e.field("Service URL", u, "The same service on its own host name (as on Google Cloud); *.localhost.floci.io resolves to 127.0.0.1 through public DNS.")
		}
		for _, t := range list(s, "template") {
			for _, c := range anyList(t["containers"]) {
				e.field("Image", mstr(c, "image"), "")
			}
		}
		e.field("Region", str(s, "location"), "")
		if str(s, "uri") == "" {
			e.Warning = "The service has no URL yet (it is not ready). Check that its container image starts and listens on $PORT (8080)."
		}
		e.Endpoints = []model.HTTPEndpoint{{Method: "ANY", Path: "/", URL: base + "/", Description: "Any method and path work."}}
		e.addHTTPSnippets()
		e.action(requestAction)
	}
}

// sandboxHostURL rewrites a floci-gcp generated URL ("http://x.run.localhost.
// floci.io:4588") to the sandbox's published port.
func (b *builder) sandboxHostURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || !strings.HasSuffix(u.Hostname(), ".localhost.floci.io") {
		return ""
	}
	_, port := b.gcpHostPort()
	u.Host = u.Hostname() + ":" + port
	return u.String()
}

func anyList(v any) []map[string]any {
	var out []map[string]any
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Cloud Functions

func (b *builder) cloudFunctions() {
	for _, f := range b.byType["google_cloudfunctions2_function"] {
		path := b.gcpPath(f, "locations", str(f, "location"), "functions", str(f, "name"))
		if path == "" {
			continue
		}
		get := b.gcp + "/v2/" + path
		e := b.add(f, KindFunction, "A Cloud Functions (2nd gen) function. The sandbox keeps its settings but doesn't run its code.",
			target{cloud: model.CloudGCP, name: str(f, "name"), url: path})
		e.Warning = "The sandbox emulates the Cloud Functions control plane only: the function is registered but never runs, and its cloudfunctions.net URL points at real Google Cloud. To run code locally, deploy it as a Cloud Run service (google_cloud_run_v2_service with a container image). Changing the function with Terraform fails in the sandbox; replace it instead."
		e.field("Function", path, "")
		for _, bc := range list(f, "build_config") {
			e.field("Runtime", mstr(bc, "runtime"), "")
			e.field("Entry point", mstr(bc, "entry_point"), "")
		}
		e.field("Region", str(f, "location"), "")
		e.field("State", str(f, "state"), "")
		e.snippet("curl", "bash", "curl "+shq(get))
		e.action(model.TryAction{ID: ActGet, Label: "Show its settings", Description: "Read the function as the sandbox stores it.", Input: "none"})
	}
}

// ---------------------------------------------------------------------------
// Cloud SQL

func (b *builder) sqlInstances() {
	for _, d := range b.byType["google_sql_database_instance"] {
		name := str(d, "name")
		if name == "" {
			continue
		}
		project := b.gcpProjectOf(d)
		version := str(d, "database_version")
		engine, port, client := "PostgreSQL", "5432", "psql"
		if strings.HasPrefix(version, "MYSQL") {
			engine, port, client = "MySQL", "3306", "mysql"
		} else if strings.HasPrefix(version, "SQLSERVER") {
			engine, port, client = "SQL Server", "1433", ""
		}
		e := b.add(d, KindDatabase, "A managed "+engine+" database server (Cloud SQL). Your code connects to it with a normal "+engine+" driver.",
			target{cloud: model.CloudGCP, name: name})
		e.Warning = fmt.Sprintf(noHostAccess, "database") + " To open a SQL shell, run the docker command below."
		e.field("Connection name", str(d, "connection_name"), "project:region:instance, used by the Cloud SQL connectors and Auth Proxy.")
		e.field("Host", first(d, "first_ip_address", "public_ip_address", "private_ip_address"), "")
		e.field("Port", port, "")
		e.field("Engine", version, "")
		var dbs, users []string
		for _, db := range b.byType["google_sql_database"] {
			if str(db, "instance") == name {
				b.done[db] = true
				dbs = append(dbs, str(db, "name"))
			}
		}
		for _, u := range b.byType["google_sql_user"] {
			if str(u, "instance") == name {
				b.done[u] = true
				users = append(users, str(u, "name"))
			}
		}
		e.field("Databases", strings.Join(dbs, ", "), "")
		e.field("Users", strings.Join(users, ", "), "Passwords are the ones set in Terraform.")
		if client != "" {
			user, db := "postgres", "postgres"
			if client == "mysql" {
				user, db = "root", ""
			}
			if len(users) > 0 {
				user = users[0]
			}
			if len(dbs) > 0 {
				db = dbs[0]
			}
			container := "floci-gcp-nimbo-" + b.wfID + "-cloudsql-" + project + "-" + name
			cmd := "docker exec -it " + shq(container) + " psql -U " + shq(user) + " -d " + shq(db)
			if client == "mysql" {
				cmd = "docker exec -it " + shq(container) + " mysql -u " + shq(user) + " -p " + shq(db)
			}
			e.snippet(client+" (in the sandbox)", "bash", "# Opens a SQL shell inside the database container of the sandbox\n"+cmd)
		}
		e.snippet("curl", "bash", "# Instance details (addresses, state, settings)\ncurl "+shq(b.gcp+"/sql/v1beta4/projects/"+project+"/instances/"+name))
	}
}

// ---------------------------------------------------------------------------
// BigQuery

func (b *builder) bigqueryDatasets() {
	for _, d := range b.byType["google_bigquery_dataset"] {
		id := str(d, "dataset_id")
		if id == "" {
			continue
		}
		project := b.gcpProjectOf(d)
		query := b.gcp + "/bigquery/v2/projects/" + project + "/queries"
		sql := "SELECT 1 AS hello"
		for _, t := range b.byType["google_bigquery_table"] {
			if str(t, "dataset_id") == id && str(t, "table_id") != "" {
				sql = "SELECT * FROM `" + id + "." + str(t, "table_id") + "` LIMIT 10"
				break
			}
		}
		e := b.add(d, KindDatabase, "A BigQuery dataset: a folder of analytics tables you query with SQL (GoogleSQL).",
			target{cloud: model.CloudGCP, name: id, url: project})
		e.field("Dataset", project+"."+id, "Write tables as `"+id+".<table>` in queries.")
		e.field("Location", str(d, "location"), "")
		e.field("Query URL", query, "POST {\"query\":\"…\",\"useLegacySql\":false} to it.")
		e.field("Tables URL", b.gcp+"/bigquery/v2/projects/"+project+"/datasets/"+id+"/tables", "GET it to list the dataset's tables.")
		body := mustJSON(map[string]any{"query": sql, "useLegacySql": false})
		e.snippet("curl", "bash", "curl -X POST "+shq(query)+" \\\n  -H 'Content-Type: application/json' \\\n  -d "+shq(body))
		e.snippet("JavaScript", "javascript", jsGCP("@google-cloud/bigquery", fmt.Sprintf(`
import { BigQuery } from "@google-cloud/bigquery";

const bigquery = new BigQuery({ projectId: %s, apiEndpoint: %s });
const [rows] = await bigquery.query(%s);
console.log(rows);`, jsStr(project), jsStr(b.gcp), jsStr(sql))))
		e.snippet("Python", "python", pyGCP("google-cloud-bigquery", fmt.Sprintf(`
from google.auth.credentials import AnonymousCredentials
from google.cloud import bigquery

client = bigquery.Client(
    project=%s,
    credentials=AnonymousCredentials(),
    client_options={"api_endpoint": %s},
)
for row in client.query(%s).result():
    print(dict(row))`, jsStr(project), jsStr(b.gcp), jsStr(sql))))
		e.action(model.TryAction{ID: ActQuery, Label: "Run a query", Description: "Run this GoogleSQL query and show the rows.", Input: "text", Default: sql})
		e.action(model.TryAction{ID: ActList, Label: "List tables", Description: "Show the dataset's tables.", Input: "none"})
	}
}

func (b *builder) bigqueryTables() {
	for _, t := range b.byType["google_bigquery_table"] {
		ds, id := str(t, "dataset_id"), str(t, "table_id")
		if ds == "" || id == "" {
			continue
		}
		project := b.gcpProjectOf(t)
		tablePath := "projects/" + project + "/datasets/" + ds + "/tables/" + id
		rows := b.gcp + "/bigquery/v2/" + tablePath + "/data"
		insert := b.gcp + "/bigquery/v2/" + tablePath + "/insertAll"
		row := bigqueryRow(str(t, "schema"))
		e := b.add(t, KindTable, "A BigQuery table: rows with a fixed schema that you add with inserts and read with SQL queries.",
			target{cloud: model.CloudGCP, name: id, url: tablePath, item: row})
		e.Label = b.label(t, "table_id")
		e.field("Table", project+"."+ds+"."+id, "Write it as `"+ds+"."+id+"` in queries.")
		e.field("Columns", bigqueryColumns(str(t, "schema")), "")
		e.field("Rows URL", rows, "GET it to read rows.")
		e.field("Insert URL", insert, "POST {\"rows\":[{\"json\":{…}}]} to it.")
		insertBody := `{"rows":[{"json":` + row + `}]}`
		e.snippet("curl", "bash", strings.Join([]string{
			"curl -X POST " + shq(insert) + " \\\n  -H 'Content-Type: application/json' \\\n  -d " + shq(insertBody),
			"curl " + shq(rows+"?maxResults=25"),
		}, "\n\n"))
		e.snippet("JavaScript", "javascript", jsGCP("@google-cloud/bigquery", fmt.Sprintf(`
import { BigQuery } from "@google-cloud/bigquery";

const bigquery = new BigQuery({ projectId: %s, apiEndpoint: %s });
const table = bigquery.dataset(%s).table(%s);

await table.insert([%s]);
const [rows] = await table.getRows({ maxResults: 25 });
console.log(rows);`, jsStr(project), jsStr(b.gcp), jsStr(ds), jsStr(id), jsLit(row))))
		e.snippet("Python", "python", pyGCP("google-cloud-bigquery", fmt.Sprintf(`
from google.auth.credentials import AnonymousCredentials
from google.cloud import bigquery

client = bigquery.Client(
    project=%s,
    credentials=AnonymousCredentials(),
    client_options={"api_endpoint": %s},
)
table = %s

print(client.insert_rows_json(table, [%s]))  # [] means every row was stored
for row in client.list_rows(table, max_results=25):
    print(dict(row))`, jsStr(project), jsStr(b.gcp), jsStr(project+"."+ds+"."+id), pyLit(row))))
		e.action(model.TryAction{ID: ActScan, Label: "Show rows", Description: "Read the first 25 rows.", Input: "none"})
		e.action(model.TryAction{ID: ActPut, Label: "Insert a row", Description: "Add this row (column → value).", Input: "json", Default: prettyJSON([]byte(row)), Writes: true})
	}
}

type bqField struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Mode string `json:"mode"`
}

func bigqueryFields(schema string) []bqField {
	var fields []bqField
	_ = json.Unmarshal([]byte(schema), &fields)
	return fields
}

func bigqueryColumns(schema string) string {
	var cols []string
	for _, f := range bigqueryFields(schema) {
		cols = append(cols, f.Name+" "+f.Type)
	}
	return strings.Join(cols, ", ")
}

// bigqueryRow is an example row for a table schema, as compact JSON.
func bigqueryRow(schema string) string {
	row := map[string]any{}
	for _, f := range bigqueryFields(schema) {
		if f.Mode == "REPEATED" {
			continue
		}
		switch f.Type {
		case "STRING":
			row[f.Name] = "example"
		case "INTEGER", "INT64":
			row[f.Name] = 1
		case "FLOAT", "FLOAT64", "NUMERIC", "BIGNUMERIC":
			row[f.Name] = 1.5
		case "BOOLEAN", "BOOL":
			row[f.Name] = true
		case "TIMESTAMP", "DATETIME":
			row[f.Name] = "2026-01-01 00:00:00"
		case "DATE":
			row[f.Name] = "2026-01-01"
		}
	}
	if len(row) == 0 {
		return "{}"
	}
	return mustJSON(row)
}

// ---------------------------------------------------------------------------
// Cloud Scheduler

func (b *builder) schedulerJobs() {
	for _, j := range b.byType["google_cloud_scheduler_job"] {
		path := b.gcpPath(j, "locations", str(j, "region"), "jobs", str(j, "name"))
		if path == "" {
			continue
		}
		run := b.gcp + "/v1/" + path + ":run"
		e := b.add(j, KindGeneric, "A Cloud Scheduler job: it runs its target on a cron schedule. Run it now to test what it triggers.",
			target{cloud: model.CloudGCP, name: str(j, "name"), url: path})
		e.Supporting = false
		e.field("Job", path, "")
		e.field("Schedule", strings.TrimSpace(str(j, "schedule")+" ("+orDefault(str(j, "time_zone"), "UTC")+")"), "Standard cron: minute hour day month weekday.")
		for _, t := range list(j, "pubsub_target") {
			e.field("Publishes to", mstr(t, "topic_name"), "Each run publishes the job's message to this topic.")
		}
		for _, t := range list(j, "http_target") {
			e.field("Calls", mstr(t, "http_method")+" "+mstr(t, "uri"), "Each run sends this request.")
		}
		e.field("State", str(j, "state"), "")
		e.field("Run URL", run, "POST {} to run the job now.")
		e.snippet("curl", "bash", "curl -X POST "+shq(run)+" -H 'Content-Type: application/json' -d '{}'")
		e.snippet("gcloud", "bash", b.gcloud("CLOUDSCHEDULER", b.gcpProjectOf(j),
			"gcloud scheduler jobs run "+shq(str(j, "name"))+" --location="+shq(str(j, "region"))))
		e.action(model.TryAction{ID: ActStart, Label: "Run now", Description: "Run the job once, as if its schedule fired.", Input: "none", Writes: true})
	}
}

// ---------------------------------------------------------------------------
// GKE

func (b *builder) gkeClusters() {
	for _, c := range b.byType["google_container_cluster"] {
		name, endpoint := str(c, "name"), str(c, "endpoint")
		if name == "" {
			continue
		}
		server := ""
		if endpoint != "" {
			server = "https://" + endpoint
		}
		// The CA certificate is read from the GKE API: Terraform stores it
		// empty when the cluster was still starting.
		api := b.gcp + "/container/v1/" + b.gcpPath(c, "locations", str(c, "location"), "clusters", name)
		e := b.add(c, KindKubernetes, "A GKE Kubernetes cluster. Use kubectl (or any Kubernetes client) with the kubeconfig below to deploy and inspect workloads.",
			target{cloud: model.CloudGCP, name: name, url: server, httpBase: api})
		for _, np := range b.byType["google_container_node_pool"] {
			if str(np, "cluster") == name || strings.HasSuffix(str(np, "cluster"), "/clusters/"+name) {
				b.done[np] = true
			}
		}
		e.field("API server", server, "The Kubernetes API of the cluster, published on your computer. The cluster doesn't survive a sandbox restart; recreate it to use it again.")
		e.field("Cluster", name, "")
		e.field("Location", str(c, "location"), "")
		e.field("Kubernetes version", first(c, "master_version", "min_master_version"), "")
		e.field("Token", "fake", "Any bearer token is accepted and gets cluster-admin rights.")
		e.field("Cluster details URL", api, "GET it for the endpoint and CA certificate (masterAuth.clusterCaCertificate).")
		if server == "" {
			e.Warning = "The cluster has no API endpoint yet. Re-run Deploy to refresh its state."
			continue
		}
		file := "kubeconfig-" + name + ".yaml"
		e.snippet("kubectl", "bash", fmt.Sprintf(`# The cluster's CA certificate, from the sandbox's GKE API
CA=$(curl -s %s | python3 -c 'import json,sys; print(json.load(sys.stdin)["masterAuth"]["clusterCaCertificate"])')
cat > %s <<EOF
apiVersion: v1
kind: Config
clusters:
- name: %s
  cluster:
    server: %s
    certificate-authority-data: $CA
users:
- name: %s
  user:
    token: fake
contexts:
- name: %s
  context:
    cluster: %s
    user: %s
current-context: %s
EOF
export KUBECONFIG=$PWD/%s
kubectl get nodes
kubectl get namespaces`, shq(api), file, name, server, name, name, name, name, name, file))
		e.action(model.TryAction{ID: ActList, Label: "List nodes and namespaces", Description: "Ask the cluster's Kubernetes API for its nodes and namespaces.", Input: "none"})
	}
}

// ---------------------------------------------------------------------------
// Supporting resources

// gcpSummaries explain Google Cloud resources that have nothing to call;
// gcpPlumbing ones are listed as supporting.
var (
	gcpSummaries = map[string]string{
		"google_service_account":         "An identity for your workloads (service account). Nothing to call; roles granted to it decide what it may do. The sandbox doesn't check identities.",
		"google_service_account_key":     "A key file for a service account. Not needed in the sandbox: every request is accepted.",
		"google_kms_key_ring":            "A folder of KMS keys. Nothing to call; use its keys to encrypt and decrypt.",
		"google_compute_network":         "A VPC network. Nothing to call; it connects the resources placed in it.",
		"google_compute_subnetwork":      "An IP range of a VPC network in one region. Nothing to call.",
		"google_compute_firewall":        "A firewall rule of a VPC network. Nothing to call; it decides which traffic may pass.",
		"google_compute_address":         "A reserved IP address. Nothing to call; attach it to a resource.",
		"google_compute_router":          "A Cloud Router of a VPC network. Nothing to call.",
		"google_compute_router_nat":      "Outbound internet access (Cloud NAT) for a VPC network. Nothing to call.",
		"google_container_node_pool":     "Worker nodes of a GKE cluster. Use the cluster's kubeconfig to run workloads on them.",
		"google_sql_database":            "A database inside a Cloud SQL instance. Connect to the instance to use it.",
		"google_sql_user":                "A user of a Cloud SQL instance. Connect to the instance with it.",
		"google_project_service":         "Turns on a Google Cloud API for the project. Nothing to call.",
		"google_storage_bucket_object":   "A file stored in a Cloud Storage bucket. Download it through the bucket.",
		"google_pubsub_topic_iam_member": "Lets an identity use a Pub/Sub topic. Nothing to call.",
	}
	gcpPlumbing = []string{
		"google_service_account", "google_service_account_key", "google_kms_key_ring", "google_compute_network",
		"google_compute_subnetwork", "google_compute_firewall", "google_compute_address", "google_compute_router",
		"google_compute_router_nat", "google_container_node_pool", "google_sql_database", "google_sql_user",
	}
)

func (b *builder) gcpSupporting() {
	for _, r := range b.ordered {
		if b.done[r] || !strings.HasPrefix(r.Type, "google_") {
			continue
		}
		summary, known := gcpSummaries[r.Type]
		plumbing := slices.Contains(gcpPlumbing, r.Type)
		if !known && !plumbing {
			continue
		}
		b.generic(r)
		e := &b.entries[len(b.entries)-1]
		if known {
			e.Summary = summary
		}
		if plumbing {
			e.Supporting = true
		}
	}
}

// ---------------------------------------------------------------------------
// Try

func (b *builder) tryGCP(ctx context.Context, e *entry, action, input string) (model.TryResult, error) {
	t := e.t
	jsonHeader := map[string]string{"Content-Type": "application/json"}
	switch e.Kind + "/" + action {
	case KindBucket + "/" + ActList:
		return doHTTP(ctx, http.MethodGet, b.gcp+"/storage/v1/b/"+url.PathEscape(t.name)+"/o?maxResults=50", nil, ""), nil
	case KindBucket + "/" + ActPut:
		var in struct {
			Key  string `json:"key"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal([]byte(input), &in); err != nil || in.Key == "" {
			return model.TryResult{Error: `Input must be {"key": "<object name>", "body": "<text>"}.`}, nil
		}
		u := b.gcp + "/upload/storage/v1/b/" + url.PathEscape(t.name) + "/o?uploadType=media&name=" + url.QueryEscape(in.Key)
		return doHTTP(ctx, http.MethodPost, u, map[string]string{"Content-Type": "text/plain; charset=utf-8"}, in.Body), nil
	case KindBucket + "/" + ActGet:
		u := b.gcp + "/storage/v1/b/" + url.PathEscape(t.name) + "/o/" + url.PathEscape(strings.TrimSpace(input)) + "?alt=media"
		return doHTTP(ctx, http.MethodGet, u, nil, ""), nil

	case KindTopic + "/" + ActPublish:
		body := mustJSON(map[string]any{"messages": []map[string]any{{"data": base64.StdEncoding.EncodeToString([]byte(input))}}})
		return doHTTP(ctx, http.MethodPost, b.gcp+"/v1/"+t.url+":publish", jsonHeader, body), nil

	case KindQueue + "/" + ActReceive:
		res := doHTTP(ctx, http.MethodPost, b.gcp+"/v1/"+t.url+":pull", jsonHeader, `{"maxMessages":10,"returnImmediately":true}`)
		if !res.OK {
			return res, nil
		}
		var pulled struct {
			ReceivedMessages []struct {
				AckID   string `json:"ackId"`
				Message struct {
					MessageID   string            `json:"messageId"`
					Data        string            `json:"data"`
					Attributes  map[string]string `json:"attributes,omitempty"`
					PublishTime string            `json:"publishTime,omitempty"`
				} `json:"message"`
			} `json:"receivedMessages"`
		}
		if err := json.Unmarshal([]byte(res.Body), &pulled); err != nil {
			return res, nil
		}
		type message struct {
			MessageID   string            `json:"messageId"`
			Data        string            `json:"data"`
			Attributes  map[string]string `json:"attributes,omitempty"`
			PublishTime string            `json:"publishTime,omitempty"`
		}
		msgs := []message{}
		var ackIDs []string
		for _, m := range pulled.ReceivedMessages {
			data := m.Message.Data
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				data = string(raw)
			}
			msgs = append(msgs, message{m.Message.MessageID, data, m.Message.Attributes, m.Message.PublishTime})
			ackIDs = append(ackIDs, m.AckID)
		}
		if len(ackIDs) > 0 {
			if ack := doHTTP(ctx, http.MethodPost, b.gcp+"/v1/"+t.url+":acknowledge", jsonHeader, mustJSON(map[string]any{"ackIds": ackIDs})); !ack.OK {
				return ack, nil
			}
		}
		return jsonResult(plural(len(msgs), "message", "messages")+" received and acknowledged", msgs), nil

	case KindSecret + "/" + ActGet:
		res := doHTTP(ctx, http.MethodGet, b.gcp+"/v1/"+t.url+"/versions/latest:access", nil, "")
		if !res.OK {
			return res, nil
		}
		var v struct {
			Name    string `json:"name"`
			Payload struct {
				Data string `json:"data"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(res.Body), &v) != nil {
			return res, nil
		}
		raw, err := base64.StdEncoding.DecodeString(v.Payload.Data)
		if err != nil {
			return res, nil
		}
		body, ct := textOrJSON(string(raw))
		return model.TryResult{OK: true, Status: v.Name, ContentType: ct, Body: body}, nil
	case KindSecret + "/" + ActPut:
		body := mustJSON(map[string]any{"payload": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(input))}})
		return doHTTP(ctx, http.MethodPost, b.gcp+"/v1/"+t.url+":addVersion", jsonHeader, body), nil

	case KindKey + "/" + ActEncrypt:
		body := mustJSON(map[string]any{"plaintext": base64.StdEncoding.EncodeToString([]byte(input))})
		res := doHTTP(ctx, http.MethodPost, b.gcp+"/v1/"+t.url+":encrypt", jsonHeader, body)
		if !res.OK {
			return res, nil
		}
		var out struct {
			Ciphertext string `json:"ciphertext"`
		}
		if json.Unmarshal([]byte(res.Body), &out) != nil || out.Ciphertext == "" {
			return res, nil
		}
		return model.TryResult{OK: true, Status: "Encrypted (base64 ciphertext; paste it into Decrypt)", ContentType: "text/plain", Body: out.Ciphertext}, nil
	case KindKey + "/" + ActDecrypt:
		ct := strings.TrimSpace(input)
		if ct == "" {
			return model.TryResult{Error: "Paste a ciphertext from Encrypt."}, nil
		}
		res := doHTTP(ctx, http.MethodPost, b.gcp+"/v1/"+t.url+":decrypt", jsonHeader, mustJSON(map[string]any{"ciphertext": ct}))
		if !res.OK {
			return res, nil
		}
		var out struct {
			Plaintext string `json:"plaintext"`
		}
		if json.Unmarshal([]byte(res.Body), &out) != nil {
			return res, nil
		}
		raw, err := base64.StdEncoding.DecodeString(out.Plaintext)
		if err != nil {
			return res, nil
		}
		return model.TryResult{OK: true, Status: "Decrypted", ContentType: "text/plain", Body: string(raw)}, nil

	case KindFunction + "/" + ActGet:
		return doHTTP(ctx, http.MethodGet, b.gcp+"/v2/"+t.url, nil, ""), nil

	case KindDatabase + "/" + ActQuery:
		body := mustJSON(map[string]any{"query": input, "useLegacySql": false})
		return bigqueryRows(doHTTP(ctx, http.MethodPost, b.gcp+"/bigquery/v2/projects/"+t.url+"/queries", jsonHeader, body)), nil
	case KindDatabase + "/" + ActList:
		res := doHTTP(ctx, http.MethodGet, b.gcp+"/bigquery/v2/projects/"+t.url+"/datasets/"+url.PathEscape(t.name)+"/tables", nil, "")
		if !res.OK {
			return res, nil
		}
		var out struct {
			Tables []struct {
				TableReference struct {
					TableID string `json:"tableId"`
				} `json:"tableReference"`
				Type string `json:"type"`
			} `json:"tables"`
		}
		if json.Unmarshal([]byte(res.Body), &out) != nil {
			return res, nil
		}
		names := []string{}
		for _, tb := range out.Tables {
			names = append(names, tb.TableReference.TableID)
		}
		return jsonResult(plural(len(names), "table", "tables"), names), nil

	case KindTable + "/" + ActScan:
		return bigqueryRows(doHTTP(ctx, http.MethodGet, b.gcp+"/bigquery/v2/"+t.url+"/data?maxResults=25", nil, ""), b.tableSchema(e)...), nil
	case KindTable + "/" + ActPut:
		var row map[string]any
		if err := json.Unmarshal([]byte(input), &row); err != nil || row == nil {
			return model.TryResult{Error: "Input must be a JSON object of column → value."}, nil
		}
		res := doHTTP(ctx, http.MethodPost, b.gcp+"/bigquery/v2/"+t.url+"/insertAll", jsonHeader, mustJSON(map[string]any{"rows": []map[string]any{{"json": row}}}))
		if !res.OK {
			return res, nil
		}
		var out struct {
			InsertErrors []any `json:"insertErrors"`
		}
		if json.Unmarshal([]byte(res.Body), &out) == nil && len(out.InsertErrors) > 0 {
			res.OK = false
			res.Error = "BigQuery rejected the row; see insertErrors."
			return res, nil
		}
		res.Status = "Row inserted"
		return res, nil

	case KindGeneric + "/" + ActStart:
		return doHTTP(ctx, http.MethodPost, b.gcp+"/v1/"+t.url+":run", jsonHeader, "{}"), nil

	case KindKubernetes + "/" + ActList:
		res := doHTTP(ctx, http.MethodGet, t.httpBase, nil, "")
		if !res.OK {
			return res, nil
		}
		var cl struct {
			MasterAuth struct {
				ClusterCaCertificate string `json:"clusterCaCertificate"`
			} `json:"masterAuth"`
		}
		_ = json.Unmarshal([]byte(res.Body), &cl)
		return kubeList(ctx, t.url, cl.MasterAuth.ClusterCaCertificate), nil
	}
	return model.TryResult{}, badRequest("action %q is not available for %s", action, e.Kind)
}

// tableSchema is the schema of the BigQuery table of e.
func (b *builder) tableSchema(e *entry) []bqField {
	for _, r := range b.byType["google_bigquery_table"] {
		if r.Address == e.Address {
			return bigqueryFields(str(r, "schema"))
		}
	}
	return nil
}

// bigqueryRows turns a query or tabledata.list response into rows of
// column → value; schema names the columns of tabledata.list responses.
func bigqueryRows(res model.TryResult, schema ...bqField) model.TryResult {
	if !res.OK {
		return res
	}
	var out struct {
		Schema struct {
			Fields []bqField `json:"fields"`
		} `json:"schema"`
		Rows []struct {
			F []struct {
				V any `json:"v"`
			} `json:"f"`
		} `json:"rows"`
		TotalRows string `json:"totalRows"`
	}
	if json.Unmarshal([]byte(res.Body), &out) != nil {
		return res
	}
	fields := out.Schema.Fields
	if len(fields) == 0 {
		fields = schema
	}
	rows := []map[string]any{}
	for _, r := range out.Rows {
		row := map[string]any{}
		for i, c := range r.F {
			name := "f" + strconv.Itoa(i)
			if i < len(fields) {
				name = fields[i].Name
			}
			row[name] = c.V
		}
		rows = append(rows, row)
	}
	result := jsonResult(plural(len(rows), "row", "rows"), rows)
	result.LatencyMs = res.LatencyMs
	return result
}

// kubeList lists the nodes and namespaces of a Kubernetes API server whose
// certificate is signed by caB64 (base64 PEM). Any bearer token is accepted.
func kubeList(ctx context.Context, server, caB64 string) model.TryResult {
	u, err := url.Parse(server)
	if err != nil || !loopback(u.Hostname()) {
		return model.TryResult{Error: "The cluster's API server " + server + " is not on this computer."}
	}
	pem, err := base64.StdEncoding.DecodeString(caB64)
	pool := x509.NewCertPool()
	if err != nil || !pool.AppendCertsFromPEM(pem) {
		return model.TryResult{Error: "The sandbox's GKE API reports no CA certificate for the cluster yet; it may still be starting."}
	}
	client := &http.Client{Timeout: tryTimeout, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	get := func(path string, v any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer fake")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("%s returned HTTP %s", path, resp.Status)
		}
		return json.NewDecoder(resp.Body).Decode(v)
	}
	var nodes, namespaces struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase      string `json:"phase"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
				NodeInfo struct {
					KubeletVersion string `json:"kubeletVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := get("/api/v1/nodes", &nodes); err != nil {
		return model.TryResult{Error: describeError(err)}
	}
	if err := get("/api/v1/namespaces", &namespaces); err != nil {
		return model.TryResult{Error: describeError(err)}
	}
	type node struct {
		Name    string `json:"name"`
		Ready   bool   `json:"ready"`
		Version string `json:"version"`
	}
	out := struct {
		Nodes      []node   `json:"nodes"`
		Namespaces []string `json:"namespaces"`
	}{Nodes: []node{}, Namespaces: []string{}}
	for _, n := range nodes.Items {
		ready := false
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" {
				ready = c.Status == "True"
			}
		}
		out.Nodes = append(out.Nodes, node{n.Metadata.Name, ready, n.Status.NodeInfo.KubeletVersion})
	}
	for _, n := range namespaces.Items {
		out.Namespaces = append(out.Namespaces, n.Metadata.Name)
	}
	return jsonResult(plural(len(out.Nodes), "node", "nodes"), out)
}

// ---------------------------------------------------------------------------
// Postman

// gcpPostman adds a "Google Cloud services (sandbox)" folder of REST calls
// and a folder per Cloud Run service; URLs use the gcpBaseUrl variable.
func (b *builder) gcpPostman(col *PostmanCollection) {
	if b.gcp == "" {
		return
	}
	var services []PostmanItem
	add := func(name, method, full, body string) {
		req := &PostmanRequest{Method: method, Header: []PostmanHeader{}, URL: b.gcpPostmanURL(full)}
		if body != "" {
			req.Header = append(req.Header, PostmanHeader{"Content-Type", "application/json"})
			req.Body = rawJSON(prettyJSON([]byte(body)))
		}
		services = append(services, PostmanItem{Name: name, Request: req})
	}
	for _, e := range b.entries {
		if e.t.cloud != model.CloudGCP {
			continue
		}
		t := e.t
		switch {
		case e.Kind == KindWebService:
			folder := PostmanItem{Name: e.Label, Description: e.Summary}
			for _, ep := range e.Endpoints {
				for _, m := range []string{http.MethodGet, http.MethodPost} {
					req := &PostmanRequest{Method: m, Header: []PostmanHeader{}, URL: b.gcpPostmanURL(ep.URL), Description: ep.Description}
					if m == http.MethodPost {
						req.Header = append(req.Header, PostmanHeader{"Content-Type", "application/json"})
						req.Body = rawJSON(sampleJSONBody)
					}
					folder.Item = append(folder.Item, PostmanItem{Name: m + " " + ep.Path, Request: req})
				}
			}
			col.Item = append(col.Item, folder)
		case e.Kind == KindBucket:
			add("Storage list objects · "+e.Label, http.MethodGet, b.gcp+"/storage/v1/b/"+url.PathEscape(t.name)+"/o?maxResults=50", "")
			upload := &PostmanRequest{
				Method: http.MethodPost, Header: []PostmanHeader{{"Content-Type", "text/plain"}},
				URL:  b.gcpPostmanURL(b.gcp + "/upload/storage/v1/b/" + url.PathEscape(t.name) + "/o?uploadType=media&name=hello.txt"),
				Body: &PostmanBody{Mode: "raw", Raw: "Hello from Nimbo"},
			}
			services = append(services, PostmanItem{Name: "Storage upload object · " + e.Label, Request: upload})
			add("Storage download object · "+e.Label, http.MethodGet, b.gcp+"/storage/v1/b/"+url.PathEscape(t.name)+"/o/hello.txt?alt=media", "")
		case e.Kind == KindTopic:
			add("Pub/Sub publish · "+e.Label, http.MethodPost, b.gcp+"/v1/"+t.url+":publish",
				mustJSON(map[string]any{"messages": []map[string]any{{"data": base64.StdEncoding.EncodeToString([]byte(helloMessage))}}}))
		case e.Kind == KindQueue:
			add("Pub/Sub pull · "+e.Label, http.MethodPost, b.gcp+"/v1/"+t.url+":pull", `{"maxMessages":10}`)
			add("Pub/Sub acknowledge · "+e.Label, http.MethodPost, b.gcp+"/v1/"+t.url+":acknowledge", `{"ackIds":["<ackId from pull>"]}`)
		case e.Kind == KindSecret:
			add("Secret Manager access latest · "+e.Label, http.MethodGet, b.gcp+"/v1/"+t.url+"/versions/latest:access", "")
		case e.Kind == KindKey && slices.ContainsFunc(e.Actions, func(a model.TryAction) bool { return a.ID == ActEncrypt }):
			add("KMS encrypt · "+e.Label, http.MethodPost, b.gcp+"/v1/"+t.url+":encrypt",
				mustJSON(map[string]any{"plaintext": base64.StdEncoding.EncodeToString([]byte("Hello from Nimbo"))}))
			add("KMS decrypt · "+e.Label, http.MethodPost, b.gcp+"/v1/"+t.url+":decrypt", `{"ciphertext":"<ciphertext from encrypt>"}`)
		case e.Kind == KindDatabase && e.Type == "google_bigquery_dataset":
			add("BigQuery query · "+e.Label, http.MethodPost, b.gcp+"/bigquery/v2/projects/"+t.url+"/queries",
				mustJSON(map[string]any{"query": e.Actions[0].Default, "useLegacySql": false}))
		case e.Kind == KindDatabase && e.Type == "google_sql_database_instance":
			add("Cloud SQL instance · "+e.Label, http.MethodGet, b.gcp+"/sql/v1beta4/projects/"+b.gcpProject()+"/instances/"+url.PathEscape(t.name), "")
		case e.Kind == KindTable:
			add("BigQuery rows · "+e.Label, http.MethodGet, b.gcp+"/bigquery/v2/"+t.url+"/data?maxResults=25", "")
			add("BigQuery insert · "+e.Label, http.MethodPost, b.gcp+"/bigquery/v2/"+t.url+"/insertAll", `{"rows":[{"json":`+t.item+`}]}`)
		case e.Kind == KindFunction:
			add("Cloud Functions get · "+e.Label, http.MethodGet, b.gcp+"/v2/"+t.url, "")
		case e.Kind == KindGeneric && e.Type == "google_cloud_scheduler_job":
			add("Cloud Scheduler run · "+e.Label, http.MethodPost, b.gcp+"/v1/"+t.url+":run", "{}")
		}
	}
	if len(services) > 0 {
		col.Item = append(col.Item, PostmanItem{
			Name:        "Google Cloud services (sandbox)",
			Description: "Google Cloud REST calls against the sandbox; no auth needed. GKE clusters are reached with kubectl (see the Endpoints tab), not Postman.",
			Item:        services,
		})
	}
	col.Variable = append(col.Variable, PostmanVariable{Key: "gcpBaseUrl", Value: b.gcp, Type: "string"})
	col.Info.Description += " gcpBaseUrl is the sandbox's Google Cloud address."
}

// gcpPostmanURL expresses a sandbox Google Cloud URL with the gcpBaseUrl
// variable.
func (b *builder) gcpPostmanURL(full string) PostmanURL {
	rest := strings.TrimPrefix(full, b.gcp)
	path, query, _ := strings.Cut(rest, "?")
	u := PostmanURL{Raw: "{{gcpBaseUrl}}" + rest, Host: []string{"{{gcpBaseUrl}}"}}
	for _, seg := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		u.Path = append(u.Path, seg)
	}
	if query != "" {
		for _, kv := range strings.Split(query, "&") {
			k, v, _ := strings.Cut(kv, "=")
			u.Query = append(u.Query, PostmanQuery{k, v})
		}
	}
	return u
}

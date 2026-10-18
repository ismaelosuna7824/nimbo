package endpoints

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// floci-oci serves every OCI service on one port, with OCI's REST paths.
// Signatures are parsed (the keyId's tenancy selects the tenancy's
// resources) but never verified; unsigned requests use the default tenancy,
// which is the one the sandbox's Terraform runs use.
const (
	ociRegion       = "us-ashburn-1"
	ociNamespace    = "floci-local"
	ociProfile      = "NIMBO"
	ociFunctionsAPI = "/20181201" // current SDKs and the CLI default to /20260325, which floci-oci doesn't serve
	ociQueueAPI     = "/20210201"
	ociStreamAPI    = "/20180418"
	ociKMSAPI       = "/20180608"
	ociSecretsAPI   = "/20190301"
	ociOKEAPI       = "/20180222"
)

func (b *builder) ociResources() {
	b.ociBuckets()
	b.ociQueues()
	b.ociStreams()
	b.ociKeys()
	b.ociVaults()
	b.ociSecrets()
	b.ociFunctions()
	b.ociClusters()
}

// ociTenancy is the tenancy the workflow's resources live in: the root
// compartment they name, else the sandbox's.
func (b *builder) ociTenancy() string {
	for _, r := range b.ordered {
		if c := str(r, "compartment_id"); strings.HasPrefix(r.Type, "oci_") && strings.HasPrefix(c, "ocid1.tenancy.") {
			return c
		}
	}
	return sandbox.SandboxIdentities().OCITenancy
}

// ociUnsigned reports whether unsigned requests (curl, Postman) reach the
// workflow's resources: floci-oci maps them to its default tenancy.
func (b *builder) ociUnsigned() bool {
	return b.ociTenancy() == sandbox.SandboxIdentities().OCITenancy
}

func (b *builder) ociSandboxAccess(base string) model.SandboxAccess {
	ids := sandbox.SandboxIdentities()
	tenancy := b.ociTenancy()
	notes := []string{
		"Run the commands above once: they create a throwaway API key and a " + ociProfile + " profile in ~/.oci/config, then point the OCI CLI at the sandbox (OCI_CLI_ENDPOINT). Any key works; the sandbox reads the signature but never checks it.",
		"SDKs take the same values: pass this address as the client's endpoint (Python service_endpoint=, JavaScript client.endpoint =). The examples on each resource generate a key in code, so they run without the profile.",
		"Object Storage namespace: " + ociNamespace + ". Kubernetes clusters listen on their own port (shown on the cluster).",
		"This address stays the same when you stop and start the sandbox.",
	}
	if b.ociUnsigned() {
		notes = append(notes[:2:2], append([]string{"Unsigned requests (curl, Postman) also work: the sandbox serves them from the default tenancy, where your resources live."}, notes[2:]...)...)
	}
	return model.SandboxAccess{
		Cloud: model.CloudOCI, URL: base, Region: ociRegion,
		Credentials: []model.AccessField{
			{Label: "Tenancy OCID", Value: tenancy, Hint: "Also the root compartment ID."},
			{Label: "User OCID", Value: ids.OCIUser},
			{Label: "Key fingerprint", Value: ids.OCIFingerprint, Hint: "Any fingerprint works."},
			{Label: "API key", Value: "any RSA key", Hint: "Generate one with: openssl genrsa -out key.pem 2048. Signatures are not verified."},
			{Label: "Object Storage namespace", Value: ociNamespace},
		},
		EnvSnippet: fmt.Sprintf(`mkdir -p ~/.oci
[ -f ~/.oci/nimbo_key.pem ] || openssl genrsa -out ~/.oci/nimbo_key.pem 2048
grep -q '^\[%s\]' ~/.oci/config 2>/dev/null || cat >> ~/.oci/config <<'EOF'
[%s]
user=%s
fingerprint=%s
tenancy=%s
region=%s
key_file=~/.oci/nimbo_key.pem
EOF
chmod 600 ~/.oci/config ~/.oci/nimbo_key.pem
export OCI_CLI_PROFILE=%s OCI_CLI_ENDPOINT=%s SUPPRESS_LABEL_WARNING=True`,
			ociProfile, ociProfile, ids.OCIUser, ids.OCIFingerprint, tenancy, ociRegion, ociProfile, base),
		Notes: notes,
	}
}

// ---------------------------------------------------------------------------
// Snippet helpers

// ociCLI is an OCI CLI snippet using the sandbox card's profile; lines
// are comments or shell commands built with ociCmd.
func (b *builder) ociCLI(lines ...string) string {
	return "# Uses the " + ociProfile + " profile from the OCI sandbox settings (any throwaway key works).\n" + strings.Join(lines, "\n")
}

// ociCmd is "oci <args>" against the sandbox.
func (b *builder) ociCmd(args string) string {
	return "oci " + args + " \\\n  --profile " + ociProfile + " --endpoint " + b.oci
}

// ociPy is a Python snippet whose config carries a freshly generated key,
// so it runs without ~/.oci/config.
func (b *builder) ociPy(imports []string, body string) string {
	ids := sandbox.SandboxIdentities()
	pip := "# pip install oci"
	if slices.Contains(imports, "import requests") {
		pip += " requests"
	}
	head := append([]string{pip, "import oci"}, imports...)
	return strings.Join(head, "\n") + fmt.Sprintf(`
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa

# The sandbox reads request signatures but never checks them: any new key works.
key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
config = {
    "tenancy": %s,
    "user": %s,
    "fingerprint": %s,
    "region": %s,
    "key_content": key.private_bytes(
        serialization.Encoding.PEM, serialization.PrivateFormat.TraditionalOpenSSL, serialization.NoEncryption()
    ).decode(),
}
endpoint = %s
`, jsStr(b.ociTenancy()), jsStr(ids.OCIUser), jsStr(ids.OCIFingerprint), jsStr(ociRegion), jsStr(b.oci)) + strings.TrimLeft(body, "\n")
}

// ociJS is a Node.js snippet (ES module) with an auth provider over a
// freshly generated key; pkgs are the oci-* packages it imports.
func (b *builder) ociJS(pkgs []string, body string) string {
	ids := sandbox.SandboxIdentities()
	all := append([]string{"oci-common"}, pkgs...)
	var imports []string
	for _, p := range all {
		imports = append(imports, fmt.Sprintf("import * as %s from %q;", strings.TrimPrefix(p, "oci-"), p))
	}
	return fmt.Sprintf(`// Node.js 18+: npm install %s, save as try.mjs, run: node try.mjs
import { generateKeyPairSync } from "node:crypto";
%s

// The sandbox reads request signatures but never checks them: any new key works.
const { privateKey } = generateKeyPairSync("rsa", {
  modulusLength: 2048,
  privateKeyEncoding: { type: "pkcs1", format: "pem" },
  publicKeyEncoding: { type: "spki", format: "pem" },
});
const provider = new common.SimpleAuthenticationDetailsProvider(
  %s,
  %s,
  %s,
  privateKey,
  null,
  common.Region.fromRegionId(%s),
);
const endpoint = %s;
`, strings.Join(all, " "), strings.Join(imports, "\n"),
		jsStr(b.ociTenancy()), jsStr(ids.OCIUser), jsStr(ids.OCIFingerprint), jsStr(ociRegion), jsStr(b.oci)) + strings.TrimLeft(body, "\n")
}

// curl adds a curl snippet when unsigned requests reach the resources.
func (b *builder) ociCurl(e *entry, cmds ...string) {
	if b.ociUnsigned() {
		e.snippet("curl", "bash", "# The sandbox accepts unsigned requests for its default tenancy.\n"+strings.Join(cmds, "\n\n"))
	}
}

// ---------------------------------------------------------------------------
// Object Storage

const ociPutDefault = `{"key":"hello.txt","body":"Hello from Nimbo"}`

func (b *builder) ociBuckets() {
	for _, bk := range b.byType["oci_objectstorage_bucket"] {
		name := str(bk, "name")
		if name == "" {
			continue
		}
		ns := cmp.Or(str(bk, "namespace"), ociNamespace)
		objects := b.oci + "/n/" + url.PathEscape(ns) + "/b/" + url.PathEscape(name) + "/o"
		object := objects + "/hello.txt"
		e := b.add(bk, KindBucket, "An Object Storage bucket for files (objects). Upload, list and download them with the URLs and examples below.",
			target{cloud: model.CloudOCI, name: name, url: objects})
		e.field("Bucket name", name, "")
		e.field("Namespace", ns, "")
		e.field("List objects URL", objects, "GET it to list objects.")
		e.field("Object URL", object, "PUT uploads the request body as hello.txt; GET downloads it. Change the last segment for other names.")
		var parCurl []string
		for _, p := range b.byType["oci_objectstorage_preauthrequest"] {
			path := cmp.Or(str(p, "full_path"), str(p, "access_uri"))
			if str(p, "bucket") != name || cmp.Or(str(p, "namespace"), ociNamespace) != ns || !strings.HasPrefix(path, "/p/") {
				continue
			}
			u := b.oci + path
			e.field("Pre-authenticated URL ("+cmp.Or(str(p, "name"), p.Name)+")", u, parHint(str(p, "access_type"), str(p, "object_name"), str(p, "time_expires")))
			if strings.HasSuffix(path, "/o/") && strings.Contains(str(p, "access_type"), "Write") {
				parCurl = append(parCurl, "# Anyone with the pre-authenticated URL can use it, no credentials:\ncurl -X PUT --data-binary 'Hello from Nimbo' "+shq(u+"shared.txt")+"\ncurl "+shq(u+"shared.txt"))
			}
		}
		b.ociCurl(e, append([]string{
			"curl -X PUT -H 'Content-Type: text/plain' --data-binary 'Hello from Nimbo' " + shq(object),
			"curl " + shq(objects),
			"curl " + shq(object),
		}, parCurl...)...)
		e.snippet("OCI CLI", "bash", b.ociCLI(
			"echo 'Hello from Nimbo' > hello.txt",
			b.ociCmd("os object put -ns "+shq(ns)+" -bn "+shq(name)+" --name hello.txt --file hello.txt --force"),
			b.ociCmd("os object list -ns "+shq(ns)+" -bn "+shq(name)),
			b.ociCmd("os object get -ns "+shq(ns)+" -bn "+shq(name)+" --name hello.txt --file -"),
		))
		e.snippet("JavaScript", "javascript", b.ociJS([]string{"oci-objectstorage"}, fmt.Sprintf(`
const client = new objectstorage.ObjectStorageClient({ authenticationDetailsProvider: provider });
client.endpoint = endpoint;
const namespaceName = %s;
const bucketName = %s;

const body = "Hello from Nimbo";
await client.putObject({ namespaceName, bucketName, objectName: "hello.txt", putObjectBody: body, contentLength: body.length });

const list = await client.listObjects({ namespaceName, bucketName });
console.log(list.listObjects.objects.map((o) => o.name));

const got = await client.getObject({ namespaceName, bucketName, objectName: "hello.txt" });
console.log(await new Response(got.value).text());`, jsStr(ns), jsStr(name))))
		e.snippet("Python", "python", b.ociPy(nil, fmt.Sprintf(`
client = oci.object_storage.ObjectStorageClient(config, service_endpoint=endpoint)
namespace, bucket = %s, %s

client.put_object(namespace, bucket, "hello.txt", b"Hello from Nimbo")
for o in client.list_objects(namespace, bucket).data.objects:
    print(o.name)
print(client.get_object(namespace, bucket, "hello.txt").data.content.decode())`, jsStr(ns), jsStr(name))))
		e.action(model.TryAction{ID: ActList, Label: "List files", Description: "Show the first 50 objects.", Input: "none"})
		e.action(model.TryAction{ID: ActPut, Label: "Upload a file", Description: "Store body as an object named key.", Input: "json", Default: ociPutDefault, Writes: true})
		e.action(model.TryAction{ID: ActGet, Label: "Download a file", Description: "Show the content of the object with this name.", Input: "text", Default: "hello.txt"})
	}
}

func parHint(access, object, expires string) string {
	what := "Anyone with this link can use it, no credentials needed"
	switch access {
	case "ObjectRead":
		what = "Anyone with this link can download " + object
	case "ObjectWrite":
		what = "Anyone with this link can upload " + object
	case "ObjectReadWrite":
		what = "Anyone with this link can upload and download " + object
	case "AnyObjectRead":
		what = "Anyone with this link can download any object: append its name"
	case "AnyObjectWrite":
		what = "Anyone with this link can upload objects: append a name and PUT"
	case "AnyObjectReadWrite":
		what = "Anyone with this link can upload (PUT) and download (GET) objects: append the object name"
	}
	if expires != "" {
		what += " (expires " + expires + ")"
	}
	return what + "."
}

// ---------------------------------------------------------------------------
// Queue

func (b *builder) ociQueues() {
	for _, q := range b.byType["oci_queue_queue"] {
		id := str(q, "id")
		if id == "" {
			continue
		}
		msgs := b.oci + ociQueueAPI + "/queues/" + id + "/messages"
		e := b.add(q, KindQueue, "A queue: one part of your app puts messages on it, another gets them and deletes them once handled.",
			target{cloud: model.CloudOCI, arn: id, url: msgs})
		e.field("Queue OCID", id, "")
		e.field("Messages endpoint", b.oci, "The service endpoint for SDK queue clients (QueueClient).")
		e.field("Messages URL", msgs, `POST {"messages":[{"content":"…"}]} to send; GET to receive.`)
		b.ociCurl(e,
			"curl -X POST -H 'Content-Type: application/json' "+shq(msgs)+" \\\n  -d "+shq(`{"messages":[{"content":"Hello from Nimbo"}]}`),
			"# Peek at up to 10 messages (visibilityInSeconds=0 leaves them visible)\ncurl "+shq(msgs+"?limit=10&visibilityInSeconds=0&timeoutInSeconds=0"),
		)
		e.snippet("OCI CLI", "bash", b.ociCLI(
			b.ociCmd("queue messages put-messages --queue-id "+shq(id)+" --messages "+shq(`[{"content":"Hello from Nimbo"}]`)),
			b.ociCmd("queue messages get-messages --queue-id "+shq(id)+" --limit 10 --visibility-in-seconds 0"),
		))
		e.snippet("JavaScript", "javascript", b.ociJS([]string{"oci-queue"}, fmt.Sprintf(`
const client = new queue.QueueClient({ authenticationDetailsProvider: provider });
client.endpoint = endpoint;
const queueId = %s;

await client.putMessages({ queueId, putMessagesDetails: { messages: [{ content: "Hello from Nimbo" }] } });

const got = await client.getMessages({ queueId, limit: 10, visibilityInSeconds: 0 });
console.log(got.getMessages.messages.map((m) => m.content));`, jsStr(id))))
		e.snippet("Python", "python", b.ociPy(nil, fmt.Sprintf(`
client = oci.queue.QueueClient(config, service_endpoint=endpoint)
queue_id = %s

client.put_messages(queue_id, oci.queue.models.PutMessagesDetails(
    messages=[oci.queue.models.PutMessagesDetailsEntry(content="Hello from Nimbo")]))

for m in client.get_messages(queue_id, limit=10, visibility_in_seconds=0).data.messages:
    print(m.content)`, jsStr(id))))
		e.action(model.TryAction{ID: ActSend, Label: "Send a message", Description: "Put this text on the queue.", Input: "text", Default: helloMessage, Writes: true})
		e.action(model.TryAction{ID: ActReceive, Label: "Peek at messages", Description: "Read up to 10 waiting messages without removing them (they stay visible).", Input: "none"})
	}
}

// ---------------------------------------------------------------------------
// Streaming

func (b *builder) ociStreams() {
	for _, s := range b.byType["oci_streaming_stream"] {
		id := str(s, "id")
		if id == "" {
			continue
		}
		base := b.oci + ociStreamAPI + "/streams/" + id
		e := b.add(s, KindStream, "A stream: an ordered log of messages. Producers append messages; readers move through them with a cursor, and messages stay for the retention period.",
			target{cloud: model.CloudOCI, arn: id, url: base, parts: max(1, atoi(str(s, "partitions")))})
		e.field("Stream OCID", id, "")
		e.field("Messages endpoint", b.oci, "The service endpoint for SDK stream clients (StreamClient).")
		e.field("Partitions", str(s, "partitions"), "")
		e.field("Messages URL", base+"/messages", `POST {"messages":[{"key":"<base64>","value":"<base64>"}]} to append; GET ?cursor=… to read.`)
		e.field("Cursors URL", base+"/cursors", `POST {"partition":"0","type":"TRIM_HORIZON"} for a cursor that starts at the oldest message.`)
		b.ociCurl(e,
			"# Values are base64: \"SGVsbG8gZnJvbSBOaW1ibw==\" is \"Hello from Nimbo\"\ncurl -X POST -H 'Content-Type: application/json' "+shq(base+"/messages")+" \\\n  -d "+shq(`{"messages":[{"key":"bmltYm8=","value":"SGVsbG8gZnJvbSBOaW1ibw=="}]}`),
			"# Read from the oldest message: get a cursor, then the messages\nCURSOR=$(curl -s -X POST -H 'Content-Type: application/json' "+shq(base+"/cursors")+" \\\n  -d '{\"partition\":\"0\",\"type\":\"TRIM_HORIZON\"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)[\"value\"])')\ncurl "+shq(base+"/messages?limit=10&cursor=")+"\"$CURSOR\"",
		)
		e.snippet("OCI CLI", "bash", b.ociCLI(
			"# Keys and values are base64",
			b.ociCmd("streaming stream message put --stream-id "+shq(id)+" \\\n  --messages \"[{\\\"key\\\":\\\"$(printf nimbo | base64)\\\",\\\"value\\\":\\\"$(printf 'Hello from Nimbo' | base64)\\\"}]\""),
			"CURSOR=$("+b.ociCmd("streaming stream cursor create-cursor --stream-id "+shq(id)+" --partition 0 --type TRIM_HORIZON --query data.value --raw-output")+")",
			b.ociCmd("streaming stream message get --stream-id "+shq(id)+" --cursor \"$CURSOR\" --limit 10"),
		))
		e.snippet("JavaScript", "javascript", b.ociJS([]string{"oci-streaming"}, fmt.Sprintf(`
const client = new streaming.StreamClient({ authenticationDetailsProvider: provider });
client.endpoint = endpoint;
const streamId = %s;
const b64 = (s) => Buffer.from(s).toString("base64");

await client.putMessages({ streamId, putMessagesDetails: { messages: [{ key: b64("nimbo"), value: b64("Hello from Nimbo") }] } });

const { cursor } = await client.createCursor({ streamId, createCursorDetails: { partition: "0", type: "TRIM_HORIZON" } });
const got = await client.getMessages({ streamId, cursor: cursor.value, limit: 10 });
console.log(got.items.map((m) => Buffer.from(m.value, "base64").toString()));`, jsStr(id))))
		e.snippet("Python", "python", b.ociPy([]string{"from base64 import b64decode, b64encode"}, fmt.Sprintf(`
client = oci.streaming.StreamClient(config, service_endpoint=endpoint)
stream_id = %s
models = oci.streaming.models

client.put_messages(stream_id, models.PutMessagesDetails(messages=[
    models.PutMessagesDetailsEntry(key=b64encode(b"nimbo").decode(), value=b64encode(b"Hello from Nimbo").decode())]))

cursor = client.create_cursor(stream_id, models.CreateCursorDetails(partition="0", type="TRIM_HORIZON")).data.value
for m in client.get_messages(stream_id, cursor, limit=10).data:
    print(b64decode(m.value).decode())`, jsStr(id))))
		e.action(model.TryAction{ID: ActPut, Label: "Append a message", Description: "Append this text to the stream (the SDKs base64-encode it for you; here Nimbo does).", Input: "text", Default: helloMessage, Writes: true})
		e.action(model.TryAction{ID: ActReceive, Label: "Read messages", Description: "Show the latest messages of every partition, decoded.", Input: "none"})
	}
}

// ---------------------------------------------------------------------------
// Vault: keys, vaults, secrets

func (b *builder) ociKeys() {
	for _, k := range b.byType["oci_kms_key"] {
		id := str(k, "id")
		if id == "" {
			continue
		}
		var algorithm string
		if shape := list(k, "key_shape"); len(shape) > 0 {
			algorithm = mstr(shape[0], "algorithm")
		}
		crypto := b.oci // floci-oci serves every vault's crypto endpoint on the emulator address
		e := b.add(k, KindKey, "An encryption key kept in a vault. Your app sends data to the vault's crypto endpoint to encrypt or decrypt it; the key itself never leaves the vault.",
			target{cloud: model.CloudOCI, arn: id, url: crypto})
		e.field("Key OCID", id, "")
		e.field("Algorithm", algorithm, "")
		e.field("Crypto endpoint", crypto, "The service endpoint for KmsCryptoClient (encrypt/decrypt).")
		e.field("Management endpoint", b.oci, "The service endpoint for KmsManagementClient (key versions, rotation).")
		if algorithm != "" && algorithm != "AES" {
			e.Warning = "The sandbox encrypts and decrypts only with AES keys; " + algorithm + " keys can sign and verify."
			continue
		}
		b.ociCurl(e,
			"# Plaintext and ciphertext are base64: \"SGVsbG8gZnJvbSBOaW1ibw==\" is \"Hello from Nimbo\"\nCIPHERTEXT=$(curl -s -X POST -H 'Content-Type: application/json' "+shq(crypto+ociKMSAPI+"/encrypt")+" \\\n  -d "+shq(`{"keyId":"`+id+`","plaintext":"SGVsbG8gZnJvbSBOaW1ibw=="}`)+" \\\n  | python3 -c 'import json,sys; print(json.load(sys.stdin)[\"ciphertext\"])')\necho \"$CIPHERTEXT\"",
			"curl -s -X POST -H 'Content-Type: application/json' "+shq(crypto+ociKMSAPI+"/decrypt")+" \\\n  -d \"{\\\"keyId\\\":\\\""+id+"\\\",\\\"ciphertext\\\":\\\"$CIPHERTEXT\\\"}\" \\\n  | python3 -c 'import base64,json,sys; print(base64.b64decode(json.load(sys.stdin)[\"plaintext\"]).decode())'",
		)
		e.snippet("OCI CLI", "bash", b.ociCLI(
			"# Plaintext and ciphertext are base64",
			"CIPHERTEXT=$("+b.ociCmd("kms crypto encrypt --key-id "+shq(id)+" --plaintext \"$(printf 'Hello from Nimbo' | base64)\" \\\n  --query data.ciphertext --raw-output")+")",
			"echo \"$CIPHERTEXT\"",
			b.ociCmd("kms crypto decrypt --key-id "+shq(id)+" --ciphertext \"$CIPHERTEXT\" \\\n  --query data.plaintext --raw-output")+" | base64 -d",
		))
		e.snippet("JavaScript", "javascript", b.ociJS([]string{"oci-keymanagement"}, fmt.Sprintf(`
const client = new keymanagement.KmsCryptoClient({ authenticationDetailsProvider: provider });
client.endpoint = endpoint;
const keyId = %s;

const enc = await client.encrypt({ encryptDataDetails: { keyId, plaintext: Buffer.from("Hello from Nimbo").toString("base64") } });
console.log("ciphertext:", enc.encryptedData.ciphertext);

const dec = await client.decrypt({ decryptDataDetails: { keyId, ciphertext: enc.encryptedData.ciphertext } });
console.log("plaintext:", Buffer.from(dec.decryptedData.plaintext, "base64").toString());`, jsStr(id))))
		e.snippet("Python", "python", b.ociPy([]string{"from base64 import b64decode, b64encode"}, fmt.Sprintf(`
client = oci.key_management.KmsCryptoClient(config, service_endpoint=endpoint)
key_id = %s
models = oci.key_management.models

enc = client.encrypt(models.EncryptDataDetails(key_id=key_id, plaintext=b64encode(b"Hello from Nimbo").decode())).data
print("ciphertext:", enc.ciphertext)

dec = client.decrypt(models.DecryptDataDetails(key_id=key_id, ciphertext=enc.ciphertext)).data
print("plaintext:", b64decode(dec.plaintext).decode())`, jsStr(id))))
		e.action(model.TryAction{ID: ActEncrypt, Label: "Encrypt", Description: "Encrypt this text with the key; copy the ciphertext to try Decrypt.", Input: "text", Default: "Hello from Nimbo"})
		e.action(model.TryAction{ID: ActDecrypt, Label: "Decrypt", Description: "Paste a ciphertext from Encrypt to get the text back.", Input: "text"})
	}
}

func (b *builder) ociVaults() {
	for _, v := range b.byType["oci_kms_vault"] {
		id := str(v, "id")
		if id == "" {
			continue
		}
		e := b.add(v, KindGeneric, "A vault: it holds your encryption keys and secrets. Use the keys and secrets themselves; the endpoints below are what their SDK clients connect to.",
			target{cloud: model.CloudOCI, arn: id})
		e.Supporting = true
		e.field("Vault OCID", id, "")
		e.field("Crypto endpoint", b.oci, "For KmsCryptoClient: encrypt and decrypt with the vault's keys.")
		e.field("Management endpoint", b.oci, "For KmsManagementClient: create, rotate and list keys.")
	}
}

func (b *builder) ociSecrets() {
	for _, s := range b.byType["oci_vault_secret"] {
		id := str(s, "id")
		if id == "" {
			continue
		}
		bundle := b.oci + ociSecretsAPI + "/secretbundles/" + id
		e := b.add(s, KindSecret, "A secret (password, API key, …) stored in a vault. Your app reads its current value, the secret bundle, at run time.",
			target{cloud: model.CloudOCI, arn: id, url: bundle})
		e.field("Secret OCID", id, "")
		e.field("Secret name", str(s, "secret_name"), "")
		e.field("Vault OCID", str(s, "vault_id"), "")
		e.field("Secret bundle URL", bundle, "GET it for the current value (base64 in secretBundleContent.content).")
		b.ociCurl(e,
			"curl -s "+shq(bundle)+" | python3 -c 'import base64,json,sys; print(base64.b64decode(json.load(sys.stdin)[\"secretBundleContent\"][\"content\"]).decode())'",
		)
		e.snippet("OCI CLI", "bash", b.ociCLI(
			"# The value is base64; base64 -d prints it",
			b.ociCmd("secrets secret-bundle get --secret-id "+shq(id)+" \\\n  --query 'data.\"secret-bundle-content\".content' --raw-output")+" | base64 -d",
		))
		e.snippet("JavaScript", "javascript", b.ociJS([]string{"oci-secrets"}, fmt.Sprintf(`
const client = new secrets.SecretsClient({ authenticationDetailsProvider: provider });
client.endpoint = endpoint;

const { secretBundle } = await client.getSecretBundle({ secretId: %s });
console.log(Buffer.from(secretBundle.secretBundleContent.content, "base64").toString());`, jsStr(id))))
		e.snippet("Python", "python", b.ociPy([]string{"from base64 import b64decode"}, fmt.Sprintf(`
client = oci.secrets.SecretsClient(config, service_endpoint=endpoint)

bundle = client.get_secret_bundle(%s).data
print(b64decode(bundle.secret_bundle_content.content).decode())`, jsStr(id))))
		e.action(model.TryAction{ID: ActGet, Label: "Read the value", Description: "Show the current value of the secret.", Input: "none"})
	}
}

// ---------------------------------------------------------------------------
// Functions

func (b *builder) ociFunctions() {
	for _, f := range b.byType["oci_functions_function"] {
		id := str(f, "id")
		if id == "" {
			continue
		}
		invoke := b.oci + ociFunctionsAPI + "/functions/" + id + "/actions/invoke"
		e := b.add(f, KindFunction, "A function: your container image runs when you invoke it. Send a request body and get the function's answer back.",
			target{cloud: model.CloudOCI, arn: id, url: invoke})
		e.field("Function OCID", id, "")
		e.field("Invoke endpoint", b.oci, "The service endpoint for FunctionsInvokeClient.")
		e.field("Invoke URL", invoke, "POST the request body to run the function.")
		e.field("Image", str(f, "image"), "Must be built with a current Fn FDK; the first call starts the function runtime and can take a while.")
		body := `{"name":"Nimbo"}`
		b.ociCurl(e, "curl -X POST -H 'Content-Type: application/json' "+shq(invoke)+" -d "+shq(body))
		e.snippet("OCI CLI", "bash", "# Uses the "+ociProfile+" profile from the OCI sandbox settings. raw-request signs the call;\n# `oci fn function invoke` targets an API version the sandbox doesn't serve.\n"+
			"oci raw-request --profile "+ociProfile+" --http-method POST \\\n  --target-uri "+shq(invoke)+" \\\n  --request-body "+shq(body))
		e.snippet("JavaScript", "javascript", b.ociJS(nil, fmt.Sprintf(`
// The SDK's FunctionsInvokeClient targets an API version the sandbox doesn't
// serve, so sign the invoke request with the SDK's signer and send it.
const url = %s;
const body = JSON.stringify({ name: "Nimbo" });
const request = { method: "POST", uri: url, headers: new Headers({ "content-type": "application/json" }), body };
await new common.DefaultRequestSigner(provider).signHttpRequest(request);
const res = await fetch(url, { method: "POST", headers: request.headers, body });
console.log(res.status, await res.text());`, jsStr(invoke))))
		e.snippet("Python", "python", b.ociPy([]string{"import requests"}, fmt.Sprintf(`
# The SDK's FunctionsInvokeClient targets an API version the sandbox doesn't
# serve, so sign the invoke request with the SDK's signer and send it.
signer = oci.signer.Signer(config["tenancy"], config["user"], config["fingerprint"], None,
                           private_key_content=config["key_content"])
res = requests.post(%s, json={"name": "Nimbo"}, auth=signer)
print(res.status_code, res.text)`, jsStr(invoke))))
		e.action(model.TryAction{ID: ActInvoke, Label: "Invoke", Description: "Run the function with this request body. The first call starts the runtime and may time out; try again.", Input: "json", Default: body})
	}
}

// ---------------------------------------------------------------------------
// Container Engine (OKE)

func (b *builder) ociClusters() {
	for _, c := range b.byType["oci_containerengine_cluster"] {
		id := str(c, "id")
		if id == "" {
			continue
		}
		var api string
		if eps := list(c, "endpoints"); len(eps) > 0 {
			api = cmp.Or(mstr(eps[0], "kubernetes"), mstr(eps[0], "public_endpoint"))
		}
		kubeconfig := b.oci + ociOKEAPI + "/clusters/" + id + "/kubeconfig/content"
		e := b.add(c, KindKubernetes, "A Kubernetes cluster (a local k3s server). Download its kubeconfig and use kubectl or any Kubernetes client.",
			target{cloud: model.CloudOCI, arn: id, url: kubeconfig})
		e.field("Cluster OCID", id, "")
		e.field("Kubernetes API", api, "The cluster's own HTTPS address on this machine (self-signed; the kubeconfig carries its CA).")
		e.field("Kubernetes version", str(c, "kubernetes_version"), "")
		e.field("Kubeconfig URL", kubeconfig, "POST {} to download the kubeconfig.")
		e.snippet("OCI CLI + kubectl", "bash", b.ociCLI(
			b.ociCmd("ce cluster create-kubeconfig --cluster-id "+shq(id)+" --file ./kubeconfig --overwrite"),
			"# kubectl runs `oci ce cluster generate-token` (signed locally) with the selected profile",
			"OCI_CLI_PROFILE="+ociProfile+" KUBECONFIG=./kubeconfig kubectl get nodes",
		))
		b.ociCurl(e, "curl -X POST -H 'Content-Type: application/json' -d '{}' "+shq(kubeconfig)+" -o kubeconfig")
		e.snippet("Python", "python", b.ociPy(nil, fmt.Sprintf(`
client = oci.container_engine.ContainerEngineClient(config, service_endpoint=endpoint)

kubeconfig = client.create_kubeconfig(%s).data.text
open("kubeconfig", "w").write(kubeconfig)
print(kubeconfig)`, jsStr(id))))
		e.action(model.TryAction{ID: ActGet, Label: "Get kubeconfig", Description: "Show the kubeconfig file for kubectl (it uses the OCI CLI to get tokens).", Input: "none"})
	}
}

// ---------------------------------------------------------------------------
// Postman

// ociPostman adds an "OCI services (sandbox)" folder of unsigned REST calls
// on the ociBaseUrl variable. Postman can't sign OCI requests; the sandbox
// accepts unsigned ones for its default tenancy, so the folder is skipped
// when the workflow's resources live in another tenancy.
func (b *builder) ociPostman(col *PostmanCollection) {
	if b.oci == "" {
		return
	}
	if !b.ociUnsigned() {
		col.Info.Description += " OCI requests are left out: they must be signed for tenancy " + b.ociTenancy() + ", which Postman can't do; use the OCI CLI or SDK examples."
		return
	}
	req := func(name, method, full, body string) PostmanItem {
		r := &PostmanRequest{Method: method, Header: []PostmanHeader{}, URL: b.ociPostmanURL(full)}
		switch {
		case body == "":
		case json.Valid([]byte(body)):
			r.Header = append(r.Header, PostmanHeader{"Content-Type", "application/json"})
			r.Body = rawJSON(prettyJSON([]byte(body)))
		default:
			r.Header = append(r.Header, PostmanHeader{"Content-Type", "text/plain"})
			r.Body = &PostmanBody{Mode: "raw", Raw: body}
		}
		return PostmanItem{Name: name, Request: r}
	}
	var items []PostmanItem
	for _, e := range b.entries {
		t := e.t
		if t.cloud != model.CloudOCI {
			continue
		}
		switch e.Kind {
		case KindBucket:
			items = append(items,
				req(e.Label+": list objects", http.MethodGet, t.url, ""),
				req(e.Label+": upload hello.txt", http.MethodPut, t.url+"/hello.txt", "Hello from Nimbo"),
				req(e.Label+": download hello.txt", http.MethodGet, t.url+"/hello.txt", ""))
		case KindQueue:
			items = append(items,
				req(e.Label+": send message", http.MethodPost, t.url, `{"messages":[{"content":"Hello from Nimbo"}]}`),
				req(e.Label+": peek messages", http.MethodGet, t.url+"?limit=10&visibilityInSeconds=0&timeoutInSeconds=0", ""))
		case KindStream:
			cursor := req(e.Label+": create cursor", http.MethodPost, t.url+"/cursors", `{"partition":"0","type":"TRIM_HORIZON"}`)
			cursor.Description = "Copy value from the answer into the get messages request's cursor parameter."
			items = append(items,
				req(e.Label+": append message (base64 key/value)", http.MethodPost, t.url+"/messages", `{"messages":[{"key":"bmltYm8=","value":"SGVsbG8gZnJvbSBOaW1ibw=="}]}`),
				cursor,
				req(e.Label+": get messages", http.MethodGet, t.url+"/messages?limit=10&cursor=<cursor>", ""))
		case KindKey:
			if len(e.Actions) == 0 {
				continue
			}
			items = append(items,
				req(e.Label+": encrypt (base64 plaintext)", http.MethodPost, t.url+ociKMSAPI+"/encrypt", `{"keyId":"`+t.arn+`","plaintext":"SGVsbG8gZnJvbSBOaW1ibw=="}`),
				req(e.Label+": decrypt", http.MethodPost, t.url+ociKMSAPI+"/decrypt", `{"keyId":"`+t.arn+`","ciphertext":"<ciphertext>"}`))
		case KindSecret:
			items = append(items, req(e.Label+": get secret bundle (base64 content)", http.MethodGet, t.url, ""))
		case KindFunction:
			items = append(items, req(e.Label+": invoke", http.MethodPost, t.url, `{"name":"Nimbo"}`))
		case KindKubernetes:
			items = append(items, req(e.Label+": get kubeconfig", http.MethodPost, t.url, `{}`))
		}
	}
	if len(items) == 0 {
		return
	}
	col.Variable = append(col.Variable, PostmanVariable{Key: "ociBaseUrl", Value: b.oci, Type: "string"})
	col.Item = append(col.Item, PostmanItem{
		Name:        "OCI services (sandbox)",
		Description: "OCI REST calls on ociBaseUrl, the sandbox's OCI address. They are sent unsigned, which only the sandbox accepts (it serves them from its default tenancy); real OCI needs signed requests.",
		Item:        items,
	})
}

// ociPostmanURL expresses a sandbox OCI URL with the ociBaseUrl variable.
func (b *builder) ociPostmanURL(full string) PostmanURL {
	rest := strings.TrimPrefix(full, b.oci)
	path, query, _ := strings.Cut(rest, "?")
	u := PostmanURL{Raw: "{{ociBaseUrl}}" + rest, Host: []string{"{{ociBaseUrl}}"}, Path: strings.Split(strings.TrimPrefix(path, "/"), "/")}
	if query != "" {
		for kv := range strings.SplitSeq(query, "&") {
			k, v, _ := strings.Cut(kv, "=")
			u.Query = append(u.Query, PostmanQuery{k, v})
		}
	}
	return u
}

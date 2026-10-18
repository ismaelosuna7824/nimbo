package endpoints

import (
	"encoding/json"
	"fmt"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
	"net/http"
	"net/url"
	"strings"
)

// Azure (floci-az). Every data plane is path-routed on the emulator port:
// blob /<account>, queue /<account>-queue, table /<account>-table, Key
// Vault /<vault>-keyvault, Cosmos DB /<account>-cosmos, App Configuration
// /<store>-appconfig, Service Bus management /<namespace>-servicebus,
// Functions /devstoreaccount1-functions, API Management
// /devstoreaccount1-apim/<service>, Event Grid /<topic>-eventgrid. The port
// serves HTTP and HTTPS (self-signed certificate at /_floci/tls-cert); keys
// and tokens are not checked, but Key Vault wants some bearer token.

const (
	// azureDevKey is the well-known development storage key floci-az hands
	// out for every storage account.
	azureDevKey = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
	// azureCosmosKey is the Cosmos DB emulator key; any base64 key works.
	azureCosmosKey = "C2y6yDjf5/R+ob0N8A7Cgv30VRDJIWEHLM+4QDU5DE2nQ9nDuVTqobD4b8mGGyPMbIZnqyMsEcaGQy67XIw/Jw=="
	// azureFunctionsAccount owns the function apps created through ARM.
	azureFunctionsAccount = "devstoreaccount1"
	azureKVVersion        = "api-version=7.4"
	azureAppConfigVersion = "api-version=1.0"
	azureCertFile         = "floci-az.pem"
	azureFakeToken        = "fake"
	azureTablePutDefault  = `{"PartitionKey":"demo","RowKey":"1","message":"Hello from Nimbo"}`
	azureSecretDefault    = `{"name":"greeting","value":"Hello from Nimbo"}`
	azureSettingDefault   = `{"key":"greeting","value":"Hello from Nimbo"}`
	azureQueueMessage     = "Hello from Nimbo"
)

// azureNoAMQP explains why Service Bus and Event Hubs have no Try actions.
const azureNoAMQP = "Messages travel over AMQP (Event Hubs also over Kafka), which the sandbox does not expose on your computer, and Nimbo's floci-az runs these brokers in mocked mode. You can inspect the setup below; send and receive from code once the app runs against real Azure."

func (b *builder) azureResources() {
	b.azStorageAccounts()
	b.azStorageContainers()
	b.azStorageQueues()
	b.azStorageTables()
	b.azKeyVaults()
	b.azKeyVaultSecrets()
	b.azServiceBus()
	b.azEventHubs()
	b.azCosmos()
	b.azAppConfigs()
	b.azFunctionApps()
	b.azContainerApps()
	b.azAPIManagement()
	b.azEventGrid()
	b.azRedis()
	b.azSQLServers()
	b.azPlumbing()
}

// ---------------------------------------------------------------------------
// Helpers

// armName is the name after "/<kind>/" in an ARM resource ID (any case).
func armName(id, kind string) string {
	parts := strings.Split(id, "/")
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], kind) {
			return parts[i+1]
		}
	}
	return ""
}

// azHTTPS is the HTTPS form of the Azure emulator address (same port).
func (b *builder) azHTTPS() string {
	return "https://" + strings.TrimPrefix(strings.TrimPrefix(b.azure, "http://"), "https://")
}

// azLocal turns an address floci-az reports for itself
// (http://localhost:4577/...) into the sandbox address on this machine.
func (b *builder) azLocal(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" || !loopback(p.Hostname()) {
		return u
	}
	out := b.azure + p.EscapedPath()
	if p.RawQuery != "" {
		out += "?" + p.RawQuery
	}
	return out
}

// azAccountKey is the key of storage account acct as reported in state,
// else the development key (floci-az accepts any key).
func (b *builder) azAccountKey(acct string) string {
	for _, sa := range b.byType["azurerm_storage_account"] {
		if str(sa, "name") == acct && str(sa, "primary_access_key") != "" {
			return str(sa, "primary_access_key")
		}
	}
	return azureDevKey
}

// azConnString is the storage connection string of acct in the sandbox.
func (b *builder) azConnString(acct string) string {
	base := b.azure + "/" + acct
	return fmt.Sprintf("DefaultEndpointsProtocol=http;AccountName=%s;AccountKey=%s;BlobEndpoint=%s;QueueEndpoint=%s-queue;TableEndpoint=%s-table;",
		acct, b.azAccountKey(acct), base, base, base)
}

// azStorageAccountOf is the storage account a container/queue/table
// belongs to (storage_account_id in azurerm 4+, storage_account_name before).
func azStorageAccountOf(r *resource) string {
	if n := armName(str(r, "storage_account_id"), "storageAccounts"); n != "" {
		return n
	}
	if n := str(r, "storage_account_name"); n != "" {
		return n
	}
	return armName(str(r, "id"), "storageAccounts")
}

// azCertSteps is the shell preamble that downloads the sandbox certificate.
func (b *builder) azCertSteps() string {
	return fmt.Sprintf("curl -s %s/_floci/tls-cert -o %s", b.azure, azureCertFile)
}

func (e *entry) secretField(label, value, hint string) {
	if value != "" {
		e.Fields = append(e.Fields, model.AccessField{Label: label, Value: value, Hint: hint, Secret: true})
	}
}

const azAnyKeyHint = "The sandbox accepts any key; this is the one it reports."

// ---------------------------------------------------------------------------
// Sandbox access

func (b *builder) azureSandboxAccess(base string) model.SandboxAccess {
	ids := sandbox.SandboxIdentities()
	hostPort := strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
	acct := "devstoreaccount1"
	if sas := b.byType["azurerm_storage_account"]; len(sas) > 0 && str(sas[0], "name") != "" {
		acct = str(sas[0], "name")
	}
	cs := b.azConnString(acct)
	return model.SandboxAccess{
		Cloud: model.CloudAzure, URL: base,
		Credentials: []model.AccessField{
			{Label: "HTTPS address", Value: "https://" + hostPort, Hint: "Same port as above: the sandbox answers HTTP and HTTPS. SDKs that insist on https:// (Key Vault, App Configuration) use this one."},
			{Label: "Certificate", Value: base + "/_floci/tls-cert", Hint: "Download it and trust it for HTTPS: NODE_EXTRA_CA_CERTS (Node.js), REQUESTS_CA_BUNDLE or connection_verify (Python), curl --cacert."},
			{Label: "Storage connection string (" + acct + ")", Value: cs, Hint: "Works with every Azure Storage SDK and az storage commands; replace the account name for another account."},
			{Label: "Storage account key", Value: azureDevKey, Secret: true, Hint: "Any key works; the sandbox doesn't check signatures."},
			{Label: "Bearer token", Value: azureFakeToken, Hint: "Key Vault and Azure Resource Manager calls need an Authorization header; any token works."},
			{Label: "Subscription ID", Value: ids.AzureSubscription},
			{Label: "Tenant ID", Value: ids.AzureTenant},
			{Label: "Client ID", Value: ids.AzureClient},
			{Label: "Client secret", Value: "fake-secret", Hint: "Any value works."},
			{Label: "Metadata host", Value: hostPort, Hint: "azurerm provider: environment = \"stack\", metadata_host = this value (needs the certificate trusted)."},
		},
		EnvSnippet: strings.Join([]string{
			fmt.Sprintf("curl -s %s/_floci/tls-cert -o %s", base, azureCertFile),
			fmt.Sprintf("export NODE_EXTRA_CA_CERTS=\"$PWD/%s\" REQUESTS_CA_BUNDLE=\"$PWD/%s\" SSL_CERT_FILE=\"$PWD/%s\"", azureCertFile, azureCertFile, azureCertFile),
			"export AZURE_STORAGE_CONNECTION_STRING=" + shq(cs),
			fmt.Sprintf("export ARM_ENVIRONMENT=stack ARM_METADATA_HOSTNAME=%s ARM_SUBSCRIPTION_ID=%s ARM_TENANT_ID=%s ARM_CLIENT_ID=%s ARM_CLIENT_SECRET=fake-secret",
				hostPort, ids.AzureSubscription, ids.AzureTenant, ids.AzureClient),
		}, "\n"),
		Notes: []string{
			"Every service shares this address and is chosen by the first path segment: /<account> (Blob), /<account>-queue, /<account>-table, /<vault>-keyvault, /<account>-cosmos, /<store>-appconfig. Azure host names such as <account>.blob.core.windows.net don't resolve on your computer; use the addresses on each card.",
			"Keys, connection-string secrets and tokens are not checked. Key Vault still wants an Authorization: Bearer header (any token) and its SDKs need disableChallengeResourceVerification (JavaScript) / verify_challenge_resource=False (Python).",
			"The certificate is self-signed: plain http:// works for curl, Storage, Cosmos DB and REST calls; trust the certificate for SDKs that require https:// (the first lines above). REQUESTS_CA_BUNDLE and SSL_CERT_FILE replace the default trust store in that shell, so use them only for sandbox scripts.",
			"The az CLI's storage commands work with the connection string (--connection-string or AZURE_STORAGE_CONNECTION_STRING); other az commands need a registered custom cloud and aren't covered here.",
			"This address stays the same when you stop and start the sandbox.",
		},
	}
}

// ---------------------------------------------------------------------------
// Storage

func (b *builder) azStorageAccounts() {
	for _, sa := range b.byType["azurerm_storage_account"] {
		acct := str(sa, "name")
		if acct == "" {
			continue
		}
		blob := b.azure + "/" + acct
		cs := b.azConnString(acct)
		e := b.add(sa, KindBucket, "A storage account: one address for blob containers (folders of files), queues and tables. Use the connection string below with any Azure Storage SDK.",
			target{cloud: model.CloudAzure, name: acct, url: blob})
		e.field("Account name", acct, "")
		e.field("Blob endpoint", blob, "Containers and files: "+blob+"/<container>/<file>.")
		e.field("Queue endpoint", blob+"-queue", "")
		e.field("Table endpoint", blob+"-table", "")
		e.field("Connection string", cs, "Paste into BlobServiceClient / QueueServiceClient / TableClient fromConnectionString, or az storage --connection-string.")
		e.secretField("Account key", b.azAccountKey(acct), azAnyKeyHint)
		e.snippet("curl", "bash", fmt.Sprintf("# List the account's blob containers\ncurl -s %s\n\n# Create a container named demo\ncurl -i -X PUT %s",
			shq(blob+"?comp=list"), shq(blob+"/demo?restype=container")))
		e.snippet("Azure CLI", "bash", fmt.Sprintf("export AZURE_STORAGE_CONNECTION_STRING=%s\naz storage container create --name demo\naz storage container list --output table", shq(cs)))
		e.snippet("JavaScript", "javascript", fmt.Sprintf(`// npm install @azure/storage-blob, save as try.mjs and run: node try.mjs
import { BlobServiceClient } from "@azure/storage-blob";

const service = BlobServiceClient.fromConnectionString(%s);
await service.getContainerClient("demo").createIfNotExists();
for await (const container of service.listContainers()) console.log(container.name);
`, jsStr(cs)))
		e.snippet("Python", "python", fmt.Sprintf(`# pip install azure-storage-blob
from azure.storage.blob import BlobServiceClient

service = BlobServiceClient.from_connection_string(%s)
if not service.get_container_client("demo").exists():
    service.create_container("demo")
print([c.name for c in service.list_containers()])
`, jsStr(cs)))
		e.action(model.TryAction{ID: ActList, Label: "List containers", Description: "Show the blob containers of this account.", Input: "none"})
	}
}

func (b *builder) azStorageContainers() {
	for _, c := range b.byType["azurerm_storage_container"] {
		acct, name := azStorageAccountOf(c), str(c, "name")
		if acct == "" || name == "" {
			continue
		}
		cu := b.azure + "/" + acct + "/" + name
		cs := b.azConnString(acct)
		e := b.add(c, KindBucket, "A blob container: a folder of files (blobs) in storage account "+acct+". Upload, list and download files with the URLs or the SDK snippets below.",
			target{cloud: model.CloudAzure, name: name, url: cu})
		e.field("Container", name, "")
		e.field("Storage account", acct, "")
		e.field("Container URL", cu, "PUT "+cu+"/<file> with header x-ms-blob-type: BlockBlob uploads; GET downloads.")
		e.field("List files URL", cu+"?restype=container&comp=list", "")
		e.field("Connection string", cs, "")
		e.snippet("curl", "bash", strings.Join([]string{
			fmt.Sprintf("curl -i -X PUT %s \\\n  -H 'x-ms-blob-type: BlockBlob' -H 'Content-Type: text/plain' \\\n  --data-binary 'Hello from Nimbo'", shq(cu+"/hello.txt")),
			"curl -s " + shq(cu+"?restype=container&comp=list"),
			"curl -s " + shq(cu+"/hello.txt"),
		}, "\n\n"))
		e.snippet("Azure CLI", "bash", fmt.Sprintf("export AZURE_STORAGE_CONNECTION_STRING=%s\naz storage blob upload --container-name %s --name hello.txt --data 'Hello from Nimbo' --overwrite\naz storage blob list --container-name %s --output table\naz storage blob download --container-name %s --name hello.txt --file hello-copy.txt",
			shq(cs), shq(name), shq(name), shq(name)))
		e.snippet("JavaScript", "javascript", fmt.Sprintf(`// npm install @azure/storage-blob, save as try.mjs and run: node try.mjs
import { BlobServiceClient } from "@azure/storage-blob";

const container = BlobServiceClient.fromConnectionString(%s).getContainerClient(%s);
const text = "Hello from Nimbo";
await container.getBlockBlobClient("hello.txt").upload(text, Buffer.byteLength(text));
for await (const blob of container.listBlobsFlat()) console.log(blob.name);
console.log((await container.getBlobClient("hello.txt").downloadToBuffer()).toString());
`, jsStr(cs), jsStr(name)))
		e.snippet("Python", "python", fmt.Sprintf(`# pip install azure-storage-blob
from azure.storage.blob import BlobServiceClient

container = BlobServiceClient.from_connection_string(%s).get_container_client(%s)
container.upload_blob("hello.txt", b"Hello from Nimbo", overwrite=True)
print([b.name for b in container.list_blobs()])
print(container.download_blob("hello.txt").readall().decode())
`, jsStr(cs), jsStr(name)))
		e.action(model.TryAction{ID: ActList, Label: "List files", Description: "Show the blobs in this container.", Input: "none"})
		e.action(model.TryAction{ID: ActPut, Label: "Upload a file", Description: "Store body as a blob named key.", Input: "json", Default: s3PutDefault, Writes: true})
		e.action(model.TryAction{ID: ActGet, Label: "Download a file", Description: "Show the content of the blob with this name.", Input: "text", Default: "hello.txt"})
	}
}

func (b *builder) azStorageQueues() {
	for _, q := range b.byType["azurerm_storage_queue"] {
		acct, name := azStorageAccountOf(q), str(q, "name")
		if acct == "" || name == "" {
			continue
		}
		qu := b.azure + "/" + acct + "-queue/" + name
		cs := b.azConnString(acct)
		e := b.add(q, KindQueue, "A storage queue: drop text messages in, and a worker picks them up later.",
			target{cloud: model.CloudAzure, name: name, url: qu})
		e.field("Queue name", name, "")
		e.field("Storage account", acct, "")
		e.field("Messages URL", qu+"/messages", "POST <QueueMessage><MessageText>…</MessageText></QueueMessage> to send; GET ?peekonly=true to look without removing.")
		e.field("Connection string", cs, "")
		e.snippet("curl", "bash", strings.Join([]string{
			fmt.Sprintf("curl -i -X POST %s \\\n  -d '<QueueMessage><MessageText>Hello from Nimbo</MessageText></QueueMessage>'", shq(qu+"/messages")),
			"curl -s " + shq(qu+"/messages?peekonly=true&numofmessages=10"),
		}, "\n\n"))
		e.snippet("Azure CLI", "bash", fmt.Sprintf("export AZURE_STORAGE_CONNECTION_STRING=%s\naz storage message put --queue-name %s --content 'Hello from Nimbo'\naz storage message peek --queue-name %s --num-messages 10",
			shq(cs), shq(name), shq(name)))
		e.snippet("JavaScript", "javascript", fmt.Sprintf(`// npm install @azure/storage-queue, save as try.mjs and run: node try.mjs
import { QueueServiceClient } from "@azure/storage-queue";

const queue = QueueServiceClient.fromConnectionString(%s).getQueueClient(%s);
await queue.sendMessage("Hello from Nimbo");
const { peekedMessageItems } = await queue.peekMessages({ numberOfMessages: 10 });
for (const m of peekedMessageItems) console.log(m.messageText);
`, jsStr(cs), jsStr(name)))
		e.snippet("Python", "python", fmt.Sprintf(`# pip install azure-storage-queue
from azure.storage.queue import QueueClient

queue = QueueClient.from_connection_string(%s, %s)
queue.send_message("Hello from Nimbo")
for m in queue.peek_messages(max_messages=10):
    print(m.content)
`, jsStr(cs), jsStr(name)))
		e.action(model.TryAction{ID: ActSend, Label: "Send a message", Description: "Add this text to the queue.", Input: "text", Default: azureQueueMessage, Writes: true})
		e.action(model.TryAction{ID: ActReceive, Label: "Peek messages", Description: "Show up to 10 waiting messages without removing them.", Input: "none"})
	}
}

func (b *builder) azStorageTables() {
	for _, t := range b.byType["azurerm_storage_table"] {
		acct, name := azStorageAccountOf(t), str(t, "name")
		if acct == "" || name == "" {
			continue
		}
		tu := b.azure + "/" + acct + "-table/" + name
		cs := b.azConnString(acct)
		e := b.add(t, KindTable, "A storage table: records (entities) identified by PartitionKey + RowKey, with any other properties you like.",
			target{cloud: model.CloudAzure, name: name, url: tu})
		e.field("Table name", name, "")
		e.field("Storage account", acct, "")
		e.field("Table URL", tu, "POST JSON entities here; GET "+tu+"() lists them (send Accept: application/json;odata=nometadata).")
		e.field("Connection string", cs, "")
		e.snippet("curl", "bash", strings.Join([]string{
			fmt.Sprintf("curl -i -X POST %s \\\n  -H 'Content-Type: application/json' -H 'Accept: application/json;odata=nometadata' \\\n  -d %s", shq(tu), shq(azureTablePutDefault)),
			fmt.Sprintf("curl -s %s -H 'Accept: application/json;odata=nometadata'", shq(tu+"()")),
		}, "\n\n"))
		e.snippet("Azure CLI", "bash", fmt.Sprintf("export AZURE_STORAGE_CONNECTION_STRING=%s\naz storage entity insert --table-name %s --if-exists replace --entity PartitionKey=demo RowKey=1 message='Hello from Nimbo'\naz storage entity query --table-name %s",
			shq(cs), shq(name), shq(name)))
		e.snippet("JavaScript", "javascript", fmt.Sprintf(`// npm install @azure/data-tables, save as try.mjs and run: node try.mjs
import { TableClient } from "@azure/data-tables";

// allowInsecureConnection: the sandbox address is plain http.
const table = TableClient.fromConnectionString(%s, %s, { allowInsecureConnection: true });
await table.upsertEntity({ partitionKey: "demo", rowKey: "1", message: "Hello from Nimbo" });
for await (const entity of table.listEntities()) console.log(entity);
`, jsStr(cs), jsStr(name)))
		e.snippet("Python", "python", fmt.Sprintf(`# pip install azure-data-tables
from azure.data.tables import TableClient

table = TableClient.from_connection_string(%s, table_name=%s)
table.upsert_entity({"PartitionKey": "demo", "RowKey": "1", "message": "Hello from Nimbo"})
for entity in table.list_entities():
    print(dict(entity))
`, jsStr(cs), jsStr(name)))
		e.action(model.TryAction{ID: ActScan, Label: "List entities", Description: "Show up to 25 entities.", Input: "none"})
		e.action(model.TryAction{ID: ActPut, Label: "Save an entity", Description: "Insert or replace this entity (PartitionKey and RowKey are required).", Input: "json", Default: azureTablePutDefault, Writes: true})
	}
}

// ---------------------------------------------------------------------------
// Key Vault

// azKVSnippets adds snippets reading and writing secret in vault.
func (b *builder) azKVSnippets(e *entry, vault, secret string) {
	vu := b.azure + "/" + vault + "-keyvault"
	vs := b.azHTTPS() + "/" + vault + "-keyvault"
	auth := "-H 'Authorization: Bearer " + azureFakeToken + "'"
	e.snippet("curl", "bash", strings.Join([]string{
		fmt.Sprintf("curl -s -X PUT %s %s \\\n  -H 'Content-Type: application/json' -d '{\"value\":\"Hello from Nimbo\"}'", shq(vu+"/secrets/"+secret+"?"+azureKVVersion), auth),
		fmt.Sprintf("curl -s %s %s", shq(vu+"/secrets/"+secret+"?"+azureKVVersion), auth),
		fmt.Sprintf("curl -s %s %s", shq(vu+"/secrets?"+azureKVVersion), auth),
	}, "\n\n"))
	e.snippet("JavaScript", "javascript", fmt.Sprintf(`// npm install @azure/keyvault-secrets, save as try.mjs, then trust the sandbox certificate and run:
//   %s && NODE_EXTRA_CA_CERTS=%s node try.mjs
import { SecretClient } from "@azure/keyvault-secrets";

// The sandbox accepts any token.
const credential = { getToken: async () => ({ token: "fake", expiresOnTimestamp: Date.now() + 3600_000 }) };
const client = new SecretClient(%s, credential, { disableChallengeResourceVerification: true });
await client.setSecret(%s, "Hello from Nimbo");
console.log((await client.getSecret(%s)).value);
for await (const s of client.listPropertiesOfSecrets()) console.log(s.name);
`, b.azCertSteps(), azureCertFile, jsStr(vs), jsStr(secret), jsStr(secret)))
	e.snippet("Python", "python", fmt.Sprintf(`# pip install azure-keyvault-secrets
# Download the sandbox certificate first: %s
import time

from azure.core.credentials import AccessToken
from azure.keyvault.secrets import SecretClient


class SandboxCredential:  # the sandbox accepts any token
    def get_token(self, *scopes, **kwargs):
        return AccessToken("fake", int(time.time()) + 3600)


client = SecretClient(%s, SandboxCredential(), verify_challenge_resource=False, connection_verify=%s)
client.set_secret(%s, "Hello from Nimbo")
print(client.get_secret(%s).value)
print([s.name for s in client.list_properties_of_secrets()])
`, b.azCertSteps(), jsStr(vs), jsStr(azureCertFile), jsStr(secret), jsStr(secret)))
}

func (b *builder) azKeyVaults() {
	for _, kv := range b.byType["azurerm_key_vault"] {
		vault := str(kv, "name")
		if vault == "" {
			continue
		}
		vu := b.azure + "/" + vault + "-keyvault"
		e := b.add(kv, KindSecret, "A key vault: a safe place for passwords, connection strings and keys. Read and write secrets with any bearer token.",
			target{cloud: model.CloudAzure, name: vault, url: vu})
		e.field("Vault name", vault, "")
		e.field("Vault URL (SDKs)", b.azHTTPS()+"/"+vault+"-keyvault", "Pass to SecretClient; trust the sandbox certificate first.")
		e.field("Vault URL (curl)", vu, "Send Authorization: Bearer "+azureFakeToken+" (any token works) and ?"+azureKVVersion+".")
		e.field("Vault URI in Azure", str(kv, "vault_uri"), "What your app uses in real Azure; it doesn't resolve on your computer.")
		secret := "greeting"
		for _, s := range b.byType["azurerm_key_vault_secret"] {
			if armName(str(s, "key_vault_id"), "vaults") == vault && str(s, "name") != "" {
				secret = str(s, "name")
				break
			}
		}
		b.azKVSnippets(e, vault, secret)
		e.action(model.TryAction{ID: ActList, Label: "List secrets", Description: "Show the names of the vault's secrets.", Input: "none"})
		e.action(model.TryAction{ID: ActGet, Label: "Read a secret", Description: "Show the current value of the secret with this name.", Input: "text", Default: secret})
		e.action(model.TryAction{ID: ActPut, Label: "Save a secret", Description: "Store value under name (a new version if it exists).", Input: "json", Default: azureSecretDefault, Writes: true})
	}
}

func (b *builder) azKeyVaultSecrets() {
	for _, s := range b.byType["azurerm_key_vault_secret"] {
		vault, name := armName(str(s, "key_vault_id"), "vaults"), str(s, "name")
		if vault == "" || name == "" {
			continue
		}
		su := b.azure + "/" + vault + "-keyvault/secrets/" + name
		e := b.add(s, KindSecret, "A secret in key vault "+vault+". Read it from your app instead of hard-coding the value.",
			target{cloud: model.CloudAzure, name: name, url: b.azure + "/" + vault + "-keyvault"})
		e.field("Secret name", name, "")
		e.field("Key vault", vault, "")
		e.field("Secret URL", su+"?"+azureKVVersion, "GET with Authorization: Bearer "+azureFakeToken+" (any token works).")
		b.azKVSnippets(e, vault, name)
		e.action(model.TryAction{ID: ActGet, Label: "Read the secret", Description: "Show its current value.", Input: "none"})
		e.action(model.TryAction{ID: ActPut, Label: "Save a new value", Description: "Store this text as the secret's new version.", Input: "text", Default: "Hello from Nimbo", Writes: true})
	}
}

// ---------------------------------------------------------------------------
// Service Bus and Event Hubs

func (b *builder) azServiceBus() {
	for _, ns := range b.byType["azurerm_servicebus_namespace"] {
		name := str(ns, "name")
		if name == "" {
			continue
		}
		mu := b.azure + "/" + name + "-servicebus"
		e := b.add(ns, KindQueue, "A Service Bus namespace: home of queues (one receiver per message) and topics (every subscription gets a copy).",
			target{cloud: model.CloudAzure, name: name, url: mu})
		e.Warning = azureNoAMQP
		e.field("Namespace", name, "")
		e.field("Management URL", mu, "GET "+mu+"/$Resources/queues or /$Resources/topics lists entities.")
		e.secretField("Connection string in Azure", str(ns, "default_primary_connection_string"), "What your app uses in real Azure.")
		e.snippet("curl", "bash", fmt.Sprintf("curl -s %s\n\ncurl -s %s", shq(mu+"/$Resources/queues"), shq(mu+"/$Resources/topics")))
		e.action(model.TryAction{ID: ActList, Label: "List queues and topics", Description: "Show the entities of this namespace.", Input: "none"})
	}
	for _, typ := range []string{"azurerm_servicebus_queue", "azurerm_servicebus_topic"} {
		for _, q := range b.byType[typ] {
			ns, name := armName(str(q, "namespace_id"), "namespaces"), str(q, "name")
			if ns == "" {
				ns = str(q, "namespace_name")
			}
			if ns == "" || name == "" {
				continue
			}
			kind, what := KindQueue, "A Service Bus queue in namespace "+ns+": each message goes to one receiver."
			if typ == "azurerm_servicebus_topic" {
				kind, what = KindTopic, "A Service Bus topic in namespace "+ns+": every subscription receives a copy of each message."
			}
			eu := b.azure + "/" + ns + "-servicebus/" + name
			e := b.add(q, kind, what, target{cloud: model.CloudAzure, name: name, url: eu})
			e.Warning = azureNoAMQP
			e.field("Name", name, "")
			e.field("Namespace", ns, "")
			e.field("Settings URL", eu, "GET shows its settings (Atom XML).")
			e.snippet("curl", "bash", "curl -s "+shq(eu))
			e.action(model.TryAction{ID: ActGet, Label: "Show settings", Description: "Show how the broker sees it.", Input: "none"})
		}
	}
}

func (b *builder) azEventHubs() {
	for _, typ := range []string{"azurerm_eventhub_namespace", "azurerm_eventhub"} {
		for _, r := range b.byType[typ] {
			name := str(r, "name")
			if name == "" {
				continue
			}
			ns := name
			what := "An Event Hubs namespace: high-throughput event streams (also reachable with Kafka clients)."
			if typ == "azurerm_eventhub" {
				ns = armName(str(r, "namespace_id"), "namespaces")
				if ns == "" {
					ns = str(r, "namespace_name")
				}
				what = "An event hub (stream) in namespace " + ns + ": producers append events, consumers read them in order per partition."
			}
			e := b.add(r, KindStream, what, target{cloud: model.CloudAzure, name: name})
			e.Warning = azureNoAMQP
			e.field("Name", name, "")
			if typ == "azurerm_eventhub" {
				e.field("Namespace", ns, "")
				e.field("Partitions", str(r, "partition_count"), "")
			}
			e.secretField("Connection string in Azure", str(r, "default_primary_connection_string"), "What your app uses in real Azure.")
		}
	}
}

// ---------------------------------------------------------------------------
// Cosmos DB

func (b *builder) azCosmosKey(acct string) string {
	for _, a := range b.byType["azurerm_cosmosdb_account"] {
		if str(a, "name") == acct && str(a, "primary_key") != "" {
			return str(a, "primary_key")
		}
	}
	return azureCosmosKey
}

func (b *builder) azCosmos() {
	for _, a := range b.byType["azurerm_cosmosdb_account"] {
		acct := str(a, "name")
		if acct == "" {
			continue
		}
		ep := b.azure + "/" + acct + "-cosmos/"
		key := b.azCosmosKey(acct)
		e := b.add(a, KindDatabase, "A Cosmos DB account (NoSQL API): databases of containers holding JSON documents you query with SQL.",
			target{cloud: model.CloudAzure, name: acct, url: strings.TrimSuffix(ep, "/")})
		e.field("Account endpoint", ep, "Pass to CosmosClient together with the key.")
		e.secretField("Key", key, azAnyKeyHint)
		e.field("Connection string", "AccountEndpoint="+ep+";AccountKey="+key+";", "")
		e.snippet("curl", "bash", "curl -s "+shq(ep+"dbs"))
		e.snippet("JavaScript", "javascript", fmt.Sprintf(`// npm install @azure/cosmos, save as try.mjs and run: node try.mjs
import { CosmosClient } from "@azure/cosmos";

const client = new CosmosClient({ endpoint: %s, key: %s });
const { resources } = await client.databases.readAll().fetchAll();
console.log(resources.map((db) => db.id));
`, jsStr(ep), jsStr(key)))
		e.snippet("Python", "python", fmt.Sprintf(`# pip install azure-cosmos
from azure.cosmos import CosmosClient

client = CosmosClient(%s, credential=%s)
print([db["id"] for db in client.list_databases()])
`, jsStr(ep), jsStr(key)))
		e.action(model.TryAction{ID: ActList, Label: "List databases", Description: "Show the databases of this account.", Input: "none"})
	}
	for _, c := range b.byType["azurerm_cosmosdb_sql_container"] {
		acct, db, name := str(c, "account_name"), str(c, "database_name"), str(c, "name")
		if acct == "" || db == "" || name == "" {
			continue
		}
		pk := "/id"
		if paths, ok := c.Values["partition_key_paths"].([]any); ok && len(paths) > 0 {
			if p, ok := paths[0].(string); ok && p != "" {
				pk = p
			}
		} else if p := str(c, "partition_key_path"); p != "" {
			pk = p
		}
		ep := b.azure + "/" + acct + "-cosmos/"
		cu := ep + "dbs/" + db + "/colls/" + name
		key := b.azCosmosKey(acct)
		item := azCosmosItem(pk)
		e := b.add(c, KindTable, "A Cosmos DB container in database "+db+": JSON documents partitioned by "+pk+", queried with SQL (SELECT * FROM c).",
			target{cloud: model.CloudAzure, name: pk, url: cu})
		e.field("Container", name, "")
		e.field("Database", db, "")
		e.field("Partition key", pk, "Every document needs this property.")
		e.field("Account endpoint", ep, "")
		e.secretField("Key", key, azAnyKeyHint)
		e.snippet("curl", "bash", fmt.Sprintf("curl -s -X POST %s \\\n  -H 'Content-Type: application/query+json' -H 'x-ms-documentdb-isquery: true' \\\n  -H 'x-ms-documentdb-query-enablecrosspartition: true' \\\n  -d '{\"query\":\"SELECT * FROM c\",\"parameters\":[]}'",
			shq(cu+"/docs")))
		e.snippet("JavaScript", "javascript", fmt.Sprintf(`// npm install @azure/cosmos, save as try.mjs and run: node try.mjs
import { CosmosClient } from "@azure/cosmos";

const client = new CosmosClient({ endpoint: %s, key: %s });
const container = client.database(%s).container(%s);
await container.items.upsert(%s);
const { resources } = await container.items.query("SELECT * FROM c").fetchAll();
console.log(resources);
`, jsStr(ep), jsStr(key), jsStr(db), jsStr(name), jsLit(item)))
		e.snippet("Python", "python", fmt.Sprintf(`# pip install azure-cosmos
from azure.cosmos import CosmosClient

client = CosmosClient(%s, credential=%s)
container = client.get_database_client(%s).get_container_client(%s)
container.upsert_item(%s)
print(list(container.query_items("SELECT * FROM c", enable_cross_partition_query=True)))
`, jsStr(ep), jsStr(key), jsStr(db), jsStr(name), pyLit(item)))
		e.action(model.TryAction{ID: ActScan, Label: "Query documents", Description: "Run this SQL query (at most 25 documents).", Input: "text", Default: "SELECT * FROM c"})
		e.action(model.TryAction{ID: ActPut, Label: "Save a document", Description: "Insert or replace this JSON document (it needs an id and the partition key).", Input: "json", Default: item, Writes: true})
	}
}

// azCosmosItem is an example document carrying partition key path pk.
func azCosmosItem(pk string) string {
	doc := map[string]any{"id": "1", "message": "Hello from Nimbo"}
	if f := strings.TrimPrefix(pk, "/"); f != "" && f != "id" && !strings.Contains(f, "/") {
		doc[f] = "demo"
	}
	return mustJSON(doc)
}

// ---------------------------------------------------------------------------
// App Configuration

func (b *builder) azAppConfigs() {
	for _, ac := range b.byType["azurerm_app_configuration"] {
		name := str(ac, "name")
		if name == "" {
			continue
		}
		hu := b.azure + "/" + name + "-appconfig"
		su := b.azHTTPS() + "/" + name + "-appconfig"
		cs := "Endpoint=" + su + ";Id=nimbo;Secret=c2FuZGJveA=="
		e := b.add(ac, KindParameter, "An App Configuration store: settings (key-values) and feature flags your apps read at start-up.",
			target{cloud: model.CloudAzure, name: name, url: hu})
		e.field("Store name", name, "")
		e.field("Connection string (SDKs)", cs, "The SDKs need https: trust the sandbox certificate. Id and Secret are not checked.")
		e.field("Settings URL (curl)", hu+"/kv?"+azureAppConfigVersion, "")
		e.field("Endpoint in Azure", str(ac, "endpoint"), "What your app uses in real Azure.")
		e.snippet("curl", "bash", strings.Join([]string{
			fmt.Sprintf("curl -s -X PUT %s \\\n  -H 'Content-Type: application/vnd.microsoft.appconfig.kv+json' -d '{\"value\":\"Hello from Nimbo\"}'", shq(hu+"/kv/greeting?"+azureAppConfigVersion)),
			"curl -s " + shq(hu+"/kv?"+azureAppConfigVersion),
		}, "\n\n"))
		e.snippet("JavaScript", "javascript", fmt.Sprintf(`// npm install @azure/app-configuration, save as try.mjs, then trust the sandbox certificate and run:
//   %s && NODE_EXTRA_CA_CERTS=%s node try.mjs
import { AppConfigurationClient } from "@azure/app-configuration";

const client = new AppConfigurationClient(%s);
await client.setConfigurationSetting({ key: "greeting", value: "Hello from Nimbo" });
console.log((await client.getConfigurationSetting({ key: "greeting" })).value);
for await (const s of client.listConfigurationSettings()) console.log(s.key, "=", s.value);
`, b.azCertSteps(), azureCertFile, jsStr(cs)))
		e.snippet("Python", "python", fmt.Sprintf(`# pip install azure-appconfiguration
# Download the sandbox certificate first: %s
from azure.appconfiguration import AzureAppConfigurationClient, ConfigurationSetting

client = AzureAppConfigurationClient.from_connection_string(%s, connection_verify=%s)
client.set_configuration_setting(ConfigurationSetting(key="greeting", value="Hello from Nimbo"))
print(client.get_configuration_setting(key="greeting").value)
print([(s.key, s.value) for s in client.list_configuration_settings()])
`, b.azCertSteps(), jsStr(cs), jsStr(azureCertFile)))
		e.action(model.TryAction{ID: ActList, Label: "List settings", Description: "Show every key-value of the store.", Input: "none"})
		e.action(model.TryAction{ID: ActGet, Label: "Read a setting", Description: "Show the value of the setting with this key.", Input: "text", Default: "greeting"})
		e.action(model.TryAction{ID: ActPut, Label: "Save a setting", Description: "Store value under key.", Input: "json", Default: azureSettingDefault, Writes: true})
	}
}

// ---------------------------------------------------------------------------
// Compute: Function Apps, Container Apps, API Management

func (b *builder) azFunctionApps() {
	fb := b.azure + "/" + azureFunctionsAccount + "-functions"
	for _, typ := range []string{"azurerm_linux_function_app", "azurerm_windows_function_app", "azurerm_function_app_flex_consumption", "azurerm_function_app"} {
		for _, app := range b.byType[typ] {
			name := str(app, "name")
			if name == "" {
				continue
			}
			admin := fb + "/admin/apps/" + name + "/functions"
			e := b.add(app, KindWebService, "A Function App: your functions run when their URL is called. Each HTTP-triggered function answers at "+fb+"/api/"+name+"/<function>.",
				target{cloud: model.CloudAzure, name: name, url: admin, httpBase: fb + "/api/" + name})
			e.field("App name", name, "")
			e.field("Invoke URL", fb+"/api/"+name+"/<function>", "Replace <function> with a function name; GET or POST.")
			e.field("Functions list URL", admin, "")
			if h := str(app, "default_hostname"); h != "" {
				e.field("Address in Azure", "https://"+h, "What your app uses in real Azure; it doesn't resolve on your computer.")
			}
			for _, f := range b.byType["azurerm_function_app_function"] {
				if armName(str(f, "function_app_id"), "sites") == name && str(f, "name") != "" {
					fn := str(f, "name")
					e.Endpoints = append(e.Endpoints, model.HTTPEndpoint{Method: "ANY", Path: "/api/" + name + "/" + fn, URL: fb + "/api/" + name + "/" + fn, Proxy: true})
				}
			}
			if len(e.Endpoints) > 0 {
				e.addHTTPSnippets()
				e.action(model.TryAction{ID: ActRequest, Label: "Call a function", Input: "http"})
			} else {
				e.snippet("curl", "bash", "curl -s "+shq(admin)+"\n\ncurl -i "+shq(fb+"/api/"+name+"/hello"))
			}
			e.action(model.TryAction{ID: ActList, Label: "List functions", Description: "Show the functions deployed to this app.", Input: "none"})
		}
	}
}

func (b *builder) azContainerApps() {
	for _, app := range b.byType["azurerm_container_app"] {
		fqdn := ""
		for _, in := range list(app, "ingress") {
			fqdn = mstr(in, "fqdn")
		}
		if fqdn == "" {
			fqdn = str(app, "latest_revision_fqdn")
		}
		if fqdn == "" {
			continue
		}
		e := b.add(app, KindWebService, "A Container App: your container image serving HTTP. In the sandbox you call it on the emulator address with its Azure host name in the Host header.",
			target{cloud: model.CloudAzure, name: str(app, "name"), url: fqdn, httpBase: b.azure})
		e.Warning = "Nimbo's floci-az runs Container Apps in mocked mode: the app is registered but no container runs, so calls answer 503 ContainerAppMocked."
		e.field("Address in Azure", "https://"+fqdn, "")
		e.field("Sandbox address", b.azure+"/", "Send Host: "+fqdn)
		e.field("Host header", fqdn, "")
		e.Endpoints = append(e.Endpoints, model.HTTPEndpoint{Method: "ANY", Path: "/", URL: b.azure + "/", Description: "Host: " + fqdn, Proxy: true})
		e.snippet("curl", "bash", fmt.Sprintf("curl -i %s -H %s", shq(b.azure+"/"), shq("Host: "+fqdn)))
		e.snippet("JavaScript", "javascript", fmt.Sprintf(`// Node.js: save as try.mjs and run: node try.mjs (fetch can't set the Host header)
import http from "node:http";

http.get(%s, { headers: { Host: %s } }, (res) => {
  let body = "";
  res.on("data", (chunk) => (body += chunk));
  res.on("end", () => console.log(res.statusCode, body));
});
`, jsStr(b.azure+"/"), jsStr(fqdn)))
		e.snippet("Python", "python", fmt.Sprintf(`import urllib.error
import urllib.request

req = urllib.request.Request(%s, headers={"Host": %s})
try:
    with urllib.request.urlopen(req) as res:
        print(res.status, res.read().decode())
except urllib.error.HTTPError as err:
    print(err.code, err.read().decode())
`, jsStr(b.azure+"/"), jsStr(fqdn)))
		e.action(model.TryAction{ID: ActRequest, Label: "Call the app", Description: "Nimbo adds the Host header for you.", Input: "http"})
	}
}

func (b *builder) azAPIManagement() {
	for _, svc := range b.byType["azurerm_api_management"] {
		name := str(svc, "name")
		if name == "" {
			continue
		}
		gw := b.azure + "/" + azureFunctionsAccount + "-apim/" + name
		e := b.add(svc, KindHTTPAPI, "An API Management gateway: one front door for your APIs, with keys, policies and routing.",
			target{cloud: model.CloudAzure, name: name, httpBase: gw})
		e.field("Gateway URL", gw, "APIs answer at "+gw+"/<api path>/<operation>.")
		if u := str(svc, "gateway_url"); b.azLocal(u) == u {
			e.field("Gateway URL in Azure", u, "")
		}
		for _, api := range b.byType["azurerm_api_management_api"] {
			if str(api, "api_management_name") != name {
				continue
			}
			apiPath := strings.Trim(str(api, "path"), "/")
			ops := 0
			for _, op := range b.byType["azurerm_api_management_api_operation"] {
				if str(op, "api_name") != str(api, "name") || str(op, "api_management_name") != name {
					continue
				}
				ops++
				p, proxy := fillPath(joinPath(apiPath, str(op, "url_template")), b.sample)
				e.Endpoints = append(e.Endpoints, model.HTTPEndpoint{Method: strings.ToUpper(orDefault(str(op, "method"), "GET")), Path: joinPath(apiPath, str(op, "url_template")), URL: gw + p, Description: str(op, "display_name"), Proxy: proxy})
			}
			if ops == 0 {
				e.Endpoints = append(e.Endpoints, model.HTTPEndpoint{Method: "ANY", Path: joinPath(apiPath, "/"), URL: gw + joinPath(apiPath, "/"), Proxy: true})
			}
		}
		for _, sub := range b.byType["azurerm_api_management_subscription"] {
			if str(sub, "api_management_name") == name {
				e.secretField("Subscription key", str(sub, "primary_key"), "Send it as the Ocp-Apim-Subscription-Key header when the API requires a subscription.")
				break
			}
		}
		if len(e.Endpoints) > 0 {
			e.addHTTPSnippets()
		}
		e.action(model.TryAction{ID: ActRequest, Label: "Send a request", Input: "http"})
	}
}

// joinPath joins URL path parts with single slashes and a leading slash.
func joinPath(parts ...string) string {
	var segs []string
	for _, p := range parts {
		if p = strings.Trim(p, "/"); p != "" {
			segs = append(segs, p)
		}
	}
	return "/" + strings.Join(segs, "/")
}

// ---------------------------------------------------------------------------
// Event Grid

func (b *builder) azEventGrid() {
	for _, t := range b.byType["azurerm_eventgrid_topic"] {
		name := str(t, "name")
		if name == "" {
			continue
		}
		ep := b.azure + "/" + name + "-eventgrid/api/events"
		if u := str(t, "endpoint"); u != "" && b.azLocal(u) != u {
			ep = b.azLocal(u)
		}
		key := str(t, "primary_access_key")
		e := b.add(t, KindTopic, "An Event Grid topic: publish events and Event Grid delivers them to every matching subscription (webhooks, queues, functions).",
			target{cloud: model.CloudAzure, name: name, url: ep})
		e.field("Publish URL", ep, "POST a JSON array of events with the aeg-sas-key header.")
		e.secretField("Access key", key, azAnyKeyHint)
		event := `[{"id":"1","subject":"demo","eventType":"Nimbo.Demo","eventTime":"2026-01-01T00:00:00Z","dataVersion":"1.0","data":{"message":"Hello from Nimbo"}}]`
		k := orDefault(key, "any-key")
		e.snippet("curl", "bash", fmt.Sprintf("curl -i -X POST %s \\\n  -H 'Content-Type: application/json' -H %s \\\n  -d %s", shq(ep), shq("aeg-sas-key: "+k), shq(event)))
		e.snippet("JavaScript", "javascript", fmt.Sprintf(`// Node.js 18+: save as try.mjs and run: node try.mjs
const res = await fetch(%s, {
  method: "POST",
  headers: { "Content-Type": "application/json", "aeg-sas-key": %s },
  body: JSON.stringify(%s),
});
console.log(res.status, await res.text());
`, jsStr(ep), jsStr(k), jsLit(event)))
		e.snippet("Python", "python", fmt.Sprintf(`import json
import urllib.request

req = urllib.request.Request(
    %s,
    method="POST",
    headers={"Content-Type": "application/json", "aeg-sas-key": %s},
    data=json.dumps(%s).encode(),
)
with urllib.request.urlopen(req) as res:
    print(res.status, res.read().decode())
`, jsStr(ep), jsStr(k), pyLit(event)))
		e.action(model.TryAction{ID: ActPublish, Label: "Publish an event", Description: "Send this JSON as the data of one event.", Input: "json", Default: helloMessage, Writes: true})
	}
}

// ---------------------------------------------------------------------------
// Databases and caches (Docker-backed engines)

// azDockerHint explains how to reach an engine floci-az started as its own
// container (published on a Docker-chosen port of this machine).
func azDockerHint(what, container, port string) string {
	return fmt.Sprintf("floci-az runs this %s as its own Docker container (%s) and publishes port %s on a port of your computer that Docker picks: find it with docker port %s %s and connect to 127.0.0.1 on that port. The host name below only resolves inside the sandbox.",
		what, container, port, container, port)
}

func (b *builder) azRedis() {
	for _, c := range b.byType["azurerm_redis_cache"] {
		host := str(c, "hostname")
		port := orDefault(str(c, "port"), "6379")
		key := str(c, "primary_access_key")
		e := b.add(c, KindCache, "An Azure Cache for Redis: an in-memory key-value store (Redis protocol), password = the access key.",
			target{cloud: model.CloudAzure, name: str(c, "name")})
		e.field("Host name", host, "")
		e.field("Port", port, "")
		e.secretField("Password (access key)", key, "")
		e.secretField("Connection string", str(c, "primary_connection_string"), "")
		if host == "" || strings.Contains(host, ".") {
			e.Warning = fmt.Sprintf(noHostAccess, "cache")
			continue
		}
		e.Warning = azDockerHint("cache", host, "6379")
		e.snippet("redis-cli", "bash", fmt.Sprintf("PORT=$(docker port %s 6379 | head -1 | sed 's/.*://')\nredis-cli -h 127.0.0.1 -p \"$PORT\" -a %s ping", shq(host), shq(key)))
		e.snippet("JavaScript", "javascript", fmt.Sprintf(`// npm install redis, save as try.mjs and run (PORT from: docker port %s 6379):
//   PORT=<port> node try.mjs
import { createClient } from "redis";

const client = createClient({ url: "redis://127.0.0.1:" + process.env.PORT, password: %s });
await client.connect();
await client.set("greeting", "Hello from Nimbo");
console.log(await client.get("greeting"));
await client.quit();
`, host, jsStr(key)))
		e.snippet("Python", "python", fmt.Sprintf(`# pip install redis; run with PORT=<port> (from: docker port %s 6379)
import os

import redis

client = redis.Redis(host="127.0.0.1", port=int(os.environ["PORT"]), password=%s)
client.set("greeting", "Hello from Nimbo")
print(client.get("greeting").decode())
`, host, jsStr(key)))
	}
}

func (b *builder) azSQLServers() {
	engines := []struct{ typ, engine, port, dbType, user, pass string }{
		{"azurerm_postgresql_flexible_server", "PostgreSQL", "5432", "azurerm_postgresql_flexible_server_database", "administrator_login", "administrator_password"},
		{"azurerm_mysql_flexible_server", "MySQL", "3306", "azurerm_mysql_flexible_database", "administrator_login", "administrator_password"},
		{"azurerm_mssql_server", "SQL Server", "1433", "azurerm_mssql_database", "administrator_login", "administrator_login_password"},
	}
	for _, eng := range engines {
		for _, s := range b.byType[eng.typ] {
			name := str(s, "name")
			host := first(s, "fqdn", "fully_qualified_domain_name")
			e := b.add(s, KindDatabase, "A managed "+eng.engine+" server.", target{cloud: model.CloudAzure, name: name})
			e.field("Host name", host, "")
			e.field("Port", eng.port, "")
			e.field("Admin user", str(s, eng.user), "")
			e.secretField("Admin password", str(s, eng.pass), "")
			var dbs []string
			for _, d := range b.byType[eng.dbType] {
				if armName(str(d, "server_id"), armServersKind(eng.typ)) == name || str(d, "server_name") == name {
					dbs = append(dbs, str(d, "name"))
				}
			}
			e.field("Databases", strings.Join(dbs, ", "), "")
			switch {
			case host == "" || strings.Contains(host, "."):
				e.Warning = fmt.Sprintf(noHostAccess, "database")
			default:
				e.Warning = azDockerHint("database", host, eng.port)
				switch eng.engine {
				case "PostgreSQL":
					e.snippet("psql", "bash", fmt.Sprintf("PORT=$(docker port %s 5432 | head -1 | sed 's/.*://')\nPGPASSWORD=%s psql -h 127.0.0.1 -p \"$PORT\" -U %s postgres -c 'select version()'",
						shq(host), shq(str(s, eng.pass)), shq(str(s, eng.user))))
				case "MySQL":
					e.snippet("mysql", "bash", fmt.Sprintf("PORT=$(docker port %s 3306 | head -1 | sed 's/.*://')\nmysql -h 127.0.0.1 -P \"$PORT\" -u %s -p%s -e 'select version()'",
						shq(host), shq(str(s, eng.user)), shq(str(s, eng.pass))))
				}
			}
		}
	}
}

// armServersKind is the ARM path segment naming servers of typ.
func armServersKind(typ string) string {
	if typ == "azurerm_mssql_server" {
		return "servers"
	}
	return "flexibleServers"
}

// ---------------------------------------------------------------------------
// Plumbing

// azureSummaries explain Azure resources that have nothing to call.
var azureSummaries = map[string]string{
	"azurerm_resource_group":                            "A resource group: a folder that holds related resources. Nothing to call.",
	"azurerm_role_assignment":                           "Grants an identity a role on a resource. Nothing to call; it decides who may do what.",
	"azurerm_role_definition":                           "A custom role: a named list of permissions. Nothing to call.",
	"azurerm_user_assigned_identity":                    "A managed identity your apps run as, so they get access without passwords. Nothing to call.",
	"azurerm_virtual_network":                           "A private network for your resources. Nothing to call.",
	"azurerm_subnet":                                    "A slice of a virtual network. Nothing to call.",
	"azurerm_network_security_group":                    "Firewall rules for a subnet or network card. Nothing to call.",
	"azurerm_subnet_network_security_group_association": "Applies firewall rules to a subnet. Nothing to call.",
	"azurerm_private_dns_zone":                          "A private DNS zone: names that resolve only inside your virtual networks. Nothing to call.",
	"azurerm_private_dns_zone_virtual_network_link":     "Makes a private DNS zone visible in a virtual network. Nothing to call.",
	"azurerm_private_endpoint":                          "A private address for a service inside a virtual network. Nothing to call.",
	"azurerm_public_ip":                                 "A public IP address. Nothing to call in the sandbox.",
	"azurerm_network_interface":                         "A virtual machine's network card. Nothing to call.",
	"azurerm_service_plan":                              "The compute plan (size and price tier) your Function Apps and Web Apps run on. Nothing to call.",
	"azurerm_container_app_environment":                 "The shared environment Container Apps run in (network and logs). Nothing to call.",
	"azurerm_log_analytics_workspace":                   "Where logs and metrics are collected. Nothing to call from here.",
	"azurerm_application_insights":                      "Application monitoring: your apps send telemetry here. Nothing to call.",
	"azurerm_key_vault_access_policy":                   "Lets an identity read or write a key vault's secrets. Nothing to call.",
	"azurerm_servicebus_subscription":                   "Receives a copy of every message sent to its topic.",
	"azurerm_servicebus_namespace_authorization_rule":   "A shared access key for a Service Bus namespace. Nothing to call.",
	"azurerm_eventhub_consumer_group":                   "A named reader position on an event hub. Nothing to call.",
	"azurerm_cosmosdb_sql_database":                     "A Cosmos DB database: it groups containers. Use the container cards to query documents.",
	"azurerm_postgresql_flexible_server_database":       "A database on a PostgreSQL server; connect through the server card.",
	"azurerm_mysql_flexible_database":                   "A database on a MySQL server; connect through the server card.",
	"azurerm_mssql_database":                            "A database on a SQL Server; connect through the server card.",
	"azurerm_function_app_function":                     "A function of a Function App; call it from the app's card.",
	"azurerm_api_management_api":                        "An API published on an API Management gateway; call it from the gateway's card.",
	"azurerm_api_management_api_operation":              "One operation (method + path) of an API Management API; call it from the gateway's card.",
	"azurerm_api_management_subscription":               "A subscription key for API Management APIs (Ocp-Apim-Subscription-Key header).",
	"azurerm_storage_table_entity":                      "A record stored in a table; list it from the table's card.",
	"azurerm_storage_blob":                              "A file stored in a blob container; download it from the container's card.",
	"azurerm_container_registry":                        "A private registry for container images. floci-az runs it as a local registry:2 container.",
}

// azPlumbing builds generic cards with Azure explanations for the types
// above that no builder handled. All of them are supporting except the
// container registry (people push images to it).
func (b *builder) azPlumbing() {
	for _, r := range b.ordered {
		s, ok := azureSummaries[r.Type]
		if !ok || b.done[r] {
			continue
		}
		b.generic(r)
		e := &b.entries[len(b.entries)-1]
		e.Summary = s
		e.Supporting = r.Type != "azurerm_container_registry"
	}
}

// ---------------------------------------------------------------------------
// Postman

// azurePostman adds an "Azure services (sandbox)" folder of REST calls and
// a folder per HTTP-serving resource; URLs use the azureBaseUrl variable.
// Service Bus and Event Hubs messaging is AMQP-only and left out.
func (b *builder) azurePostman(col *PostmanCollection) {
	if b.azure == "" {
		return
	}
	var services []PostmanItem
	add := func(name, method, full string, headers []PostmanHeader, body string) {
		req := &PostmanRequest{Method: method, Header: append([]PostmanHeader{}, headers...), URL: b.azurePostmanURL(full)}
		if body != "" {
			if json.Valid([]byte(body)) {
				req.Body = rawJSON(prettyJSON([]byte(body)))
			} else {
				req.Body = &PostmanBody{Mode: "raw", Raw: body}
			}
		}
		services = append(services, PostmanItem{Name: name, Request: req})
	}
	jsonH := []PostmanHeader{{"Content-Type", "application/json"}}
	kvH := []PostmanHeader{{"Authorization", "Bearer " + azureFakeToken}}
	kvJSON := []PostmanHeader{{"Authorization", "Bearer " + azureFakeToken}, {"Content-Type", "application/json"}}
	for _, e := range b.entries {
		if e.t.cloud != model.CloudAzure {
			continue
		}
		t := e.t
		switch e.Type {
		case "azurerm_storage_account":
			add("Blob list containers · "+e.Label, http.MethodGet, t.url+"?comp=list", nil, "")
		case "azurerm_storage_container":
			add("Blob list files · "+e.Label, http.MethodGet, t.url+"?restype=container&comp=list", nil, "")
			add("Blob upload hello.txt · "+e.Label, http.MethodPut, t.url+"/hello.txt", []PostmanHeader{{"x-ms-blob-type", "BlockBlob"}, {"Content-Type", "text/plain"}}, "Hello from Nimbo")
			add("Blob download hello.txt · "+e.Label, http.MethodGet, t.url+"/hello.txt", nil, "")
		case "azurerm_storage_queue":
			add("Queue send message · "+e.Label, http.MethodPost, t.url+"/messages", []PostmanHeader{{"Content-Type", "application/xml"}},
				"<QueueMessage><MessageText>Hello from Nimbo</MessageText></QueueMessage>")
			add("Queue peek messages · "+e.Label, http.MethodGet, t.url+"/messages?peekonly=true&numofmessages=10", nil, "")
		case "azurerm_storage_table":
			nometa := PostmanHeader{"Accept", "application/json;odata=nometadata"}
			add("Table insert entity · "+e.Label, http.MethodPost, t.url, append([]PostmanHeader{nometa}, jsonH...), azureTablePutDefault)
			add("Table list entities · "+e.Label, http.MethodGet, t.url+"()?$top=25", []PostmanHeader{nometa}, "")
		case "azurerm_key_vault":
			add("Key Vault list secrets · "+e.Label, http.MethodGet, t.url+"/secrets?"+azureKVVersion, kvH, "")
			add("Key Vault set secret · "+e.Label, http.MethodPut, t.url+"/secrets/greeting?"+azureKVVersion, kvJSON, `{"value":"Hello from Nimbo"}`)
			add("Key Vault get secret · "+e.Label, http.MethodGet, t.url+"/secrets/greeting?"+azureKVVersion, kvH, "")
		case "azurerm_key_vault_secret":
			add("Key Vault get secret · "+e.Label, http.MethodGet, t.url+"/secrets/"+t.name+"?"+azureKVVersion, kvH, "")
		case "azurerm_servicebus_namespace":
			add("Service Bus list queues · "+e.Label, http.MethodGet, t.url+"/$Resources/queues", nil, "")
			add("Service Bus list topics · "+e.Label, http.MethodGet, t.url+"/$Resources/topics", nil, "")
		case "azurerm_cosmosdb_account":
			add("Cosmos DB list databases · "+e.Label, http.MethodGet, t.url+"/dbs", nil, "")
		case "azurerm_cosmosdb_sql_container":
			add("Cosmos DB query · "+e.Label, http.MethodPost, t.url+"/docs", []PostmanHeader{
				{"Content-Type", "application/query+json"}, {"x-ms-documentdb-isquery", "true"}, {"x-ms-documentdb-query-enablecrosspartition", "true"},
			}, `{"query":"SELECT * FROM c","parameters":[]}`)
		case "azurerm_app_configuration":
			add("App Configuration list settings · "+e.Label, http.MethodGet, t.url+"/kv?"+azureAppConfigVersion, nil, "")
			add("App Configuration set setting · "+e.Label, http.MethodPut, t.url+"/kv/greeting?"+azureAppConfigVersion,
				[]PostmanHeader{{"Content-Type", "application/vnd.microsoft.appconfig.kv+json"}}, `{"value":"Hello from Nimbo"}`)
		case "azurerm_eventgrid_topic":
			add("Event Grid publish · "+e.Label, http.MethodPost, t.url, append([]PostmanHeader{{"aeg-sas-key", "any-key"}}, jsonH...),
				`[{"id":"1","subject":"demo","eventType":"Nimbo.Demo","eventTime":"2026-01-01T00:00:00Z","dataVersion":"1.0","data":{"message":"Hello from Nimbo"}}]`)
		}
		if len(e.Endpoints) > 0 && (e.Kind == KindWebService || e.Kind == KindHTTPAPI) {
			folder := PostmanItem{Name: e.Label, Description: e.Summary}
			var hdr []PostmanHeader
			if e.Type == "azurerm_container_app" {
				hdr = []PostmanHeader{{"Host", t.url}}
			}
			for _, ep := range e.Endpoints {
				methods := []string{ep.Method}
				if ep.Method == "ANY" {
					methods = []string{http.MethodGet, http.MethodPost}
				}
				for _, m := range methods {
					req := &PostmanRequest{Method: m, Header: append([]PostmanHeader{}, hdr...), URL: b.azurePostmanURL(ep.URL), Description: ep.Description}
					if m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch {
						req.Header = append(req.Header, jsonH...)
						req.Body = rawJSON(sampleJSONBody)
					}
					folder.Item = append(folder.Item, PostmanItem{Name: m + " " + ep.Path, Request: req})
				}
			}
			col.Item = append(col.Item, folder)
		}
	}
	if len(services) > 0 {
		col.Item = append(col.Item, PostmanItem{
			Name:        "Azure services (sandbox)",
			Description: "Azure REST calls against the sandbox over plain HTTP; keys are not checked and Key Vault takes any bearer token. Service Bus and Event Hubs messages use AMQP/Kafka, which Postman can't send, so only their management calls are included.",
			Item:        services,
		})
	}
	col.Variable = append(col.Variable, PostmanVariable{Key: "azureBaseUrl", Value: b.azure, Type: "string"})
	col.Info.Description += " azureBaseUrl is the sandbox's Azure address."
}

// azurePostmanURL expresses a sandbox Azure URL with the azureBaseUrl
// variable.
func (b *builder) azurePostmanURL(full string) PostmanURL {
	rest := strings.TrimPrefix(full, b.azure)
	path, query, _ := strings.Cut(rest, "?")
	u := PostmanURL{Raw: "{{azureBaseUrl}}" + rest, Host: []string{"{{azureBaseUrl}}"}}
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

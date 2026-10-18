package endpoints

import (
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const azSub = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/"

// azureFixture is a state with one resource of every Azure type the
// builder knows, as azurerm 5 reports them, on the emulator at base.
func azureFixture(base string) (*model.Workflow, *model.DeployedState, Sandbox) {
	sa := azSub + "Microsoft.Storage/storageAccounts/nimbosa"
	st := &model.DeployedState{WorkflowID: "wfaz", Resources: []model.DeployedResource{
		res("azurerm_resource_group.rg", map[string]any{"name": "rg", "id": "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg"}),
		res("azurerm_storage_account.sa", map[string]any{"name": "nimbosa", "id": sa, "primary_access_key": azureDevKey,
			"primary_blob_endpoint": "http://nimbosa.blob.core.windows.net/"}),
		res("azurerm_storage_container.uploads", map[string]any{"name": "uploads", "storage_account_id": sa}),
		res("azurerm_storage_queue.jobs", map[string]any{"name": "jobs", "storage_account_id": sa}),
		res("azurerm_storage_table.people", map[string]any{"name": "people", "storage_account_name": "nimbosa"}),
		res("azurerm_key_vault.kv", map[string]any{"name": "nimbo-kv", "id": azSub + "Microsoft.KeyVault/vaults/nimbo-kv", "vault_uri": "https://nimbo-kv.vault.azure.net/"}),
		res("azurerm_key_vault_secret.db", map[string]any{"name": "db-password", "key_vault_id": azSub + "Microsoft.KeyVault/vaults/nimbo-kv"}),
		res("azurerm_servicebus_namespace.sb", map[string]any{"name": "nimbobus"}),
		res("azurerm_servicebus_queue.orders", map[string]any{"name": "orders", "namespace_id": azSub + "Microsoft.ServiceBus/namespaces/nimbobus"}),
		res("azurerm_servicebus_topic.events", map[string]any{"name": "events", "namespace_id": azSub + "Microsoft.ServiceBus/namespaces/nimbobus"}),
		res("azurerm_servicebus_subscription.all", map[string]any{"name": "all", "topic_id": azSub + "Microsoft.ServiceBus/namespaces/nimbobus/topics/events"}),
		res("azurerm_eventhub_namespace.eh", map[string]any{"name": "nimbo-eh"}),
		res("azurerm_eventhub.clicks", map[string]any{"name": "clicks", "namespace_id": azSub + "Microsoft.EventHub/namespaces/nimbo-eh", "partition_count": float64(2)}),
		res("azurerm_cosmosdb_account.cosmos", map[string]any{"name": "nimbo-cosmos"}),
		res("azurerm_cosmosdb_sql_database.appdb", map[string]any{"name": "appdb", "account_name": "nimbo-cosmos"}),
		res("azurerm_cosmosdb_sql_container.items", map[string]any{"name": "items", "account_name": "nimbo-cosmos", "database_name": "appdb", "partition_key_paths": []any{"/category"}}),
		res("azurerm_app_configuration.conf", map[string]any{"name": "nimbo-conf", "endpoint": "https://nimbo-conf.azconfig.io"}),
		res("azurerm_service_plan.plan", map[string]any{"name": "plan"}),
		res("azurerm_linux_function_app.fn", map[string]any{"name": "nimbo-fn", "default_hostname": "nimbo-fn.azurewebsites.net"}),
		res("azurerm_function_app_function.hello", map[string]any{"name": "hello", "function_app_id": azSub + "Microsoft.Web/sites/nimbo-fn"}),
		res("azurerm_container_app.web", map[string]any{"name": "web", "ingress": []any{map[string]any{"fqdn": "web.env.azurecontainerapps.io"}}}),
		res("azurerm_api_management.apim", map[string]any{"name": "nimbo-apim", "gateway_url": "https://nimbo-apim.azure-api.net"}),
		res("azurerm_api_management_api.echo", map[string]any{"name": "echo", "api_management_name": "nimbo-apim", "path": "echo"}),
		res("azurerm_api_management_api_operation.get", map[string]any{"api_name": "echo", "api_management_name": "nimbo-apim", "method": "GET", "url_template": "/hello"}),
		res("azurerm_eventgrid_topic.eg", map[string]any{"name": "nimbo-eg", "endpoint": "http://localhost:4577/nimbo-eg-eventgrid/api/events", "primary_access_key": "k1"}),
		res("azurerm_redis_cache.cache", map[string]any{"name": "cache", "hostname": "floci-az-x-redis-1", "port": float64(6379), "primary_access_key": "pw"}),
		res("azurerm_postgresql_flexible_server.pg", map[string]any{"name": "pg", "fqdn": "floci-az-x-pg-pg", "administrator_login": "nimbo", "administrator_password": "pw"}),
		res("azurerm_postgresql_flexible_server_database.appdb", map[string]any{"name": "appdb", "server_id": azSub + "Microsoft.DBforPostgreSQL/flexibleServers/pg"}),
		res("azurerm_mssql_server.sql", map[string]any{"name": "sql", "fully_qualified_domain_name": "sql.database.windows.net"}),
		res("azurerm_role_assignment.ra", map[string]any{"id": "ra"}),
		res("azurerm_virtual_network.vnet", map[string]any{"name": "vnet"}),
		res("azurerm_container_registry.acr", map[string]any{"name": "acr", "login_server": "acr.azurecr.io"}),
	}}
	sb := Sandbox{Bases: map[model.Cloud]string{model.CloudAzure: base}, Running: map[model.Cloud]bool{model.CloudAzure: true}}
	return &model.Workflow{ID: "wfaz", Name: "Azure"}, st, sb
}

func TestAzureBuild(t *testing.T) {
	const azBase = "http://127.0.0.1:45001"
	info := Build(azureFixture(azBase))

	kinds := map[string]string{
		"azurerm_storage_account.sa":            KindBucket,
		"azurerm_storage_container.uploads":     KindBucket,
		"azurerm_storage_queue.jobs":            KindQueue,
		"azurerm_storage_table.people":          KindTable,
		"azurerm_key_vault.kv":                  KindSecret,
		"azurerm_key_vault_secret.db":           KindSecret,
		"azurerm_servicebus_namespace.sb":       KindQueue,
		"azurerm_servicebus_queue.orders":       KindQueue,
		"azurerm_servicebus_topic.events":       KindTopic,
		"azurerm_eventhub.clicks":               KindStream,
		"azurerm_cosmosdb_account.cosmos":       KindDatabase,
		"azurerm_cosmosdb_sql_container.items":  KindTable,
		"azurerm_app_configuration.conf":        KindParameter,
		"azurerm_linux_function_app.fn":         KindWebService,
		"azurerm_container_app.web":             KindWebService,
		"azurerm_api_management.apim":           KindHTTPAPI,
		"azurerm_eventgrid_topic.eg":            KindTopic,
		"azurerm_redis_cache.cache":             KindCache,
		"azurerm_postgresql_flexible_server.pg": KindDatabase,
		"azurerm_mssql_server.sql":              KindDatabase,
		"azurerm_role_assignment.ra":            KindGeneric,
	}
	for addr, kind := range kinds {
		if r := byAddr(t, info, addr); r.Kind != kind {
			t.Errorf("%s kind = %q, want %q", addr, r.Kind, kind)
		}
	}

	sa := byAddr(t, info, "azurerm_storage_account.sa")
	cs := field(sa, "Connection string")
	if !strings.Contains(cs, "BlobEndpoint="+azBase+"/nimbosa;") || !strings.Contains(cs, "QueueEndpoint="+azBase+"/nimbosa-queue;") || !strings.Contains(cs, "TableEndpoint="+azBase+"/nimbosa-table;") {
		t.Errorf("connection string = %q", cs)
	}
	if got := field(byAddr(t, info, "azurerm_storage_container.uploads"), "Container URL"); got != azBase+"/nimbosa/uploads" {
		t.Errorf("container URL = %q", got)
	}
	if got := field(byAddr(t, info, "azurerm_storage_table.people"), "Table URL"); got != azBase+"/nimbosa-table/people" {
		t.Errorf("table URL = %q (storage_account_name form)", got)
	}
	kv := byAddr(t, info, "azurerm_key_vault.kv")
	if got := field(kv, "Vault URL (SDKs)"); got != "https://127.0.0.1:45001/nimbo-kv-keyvault" {
		t.Errorf("vault SDK URL = %q", got)
	}
	if a := hasAction(kv, ActGet); a == nil || a.Default != "db-password" {
		t.Errorf("vault get action = %+v, want default db-password", a)
	}
	if got := field(byAddr(t, info, "azurerm_eventgrid_topic.eg"), "Publish URL"); got != azBase+"/nimbo-eg-eventgrid/api/events" {
		t.Errorf("event grid URL = %q", got)
	}
	fn := byAddr(t, info, "azurerm_linux_function_app.fn")
	if len(fn.Endpoints) != 1 || fn.Endpoints[0].URL != azBase+"/devstoreaccount1-functions/api/nimbo-fn/hello" {
		t.Errorf("function endpoints = %+v", fn.Endpoints)
	}
	apim := byAddr(t, info, "azurerm_api_management.apim")
	if len(apim.Endpoints) != 1 || apim.Endpoints[0].URL != azBase+"/devstoreaccount1-apim/nimbo-apim/echo/hello" || apim.Endpoints[0].Method != "GET" {
		t.Errorf("apim endpoints = %+v", apim.Endpoints)
	}
	ca := byAddr(t, info, "azurerm_container_app.web")
	if ca.Warning == "" || !strings.Contains(ca.Snippets[0].Code, "Host: web.env.azurecontainerapps.io") {
		t.Errorf("container app = %+v", ca)
	}
	cos := byAddr(t, info, "azurerm_cosmosdb_sql_container.items")
	if a := hasAction(cos, ActPut); a == nil || !strings.Contains(a.Default, `"category":"demo"`) {
		t.Errorf("cosmos put default = %+v", a)
	}
	redis := byAddr(t, info, "azurerm_redis_cache.cache")
	if !strings.Contains(redis.Warning, "docker port floci-az-x-redis-1 6379") {
		t.Errorf("redis warning = %q", redis.Warning)
	}
	if !strings.Contains(byAddr(t, info, "azurerm_mssql_server.sql").Warning, "Not reachable") {
		t.Error("mssql without a container should warn it is not reachable")
	}
	pg := byAddr(t, info, "azurerm_postgresql_flexible_server.pg")
	if field(pg, "Databases") != "appdb" {
		t.Errorf("pg databases = %q", field(pg, "Databases"))
	}
	for _, addr := range []string{"azurerm_servicebus_queue.orders", "azurerm_eventhub.clicks"} {
		if byAddr(t, info, addr).Warning != azureNoAMQP {
			t.Errorf("%s should carry the AMQP warning", addr)
		}
	}

	for addr, want := range map[string]bool{
		"azurerm_resource_group.rg": true, "azurerm_role_assignment.ra": true, "azurerm_virtual_network.vnet": true,
		"azurerm_service_plan.plan": true, "azurerm_servicebus_subscription.all": true, "azurerm_cosmosdb_sql_database.appdb": true,
		"azurerm_postgresql_flexible_server_database.appdb": true, "azurerm_function_app_function.hello": true,
		"azurerm_container_registry.acr": false, "azurerm_storage_account.sa": false, "azurerm_key_vault.kv": false,
	} {
		if got := byAddr(t, info, addr).Supporting; got != want {
			t.Errorf("%s supporting = %v, want %v", addr, got, want)
		}
	}

	if len(info.Sandbox) != 1 || info.Sandbox[0].Cloud != model.CloudAzure {
		t.Fatalf("sandbox = %+v", info.Sandbox)
	}
	env := info.Sandbox[0].EnvSnippet
	for _, want := range []string{"/_floci/tls-cert -o floci-az.pem", "NODE_EXTRA_CA_CERTS=", "REQUESTS_CA_BUNDLE=", "AZURE_STORAGE_CONNECTION_STRING=", "ARM_METADATA_HOSTNAME=127.0.0.1:45001"} {
		if !strings.Contains(env, want) {
			t.Errorf("env snippet lacks %q:\n%s", want, env)
		}
	}
}

func TestAzurePostman(t *testing.T) {
	col := Postman(azureFixture("http://127.0.0.1:45001"))
	var names []string
	var services *PostmanItem
	for i, it := range col.Item {
		names = append(names, it.Name)
		if it.Name == "Azure services (sandbox)" {
			services = &col.Item[i]
		}
	}
	if services == nil {
		t.Fatalf("no Azure services folder in %v", names)
	}
	var reqs []string
	for _, it := range services.Item {
		reqs = append(reqs, it.Request.Method+" "+it.Request.URL.Raw)
	}
	joined := strings.Join(reqs, "\n")
	for _, want := range []string{
		"GET {{azureBaseUrl}}/nimbosa?comp=list",
		"PUT {{azureBaseUrl}}/nimbosa/uploads/hello.txt",
		"POST {{azureBaseUrl}}/nimbosa-queue/jobs/messages",
		"GET {{azureBaseUrl}}/nimbo-kv-keyvault/secrets?api-version=7.4",
		"POST {{azureBaseUrl}}/nimbo-cosmos-cosmos/dbs/appdb/colls/items/docs",
		"PUT {{azureBaseUrl}}/nimbo-conf-appconfig/kv/greeting?api-version=1.0",
		"POST {{azureBaseUrl}}/nimbo-eg-eventgrid/api/events",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	found := false
	for _, v := range col.Variable {
		found = found || (v.Key == "azureBaseUrl" && v.Value == "http://127.0.0.1:45001")
	}
	if !found {
		t.Errorf("variables = %+v", col.Variable)
	}
}

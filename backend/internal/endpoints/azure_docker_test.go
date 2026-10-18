//go:build docker

package endpoints

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// startFlociAz runs a throwaway floci-az (TLS on, as in the sandbox) and
// returns its host URL.
func startFlociAz(t *testing.T) string {
	t.Helper()
	var b [3]byte
	_, _ = rand.Read(b[:])
	name := "nimbo-test-floci-az-" + hex.EncodeToString(b[:])
	if out, err := exec.Command("docker", "run", "-d", "--name", name, "-e", "FLOCI_AZ_TLS_ENABLED=true", "-p", "127.0.0.1::4577", "floci/floci-az:latest").CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "rm", "-f", "-v", name).CombinedOutput(); err != nil {
			t.Errorf("docker rm: %v: %s", err, out)
		}
	})
	out, err := exec.Command("docker", "port", name, "4577/tcp").Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	base := "http://" + strings.TrimSpace(strings.Split(string(out), "\n")[0])
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/ready"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("floci-az did not become ready")
	return ""
}

func azCall(t *testing.T, method, u, contentType, body string) {
	t.Helper()
	req, err := http.NewRequest(method, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s: %s %s", method, u, resp.Status, data)
	}
}

// TestAzureTryLive runs every Try action of the Azure fixture against a
// real floci-az.
func TestAzureTryLive(t *testing.T) {
	base := startFlociAz(t)
	arm := base + "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/"
	const j = "application/json"
	// What Terraform would have created (floci-az 0.15 can't create most of
	// these through ARM; the data planes are what Try uses).
	azCall(t, http.MethodPut, base+"/nimbosa/uploads?restype=container", "", "")
	azCall(t, http.MethodPut, base+"/nimbosa-queue/jobs", "", "")
	azCall(t, http.MethodPost, base+"/nimbosa-table/Tables", j, `{"TableName":"people"}`)
	azCall(t, http.MethodPut, base+"/nimbobus-servicebus/orders", "", "")
	azCall(t, http.MethodPut, base+"/nimbobus-servicebus/events", "application/atom+xml",
		`<entry xmlns="http://www.w3.org/2005/Atom"><content type="application/xml"><TopicDescription xmlns="http://schemas.microsoft.com/netservices/2010/10/servicebus/connect"/></content></entry>`)
	azCall(t, http.MethodPost, base+"/nimbo-cosmos-cosmos/dbs", j, `{"id":"appdb"}`)
	azCall(t, http.MethodPost, base+"/nimbo-cosmos-cosmos/dbs/appdb/colls", j, `{"id":"items","partitionKey":{"paths":["/category"],"kind":"Hash"}}`)
	azCall(t, http.MethodPut, arm+"Microsoft.EventGrid/topics/nimbo-eg?api-version=2022-06-15", j, `{"location":"eastus","properties":{}}`)
	azCall(t, http.MethodPut, arm+"Microsoft.Web/sites/nimbo-fn?api-version=2022-03-01", j, `{"location":"eastus","kind":"functionapp,linux","properties":{"siteConfig":{"linuxFxVersion":"Node|20"}}}`)
	azCall(t, http.MethodPut, arm+"Microsoft.ApiManagement/service/nimbo-apim?api-version=2022-08-01", j, `{"location":"eastus","sku":{"name":"Developer","capacity":1},"properties":{"publisherEmail":"a@b.c","publisherName":"x"}}`)
	azCall(t, http.MethodPut, arm+"Microsoft.ApiManagement/service/nimbo-apim/apis/echo?api-version=2022-08-01", j, `{"properties":{"displayName":"echo","path":"echo","protocols":["https"],"subscriptionRequired":false}}`)
	azCall(t, http.MethodPut, arm+"Microsoft.ApiManagement/service/nimbo-apim/apis/echo/operations/get?api-version=2022-08-01", j, `{"properties":{"displayName":"get","method":"GET","urlTemplate":"/hello"}}`)
	azCall(t, http.MethodPut, arm+"Microsoft.ApiManagement/service/nimbo-apim/apis/echo/operations/get/policies/policy?api-version=2022-08-01", j,
		`{"properties":{"format":"xml","value":"<policies><inbound><return-response><set-status code=\"200\"/><set-body>hi from apim</set-body></return-response></inbound></policies>"}}`)
	azCall(t, http.MethodPut, arm+"Microsoft.App/managedEnvironments/env?api-version=2025-07-01", j, `{"location":"eastus","properties":{}}`)
	azCall(t, http.MethodPut, arm+"Microsoft.App/containerApps/web?api-version=2025-07-01", j,
		`{"location":"eastus","properties":{"environmentId":"/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.App/managedEnvironments/env","configuration":{"ingress":{"external":true,"targetPort":80}},"template":{"containers":[{"name":"web","image":"nginx:alpine"}]}}}`)

	wf, st, sb := azureFixture(base)
	// The container app's FQDN is generated by the emulator.
	resp, err := http.Get(arm + "Microsoft.App/containerApps/web?api-version=2025-07-01")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fqdn := between(string(data), `"fqdn":"`, `"`)
	if fqdn == "" {
		t.Fatalf("no fqdn in %s", data)
	}
	for i := range st.Resources {
		if st.Resources[i].Type == "azurerm_container_app" {
			st.Resources[i].Values["ingress"] = []any{map[string]any{"fqdn": fqdn}}
		}
	}

	info := Build(wf, st, sb)
	ctx := context.Background()
	// Terraform would have stored the vault's secret.
	if res, err := Try(ctx, wf, st, sb, model.TryRequest{NodeID: "azurerm_key_vault_secret.db", Action: ActPut, Input: "s3cret"}); err != nil || !res.OK {
		t.Fatalf("seed secret: %+v, %v", res, err)
	}
	ran := 0
	for _, r := range info.Resources {
		if r.Address == "azurerm_linux_function_app.fn" {
			// Invoking needs deployed code (a runtime container); listing is enough.
			r.Actions = slices.DeleteFunc(r.Actions, func(a model.TryAction) bool { return a.ID == ActRequest })
		}
		// Writes first so reads find something.
		acts := slices.Clone(r.Actions)
		slices.SortStableFunc(acts, func(a, b model.TryAction) int {
			switch {
			case a.Writes && !b.Writes:
				return -1
			case b.Writes && !a.Writes:
				return 1
			}
			return 0
		})
		for _, a := range acts {
			req := model.TryRequest{NodeID: r.Address, Action: a.ID}
			if a.Input == "http" {
				req.Method = http.MethodGet
			}
			res, err := Try(ctx, wf, st, sb, req)
			if err != nil {
				t.Errorf("%s %s: %v", r.Address, a.ID, err)
				continue
			}
			ran++
			t.Logf("%s %s → %s %s", r.Address, a.ID, res.Status, strings.Join(strings.Fields(res.Body), " "))
			switch r.Address {
			case "azurerm_container_app.web":
				// Mocked mode: the Host header reached the app's ingress.
				if !strings.Contains(res.Body, "ContainerAppMocked") {
					t.Errorf("container app: %+v", res)
				}
				continue
			}
			if !res.OK {
				t.Errorf("%s %s: %+v", r.Address, a.ID, res)
			}
		}
	}
	if ran < 26 {
		t.Errorf("only %d actions ran", ran)
	}

	// Results carry what was written.
	check := func(addr, action, input, want string) {
		t.Helper()
		res, err := Try(ctx, wf, st, sb, model.TryRequest{NodeID: addr, Action: action, Input: input})
		if err != nil || !res.OK || !strings.Contains(res.Body, want) {
			t.Errorf("%s %s = %+v, %v; want body with %q", addr, action, res, err, want)
		}
	}
	check("azurerm_storage_container.uploads", ActGet, "hello.txt", "Hello from Nimbo")
	check("azurerm_storage_container.uploads", ActList, "", `"hello.txt"`)
	check("azurerm_storage_queue.jobs", ActReceive, "", `"text": "Hello from Nimbo"`)
	check("azurerm_storage_table.people", ActScan, "", `"message": "Hello from Nimbo"`)
	check("azurerm_storage_account.sa", ActList, "", `"uploads"`)
	check("azurerm_key_vault.kv", ActGet, "greeting", `"value": "Hello from Nimbo"`)
	check("azurerm_key_vault.kv", ActList, "", `"db-password"`)
	check("azurerm_key_vault_secret.db", ActGet, "", `"value": "Hello from Nimbo"`)
	check("azurerm_servicebus_namespace.sb", ActList, "", `"orders"`)
	check("azurerm_cosmosdb_sql_container.items", ActScan, "SELECT * FROM c WHERE c.category = 'demo'", `"Hello from Nimbo"`)
	check("azurerm_cosmosdb_account.cosmos", ActList, "", `"appdb"`)
	check("azurerm_app_configuration.conf", ActGet, "greeting", `"Hello from Nimbo"`)
	check("azurerm_linux_function_app.fn", ActList, "", "")
	res, err := Try(ctx, wf, st, sb, model.TryRequest{NodeID: "azurerm_api_management.apim", Action: ActRequest, Method: "GET", Path: "echo/hello"})
	if err != nil || !res.OK || res.Body != "hi from apim" {
		t.Errorf("apim relative path = %+v, %v", res, err)
	}
	// https:// sandbox URLs trust the emulator's self-signed certificate.
	httpsURL := strings.Replace(base, "http://", "https://", 1) + "/devstoreaccount1-apim/nimbo-apim/echo/hello"
	res, err = Try(ctx, wf, st, sb, model.TryRequest{NodeID: "azurerm_api_management.apim", Action: ActRequest, Method: "GET", Path: httpsURL})
	if err != nil || !res.OK || res.Body != "hi from apim" {
		t.Errorf("apim over https = %+v, %v", res, err)
	}
}

func between(s, start, end string) string {
	_, rest, ok := strings.Cut(s, start)
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, end)
	return v
}

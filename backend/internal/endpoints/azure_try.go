package endpoints

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// tryAzure runs a non-HTTP action on an Azure resource through the
// emulator's REST data planes (plain HTTP on the sandbox port).
func (b *builder) tryAzure(ctx context.Context, e *entry, action, input string) (model.TryResult, error) {
	t := e.t
	kvAuth := map[string]string{"Authorization": "Bearer " + azureFakeToken}
	switch e.Type + "/" + action {
	// Storage
	case "azurerm_storage_account/" + ActList:
		res := doHTTP(ctx, http.MethodGet, t.url+"?comp=list", nil, "")
		return azXMLList(res, "containers", func(d *xml.Decoder) ([]any, error) {
			var v struct {
				Names []string `xml:"Containers>Container>Name"`
			}
			err := d.Decode(&v)
			return anySlice(v.Names), err
		}), nil
	case "azurerm_storage_container/" + ActList:
		res := doHTTP(ctx, http.MethodGet, t.url+"?restype=container&comp=list", nil, "")
		return azXMLList(res, "files", func(d *xml.Decoder) ([]any, error) {
			var v struct {
				Blobs []struct {
					Name string `xml:"Name"`
					Size int64  `xml:"Properties>Content-Length"`
					Type string `xml:"Properties>Content-Type"`
				} `xml:"Blobs>Blob"`
			}
			err := d.Decode(&v)
			out := make([]any, len(v.Blobs))
			for i, bl := range v.Blobs {
				out[i] = map[string]any{"name": bl.Name, "size": bl.Size, "contentType": bl.Type}
			}
			return out, err
		}), nil
	case "azurerm_storage_container/" + ActPut:
		var in struct {
			Key  string `json:"key"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal([]byte(input), &in); err != nil || in.Key == "" {
			return model.TryResult{Error: `Input must be {"key": "<file name>", "body": "<text>"}.`}, nil
		}
		return doHTTP(ctx, http.MethodPut, t.url+"/"+escapePath(in.Key),
			map[string]string{"x-ms-blob-type": "BlockBlob", "Content-Type": "text/plain; charset=utf-8"}, in.Body), nil
	case "azurerm_storage_container/" + ActGet:
		return doHTTP(ctx, http.MethodGet, t.url+"/"+escapePath(strings.TrimSpace(input)), nil, ""), nil
	case "azurerm_storage_queue/" + ActSend:
		var msg strings.Builder
		_ = xml.EscapeText(&msg, []byte(input))
		return doHTTP(ctx, http.MethodPost, t.url+"/messages", map[string]string{"Content-Type": "application/xml"},
			"<QueueMessage><MessageText>"+msg.String()+"</MessageText></QueueMessage>"), nil
	case "azurerm_storage_queue/" + ActReceive:
		res := doHTTP(ctx, http.MethodGet, t.url+"/messages?peekonly=true&numofmessages=10", nil, "")
		return azXMLList(res, "messages", func(d *xml.Decoder) ([]any, error) {
			var v struct {
				Messages []struct {
					ID       string `xml:"MessageId"`
					Inserted string `xml:"InsertionTime"`
					Text     string `xml:"MessageText"`
				} `xml:"QueueMessage"`
			}
			err := d.Decode(&v)
			out := make([]any, len(v.Messages))
			for i, m := range v.Messages {
				out[i] = map[string]any{"id": m.ID, "inserted": m.Inserted, "text": m.Text}
			}
			return out, err
		}), nil
	case "azurerm_storage_table/" + ActScan:
		res := doHTTP(ctx, http.MethodGet, t.url+"()?$top=25", map[string]string{"Accept": "application/json;odata=nometadata"}, "")
		return azJSONField(res, "value"), nil
	case "azurerm_storage_table/" + ActPut:
		var ent map[string]any
		if err := json.Unmarshal([]byte(input), &ent); err != nil {
			return model.TryResult{Error: "Input must be a JSON object."}, nil
		}
		pk, _ := ent["PartitionKey"].(string)
		rk, _ := ent["RowKey"].(string)
		if pk == "" || rk == "" {
			return model.TryResult{Error: "The entity needs string PartitionKey and RowKey properties."}, nil
		}
		key := fmt.Sprintf("(PartitionKey='%s',RowKey='%s')", url.PathEscape(strings.ReplaceAll(pk, "'", "''")), url.PathEscape(strings.ReplaceAll(rk, "'", "''")))
		res := doHTTP(ctx, http.MethodPut, t.url+key, map[string]string{"Content-Type": "application/json", "Accept": "application/json;odata=nometadata"}, input)
		if res.OK && res.Body == "" {
			res.Body, res.ContentType = prettyJSON([]byte(input)), "application/json"
		}
		return res, nil

	// Key Vault
	case "azurerm_key_vault/" + ActList:
		res := doHTTP(ctx, http.MethodGet, t.url+"/secrets?"+azureKVVersion, kvAuth, "")
		if !res.OK {
			return res, nil
		}
		var v struct {
			Value []struct {
				ID string `json:"id"`
			} `json:"value"`
		}
		if json.Unmarshal([]byte(res.Body), &v) == nil {
			names := make([]string, len(v.Value))
			for i, s := range v.Value {
				names[i] = s.ID[strings.LastIndex(s.ID, "/")+1:]
			}
			out := jsonResult(res.Status, map[string]any{"secrets": names})
			return out, nil
		}
		return res, nil
	case "azurerm_key_vault/" + ActGet:
		return azSecretValue(doHTTP(ctx, http.MethodGet, t.url+"/secrets/"+escapePath(strings.TrimSpace(input))+"?"+azureKVVersion, kvAuth, "")), nil
	case "azurerm_key_vault/" + ActPut:
		var in struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal([]byte(input), &in); err != nil || in.Name == "" {
			return model.TryResult{Error: `Input must be {"name": "<secret name>", "value": "<text>"}.`}, nil
		}
		return doHTTP(ctx, http.MethodPut, t.url+"/secrets/"+escapePath(in.Name)+"?"+azureKVVersion, withJSON(kvAuth), mustJSON(map[string]string{"value": in.Value})), nil
	case "azurerm_key_vault_secret/" + ActGet:
		return azSecretValue(doHTTP(ctx, http.MethodGet, t.url+"/secrets/"+escapePath(t.name)+"?"+azureKVVersion, kvAuth, "")), nil
	case "azurerm_key_vault_secret/" + ActPut:
		return doHTTP(ctx, http.MethodPut, t.url+"/secrets/"+escapePath(t.name)+"?"+azureKVVersion, withJSON(kvAuth), mustJSON(map[string]string{"value": input})), nil

	// Service Bus (management plane)
	case "azurerm_servicebus_namespace/" + ActList:
		out := map[string]any{}
		for _, kind := range []string{"queues", "topics"} {
			res := doHTTP(ctx, http.MethodGet, t.url+"/$Resources/"+kind, nil, "")
			if !res.OK {
				return res, nil
			}
			var feed struct {
				Entries []string `xml:"entry>title"`
			}
			if err := xml.Unmarshal([]byte(res.Body), &feed); err != nil {
				return res, nil
			}
			out[kind] = anySlice(feed.Entries)
		}
		return jsonResult("200 OK", out), nil
	case "azurerm_servicebus_queue/" + ActGet, "azurerm_servicebus_topic/" + ActGet:
		return doHTTP(ctx, http.MethodGet, t.url, nil, ""), nil

	// Cosmos DB
	case "azurerm_cosmosdb_account/" + ActList:
		res := doHTTP(ctx, http.MethodGet, t.url+"/dbs", nil, "")
		if !res.OK {
			return res, nil
		}
		var v struct {
			Databases []struct {
				ID string `json:"id"`
			} `json:"Databases"`
		}
		if json.Unmarshal([]byte(res.Body), &v) == nil {
			ids := make([]string, len(v.Databases))
			for i, d := range v.Databases {
				ids[i] = d.ID
			}
			return jsonResult(res.Status, map[string]any{"databases": ids}), nil
		}
		return res, nil
	case "azurerm_cosmosdb_sql_container/" + ActScan:
		q := mustJSON(map[string]any{"query": strings.TrimSpace(input), "parameters": []any{}})
		res := doHTTP(ctx, http.MethodPost, t.url+"/docs", map[string]string{
			"Content-Type": "application/query+json", "x-ms-documentdb-isquery": "true",
			"x-ms-documentdb-query-enablecrosspartition": "true", "x-ms-max-item-count": "25",
		}, q)
		return azJSONField(res, "Documents"), nil
	case "azurerm_cosmosdb_sql_container/" + ActPut:
		var doc map[string]any
		if err := json.Unmarshal([]byte(input), &doc); err != nil {
			return model.TryResult{Error: "Input must be a JSON object."}, nil
		}
		if id, _ := doc["id"].(string); id == "" {
			return model.TryResult{Error: `The document needs a string "id".`}, nil
		}
		pk, ok := lookupPath(doc, t.name)
		if !ok {
			return model.TryResult{Error: "The document needs the partition key property " + t.name + "."}, nil
		}
		return doHTTP(ctx, http.MethodPost, t.url+"/docs", map[string]string{
			"Content-Type": "application/json", "x-ms-documentdb-is-upsert": "true",
			"x-ms-documentdb-partitionkey": mustJSON([]any{pk}),
		}, input), nil

	// App Configuration
	case "azurerm_app_configuration/" + ActList:
		res := doHTTP(ctx, http.MethodGet, t.url+"/kv?"+azureAppConfigVersion, nil, "")
		if !res.OK {
			return res, nil
		}
		var v struct {
			Items []struct {
				Key   string  `json:"key"`
				Label *string `json:"label"`
				Value string  `json:"value"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(res.Body), &v) == nil {
			out := make([]any, len(v.Items))
			for i, it := range v.Items {
				m := map[string]any{"key": it.Key, "value": it.Value}
				if it.Label != nil {
					m["label"] = *it.Label
				}
				out[i] = m
			}
			return jsonResult(res.Status, map[string]any{"settings": out}), nil
		}
		return res, nil
	case "azurerm_app_configuration/" + ActGet:
		return doHTTP(ctx, http.MethodGet, t.url+"/kv/"+escapePath(strings.TrimSpace(input))+"?"+azureAppConfigVersion, nil, ""), nil
	case "azurerm_app_configuration/" + ActPut:
		var in struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal([]byte(input), &in); err != nil || in.Key == "" {
			return model.TryResult{Error: `Input must be {"key": "<setting key>", "value": "<text>"}.`}, nil
		}
		return doHTTP(ctx, http.MethodPut, t.url+"/kv/"+escapePath(in.Key)+"?"+azureAppConfigVersion,
			map[string]string{"Content-Type": "application/vnd.microsoft.appconfig.kv+json"}, mustJSON(map[string]string{"value": in.Value})), nil

	// Event Grid
	case "azurerm_eventgrid_topic/" + ActPublish:
		var data any
		if err := json.Unmarshal([]byte(input), &data); err != nil {
			return model.TryResult{Error: "Input must be valid JSON."}, nil
		}
		ev := []map[string]any{{
			"id": fmt.Sprintf("nimbo-%d", time.Now().UnixNano()), "subject": "nimbo/try", "eventType": "Nimbo.Try",
			"eventTime": time.Now().UTC().Format(time.RFC3339), "dataVersion": "1.0", "data": data,
		}}
		return doHTTP(ctx, http.MethodPost, t.url, map[string]string{"Content-Type": "application/json", "aeg-sas-key": "nimbo"}, mustJSON(ev)), nil
	}
	if e.Kind == KindWebService && action == ActList && t.url != "" {
		// Function Apps: the emulator's admin API lists deployed functions.
		return doHTTP(ctx, http.MethodGet, t.url, nil, ""), nil
	}
	return model.TryResult{}, badRequest("action %q is not available for %s", action, e.Kind)
}

// tryAzureHTTP runs an HTTP action on an Azure resource: Container Apps get
// their Host header, and https:// sandbox URLs trust the sandbox's
// self-signed certificate (fetched from the emulator on each call).
func (b *builder) tryAzureHTTP(ctx context.Context, e *entry, req model.TryRequest) model.TryResult {
	headers := req.Headers
	if e.Type == "azurerm_container_app" && e.t.url != "" && !headerSet(headers, "Host") {
		headers = maps.Clone(headers)
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Host"] = e.t.url
	}
	client := tryClient
	if strings.HasPrefix(req.Path, "https://") {
		c, err := b.azureTLSClient(ctx, req.Path)
		if err != nil {
			return model.TryResult{Error: "Couldn't load the sandbox certificate: " + err.Error()}
		}
		defer c.CloseIdleConnections()
		client = c
	}
	return doHTTPWith(ctx, client, req.Method, req.Path, headers, req.Input)
}

// azureTLSClient is tryClient trusting the certificate served by the
// emulator answering target (over plain HTTP on the same port).
func (b *builder) azureTLSClient(ctx context.Context, target string) (*http.Client, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+u.Host+"/_floci/tls-cert", nil)
	if err != nil {
		return nil, err
	}
	resp, err := tryClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	pem, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s from /_floci/tls-cert (is TLS enabled?)", resp.Status)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificate in /_floci/tls-cert")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig.RootCAs = pool
	c := *tryClient
	c.Transport = tr
	return &c, nil
}

func headerSet(h map[string]string, name string) bool {
	for k := range h {
		if strings.EqualFold(strings.TrimSpace(k), name) {
			return true
		}
	}
	return false
}

func withJSON(h map[string]string) map[string]string {
	out := maps.Clone(h)
	out["Content-Type"] = "application/json"
	return out
}

// escapePath escapes each segment of a blob or key name, keeping slashes.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

func anySlice[T any](s []T) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// azXMLList turns a successful XML listing into {"<key>": [...]} JSON.
func azXMLList(res model.TryResult, key string, parse func(*xml.Decoder) ([]any, error)) model.TryResult {
	if !res.OK {
		return res
	}
	items, err := parse(xml.NewDecoder(strings.NewReader(res.Body)))
	if err != nil {
		return res
	}
	return jsonResult(res.Status, map[string]any{key: items})
}

// azJSONField keeps only field of a successful JSON response.
func azJSONField(res model.TryResult, field string) model.TryResult {
	if !res.OK {
		return res
	}
	var v map[string]json.RawMessage
	if json.Unmarshal([]byte(res.Body), &v) != nil || v[field] == nil {
		return res
	}
	res.Body = prettyJSON(v[field])
	res.ContentType = "application/json"
	return res
}

// azSecretValue shows a Key Vault secret's value and version.
func azSecretValue(res model.TryResult) model.TryResult {
	if !res.OK {
		return res
	}
	var v struct {
		Value string `json:"value"`
		ID    string `json:"id"`
	}
	if json.Unmarshal([]byte(res.Body), &v) != nil {
		return res
	}
	return jsonResult(res.Status, map[string]any{"value": v.Value, "version": v.ID[strings.LastIndex(v.ID, "/")+1:]})
}

// lookupPath reads a partition key path ("/a/b") from doc.
func lookupPath(doc map[string]any, path string) (any, bool) {
	var cur any = doc
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}

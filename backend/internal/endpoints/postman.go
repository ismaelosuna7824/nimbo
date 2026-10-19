package endpoints

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// PostmanSchema is the Postman Collection v2.1 schema URL.
const PostmanSchema = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"

// Postman Collection v2.1 documents (the subset Nimbo writes).
type (
	PostmanCollection struct {
		Info     PostmanInfo       `json:"info"`
		Item     []PostmanItem     `json:"item"`
		Variable []PostmanVariable `json:"variable"`
	}
	PostmanInfo struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Schema      string `json:"schema"`
	}
	PostmanVariable struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Type  string `json:"type,omitempty"`
	}
	// PostmanItem is a folder (Item set) or a request (Request set).
	PostmanItem struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Item        []PostmanItem   `json:"item,omitempty"`
		Request     *PostmanRequest `json:"request,omitempty"`
	}
	PostmanRequest struct {
		Method      string          `json:"method"`
		Header      []PostmanHeader `json:"header"`
		URL         PostmanURL      `json:"url"`
		Body        *PostmanBody    `json:"body,omitempty"`
		Auth        *PostmanAuth    `json:"auth,omitempty"`
		Description string          `json:"description,omitempty"`
	}
	PostmanHeader struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	PostmanURL struct {
		Raw      string         `json:"raw"`
		Protocol string         `json:"protocol,omitempty"`
		Host     []string       `json:"host"`
		Port     string         `json:"port,omitempty"`
		Path     []string       `json:"path,omitempty"`
		Query    []PostmanQuery `json:"query,omitempty"`
	}
	PostmanQuery struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	PostmanBody struct {
		Mode       string              `json:"mode"`
		Raw        string              `json:"raw,omitempty"`
		URLEncoded []PostmanQuery      `json:"urlencoded,omitempty"`
		Options    *PostmanBodyOptions `json:"options,omitempty"`
	}
	PostmanBodyOptions struct {
		Raw struct {
			Language string `json:"language"`
		} `json:"raw"`
	}
	PostmanAuth struct {
		Type  string            `json:"type"`
		AWSv4 []PostmanVariable `json:"awsv4"`
	}
)

// Postman builds a collection for the deployed resources: a folder per
// HTTP API and function URL with a request per route, an "AWS services
// (sandbox)" folder of SigV4-signed requests for queues, topics, tables,
// buckets and functions, and folders of other clouds' REST calls. AWS URLs
// use the baseUrl variable (the AWS emulator); other clouds add their own.
func Postman(wf *model.Workflow, st *model.DeployedState, sb Sandbox) PostmanCollection {
	b := newBuilder(wf, st, sb)
	name := b.wfID
	if wf != nil && wf.Name != "" {
		name = wf.Name
	}
	col := PostmanCollection{
		Info: PostmanInfo{
			Name:        name + " (Nimbo sandbox)",
			Description: "Requests for what is deployed in the local Nimbo sandbox of \"" + name + "\". baseUrl is the sandbox's AWS address; it stays the same across sandbox restarts.",
			Schema:      PostmanSchema,
		},
		Item:     []PostmanItem{},
		Variable: []PostmanVariable{{Key: "baseUrl", Value: b.aws, Type: "string"}},
	}
	if b.aws != "" {
		b.awsPostman(&col)
	}
	b.instancePostman(&col)
	b.gcpPostman(&col)
	b.azurePostman(&col)
	b.ociPostman(&col)
	return col
}

// awsPostman adds a folder per HTTP API and function URL and an "AWS
// services (sandbox)" folder of SigV4-signed requests.
func (b *builder) awsPostman(col *PostmanCollection) {
	var services []PostmanItem
	for _, e := range b.entries {
		if e.t.cloud != model.CloudAWS {
			continue
		}
		switch e.Kind {
		case KindHTTPAPI, KindFunctionURL:
			folder := PostmanItem{Name: e.Label, Description: e.Summary}
			for _, ep := range e.Endpoints {
				methods := []string{ep.Method}
				if ep.Method == "ANY" {
					methods = []string{http.MethodGet, http.MethodPost}
				}
				for _, m := range methods {
					req := &PostmanRequest{Method: m, Header: []PostmanHeader{}, URL: b.postmanURL(ep.URL), Description: ep.Description}
					if m != http.MethodGet && m != http.MethodDelete && m != http.MethodHead {
						req.Header = append(req.Header, PostmanHeader{"Content-Type", "application/json"})
						req.Body = rawJSON(sampleJSONBody)
					}
					folder.Item = append(folder.Item, PostmanItem{Name: m + " " + ep.Path, Request: req})
				}
			}
			if len(folder.Item) > 0 {
				col.Item = append(col.Item, folder)
			}
		case KindQueue:
			services = append(services,
				b.awsJSON(e, "sqs", "SQS SendMessage · "+e.Label, "AmazonSQS.SendMessage", "1.0",
					mustJSON(map[string]any{"QueueUrl": e.t.url, "MessageBody": helloMessage})),
				b.awsJSON(e, "sqs", "SQS ReceiveMessage · "+e.Label, "AmazonSQS.ReceiveMessage", "1.0",
					mustJSON(map[string]any{"QueueUrl": e.t.url, "MaxNumberOfMessages": 10, "VisibilityTimeout": 0})))
		case KindTopic:
			services = append(services, PostmanItem{Name: "SNS Publish · " + e.Label, Request: &PostmanRequest{
				Method: http.MethodPost,
				Header: []PostmanHeader{{"Content-Type", "application/x-www-form-urlencoded"}},
				URL:    b.postmanURL(b.aws + "/"),
				Body: &PostmanBody{Mode: "urlencoded", URLEncoded: []PostmanQuery{
					{"Action", "Publish"}, {"Version", "2010-03-31"}, {"TopicArn", e.t.arn}, {"Message", helloMessage},
				}},
				Auth: awsv4(e.t.region, "sns"),
			}})
		case KindTable:
			services = append(services,
				b.awsJSON(e, "dynamodb", "DynamoDB Scan · "+e.Label, "DynamoDB_20120810.Scan", "1.0",
					mustJSON(map[string]any{"TableName": e.t.name, "Limit": 25})),
				b.awsJSON(e, "dynamodb", "DynamoDB PutItem · "+e.Label, "DynamoDB_20120810.PutItem", "1.0",
					`{"TableName":`+jsStr(e.t.name)+`,"Item":`+e.t.item+`}`))
		case KindBucket:
			u := b.postmanURL(b.aws + "/" + e.t.name)
			u.Raw += "?list-type=2&max-keys=50"
			u.Query = []PostmanQuery{{"list-type", "2"}, {"max-keys", "50"}}
			services = append(services, PostmanItem{Name: "S3 ListObjectsV2 · " + e.Label, Request: &PostmanRequest{
				Method: http.MethodGet, Header: []PostmanHeader{}, URL: u, Auth: awsv4(e.t.region, "s3"),
			}})
		case KindFunction:
			services = append(services, PostmanItem{Name: "Lambda Invoke · " + e.Label, Request: &PostmanRequest{
				Method: http.MethodPost,
				Header: []PostmanHeader{{"Content-Type", "application/json"}},
				URL:    b.postmanURL(b.aws + "/2015-03-31/functions/" + url.PathEscape(e.t.name) + "/invocations"),
				Body:   rawJSON("{}"),
				Auth:   awsv4(e.t.region, "lambda"),
			}})
		}
	}
	if len(services) > 0 {
		col.Item = append(col.Item, PostmanItem{
			Name:        "AWS services (sandbox)",
			Description: "AWS API calls signed with the sandbox's test credentials (AWS Signature v4: access key test, secret key test).",
			Item:        services,
		})
	}
}

// postmanURL expresses a sandbox AWS URL with the baseUrl variable.
func (b *builder) postmanURL(full string) PostmanURL {
	rest := strings.TrimPrefix(full, b.aws)
	path, query, _ := strings.Cut(rest, "?")
	u := PostmanURL{Raw: "{{baseUrl}}" + rest, Host: []string{"{{baseUrl}}"}}
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

func (b *builder) awsJSON(e entry, service, name, target, version, body string) PostmanItem {
	return PostmanItem{Name: name, Request: &PostmanRequest{
		Method: http.MethodPost,
		Header: []PostmanHeader{{"Content-Type", "application/x-amz-json-" + version}, {"X-Amz-Target", target}},
		URL:    b.postmanURL(b.aws + "/"),
		Body:   rawJSON(prettyJSON([]byte(body))),
		Auth:   awsv4(e.t.region, service),
	}}
}

func awsv4(region, service string) *PostmanAuth {
	return &PostmanAuth{Type: "awsv4", AWSv4: []PostmanVariable{
		{Key: "accessKey", Value: "test", Type: "string"},
		{Key: "secretKey", Value: "test", Type: "string"},
		{Key: "region", Value: region, Type: "string"},
		{Key: "service", Value: service, Type: "string"},
	}}
}

func rawJSON(s string) *PostmanBody {
	body := &PostmanBody{Mode: "raw", Raw: s, Options: &PostmanBodyOptions{}}
	body.Options.Raw.Language = "json"
	return body
}

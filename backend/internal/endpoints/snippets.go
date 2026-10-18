package endpoints

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// shq quotes s for POSIX shells.
func shq(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// jsStr is a double-quoted string literal valid in JavaScript and Python.
func jsStr(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSpace(buf.String())
}

// compactJSON re-encodes a JSON document on one line; invalid input is
// returned unchanged.
func compactJSON(s string) string {
	var buf bytes.Buffer
	if json.Compact(&buf, []byte(s)) != nil {
		return s
	}
	return buf.String()
}

// jsLit renders a JSON document as a JavaScript expression.
func jsLit(doc string) string {
	var v any
	if json.Unmarshal([]byte(doc), &v) != nil {
		return jsStr(doc)
	}
	return renderLit(v, "js")
}

// pyLit renders a JSON document as a Python expression.
func pyLit(doc string) string {
	var v any
	if json.Unmarshal([]byte(doc), &v) != nil {
		return jsStr(doc)
	}
	return renderLit(v, "py")
}

func renderLit(v any, lang string) string {
	switch x := v.(type) {
	case nil:
		if lang == "py" {
			return "None"
		}
		return "null"
	case bool:
		if lang == "py" {
			if x {
				return "True"
			}
			return "False"
		}
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return jsStr(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = renderLit(e, lang)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			key := jsStr(k)
			if lang == "js" && isIdent(k) {
				key = k
			}
			parts[i] = key + ": " + renderLit(x[k], lang)
		}
		if lang == "py" {
			return "{" + strings.Join(parts, ", ") + "}"
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	return "null"
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// AWS SDK and CLI snippets

// awsCLI renders AWS CLI commands preceded by the sandbox credentials. The
// endpoint is passed explicitly so CLI v1 works too.
func awsCLI(base, region string, cmds ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=%s\n", region)
	for _, c := range cmds {
		b.WriteString("aws --endpoint-url " + base + " " + c + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// sdkClient describes one AWS SDK service client for snippets.
type sdkClient struct {
	jsPkg    string // @aws-sdk/client-sqs
	jsClient string // SQSClient
	pyName   string // sqs
	s3       bool   // needs path-style addressing
}

var (
	sdkSQS     = sdkClient{"@aws-sdk/client-sqs", "SQSClient", "sqs", false}
	sdkSNS     = sdkClient{"@aws-sdk/client-sns", "SNSClient", "sns", false}
	sdkS3      = sdkClient{"@aws-sdk/client-s3", "S3Client", "s3", true}
	sdkDynamo  = sdkClient{"@aws-sdk/client-dynamodb", "DynamoDBClient", "dynamodb", false}
	sdkLambda  = sdkClient{"@aws-sdk/client-lambda", "LambdaClient", "lambda", false}
	sdkKinesis = sdkClient{"@aws-sdk/client-kinesis", "KinesisClient", "kinesis", false}
	sdkEvents  = sdkClient{"@aws-sdk/client-eventbridge", "EventBridgeClient", "events", false}
	sdkSFN     = sdkClient{"@aws-sdk/client-sfn", "SFNClient", "stepfunctions", false}
	sdkSecrets = sdkClient{"@aws-sdk/client-secrets-manager", "SecretsManagerClient", "secretsmanager", false}
	sdkSSM     = sdkClient{"@aws-sdk/client-ssm", "SSMClient", "ssm", false}
	sdkCognito = sdkClient{"@aws-sdk/client-cognito-identity-provider", "CognitoIdentityProviderClient", "cognito-idp", false}
)

// jsSDK renders an ES module using AWS SDK for JavaScript v3: commands are
// imported from the client package and body runs with top-level await.
func jsSDK(c sdkClient, base, region string, commands []string, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// npm install %s\n// Save as try.mjs and run: node try.mjs\n", c.jsPkg)
	fmt.Fprintf(&b, "import { %s } from %s;\n\n", strings.Join(append([]string{c.jsClient}, commands...), ", "), jsStr(c.jsPkg))
	fmt.Fprintf(&b, "const client = new %s({\n  endpoint: %s,\n  region: %s,\n  credentials: { accessKeyId: \"test\", secretAccessKey: \"test\" },\n", c.jsClient, jsStr(base), jsStr(region))
	if c.s3 {
		b.WriteString("  forcePathStyle: true,\n")
	}
	b.WriteString("});\n\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
}

// pySDK renders a Python script using boto3.
func pySDK(c sdkClient, base, region string, imports []string, body string) string {
	var b strings.Builder
	b.WriteString("# pip install boto3\n")
	b.WriteString("import boto3\n")
	for _, imp := range imports {
		b.WriteString(imp + "\n")
	}
	if c.s3 {
		b.WriteString("from botocore.config import Config\n")
	}
	fmt.Fprintf(&b, "\nclient = boto3.client(\n    %s,\n    endpoint_url=%s,\n    region_name=%s,\n    aws_access_key_id=\"test\",\n    aws_secret_access_key=\"test\",\n", jsStr(c.pyName), jsStr(base), jsStr(region))
	if c.s3 {
		b.WriteString("    config=Config(s3={\"addressing_style\": \"path\"}),\n")
	}
	b.WriteString(")\n\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// HTTP snippets

// httpCall is one concrete request of an HTTP snippet.
type httpCall struct {
	method, url, body string
}

// httpCalls picks up to four example requests from eps: ANY routes become
// a GET and a POST with a JSON body.
func httpCalls(eps []endpointRef) []httpCall {
	var out []httpCall
	for _, ep := range eps {
		switch ep.Method {
		case "ANY":
			out = append(out, httpCall{http.MethodGet, ep.URL, ""}, httpCall{http.MethodPost, ep.URL, sampleJSONBody})
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			out = append(out, httpCall{ep.Method, ep.URL, sampleJSONBody})
		default:
			out = append(out, httpCall{ep.Method, ep.URL, ""})
		}
		if len(out) >= 4 {
			break
		}
	}
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

type endpointRef struct{ Method, URL string }

func curlSnippet(calls []httpCall) string {
	lines := make([]string, 0, len(calls))
	for _, c := range calls {
		switch {
		case c.body != "":
			lines = append(lines, fmt.Sprintf("curl -i -X %s %s \\\n  -H 'Content-Type: application/json' \\\n  -d %s", c.method, shq(c.url), shq(c.body)))
		case c.method == http.MethodGet:
			lines = append(lines, "curl -i "+shq(c.url))
		default:
			lines = append(lines, fmt.Sprintf("curl -i -X %s %s", c.method, shq(c.url)))
		}
	}
	return strings.Join(lines, "\n\n")
}

func fetchSnippet(calls []httpCall) string {
	var b strings.Builder
	b.WriteString("// Node.js 18+: save as try.mjs and run: node try.mjs\n")
	for i, c := range calls {
		if i > 0 {
			b.WriteString("\n")
		}
		v := "res"
		if i > 0 {
			v = "res" + strconv.Itoa(i+1)
		}
		if c.body == "" && c.method == http.MethodGet {
			fmt.Fprintf(&b, "const %s = await fetch(%s);\n", v, jsStr(c.url))
		} else if c.body == "" {
			fmt.Fprintf(&b, "const %s = await fetch(%s, { method: %s });\n", v, jsStr(c.url), jsStr(c.method))
		} else {
			fmt.Fprintf(&b, "const %s = await fetch(%s, {\n  method: %s,\n  headers: { \"Content-Type\": \"application/json\" },\n  body: JSON.stringify(%s),\n});\n", v, jsStr(c.url), jsStr(c.method), jsLit(c.body))
		}
		fmt.Fprintf(&b, "console.log(%s.status, await %s.text());\n", v, v)
	}
	return b.String()
}

func urllibSnippet(calls []httpCall) string {
	var b strings.Builder
	b.WriteString("import json\nimport urllib.request\n")
	for _, c := range calls {
		b.WriteString("\n")
		if c.body == "" {
			fmt.Fprintf(&b, "req = urllib.request.Request(%s, method=%s)\n", jsStr(c.url), jsStr(c.method))
		} else {
			fmt.Fprintf(&b, "req = urllib.request.Request(\n    %s,\n    method=%s,\n    headers={\"Content-Type\": \"application/json\"},\n    data=json.dumps(%s).encode(),\n)\n", jsStr(c.url), jsStr(c.method), pyLit(c.body))
		}
		b.WriteString("with urllib.request.urlopen(req) as res:\n    print(res.status, res.read().decode())\n")
	}
	return b.String()
}

// addHTTPSnippets adds curl, JavaScript (fetch) and Python (urllib)
// snippets for the endpoints of e.
func (e *entry) addHTTPSnippets() {
	refs := make([]endpointRef, len(e.Endpoints))
	for i, ep := range e.Endpoints {
		refs[i] = endpointRef{ep.Method, ep.URL}
	}
	calls := httpCalls(refs)
	if len(calls) == 0 {
		return
	}
	e.snippet("curl", "bash", curlSnippet(calls))
	e.snippet("JavaScript", "javascript", fetchSnippet(calls))
	e.snippet("Python", "python", urllibSnippet(calls))
}

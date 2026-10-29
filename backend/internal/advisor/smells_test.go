package advisor

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ---------------------------------------------------------------------------
// Terraform design smells

// lambdaFn is a function with its own role.
func lambdaFn(name, extra string) string {
	return fmt.Sprintf(`
resource "aws_iam_role" %[1]q {
  name               = %[1]q
  assume_role_policy = "{}"
}

resource "aws_lambda_function" %[1]q {
  function_name = %[1]q
  role          = aws_iam_role.%[1]s.arn
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  filename      = "function.zip"
%[2]s
}
`, name, extra)
}

// rolePolicy grants the role of fn actions on resources (HCL list items).
func rolePolicy(fn, actions, resources string) string {
	return fmt.Sprintf(`
resource "aws_iam_role_policy" "%[1]s_%[4]d" {
  role = aws_iam_role.%[1]s.id
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = [%[2]s], Resource = [%[3]s] }]
  })
}
`, fn, actions, resources, len(actions)+len(resources))
}

const restIntegration = `
resource "aws_api_gateway_rest_api" "api" {
  name = "api"
}

resource "aws_api_gateway_integration" "%[1]s" {
  rest_api_id             = aws_api_gateway_rest_api.api.id
  resource_id             = "res"
  http_method             = "POST"
  type                    = "AWS_PROXY"
  integration_http_method = "POST"
  uri                     = aws_lambda_function.%[1]s.invoke_arn
%[2]s
}
`

const queues = `
resource "aws_sqs_queue" "emails" {
  name = "emails"
}

resource "aws_sqs_queue" "invoices" {
  name = "invoices"
}
`

const ordersTable = `
resource "aws_dynamodb_table" "orders" {
  name         = "orders"
  hash_key     = "id"
  billing_mode = "PAY_PER_REQUEST"
%s
  attribute {
    name = "id"
    type = "S"
  }
}

resource "aws_sns_topic" "order_events" {
  name = "order-events"
}
`

const rdsDB = `
resource "aws_db_instance" "shared" {
  identifier        = "shared"
  engine            = "postgres"
  instance_class    = "db.t4g.micro"
  allocated_storage = 20
  username          = "app"
  manage_master_user_password = true
  backup_retention_period     = 7
}
`

func dbEnv(db string) string {
	return fmt.Sprintf("  environment {\n    variables = {\n      DB_HOST = %s.address\n    }\n  }", db)
}

var (
	chainDesign = lambdaFn("orders", "") + lambdaFn("billing", "") +
		rolePolicy("orders", `"lambda:InvokeFunction"`, "aws_lambda_function.billing.arn")
	chainDocDesign = lambdaFn("orders", "") + lambdaFn("billing", "") + `
data "aws_iam_policy_document" "orders" {
  statement {
    actions   = ["lambda:Invoke*"]
    resources = [aws_lambda_function.billing.arn]
  }
}

resource "aws_iam_role_policy" "orders" {
  role   = aws_iam_role.orders.id
  policy = data.aws_iam_policy_document.orders.json
}
`
	fanOutDesign  = lambdaFn("checkout", "") + queues + rolePolicy("checkout", `"sqs:SendMessage"`, "aws_sqs_queue.emails.arn, aws_sqs_queue.invoices.arn")
	asyncDesign   = lambdaFn("report", "  timeout = 300") + fmt.Sprintf(restIntegration, "report", "")
	valetDesign   = lambdaFn("upload", "") + fmt.Sprintf(restIntegration, "upload", "") + "\nresource \"aws_s3_bucket\" \"user_files\" {\n  bucket = \"user-files\"\n}\n" + rolePolicy("upload", `"s3:PutObject"`, `"${aws_s3_bucket.user_files.arn}/*"`)
	outboxDesign  = lambdaFn("place_order", "") + fmt.Sprintf(ordersTable, "") + rolePolicy("place_order", `"dynamodb:PutItem", "dynamodb:GetItem"`, "aws_dynamodb_table.orders.arn") + rolePolicy("place_order", `"sns:Publish"`, "aws_sns_topic.order_events.arn")
	sharedRDS     = rdsDB + lambdaFn("orders", dbEnv("aws_db_instance.shared")) + lambdaFn("billing", dbEnv("aws_db_instance.shared")) + lambdaFn("shipping", dbEnv("aws_db_instance.shared"))
	azureSharedDB = `
resource "azurerm_postgresql_flexible_server" "db" {
  name                = "db"
  resource_group_name = "rg"
  location            = "westeurope"
}
` + azureApp("orders", "azurerm_postgresql_flexible_server.db.fqdn") + azureApp("billing", "azurerm_postgresql_flexible_server.db.fqdn") + azureApp("shipping", "azurerm_postgresql_flexible_server.db.fqdn")
	gcpSharedDB = `
resource "google_sql_database_instance" "db" {
  name             = "db"
  database_version = "POSTGRES_16"
  region           = "us-central1"
  settings {
    tier = "db-f1-micro"
    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
    }
    ip_configuration {
      ipv4_enabled = false
    }
  }
}
` + gcpRun("orders", "google_sql_database_instance.db.connection_name") + gcpRun("billing", "google_sql_database_instance.db.connection_name") + gcpRun("shipping", "google_sql_database_instance.db.connection_name")
)

func azureApp(name, db string) string {
	return fmt.Sprintf(`
resource "azurerm_linux_web_app" %[1]q {
  name                = %[1]q
  resource_group_name = "rg"
  location            = "westeurope"
  service_plan_id     = "plan"
  app_settings = {
    DB_HOST = %[2]s
  }
  site_config {}
}
`, name, db)
}

func gcpRun(name, db string) string {
	return fmt.Sprintf(`
resource "google_cloud_run_v2_service" %[1]q {
  name     = %[1]q
  location = "us-central1"
  ingress  = "INGRESS_TRAFFIC_INTERNAL_ONLY"
  template {
    containers {
      image = "us-docker.pkg.dev/cloudrun/container/hello"
      env {
        name  = "DB"
        value = %[2]s
      }
    }
  }
}
`, name, db)
}

// terraformSmellDesigns trigger every Terraform design smell between them.
var terraformSmellDesigns = []string{chainDesign, fanOutDesign, asyncDesign, valetDesign, outboxDesign, sharedRDS, azureSharedDB, gcpSharedDB}

func TestTerraformSmells(t *testing.T) {
	tests := []struct {
		name    string
		wf      string // workflow name
		src     string
		want    []string
		without []string
	}{
		// Function chains.
		{"function invoking another", "shop", chainDesign, []string{"aws_lambda_chain_queue"}, nil},
		{"invoke granted through a policy document with a wildcard", "shop", chainDocDesign, []string{"aws_lambda_chain_queue"}, nil},
		{"function naming another without invoking it", "shop", lambdaFn("orders", "  environment {\n    variables = {\n      NEXT = aws_lambda_function.billing.function_name\n    }\n  }") + lambdaFn("billing", ""),
			nil, []string{"aws_lambda_chain_queue"}},
		{"function already sending to a queue", "shop", lambdaFn("orders", "") + lambdaFn("billing", "") + queues + rolePolicy("orders", `"sqs:SendMessage"`, "aws_sqs_queue.emails.arn"),
			nil, []string{"aws_lambda_chain_queue", "aws_fan_out_topic"}},
		// Fan-out.
		{"producer sending to two queues", "shop", fanOutDesign, []string{"aws_fan_out_topic"}, nil},
		{"producer sending to one queue", "shop", lambdaFn("checkout", "") + queues + rolePolicy("checkout", `"sqs:SendMessage"`, "aws_sqs_queue.emails.arn"), nil, []string{"aws_fan_out_topic"}},
		{"queues already subscribed to a topic", "shop", fanOutDesign + `
resource "aws_sns_topic" "events" {
  name = "events"
}

resource "aws_sns_topic_subscription" "emails" {
  topic_arn = aws_sns_topic.events.arn
  protocol  = "sqs"
  endpoint  = aws_sqs_queue.emails.arn
}
`, nil, []string{"aws_fan_out_topic"}},
		// Asynchronous request-reply.
		{"5-minute function behind a REST API", "shop", asyncDesign, []string{"aws_api_async_request_reply"}, nil},
		{"short function behind a REST API", "shop", lambdaFn("report", "  timeout = 10") + fmt.Sprintf(restIntegration, "report", ""), nil, []string{"aws_api_async_request_reply"}},
		{"integration timeout raised above the function's", "shop", lambdaFn("report", "  timeout = 60") + fmt.Sprintf(restIntegration, "report", "  timeout_milliseconds = 90000"), nil, []string{"aws_api_async_request_reply"}},
		{"long function not behind an API", "shop", lambdaFn("report", "  timeout = 300"), nil, []string{"aws_api_async_request_reply"}},
		{"long function already consuming a job queue", "shop", asyncDesign + `
resource "aws_sqs_queue" "jobs" {
  name = "jobs"
}

resource "aws_lambda_event_source_mapping" "jobs" {
  event_source_arn = aws_sqs_queue.jobs.arn
  function_name    = aws_lambda_function.report.arn
}
`, nil, []string{"aws_api_async_request_reply"}},
		// Valet key.
		{"upload function behind the API writing a bucket", "shop", valetDesign, []string{"aws_valet_key_uploads"}, nil},
		{"upload function not behind an API", "shop", lambdaFn("upload", "") + "\nresource \"aws_s3_bucket\" \"user_files\" {\n  bucket = \"user-files\"\n}\n" + rolePolicy("upload", `"s3:PutObject"`, "aws_s3_bucket.user_files.arn"),
			nil, []string{"aws_valet_key_uploads"}},
		{"API function writing audit records", "shop", lambdaFn("api", "") + fmt.Sprintf(restIntegration, "api", "") + "\nresource \"aws_s3_bucket\" \"audit\" {\n  bucket = \"audit-log\"\n}\n" + rolePolicy("api", `"s3:PutObject"`, "aws_s3_bucket.audit.arn"),
			nil, []string{"aws_valet_key_uploads"}},
		{"bucket already taking browser uploads", "shop", valetDesign + `
resource "aws_s3_bucket_cors_configuration" "user_files" {
  bucket = aws_s3_bucket.user_files.id
  cors_rule {
    allowed_methods = ["PUT"]
    allowed_origins = ["https://app.example.com"]
  }
}
`, nil, []string{"aws_valet_key_uploads"}},
		// Transactional outbox.
		{"function writing a table and publishing", "shop", outboxDesign, []string{"aws_transactional_outbox"}, nil},
		{"table already streaming", "shop", lambdaFn("place_order", "") + fmt.Sprintf(ordersTable, "  stream_enabled   = true\n  stream_view_type = \"NEW_IMAGE\"") +
			rolePolicy("place_order", `"dynamodb:PutItem"`, "aws_dynamodb_table.orders.arn") + rolePolicy("place_order", `"sns:Publish"`, "aws_sns_topic.order_events.arn"), nil, []string{"aws_transactional_outbox"}},
		{"function only writing the table", "shop", lambdaFn("place_order", "") + fmt.Sprintf(ordersTable, "") + rolePolicy("place_order", `"dynamodb:PutItem"`, "aws_dynamodb_table.orders.arn"),
			nil, []string{"aws_transactional_outbox"}},
		// Shared databases.
		{"three services on one RDS instance", "shop", sharedRDS, []string{"aws_database_per_service"}, []string{"aws_cache_aside"}},
		{"three services on one RDS instance in production", "shop-prod", sharedRDS, []string{"aws_database_per_service", "aws_cache_aside"}, nil},
		{"two services on one RDS instance in production", "shop-prod", rdsDB + lambdaFn("orders", dbEnv("aws_db_instance.shared")) + lambdaFn("billing", dbEnv("aws_db_instance.shared")),
			[]string{"aws_cache_aside"}, []string{"aws_database_per_service"}},
		{"functions of one service (same role) on one instance", "shop-prod", rdsDB + `
resource "aws_iam_role" "api" {
  name               = "api"
  assume_role_policy = "{}"
}
` + sameRoleFn("list", "aws_db_instance.shared") + sameRoleFn("create", "aws_db_instance.shared") + sameRoleFn("delete", "aws_db_instance.shared"),
			nil, []string{"aws_database_per_service", "aws_cache_aside"}},
		{"a cache already in place", "shop-prod", sharedRDS + `
resource "aws_elasticache_replication_group" "cache" {
  replication_group_id = "cache"
  description          = "cache"
}
`, []string{"aws_database_per_service"}, []string{"aws_cache_aside"}},
		{"three Azure apps on one server", "shop-prod", azureSharedDB, []string{"azure_database_per_service", "azure_cache_aside"}, nil},
		{"three Azure apps with a database each", "shop", `
resource "azurerm_postgresql_flexible_server" "db" {
  name                = "db"
  resource_group_name = "rg"
  location            = "westeurope"
}
` + azureOwnDB("orders") + azureOwnDB("billing") + azureOwnDB("shipping"), nil, []string{"azure_database_per_service"}},
		{"three Cloud Run services on one instance", "shop-prod", gcpSharedDB, []string{"gcp_database_per_service", "gcp_cache_aside"}, nil},
		{"two Cloud Run services on one instance, development", "shop", gcpRun("orders", "google_sql_database_instance.db.connection_name") + gcpRun("billing", "google_sql_database_instance.db.connection_name") + `
resource "google_sql_database_instance" "db" {
  name             = "db"
  database_version = "POSTGRES_16"
  region           = "us-central1"
  settings {
    tier = "db-f1-micro"
  }
}
`, nil, []string{"gcp_database_per_service", "gcp_cache_aside"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(Recommend(importHCL(t, tc.wf, tc.src), testCatalog(t)))
			for _, id := range tc.want {
				if !slices.Contains(got, id) {
					t.Errorf("missing %s in %v", id, got)
				}
			}
			for _, id := range tc.without {
				if slices.Contains(got, id) {
					t.Errorf("unexpected %s in %v", id, got)
				}
			}
		})
	}
}

func sameRoleFn(name, db string) string {
	return fmt.Sprintf(`
resource "aws_lambda_function" %[1]q {
  function_name = %[1]q
  role          = aws_iam_role.api.arn
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  filename      = "function.zip"
%[2]s
}
`, name, dbEnv(db))
}

func azureOwnDB(name string) string {
	return fmt.Sprintf(`
resource "azurerm_postgresql_flexible_server_database" %[1]q {
  name      = %[1]q
  server_id = azurerm_postgresql_flexible_server.db.id
}
`, name) + azureApp(name, fmt.Sprintf("azurerm_postgresql_flexible_server_database.%s.name", name))
}

func TestSmellSnippetsNameTheDesign(t *testing.T) {
	c := testCatalog(t)
	find := func(src, id string) Recommendation {
		t.Helper()
		for _, r := range Recommend(importHCL(t, "shop-prod", src), c) {
			if r.ID == id {
				return r
			}
		}
		t.Fatalf("no %s", id)
		return Recommendation{}
	}
	tests := []struct {
		src, id string
		want    []string
	}{
		{chainDesign, "aws_lambda_chain_queue", []string{"function_name = aws_lambda_function.billing.arn", "role = aws_iam_role.orders.id", `"sqs:SendMessage"`, "role = aws_iam_role.billing.id", "visibility_timeout_seconds = 30"}},
		{fanOutDesign, "aws_fan_out_topic", []string{"endpoint = aws_sqs_queue.emails.arn", "endpoint = aws_sqs_queue.invoices.arn", `"aws:SourceArn"`, `"sns:Publish"`}},
		{asyncDesign, "aws_api_async_request_reply", []string{"function_name = aws_lambda_function.report.arn", "visibility_timeout_seconds = 1800", "202 Accepted"}},
		{valetDesign, "aws_valet_key_uploads", []string{"bucket = aws_s3_bucket.user_files.id", `allowed_methods = ["PUT"]`}},
		{outboxDesign, "aws_transactional_outbox", []string{"stream_enabled = true", "source = aws_dynamodb_table.orders.stream_arn", "target = aws_sns_topic.order_events.arn"}},
		{sharedRDS, "aws_database_per_service", []string{"aws_lambda_function.orders keeps it", `resource "aws_db_instance" "billing_db"`, `resource "aws_db_instance" "shipping_db"`, "manage_master_user_password = true"}},
		{gcpSharedDB, "gcp_database_per_service", []string{`resource "google_sql_database" "billing"`, "password_wo"}},
	}
	for _, tc := range tests {
		r := find(tc.src, tc.id)
		// terraform fmt aligns the equal signs: compare words.
		snippet := strings.Join(strings.Fields(r.Snippet), " ")
		for _, w := range tc.want {
			if !strings.Contains(snippet, w) {
				t.Errorf("%s: snippet lacks %q:\n%s", tc.id, w, r.Snippet)
			}
		}
	}
}

func TestSmellsRankAfterSecurity(t *testing.T) {
	// A public API whose functions share a database: the edge and data
	// items come first, the design smells after them.
	src := restAPI + sharedRDS + rolePolicy("orders", `"lambda:InvokeFunction"`, "aws_lambda_function.billing.arn")
	recs, _ := candidates(importHCL(t, "shop-prod", src), testCatalog(t))
	design := false
	for _, r := range recs {
		if r.priority == prioDesign {
			design = true
			continue
		}
		if design && r.priority <= prioData {
			t.Errorf("%s (priority %d) ranks after a design smell", r.ID, r.priority)
		}
	}
	if !design {
		t.Fatalf("no design smell in %v", recs)
	}
}

// ---------------------------------------------------------------------------
// Docker and Kubernetes design smells

func dependsOn(c *model.ComposeModel, source string, targets ...string) {
	for _, t := range targets {
		c.Graph.Edges = append(c.Graph.Edges, model.ComposeEdge{ID: source + t, Source: model.ComposeServiceID(source), Target: model.ComposeServiceID(t), Kind: model.ComposeEdgeDependsOn})
	}
}

func env(key, value string) func(*model.ServiceFacts) {
	return func(s *model.ServiceFacts) {
		s.Environment = append(s.Environment, model.ServiceEnv{Key: key, Value: &value})
	}
}

// composeShared is a project whose apps all depend on one database.
func composeShared(name, dbImage string, apps ...model.ComposeNode) *model.Workflow {
	wf := composeWF(name, append([]model.ComposeNode{svc("db", dbImage, hardened)}, apps...)...)
	for _, a := range apps {
		dependsOn(wf.Compose, "db", a.Name)
	}
	return wf
}

func app(name string, mutate ...func(*model.ServiceFacts)) model.ComposeNode {
	return svc(name, "node", append([]func(*model.ServiceFacts){hardened}, mutate...)...)
}

func TestDockerSmells(t *testing.T) {
	tests := []struct {
		name    string
		wf      *model.Workflow
		want    []string
		without []string
	}{
		{"three apps on one postgres", composeShared("shop", "postgres", app("orders"), app("billing"), app("shipping")), []string{"docker_database_per_service"}, []string{"docker_cache_aside"}},
		{"three apps on one mysql in production", composeShared("shop-prod", "mysql", app("orders"), app("billing"), app("shipping")), []string{"docker_database_per_service", "docker_cache_aside"}, nil},
		{"two apps on one postgres", composeShared("shop", "postgres", app("web"), app("worker")), nil, []string{"docker_database_per_service"}},
		{"two apps and an inactive profile service on one postgres", func() *model.Workflow {
			wf := composeShared("shop", "postgres", app("web"), app("worker"))
			debug := app("debug")
			debug.Inactive = true
			wf.Compose.Graph.Nodes = append(wf.Compose.Graph.Nodes, debug)
			dependsOn(wf.Compose, "db", "debug")
			return wf
		}(), nil, []string{"docker_database_per_service"}},
		{"three apps with a database each on one server", composeShared("shop", "postgres",
			app("orders", env("DATABASE_URL", "postgres://orders@db:5432/orders")), app("billing", env("DATABASE_URL", "postgres://billing@db:5432/billing")), app("shipping", env("DB_NAME", "shipping"))),
			nil, []string{"docker_database_per_service"}},
		{"three apps, two on the same database", composeShared("shop", "postgres",
			app("orders", env("DATABASE_URL", "postgres://app@db:5432/shop")), app("billing", env("DATABASE_URL", "postgres://app@db:5432/shop")), app("shipping", env("DB_NAME", "shipping"))),
			[]string{"docker_database_per_service"}, nil},
		{"a cache already running", func() *model.Workflow {
			wf := composeShared("shop-prod", "postgres", app("web"), app("worker"))
			wf.Compose.Graph.Nodes = append(wf.Compose.Graph.Nodes, svc("cache", "valkey/valkey", hardened))
			return wf
		}(), nil, []string{"docker_cache_aside"}},
		{"services publishing ports get one gateway", composeWF("shop", app("web", publish("3000", "3000")), app("api", publish("8080", "8080"))), []string{"docker_reverse_proxy"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Recommend(tc.wf, nil)
			for _, id := range tc.want {
				if !slices.Contains(ids(got), id) {
					t.Errorf("missing %s in %v", id, ids(got))
				}
			}
			for _, id := range tc.without {
				if slices.Contains(ids(got), id) {
					t.Errorf("unexpected %s in %v", id, ids(got))
				}
			}
			for _, r := range got {
				if r.ID == "docker_reverse_proxy" && len(r.Nodes) > 1 && r.Pattern != "api-gateway" {
					t.Errorf("reverse proxy for %v: pattern %q, want api-gateway", r.Nodes, r.Pattern)
				}
			}
		})
	}
}

func kubeDeploy(name, image string) model.KubeNode {
	repo, tag, _ := strings.Cut(image, ":")
	return model.KubeNode{ID: "Deployment/shop/" + name, Kind: "Deployment", APIVersion: "apps/v1", Name: name, Namespace: "shop", Category: model.KubeCatWorkload,
		Workload: &model.KubeWorkload{Replicas: new(1), Selector: map[string]string{"app": name}, Containers: []model.KubeContainer{{
			Name: name, Image: image, ImageRef: &model.ImageRef{Repo: repo, Tag: tag}, Ports: []model.KubeContainerPort{{ContainerPort: 8080}},
		}}}}
}

func kubeSvc(name, typ string, port int) model.KubeNode {
	return model.KubeNode{ID: "Service/shop/" + name, Kind: "Service", APIVersion: "v1", Name: name, Namespace: "shop", Category: model.KubeCatNetwork,
		Service: &model.KubeServiceFacts{Type: typ, Selector: map[string]string{"app": name}, Ports: []model.KubeServicePort{{Port: port}}}}
}

func kubeEdge(kind, source, target string) model.KubeEdge {
	return model.KubeEdge{ID: kind + source + target, Source: source, Target: target, Kind: kind}
}

// kubeShared is a namespace whose apps all reach one database Service.
func kubeShared(name, dbImage string, apps ...string) *model.Workflow {
	nodes := []model.KubeNode{kubeDeploy("db", dbImage), kubeSvc("db", "ClusterIP", 5432)}
	edges := []model.KubeEdge{kubeEdge(model.KubeEdgeSelects, "Service/shop/db", "Deployment/shop/db")}
	for _, a := range apps {
		nodes = append(nodes, kubeDeploy(a, "ghcr.io/shop/"+a+":1"))
		edges = append(edges, kubeEdge(model.KubeEdgeInferred, "Deployment/shop/"+a, "Service/shop/db"))
	}
	return kubeWF(name, nodes, edges)
}

// kubeExposed exposes each app on a Service of type typ.
func kubeExposed(typ string, apps ...string) *model.Workflow {
	var nodes []model.KubeNode
	var edges []model.KubeEdge
	for _, a := range apps {
		image := "ghcr.io/shop/" + a + ":1"
		if a == "db" {
			image = "postgres:16"
		}
		nodes = append(nodes, kubeDeploy(a, image), kubeSvc(a, typ, 80))
		edges = append(edges, kubeEdge(model.KubeEdgeSelects, "Service/shop/"+a, "Deployment/shop/"+a))
	}
	return kubeWF("shop", nodes, edges)
}

func TestKubernetesSmells(t *testing.T) {
	routed := kubeExposed("LoadBalancer", "web", "api")
	routed.Kubernetes.Graph.Nodes = append(routed.Kubernetes.Graph.Nodes, model.KubeNode{ID: "Ingress/shop/web", Kind: "Ingress", APIVersion: "networking.k8s.io/v1", Name: "web", Namespace: "shop",
		Routes: []model.KubeRoute{{Host: "web.localhost", Service: "web", Port: "80"}}})
	routed.Kubernetes.Graph.Edges = append(routed.Kubernetes.Graph.Edges, kubeEdge(model.KubeEdgeRoutes, "Ingress/shop/web", "Service/shop/web"))
	tests := []struct {
		name    string
		wf      *model.Workflow
		want    []string
		without []string
	}{
		{"three apps on one postgres", kubeShared("shop", "postgres:16", "orders", "billing", "shipping"), []string{"k8s_database_per_service"}, []string{"k8s_cache_aside"}},
		{"two apps on one postgres in production", kubeShared("shop-prod", "postgres:16", "web", "worker"), []string{"k8s_cache_aside"}, []string{"k8s_database_per_service"}},
		{"three apps on one mongo", kubeShared("shop", "mongo:7", "orders", "billing", "shipping"), nil, []string{"k8s_database_per_service"}},
		{"two apps on their own load balancers", kubeExposed("LoadBalancer", "web", "api"), []string{"k8s_single_entry_ingress"}, nil},
		{"two apps on node ports", kubeExposed("NodePort", "web", "api"), []string{"k8s_single_entry_ingress"}, nil},
		{"one app and a database on load balancers", kubeExposed("LoadBalancer", "web", "db"), nil, []string{"k8s_single_entry_ingress"}},
		{"one exposed app, the other behind an Ingress", routed, nil, []string{"k8s_single_entry_ingress"}},
		{"cluster-internal services", kubeExposed("ClusterIP", "web", "api"), nil, []string{"k8s_single_entry_ingress"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(Recommend(tc.wf, nil))
			for _, id := range tc.want {
				if !slices.Contains(got, id) {
					t.Errorf("missing %s in %v", id, got)
				}
			}
			for _, id := range tc.without {
				if slices.Contains(got, id) {
					t.Errorf("unexpected %s in %v", id, got)
				}
			}
		})
	}
}

// localSmellWorkflows trigger every docker and Kubernetes design smell.
func localSmellWorkflows() []*model.Workflow {
	return []*model.Workflow{
		composeShared("shop-prod", "postgres", app("orders"), app("billing"), app("shipping")),
		kubeShared("shop-prod", "postgres:16", "orders", "billing", "shipping"),
		kubeExposed("LoadBalancer", "web", "api"),
	}
}

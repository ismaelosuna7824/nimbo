package advisor

import (
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v4"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

var (
	catOnce sync.Once
	cat     *catalog.Catalog
	catErr  error
)

func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	catOnce.Do(func() { cat, catErr = catalog.Load() })
	if catErr != nil {
		t.Fatalf("catalog: %v", catErr)
	}
	return cat
}

func importHCL(t *testing.T, name, src string) *model.Workflow {
	t.Helper()
	wf, diags, err := hclcodec.Import(name, map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, d := range diags {
		if d.Severity == model.SeverityError {
			t.Fatalf("import: %+v", d)
		}
	}
	hclcodec.Normalize(wf)
	return wf
}

func ids(recs []Recommendation) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}

func candidateIDs(t *testing.T, wf *model.Workflow) []string {
	t.Helper()
	recs, _ := candidates(wf, testCatalog(t))
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}

// ---------------------------------------------------------------------------
// Terraform designs

const restAPI = `
resource "aws_api_gateway_rest_api" "shop" {
  name = "shop"
}

resource "aws_api_gateway_stage" "live" {
  rest_api_id   = aws_api_gateway_rest_api.shop.id
  deployment_id = "abc123"
  stage_name    = "live"
}
`

const restAPIWithWAF = restAPI + `
resource "aws_wafv2_web_acl_association" "live" {
  resource_arn = aws_api_gateway_stage.live.arn
  web_acl_arn  = "arn:aws:wafv2:us-east-1:000000000000:regional/webacl/edge/1"
}
`

const privateRESTAPI = `
resource "aws_api_gateway_rest_api" "internal" {
  name = "internal"
  endpoint_configuration {
    types = ["PRIVATE"]
  }
}

resource "aws_api_gateway_stage" "v1" {
  rest_api_id   = aws_api_gateway_rest_api.internal.id
  deployment_id = "abc123"
  stage_name    = "v1"
}
`

const internalOnly = `
resource "aws_sqs_queue" "jobs_dlq" {
  name = "jobs-dlq"
}

resource "aws_sqs_queue" "jobs" {
  name = "jobs"
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.jobs_dlq.arn
    maxReceiveCount     = 5
  })
}

resource "aws_lb" "internal" {
  name               = "internal"
  internal           = true
  load_balancer_type = "application"
  subnets            = ["subnet-1", "subnet-2"]
}
`

const httpAPI = `
resource "aws_apigatewayv2_api" "http" {
  name          = "http"
  protocol_type = "HTTP"
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.http.id
  name        = "$default"
  auto_deploy = true
}
`

const httpAPIHardened = httpAPI + `
resource "aws_cloudwatch_log_group" "access" {
  name              = "/aws/apigateway/http"
  retention_in_days = 30
}

resource "aws_apigatewayv2_stage" "hardened" {
  api_id = aws_apigatewayv2_api.http.id
  name   = "v1"
  default_route_settings {
    throttling_burst_limit = 100
    throttling_rate_limit  = 50
  }
  access_log_settings {
    destination_arn = aws_cloudwatch_log_group.access.arn
    format          = "$context.requestId"
  }
}
`

const apimDesign = `
resource "azurerm_api_management" "apim" {
  name                = "apim"
  resource_group_name = "rg"
  location            = "westeurope"
  publisher_name      = "Shop"
  publisher_email     = "ops@example.com"
  sku_name            = "Developer_1"
}
`

const apimLimited = apimDesign + `
resource "azurerm_api_management_policy" "global" {
  api_management_id = azurerm_api_management.apim.id
  xml_content       = "<policies><inbound><rate-limit-by-key calls=\"10\" renewal-period=\"60\" counter-key=\"x\" /></inbound></policies>"
}
`

const armorDesign = `
resource "google_compute_backend_service" "web" {
  name = "web"
}
`

const armorProtected = `
resource "google_compute_security_policy" "edge" {
  name = "edge"
}

resource "google_compute_backend_service" "web" {
  name            = "web"
  security_policy = google_compute_security_policy.edge.id
}
`

const ociLB = `
variable "compartment_id" {}

resource "oci_load_balancer_load_balancer" "lb" {
  compartment_id = var.compartment_id
  display_name   = "lb"
  shape          = "flexible"
  subnet_ids     = ["subnet"]
}
`

const ociLBProtected = ociLB + `
resource "oci_waf_web_app_firewall" "lb" {
  compartment_id             = var.compartment_id
  backend_type               = "LOAD_BALANCER"
  load_balancer_id           = oci_load_balancer_load_balancer.lb.id
  web_app_firewall_policy_id = "ocid1.webappfirewallpolicy.oc1..x"
}
`

func TestTerraformTriggersAndCompanions(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    []string // present in Recommend
		without []string // absent from Recommend
	}{
		{"public REST API without WAF", restAPI, []string{"aws_public_api_waf", "aws_api_throttling"}, nil},
		{"REST API with a WAF association", restAPIWithWAF, []string{"aws_api_throttling"}, []string{"aws_public_api_waf"}},
		{"private REST API", privateRESTAPI, nil, []string{"aws_public_api_waf", "aws_http_api_edge_waf", "aws_cloudfront_waf"}},
		{"HTTP API: throttling, and CloudFront + WAF for production", httpAPI, []string{"aws_api_throttling", "aws_http_api_edge_waf"}, []string{"aws_public_api_waf"}},
		{"APIM without rate limit", apimDesign, []string{"azure_apim_rate_limit"}, nil},
		{"APIM with a rate-limit policy", apimLimited, nil, []string{"azure_apim_rate_limit"}},
		{"external backend service without Cloud Armor", armorDesign, []string{"gcp_cloud_armor"}, nil},
		{"backend service with Cloud Armor", armorProtected, nil, []string{"gcp_cloud_armor"}},
		{"public OCI load balancer", ociLB, []string{"oci_lb_waf"}, nil},
		{"OCI load balancer with WAF", ociLBProtected, nil, []string{"oci_lb_waf"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(Recommend(importHCL(t, "test", tc.src), testCatalog(t)))
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

func TestHTTPAPIThrottlingDisappears(t *testing.T) {
	recs := Recommend(importHCL(t, "test", httpAPIHardened), testCatalog(t))
	for _, r := range recs {
		if r.ID == "aws_api_throttling" {
			if !reflect.DeepEqual(r.Nodes, []string{"aws_apigatewayv2_stage.default"}) {
				t.Errorf("throttling nodes = %v, want only the unhardened stage", r.Nodes)
			}
		}
	}
}

func TestInternalOnlyDesignGetsNoEdgeOrProductionItems(t *testing.T) {
	recs := Recommend(importHCL(t, "jobs", internalOnly), testCatalog(t))
	for _, r := range recs {
		if r.Scope == ScopeProduction {
			t.Errorf("production-scope %s for an internal dev design", r.ID)
		}
		if strings.Contains(r.ID, "waf") || strings.Contains(r.ID, "throttl") {
			t.Errorf("edge recommendation %s without a public entry point", r.ID)
		}
		if r.ID == "aws_sqs_dlq" {
			t.Errorf("DLQ recommended though the queue has one and the DLQ itself needs none: %v", r.Nodes)
		}
	}
}

func TestProductionIntentEnablesProductionItems(t *testing.T) {
	wf := importHCL(t, "jobs-prod", internalOnly)
	_, production := candidates(wf, testCatalog(t))
	if !production {
		t.Fatal("a workflow named jobs-prod should look production-intended")
	}
	got := ids(Recommend(wf, testCatalog(t)))
	if !slices.Contains(got, "terraform_environments") {
		t.Errorf("single-environment production design: want terraform_environments in %v", got)
	}
	wf.Environments = []model.Environment{{Name: "dev"}, {Name: "prod"}}
	if got := ids(Recommend(wf, testCatalog(t))); slices.Contains(got, "terraform_environments") {
		t.Errorf("two environments: unexpected terraform_environments in %v", got)
	}
}

func TestOrderingCapAndDeterminism(t *testing.T) {
	for _, src := range terraformKitchenSinks {
		wf := importHCL(t, "shop-prod", src)
		a := Recommend(wf, testCatalog(t))
		b := Recommend(importHCL(t, "shop-prod", src), testCatalog(t))
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("not deterministic:\n%v\n%v", ids(a), ids(b))
		}
		if len(a) == 0 || len(a) > maxRecommendations {
			t.Fatalf("got %d recommendations, want 1..%d", len(a), maxRecommendations)
		}
		recs, _ := candidates(wf, testCatalog(t))
		for i := 1; i < len(recs); i++ {
			if recs[i-1].priority > recs[i].priority {
				t.Fatalf("not ordered by value: %s before %s", recs[i-1].ID, recs[i].ID)
			}
		}
		if recs[0].priority != prioEdge {
			t.Errorf("first recommendation %s is not an edge (security) item", recs[0].ID)
		}
	}
}

func TestEmptyAndNil(t *testing.T) {
	if got := Recommend(nil, nil); got == nil || len(got) != 0 {
		t.Errorf("nil workflow: %v", got)
	}
	if got := Recommend(&model.Workflow{}, testCatalog(t)); got == nil || len(got) != 0 {
		t.Errorf("empty workflow: %v", got)
	}
	if got := Recommend(&model.Workflow{Kind: model.WorkflowDocker}, nil); got == nil || len(got) != 0 {
		t.Errorf("docker workflow without a compose model: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Docker

func composeWF(name string, svcs ...model.ComposeNode) *model.Workflow {
	return &model.Workflow{Name: name, Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Project: name, Graph: model.ComposeGraph{Nodes: svcs}}}
}

func svc(name, repo string, mutate ...func(*model.ServiceFacts)) model.ComposeNode {
	s := &model.ServiceFacts{Ports: []model.ServicePort{}, Environment: []model.ServiceEnv{}, Volumes: []model.ServiceMount{}}
	if repo != "" {
		s.Image = &model.ImageRef{Ref: repo + ":1", Repo: repo, Tag: "1"}
	}
	for _, m := range mutate {
		m(s)
	}
	return model.ComposeNode{ID: model.ComposeServiceID(name), Kind: model.ComposeService, Name: name, Service: s}
}

func publish(host, container string) func(*model.ServiceFacts) {
	return func(s *model.ServiceFacts) {
		s.Ports = append(s.Ports, model.ServicePort{Host: host, Container: container, Published: true})
	}
}

func hardened(s *model.ServiceFacts) {
	s.Resources = &model.ServiceResources{CPUs: "1", Memory: "512M"}
	s.Healthcheck = &model.ServiceHealthcheck{Test: "true"}
}

func TestDocker(t *testing.T) {
	tests := []struct {
		name    string
		wf      *model.Workflow
		want    []string
		without []string
	}{
		{"app publishing a port", composeWF("shop", svc("web", "node", publish("3000", "3000"), hardened)), []string{"docker_reverse_proxy"}, []string{"docker_waf_crs"}},
		{"behind a proxy", composeWF("shop", svc("web", "node", hardened), svc("proxy", "nginx", publish("80", "80"), hardened)), nil, []string{"docker_reverse_proxy"}},
		{"proxy but app still published", composeWF("shop", svc("web", "node", publish("3000", "3000"), hardened), svc("proxy", "nginx", publish("80", "80"), hardened)), []string{"docker_reverse_proxy"}, nil},
		{"production adds the OWASP CRS", composeWF("shop-prod", svc("web", "node", publish("3000", "3000"), hardened)), []string{"docker_reverse_proxy", "docker_waf_crs", "docker_environments"}, nil},
		{"published database", composeWF("shop", svc("db", "postgres", publish("5432", "5432"), hardened)), []string{"docker_private_data", "docker_data_volumes"}, []string{"docker_reverse_proxy"}},
		{"database with a volume, local only", composeWF("shop", svc("db", "postgres", hardened, func(s *model.ServiceFacts) {
			s.Ports = append(s.Ports, model.ServicePort{HostIP: "127.0.0.1", Host: "5432", Container: "5432", Published: true})
			s.Volumes = append(s.Volumes, model.ServiceMount{Type: "volume", Source: "db-data", Target: "/var/lib/postgresql/data"})
		})), nil, []string{"docker_private_data", "docker_data_volumes", "docker_reverse_proxy"}},
		{"credentials in environment", composeWF("shop", svc("db", "postgres", hardened, func(s *model.ServiceFacts) {
			s.Environment = append(s.Environment, model.ServiceEnv{Key: "POSTGRES_PASSWORD", Secret: true})
		})), []string{"docker_secrets"}, nil},
		{"no limits or health check", composeWF("shop", svc("worker", "python")), []string{"docker_limits_healthchecks"}, nil},
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

func TestDockerProxySnippetEscapesInterpolation(t *testing.T) {
	recs := Recommend(composeWF("shop", svc("web", "node", publish("3000", "3000"))), nil)
	for _, r := range recs {
		if r.ID != "docker_reverse_proxy" {
			continue
		}
		for _, want := range []string{"$$binary_remote_addr", "proxy_pass http://web:3000;", "listen 3000;", `"3000:3000"`} {
			if !strings.Contains(r.Snippet, want) {
				t.Errorf("snippet lacks %q:\n%s", want, r.Snippet)
			}
		}
		return
	}
	t.Fatal("no docker_reverse_proxy")
}

// ---------------------------------------------------------------------------
// Kubernetes

func kubeWF(name string, nodes []model.KubeNode, edges []model.KubeEdge) *model.Workflow {
	return &model.Workflow{Name: name, Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{
		Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s"}},
		Graph:   model.KubeGraph{Nodes: nodes, Edges: edges},
	}}
}

var (
	webDeploy = model.KubeNode{ID: "Deployment/shop/web", Kind: "Deployment", APIVersion: "apps/v1", Name: "web", Namespace: "shop", Category: model.KubeCatWorkload,
		Workload: &model.KubeWorkload{Replicas: new(2), Selector: map[string]string{"app": "web"}, Containers: []model.KubeContainer{{
			Name: "web", Image: "nginx:1.27", ImageRef: &model.ImageRef{Repo: "nginx", Tag: "1.27"}, Ports: []model.KubeContainerPort{{ContainerPort: 80}},
		}}}}
	webService = model.KubeNode{ID: "Service/shop/web", Kind: "Service", APIVersion: "v1", Name: "web", Namespace: "shop", Category: model.KubeCatNetwork,
		Service: &model.KubeServiceFacts{Type: "ClusterIP", Selector: map[string]string{"app": "web"}}}
	webIngress = model.KubeNode{ID: "Ingress/shop/web", Kind: "Ingress", APIVersion: "networking.k8s.io/v1", Name: "web", Namespace: "shop", Category: model.KubeCatNetwork,
		Routes: []model.KubeRoute{{Host: "shop.example.com", Path: "/", Service: "web", Port: "80", Class: "traefik"}}}
	rateLimit = model.KubeNode{ID: "Middleware.traefik.io/shop/rate-limit", Kind: "Middleware", APIVersion: "traefik.io/v1alpha1", Name: "rate-limit", Namespace: "shop", Category: model.KubeCatCustom, Custom: true}
	selects   = model.KubeEdge{ID: "e1", Source: "Service/shop/web", Target: "Deployment/shop/web", Kind: model.KubeEdgeSelects}
	routes    = model.KubeEdge{ID: "e2", Source: "Ingress/shop/web", Target: "Service/shop/web", Kind: model.KubeEdgeRoutes}
)

func TestKubernetes(t *testing.T) {
	tests := []struct {
		name    string
		wf      *model.Workflow
		want    []string
		without []string
	}{
		{"Ingress without rate limit", kubeWF("shop", []model.KubeNode{webDeploy, webService, webIngress}, []model.KubeEdge{selects, routes}),
			[]string{"k8s_ingress_rate_limit", "k8s_network_policy", "k8s_resources_probes"}, []string{"k8s_hpa", "k8s_pdb", "k8s_ingress_tls"}},
		{"Ingress with a Traefik RateLimit middleware", kubeWF("shop", []model.KubeNode{webDeploy, webService, webIngress, rateLimit}, []model.KubeEdge{selects, routes}),
			nil, []string{"k8s_ingress_rate_limit"}},
		{"production adds HPA, PDB, TLS and overlays", kubeWF("shop-prod", []model.KubeNode{webDeploy, webService, webIngress}, []model.KubeEdge{selects, routes}),
			[]string{"k8s_ingress_rate_limit", "k8s_hpa", "k8s_pdb", "k8s_ingress_tls", "k8s_environments"}, nil},
		{"no Ingress, no edge items", kubeWF("shop", []model.KubeNode{webDeploy, webService}, []model.KubeEdge{selects}),
			nil, []string{"k8s_ingress_rate_limit", "k8s_ingress_tls"}},
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

// ---------------------------------------------------------------------------
// Every recommendation: metadata, additions, rules and snippets

// terraformKitchenSinks trigger every Terraform recommendation between
// them.
var terraformKitchenSinks = []string{
	restAPI + httpAPI + `
resource "aws_lb" "web" {
  name               = "web"
  load_balancer_type = "application"
  subnets            = ["subnet-1", "subnet-2"]
}

resource "aws_cloudfront_distribution" "cdn" {
  enabled = true
}

resource "aws_iam_role" "fn" {
  name               = "fn"
  assume_role_policy = "{}"
}

resource "aws_lambda_function" "api" {
  function_name = "api"
  role          = aws_iam_role.fn.arn
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  filename      = "function.zip"
  environment {
    variables = {
      DB_PASSWORD = "hunter2"
    }
  }
}

resource "aws_db_instance" "db" {
  identifier          = "db"
  engine              = "postgres"
  instance_class      = "db.t3.micro"
  allocated_storage   = 20
  username            = "app"
  password            = "hunter2hunter2"
  publicly_accessible = true
}

resource "aws_dynamodb_table" "orders" {
  name         = "orders"
  hash_key     = "id"
  billing_mode = "PAY_PER_REQUEST"
  attribute {
    name = "id"
    type = "S"
  }
}

resource "aws_s3_bucket" "assets" {
  bucket = "assets"
}

resource "aws_sqs_queue" "jobs" {
  name = "jobs"
}

resource "aws_sns_topic" "events" {
  name = "events"
}

resource "aws_ecs_cluster" "main" {
  name = "main"
}

resource "aws_ecs_service" "api" {
  name            = "api"
  cluster         = aws_ecs_cluster.main.id
  task_definition = "api:1"
  desired_count   = 1
}

resource "aws_autoscaling_group" "workers" {
  min_size = 1
  max_size = 3
}

resource "aws_cloudwatch_log_group" "misc" {
  name = "misc"
}
`,
	apimDesign + `
resource "azurerm_cdn_frontdoor_profile" "fd" {
  name                = "fd"
  resource_group_name = "rg"
  sku_name            = "Premium_AzureFrontDoor"
}

resource "azurerm_cdn_frontdoor_endpoint" "fd" {
  name                     = "fd"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.fd.id
}

resource "azurerm_application_gateway" "gw" {
  name                = "gw"
  resource_group_name = "rg"
  location            = "westeurope"
}

resource "azurerm_mssql_server" "sql" {
  name                         = "sql"
  resource_group_name          = "rg"
  location                     = "westeurope"
  version                      = "12.0"
  administrator_login          = "app"
  administrator_login_password = "hunter2Hunter2!"
}

resource "azurerm_storage_account" "st" {
  name                     = "st"
  resource_group_name      = "rg"
  location                 = "westeurope"
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

resource "azurerm_servicebus_queue" "orders" {
  name         = "orders"
  namespace_id = "ns"
}

resource "azurerm_linux_virtual_machine_scale_set" "vmss" {
  name                = "vmss"
  resource_group_name = "rg"
  location            = "westeurope"
}
`,
	`
resource "azurerm_service_plan" "plan" {
  name                = "plan"
  resource_group_name = "rg"
  location            = "westeurope"
  os_type             = "Linux"
  sku_name            = "P1v3"
}

resource "azurerm_linux_web_app" "web" {
  name                = "web"
  resource_group_name = "rg"
  location            = "westeurope"
  service_plan_id     = azurerm_service_plan.plan.id
  app_settings = {
    API_TOKEN = "abc"
  }
  site_config {}
}

resource "azurerm_container_app" "api" {
  name                         = "api"
  resource_group_name          = "rg"
  container_app_environment_id = "env"
  revision_mode                = "Single"
  ingress {
    external_enabled = true
    target_port      = 80
  }
}
`,
	armorDesign + `
resource "google_cloud_run_v2_service" "api" {
  name     = "api"
  location = "us-central1"
  template {
    containers {
      image = "us-docker.pkg.dev/cloudrun/container/hello"
      env {
        name  = "API_KEY"
        value = "abc"
      }
    }
  }
}

resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.api.name
  location = google_cloud_run_v2_service.api.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_compute_network" "vpc" {
  name = "vpc"
}

resource "google_sql_database_instance" "db" {
  name             = "db"
  database_version = "POSTGRES_16"
  region           = "us-central1"
  settings {
    tier = "db-f1-micro"
  }
}

resource "google_storage_bucket" "files" {
  name     = "files"
  location = "US"
}

resource "google_pubsub_topic" "events" {
  name = "events"
}

resource "google_pubsub_subscription" "worker" {
  name  = "worker"
  topic = google_pubsub_topic.events.id
}

resource "google_compute_instance_group_manager" "mig" {
  name               = "mig"
  zone               = "us-central1-a"
  base_instance_name = "mig"
  version {
    instance_template = "tpl"
  }
}
`,
	ociLB + `
resource "oci_apigateway_gateway" "gw" {
  compartment_id = var.compartment_id
  endpoint_type  = "PUBLIC"
  subnet_id      = "subnet"
}

resource "oci_apigateway_deployment" "api" {
  compartment_id = var.compartment_id
  gateway_id     = oci_apigateway_gateway.gw.id
  path_prefix    = "/v1"
  specification {
    routes {
      path = "/"
      backend {
        type = "HTTP_BACKEND"
        url  = "https://example.com"
      }
    }
  }
}

resource "oci_database_autonomous_database" "adb" {
  compartment_id = var.compartment_id
  db_name        = "adb"
  admin_password = "Hunter2Hunter2#"
}

resource "oci_objectstorage_bucket" "files" {
  compartment_id = var.compartment_id
  name           = "files"
  namespace      = "ns"
}

resource "oci_queue_queue" "jobs" {
  compartment_id = var.compartment_id
  display_name   = "jobs"
}

resource "oci_core_instance_pool" "pool" {
  compartment_id            = var.compartment_id
  instance_configuration_id = "cfg"
  size                      = 2
}
`,
}

// localWorkflows trigger every docker and Kubernetes recommendation.
func localWorkflows() []*model.Workflow {
	db := svc("db", "postgres", publish("5432", "5432"), func(s *model.ServiceFacts) {
		s.Environment = append(s.Environment, model.ServiceEnv{Key: "POSTGRES_PASSWORD", Secret: true})
	})
	secretEnv := webDeploy
	secretEnv.Workload = &model.KubeWorkload{Replicas: new(3), Selector: map[string]string{"app": "web"}, Containers: []model.KubeContainer{{
		Name: "web", Image: "nginx:1.27", ImageRef: &model.ImageRef{Repo: "nginx", Tag: "1.27"},
		Env: []model.KubeEnv{{Name: "API_TOKEN", Secret: true}}, Ports: []model.KubeContainerPort{{ContainerPort: 80}},
	}}}
	pg := model.KubeNode{ID: "Deployment/shop/db", Kind: "Deployment", APIVersion: "apps/v1", Name: "db", Namespace: "shop", Category: model.KubeCatWorkload,
		Workload: &model.KubeWorkload{Selector: map[string]string{"app": "db"}, Containers: []model.KubeContainer{{Name: "postgres", Image: "postgres:16", ImageRef: &model.ImageRef{Repo: "postgres", Tag: "16"}}}}}
	cm := model.KubeNode{ID: "ConfigMap/shop/config", Kind: "ConfigMap", APIVersion: "v1", Name: "config", Namespace: "shop", Category: model.KubeCatConfig, Data: []string{"DB_PASSWORD", "LOG_LEVEL"}}
	return []*model.Workflow{
		composeWF("shop-prod", svc("web", "node", publish("3000", "3000")), db),
		kubeWF("shop-prod", []model.KubeNode{secretEnv, webService, webIngress, pg, cm}, []model.KubeEdge{selects, routes}),
	}
}

// ruleCodes are every rule code of the three engines.
func ruleCodes() map[string]bool {
	out := map[string]bool{}
	for _, r := range slices.Concat(lint.Rules(), compose.Rules(), kuberules.Rules()) {
		out[r.Code] = true
	}
	return out
}

// allIDs lists every recommendation ID the recommenders can produce.
var allIDs = []string{
	"aws_public_api_waf", "aws_cloudfront_waf", "aws_http_api_edge_waf", "aws_api_throttling", "aws_secrets_manager", "aws_private_database",
	"aws_data_protection", "aws_sqs_dlq", "aws_encryption_cmk", "aws_autoscaling", "aws_alarms", "aws_log_retention", "aws_tracing",
	"azure_apim_rate_limit", "azure_frontdoor_waf", "azure_appgw_waf", "azure_public_app_frontdoor", "azure_key_vault_secrets", "azure_storage_recovery",
	"azure_private_endpoints", "azure_servicebus_dead_letter", "azure_autoscale", "azure_alerts", "azure_app_insights",
	"gcp_cloud_armor", "gcp_cloud_run_edge", "gcp_secret_manager", "gcp_data_protection", "gcp_sql_private_ip", "gcp_pubsub_dead_letter",
	"gcp_encryption_cmek", "gcp_autoscaling", "gcp_alerts",
	"oci_lb_waf", "oci_apigw_rate_limit", "oci_vault_secrets", "oci_bucket_versioning", "oci_queue_dead_letter", "oci_encryption_cmk", "oci_autoscaling", "oci_alarms",
	"terraform_environments",
	"docker_reverse_proxy", "docker_waf_crs", "docker_secrets", "docker_private_data", "docker_data_volumes", "docker_limits_healthchecks", "docker_environments",
	"k8s_ingress_rate_limit", "k8s_ingress_tls", "k8s_secrets", "k8s_network_policy", "k8s_persistent_data", "k8s_resources_probes", "k8s_hpa", "k8s_pdb", "k8s_environments",
}

// Validation codes a snippet may never produce; blocks that merge into an
// existing resource may still miss its required arguments.
var (
	snippetErrors = []string{catalog.CodeUnknownType, catalog.CodeUnknownAttribute, catalog.CodeUnknownBlock, catalog.CodeReadOnlyAttribute, catalog.CodeTooManyBlocks}
	missingCodes  = []string{catalog.CodeMissingAttribute, catalog.CodeMissingBlock}
)

func TestEveryRecommendation(t *testing.T) {
	c := testCatalog(t)
	codes := ruleCodes()
	seen := map[string]bool{}
	check := func(t *testing.T, design *model.Workflow, r rec) {
		t.Helper()
		seen[r.ID] = true
		if !slices.Contains(allIDs, r.ID) {
			t.Errorf("%s: not listed in allIDs", r.ID)
		}
		if r.Title == "" || r.Why == "" || len(r.Pillars) == 0 || len(r.Add) == 0 || r.Snippet == "" {
			t.Errorf("%s: incomplete %+v", r.ID, r.Recommendation)
		}
		if r.Scope != ScopeRecommended && r.Scope != ScopeProduction {
			t.Errorf("%s: scope %q", r.ID, r.Scope)
		}
		if r.Local != LocalRuns && !strings.HasPrefix(r.Local, LocalExportOnly) && !strings.HasPrefix(r.Local, localEquivalent) {
			t.Errorf("%s: local %q", r.ID, r.Local)
		}
		if !strings.HasPrefix(r.Docs, "https://") {
			t.Errorf("%s: docs %q", r.ID, r.Docs)
		}
		for _, code := range r.Rules {
			if !codes[code] {
				t.Errorf("%s: unknown rule code %s", r.ID, code)
			}
		}
		for _, a := range r.Add {
			if a.Type == "" || a.Purpose == "" {
				t.Errorf("%s: incomplete addition %+v", r.ID, a)
			}
			if isTerraformType(a.Type) && !c.HasType(model.KindResource, a.Type) {
				t.Errorf("%s: addition %s is not a resource type of the catalog", r.ID, a.Type)
			}
		}
		if design.IsTerraform() {
			checkHCL(t, c, design, r)
		} else {
			checkYAML(t, r)
		}
	}
	for _, src := range terraformKitchenSinks {
		design := importHCL(t, "kitchen", src)
		recs, _ := candidates(design, c)
		for _, r := range recs {
			check(t, design, r)
		}
	}
	for _, wf := range localWorkflows() {
		recs, _ := candidates(wf, c)
		for _, r := range recs {
			check(t, wf, r)
		}
	}
	for _, id := range allIDs {
		if !seen[id] {
			t.Errorf("%s is never produced by the test designs: its snippet is unchecked", id)
		}
	}
}

// checkHCL imports the snippet as Terraform and validates it against the
// provider schemas.
func checkHCL(t *testing.T, c *catalog.Catalog, design *model.Workflow, r rec) {
	t.Helper()
	wf, diags, err := hclcodec.Import("snippet", map[string][]byte{"main.tf": []byte(r.Snippet)})
	if err != nil {
		t.Errorf("%s: snippet does not parse: %v\n%s", r.ID, err, r.Snippet)
		return
	}
	for _, d := range diags {
		if d.Severity == model.SeverityError {
			t.Errorf("%s: snippet: %s\n%s", r.ID, d.Message, r.Snippet)
		}
	}
	existing := map[string]bool{}
	for i := range design.Nodes {
		existing[hclcodec.Address(&design.Nodes[i])] = true
	}
	addrOf := map[string]string{}
	for i := range wf.Nodes {
		addrOf[wf.Nodes[i].ID] = hclcodec.Address(&wf.Nodes[i])
	}
	for _, d := range c.Validate(wf) {
		switch {
		case slices.Contains(snippetErrors, d.Code):
			t.Errorf("%s: %s: %s (%s)", r.ID, addrOf[d.NodeID], d.Message, d.Attribute)
		case slices.Contains(missingCodes, d.Code) && !existing[addrOf[d.NodeID]]:
			t.Errorf("%s: new block %s: %s", r.ID, addrOf[d.NodeID], d.Message)
		}
	}
	for _, n := range r.Nodes {
		if !existing[n] {
			t.Errorf("%s: node %s is not an address of the design", r.ID, n)
		}
	}
}

// checkYAML parses every document of a YAML snippet.
func checkYAML(t *testing.T, r rec) {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(r.Snippet))
	docs := 0
	for {
		var v map[string]any
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Errorf("%s: snippet is not YAML: %v\n%s", r.ID, err, r.Snippet)
			return
		}
		if len(v) == 0 {
			t.Errorf("%s: empty YAML document\n%s", r.ID, r.Snippet)
		}
		docs++
	}
	if docs == 0 {
		t.Errorf("%s: no YAML document", r.ID)
	}
}

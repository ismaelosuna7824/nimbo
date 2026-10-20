package api

import (
	"maps"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// blockHeader matches the header of a top-level block (resource "a" "b" {).
var blockHeader = regexp.MustCompile(`(?m)^[a-z_]+(?: "[^"]*")*\s*\{`)

func blockHeaders(src string) []string {
	out := blockHeader.FindAllString(src, -1)
	slices.Sort(out)
	return out
}

// applyCode does what apply_terraform_code does: the files are merged over
// the current ones ("" deletes a file) and PUT back. It returns the merged
// files and the saved workflow.
func applyCode(t *testing.T, e *testEnv, id string, edit map[string]string) (map[string]string, model.Workflow) {
	t.Helper()
	var cur struct {
		Files map[string]string `json:"files"`
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+id+"/terraform", nil, &cur)
	files := maps.Clone(cur.Files)
	if files == nil {
		files = map[string]string{}
	}
	for name, content := range edit {
		if content == "" {
			delete(files, name)
		} else {
			files[name] = content
		}
	}
	var wf model.Workflow
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+id+"/terraform", map[string]any{"files": files}, &wf)
	return files, wf
}

// assertWrittenLayout checks that the exported Terraform has exactly the
// written files, each with exactly the blocks written in it.
func assertWrittenLayout(t *testing.T, e *testEnv, id string, written map[string]string) map[string]string {
	t.Helper()
	var got struct {
		Files map[string]string `json:"files"`
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+id+"/terraform", nil, &got)
	if a, b := slices.Sorted(maps.Keys(got.Files)), slices.Sorted(maps.Keys(written)); !slices.Equal(a, b) {
		t.Errorf("exported files %v, written %v", a, b)
	}
	for name, src := range written {
		if a, b := blockHeaders(got.Files[name]), blockHeaders(src); !slices.Equal(a, b) {
			t.Errorf("%s holds\n  %v\nwritten\n  %v", name, a, b)
		}
	}
	return got.Files
}

// checkCodeLayout applies edit to a new workflow seeded with seed, checks
// every block stays in the file it was written in, and that applying edit
// again changes nothing.
func checkCodeLayout(t *testing.T, seed, edit map[string]string) {
	t.Helper()
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": "Shop"}, &wf)
	var seeded model.Workflow
	if seed != nil {
		_, seeded = applyCode(t, e, wf.ID, seed)
	}
	written, first := applyCode(t, e, wf.ID, edit)
	files := assertWrittenLayout(t, e, wf.ID, written)

	// Blocks rewritten in another file keep their node.
	for _, n := range seeded.Nodes {
		if kept := nodeByID(&first, n.ID); kept == nil || kept.Type != n.Type || kept.Name != n.Name {
			t.Errorf("node %s.%s lost its ID", n.Type, n.Name)
		}
	}

	_, second := applyCode(t, e, wf.ID, edit)
	if again := assertWrittenLayout(t, e, wf.ID, written); !reflect.DeepEqual(files, again) {
		t.Error("applying the same code again changed the export")
	}
	fileOf := func(w *model.Workflow) map[string]string {
		out := map[string]string{}
		for _, n := range w.Nodes {
			out[n.ID] = n.File + "|" + w.NodeFiles[n.ID]
		}
		return out
	}
	if a, b := fileOf(&first), fileOf(&second); !reflect.DeepEqual(a, b) {
		t.Errorf("applying the same code again changed the node files:\n%v\n%v", a, b)
	}
}

// The AWS shop: the first apply writes versions/providers/network/alb/dns/
// ecs files and deletes the starter main.tf, whose VPC moves to network.tf.
// Network-category blocks written to alb.tf and dns.tf used to drag the
// network.tf ones along.
func TestPutTerraformKeepsWrittenFilesAWS(t *testing.T) {
	checkCodeLayout(t, map[string]string{
		"main.tf": `resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_s3_bucket" "site" {}
`,
	}, map[string]string{
		"main.tf": "",
		"versions.tf": `terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}
`,
		"providers.tf": `provider "aws" {
  region = var.aws_region
}

provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}
`,
		"variables.tf": `variable "aws_region" {
  type        = string
  description = "AWS region"
  default     = "us-east-1"
}
`,
		"network.tf": `resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_subnet" "public" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.0.0/24"
}

resource "aws_subnet" "app" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.1.0/24"
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
}

resource "aws_eip" "nat" {
  domain = "vpc"
}

resource "aws_nat_gateway" "main" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public.id
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id
}

resource "aws_security_group" "app" {
  vpc_id = aws_vpc.main.id
}
`,
		"alb.tf": `resource "aws_security_group" "alb" {
  vpc_id = aws_vpc.main.id
}

resource "aws_lb" "main" {
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = [aws_subnet.public.id]
}

resource "aws_lb_target_group" "api" {
  vpc_id   = aws_vpc.main.id
  port     = 8080
  protocol = "HTTP"
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.main.arn
  port              = 443
}
`,
		"dns.tf": `resource "aws_route53_zone" "main" {
  name = "shop.example.com"
}

resource "aws_route53_record" "origin" {
  zone_id = aws_route53_zone.main.zone_id
  name    = "origin"
  type    = "A"
}

resource "aws_acm_certificate" "alb" {
  domain_name       = "origin.shop.example.com"
  validation_method = "DNS"
}
`,
		"ecs.tf": `resource "aws_ecs_cluster" "main" {
  name = "shop"
}

resource "aws_ecs_service" "api" {
  cluster = aws_ecs_cluster.main.id
}
`,
		"storage.tf": `resource "aws_s3_bucket" "site" {}
`,
	})
}

// The GCP shop: IAM bindings of Cloud Run, Pub/Sub and secrets written to
// iam.tf used to pull the services, topics and secrets written where the
// recommended layout puts them into iam.tf.
func TestPutTerraformKeepsWrittenFilesGCP(t *testing.T) {
	checkCodeLayout(t, nil, map[string]string{
		"containers.tf": `resource "google_cloud_run_v2_service" "api" {
  name     = "api"
  location = "us-central1"
  template {
    containers {
      image = "us-docker.pkg.dev/cloudrun/container/hello"
    }
  }
}

resource "google_cloud_run_v2_service" "checkout" {
  name     = "checkout"
  location = "us-central1"
  template {
    containers {
      image = "us-docker.pkg.dev/cloudrun/container/hello"
    }
  }
}
`,
		"messaging.tf": `resource "google_pubsub_topic" "orders" {
  name = "orders"
}

resource "google_pubsub_subscription" "orders" {
  name  = "orders"
  topic = google_pubsub_topic.orders.id
}
`,
		"secrets.tf": `resource "google_secret_manager_secret" "db" {
  secret_id = "db"
  replication {
    auto {}
  }
}
`,
		"iam.tf": `resource "google_service_account" "api" {
  account_id = "api"
}

resource "google_cloud_run_v2_service_iam_member" "api_invoker" {
  name   = google_cloud_run_v2_service.api.name
  role   = "roles/run.invoker"
  member = "allUsers"
}

resource "google_pubsub_topic_iam_member" "checkout_publisher" {
  topic  = google_pubsub_topic.orders.id
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:${google_service_account.api.email}"
}

resource "google_secret_manager_secret_iam_member" "api_db" {
  secret_id = google_secret_manager_secret.db.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.api.email}"
}
`,
	})
}

// The Azure shop: main.tf (resource group, random suffix) and network.tf
// written where the recommended layout puts them used to be emptied into
// database.tf and dns.tf.
func TestPutTerraformKeepsWrittenFilesAzure(t *testing.T) {
	checkCodeLayout(t, nil, map[string]string{
		"main.tf": `resource "azurerm_resource_group" "main" {
  name     = "shop"
  location = "eastus"
}

resource "random_string" "suffix" {
  length = 6
}
`,
		"network.tf": `resource "azurerm_virtual_network" "main" {
  name                = "shop"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  address_space       = ["10.0.0.0/16"]
}

resource "azurerm_subnet" "app" {
  name                 = "app"
  resource_group_name  = azurerm_resource_group.main.name
  virtual_network_name = azurerm_virtual_network.main.name
  address_prefixes     = ["10.0.1.0/24"]
}

resource "azurerm_subnet" "data" {
  name                 = "data"
  resource_group_name  = azurerm_resource_group.main.name
  virtual_network_name = azurerm_virtual_network.main.name
  address_prefixes     = ["10.0.2.0/24"]
}

resource "azurerm_network_security_group" "app" {
  name                = "app"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
}
`,
		"dns.tf": `resource "azurerm_dns_zone" "main" {
  name                = "shop.example.com"
  resource_group_name = azurerm_resource_group.main.name
}

resource "azurerm_private_dns_zone" "postgres" {
  name                = "shop.postgres.database.azure.com"
  resource_group_name = azurerm_resource_group.main.name
}
`,
		"database.tf": `resource "azurerm_postgresql_flexible_server" "main" {
  name                = "shop-${random_string.suffix.result}"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  delegated_subnet_id = azurerm_subnet.data.id
  private_dns_zone_id = azurerm_private_dns_zone.postgres.id
}
`,
	})
}

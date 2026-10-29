package lint

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func cloudRunEnv(name, value string) string {
	return `resource "google_cloud_run_v2_service" "api" {
  name     = "api"
  location = var.gcp_region
  template {
    containers {
      image = "app"
      env {
        name  = "` + name + `"
        value = ` + value + `
      }
    }
  }
}`
}

func TestEmulatorHostLoopback(t *testing.T) {
	const code = "bp_emulator_host_loopback"
	cases := []ruleCase{
		{"cloud run literal localhost", cloudRunEnv("PUBSUB_EMULATOR_HOST", `"localhost:8085"`), 1},
		{"cloud run floci subdomain", cloudRunEnv("STORAGE_EMULATOR_HOST", `"http://storage.localhost.floci.io:4588"`), 1},
		{"cloud run ipv6 loopback", cloudRunEnv("FIRESTORE_EMULATOR_HOST", `"[::1]:8080"`), 1},
		{"cloud run var reference", cloudRunEnv("PUBSUB_EMULATOR_HOST", `var.gcp_emulator_host`), 0},
		{"cloud run nimbo pod host", cloudRunEnv("PUBSUB_EMULATOR_HOST", `"nimbo-orders-pod:4588"`), 0},
		{"cloud run real host", cloudRunEnv("PUBSUB_EMULATOR_HOST", `"pubsub-emulator.internal:8085"`), 0},
		{"cloud run unrelated env", cloudRunEnv("API_URL", `"http://localhost:8080"`), 0},
		{"lambda endpoint", `resource "aws_lambda_function" "f" {
  function_name = "f"
  role          = "r"
  environment {
    variables = {
      AWS_ENDPOINT_URL_SQS = "http://localhost.floci.io:4566"
      TABLE                = "t"
    }
  }
}`, 1},
		{"lambda reference", `resource "aws_lambda_function" "f" {
  function_name = "f"
  role          = "r"
  environment {
    variables = { AWS_ENDPOINT_URL = var.endpoint }
  }
}`, 0},
		{"cloud functions", `resource "google_cloudfunctions2_function" "f" {
  name = "f"
  service_config {
    environment_variables = { PUBSUB_EMULATOR_HOST = "127.0.0.1:8085" }
  }
}`, 1},
		{"ecs jsonencode", `resource "aws_ecs_task_definition" "t" {
  family = "t"
  container_definitions = jsonencode([{
    name  = "app"
    image = var.image
    environment = [{ name = "AWS_ENDPOINT_URL", value = "http://localhost:4566" }]
  }])
}`, 1},
		{"ecs heredoc json", `resource "aws_ecs_task_definition" "t" {
  family = "t"
  container_definitions = <<EOF
[{"name": "app", "environment": [{"name": "AWS_ENDPOINT_URL", "value": "http://localhost:4566"}]}]
EOF
}`, 1},
		{"container app", `resource "azurerm_container_app" "a" {
  name = "a"
  template {
    container {
      name = "app"
      env {
        name  = "STORAGE_EMULATOR_HOST"
        value = "localhost:4443"
      }
    }
  }
}`, 1},
		{"function app settings", `resource "azurerm_linux_function_app" "a" {
  name         = "a"
  app_settings = { AWS_ENDPOINT_URL = "http://localhost:4566", MODE = "x" }
}`, 1},
		{"other resource type", `resource "aws_ssm_parameter" "p" {
  name  = "p"
  type  = "String"
  value = "localhost:8085"
  tags  = { PUBSUB_EMULATOR_HOST = "localhost:8085" }
}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findings(t, tc.src, code)
			if len(got) != tc.want {
				t.Fatalf("got %d findings, want %d: %+v", len(got), tc.want, got)
			}
			for _, d := range got {
				if d.Severity != model.SeverityWarning || d.Fix == "" || d.NodeID == "" || d.Attribute == "" {
					t.Errorf("incomplete finding %+v", d)
				}
			}
		})
	}
}

func TestEmulatorHostLoopbackEnvironments(t *testing.T) {
	envFindings := func(values map[string]string) []model.Diagnostic {
		t.Helper()
		wf := importHCL(t, `variable "extra_env" {
  type        = map(string)
  description = "x"
}`)
		env := model.Environment{Name: "dev", Values: map[string]model.Value{}}
		for k, v := range values {
			env.Values[k] = model.Value{Expr: v}
		}
		wf.Environments = []model.Environment{env}
		var out []model.Diagnostic
		for _, d := range Check(wf, testCatalog, model.BestPracticeSettings{}, nil) {
			if d.Code == "bp_emulator_host_loopback" {
				out = append(out, d)
			}
		}
		return out
	}
	got := envFindings(map[string]string{"extra_env": `{ PUBSUB_EMULATOR_HOST = "localhost.floci.io:4588" }`})
	if len(got) != 1 || !strings.Contains(got[0].Message, `"extra_env"`) || !strings.Contains(got[0].Message, `"dev"`) {
		t.Fatalf("map value: %+v", got)
	}
	// A literal (JSON) value as the environment editor stores it.
	wf := importHCL(t, `variable "extra_env" {
  type        = map(string)
  description = "x"
}`)
	raw, _ := json.Marshal(map[string]string{"AWS_ENDPOINT_URL": "http://127.0.0.1:4566"})
	wf.Environments = []model.Environment{{Name: "dev", Values: map[string]model.Value{"extra_env": {Literal: raw}}}}
	n := 0
	for _, d := range Check(wf, testCatalog, model.BestPracticeSettings{}, nil) {
		if d.Code == "bp_emulator_host_loopback" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("literal map value: %d findings", n)
	}
	for _, v := range []string{
		`{ PUBSUB_EMULATOR_HOST = "nimbo-orders-pod:4588" }`,
		`{ PUBSUB_EMULATOR_HOST = var.host }`,
		`{ API_URL = "http://localhost:8080" }`,
		`"localhost:8085"`,
	} {
		if got := envFindings(map[string]string{"extra_env": v}); len(got) != 0 {
			t.Errorf("%s: unexpected %+v", v, got)
		}
	}
}

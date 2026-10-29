package codeguide

import (
	"fmt"
	"strings"
)

// lambdaRuntime is the Lambda runtime and handler per language, matching
// the skeletons (all five runtimes verified on Floci).
var lambdaRuntime = map[string][2]string{
	"javascript": {"nodejs22.x", "index.handler"},
	"typescript": {"nodejs22.x", "index.handler"},
	"python":     {"python3.12", "handler.handler"},
	"go":         {"provided.al2023", "bootstrap"},
	"java":       {"java21", "com.example.api.Handler::handleRequest"},
	"csharp":     {"dotnet8", "Api::Api.Function::HandleAsync"},
}

// needsBuild reports whether the function artifact comes from a package
// stage (dist) instead of the source folder (src).
func needsBuild(language string) bool {
	return language != "javascript" && language != "python"
}

func packaging(o Options) Packaging {
	svc := serviceName(o.Style)
	dir := "services/" + svc
	var p Packaging
	switch o.Target {
	case TargetFunction:
		p = functionPackaging(o, svc, dir)
	case TargetVM:
		p = vmPackaging(o, svc, dir)
	default:
		p = containerPackaging(o, svc, dir)
	}
	if strings.Contains(p.Terraform, "var."+svc+"_image") {
		p.Terraform = imageVariable(svc) + "\n" + p.Terraform
	}
	return p
}

// imageVariable declares the service image. The nimbo.local/<svc>:latest
// default is built by Nimbo from services/<svc>/Dockerfile (runtime stage)
// before Terraform runs; set it to a registry reference for the real cloud.
func imageVariable(svc string) string {
	return fmt.Sprintf(`variable "%[1]s_image" {
  type        = string
  description = "Image of services/%[1]s: nimbo.local/%[1]s:latest is built by Nimbo locally; set a registry reference for the real cloud."
  default     = "nimbo.local/%[1]s:latest"
}
`, svc)
}

func functionPackaging(o Options, svc, dir string) Packaging {
	if o.Platform == "oci" {
		return Packaging{
			Kind: "image",
			How:  "OCI Functions run container images from the Dockerfile's runtime stage (Fn FDK base image). " + imageHow + " config values become env vars: keep secrets in OCI Vault.",
			Terraform: fmt.Sprintf(`resource "oci_functions_application" "%[1]s" {
  compartment_id = var.compartment_ocid
  display_name   = "%[1]s"
  subnet_ids     = [oci_core_subnet.private.id]
}

resource "oci_functions_function" "%[1]s" {
  application_id = oci_functions_application.%[1]s.id
  display_name   = "%[1]s"
  image          = var.%[1]s_image
  memory_in_mbs  = "256"
  config = {
    LOG_LEVEL = "info"
  }
}
`, svc),
		}
	}
	kind, src := "zip", dir+"/src"
	how := "Terraform zips " + src + " directly (no build): the runtime provides the SDK."
	if needsBuild(o.Language) {
		kind, src = "build-zip", dir+"/dist"
		how = "Nimbo builds the Dockerfile's package stage into " + src + " before every plan/apply/destroy (docker build --target package --output type=local,dest=" + src + " " + dir + "), then Terraform zips it. dist is generated: never edit or commit it."
	}
	archive := fmt.Sprintf(`data "archive_file" "%[1]s" {
  type        = "zip"
  source_dir  = "${path.module}/%[2]s"
  output_path = "${path.module}/build/%[1]s.zip"
}
`, svc, src)
	entry := "api"
	if o.Mode == ModeArchitecture {
		entry = "smoke"
	}
	switch o.Platform {
	case "azure":
		stack := `node_version = "20"`
		if o.Language == "python" {
			stack = `python_version = "3.12"`
		}
		remote := "true"
		if kind == "build-zip" {
			remote = "false"
		}
		return Packaging{
			Kind: kind,
			How: how + " zip_deploy_file deploys the zip; SCM_DO_BUILD_DURING_DEPLOYMENT=true makes Azure install package.json/requirements.txt dependencies (remote build). Routes are under /api. " +
				"Azure Functions are export only in Nimbo's sandbox: run the same code locally as a container (target container, Azure Container Apps).",
			Terraform: archive + fmt.Sprintf(`
resource "azurerm_service_plan" "%[1]s" {
  name                = "%[1]s-plan"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  os_type             = "Linux"
  sku_name            = "Y1"
}

resource "azurerm_linux_function_app" "%[1]s" {
  name                       = "%[1]s-functions"
  resource_group_name        = azurerm_resource_group.main.name
  location                   = azurerm_resource_group.main.location
  service_plan_id            = azurerm_service_plan.%[1]s.id
  storage_account_name       = azurerm_storage_account.functions.name
  storage_account_access_key = azurerm_storage_account.functions.primary_access_key
  zip_deploy_file            = data.archive_file.%[1]s.output_path
  app_settings = {
    SCM_DO_BUILD_DURING_DEPLOYMENT = "%[2]s"
  }
  site_config {
    application_stack {
      %[3]s
    }
  }
}
`, svc, remote, stack),
		}
	case "gcp":
		runtime := "nodejs22"
		if o.Language == "python" {
			runtime = "python312"
		}
		return Packaging{
			Kind: kind,
			How: how + " The zip is uploaded to a bucket (object name carries the hash so changes redeploy) and Cloud Build installs package.json/requirements.txt dependencies. " +
				"Cloud Functions are export only in Nimbo's sandbox: run the same code locally on Cloud Run (target container).",
			Terraform: archive + fmt.Sprintf(`
resource "google_storage_bucket" "functions" {
  name                        = "${var.gcp_project}-functions"
  location                    = var.gcp_region
  uniform_bucket_level_access = true
}

resource "google_storage_bucket_object" "%[1]s" {
  name   = "%[1]s-${data.archive_file.%[1]s.output_md5}.zip"
  bucket = google_storage_bucket.functions.name
  source = data.archive_file.%[1]s.output_path
}

resource "google_cloudfunctions2_function" "%[1]s" {
  name     = "%[1]s"
  location = var.gcp_region
  build_config {
    runtime     = "%[2]s"
    entry_point = "%[3]s"
    source {
      storage_source {
        bucket = google_storage_bucket.functions.name
        object = google_storage_bucket_object.%[1]s.name
      }
    }
  }
  service_config {
    max_instance_count = 10
    available_memory   = "256M"
    timeout_seconds    = 30
  }
}
`, svc, runtime, entry),
		}
	}
	rt := lambdaRuntime[o.Language]
	env := "TABLE_NAME = aws_dynamodb_table.items.name"
	if o.Mode == ModeArchitecture {
		env = "TABLE_NAME = aws_dynamodb_table.items.name # one env var per connected store: BUCKET_NAME, QUEUE_URL, TOPIC_ARN"
	}
	if o.Language == "go" {
		how += " The binary is built for the builder's CPU: on real AWS set architectures to match (arm64 on Apple silicon/Graviton builders)."
	}
	return Packaging{
		Kind: kind,
		How:  how + " Runs locally on Floci (nodejs22.x, python3.12, provided.al2023, java21 and dotnet8 verified).",
		Terraform: archive + fmt.Sprintf(`
resource "aws_lambda_function" "%[1]s" {
  function_name    = "%[1]s"
  role             = aws_iam_role.%[1]s.arn
  runtime          = "%[2]s"
  handler          = "%[3]s"
  filename         = data.archive_file.%[1]s.output_path
  source_code_hash = data.archive_file.%[1]s.output_base64sha256
  timeout          = 10
  memory_size      = 512
  environment {
    variables = {
      %[4]s
    }
  }
}
`, svc, rt[0], rt[1], env),
	}
}

const imageHow = "The image variable defaults to nimbo.local/<svc>:latest: Nimbo builds it from services/<svc>/Dockerfile (runtime stage) before Terraform runs, so the local sandbox needs no registry. For the real cloud push the runtime stage to the platform registry (in CI) and set the variable to that reference."

func containerPackaging(o Options, svc, dir string) Packaging {
	switch o.Platform {
	case "docker":
		return Packaging{Kind: "compose-build", How: fmt.Sprintf(`Nimbo builds the service from its Dockerfile on up. In compose.yaml:
services:
  %[1]s:
    build: ./%[2]s
    ports: ["8080:8080"]
    environment:
      DATABASE_URL: postgres://app@db:5432/app
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/health"]`, svc, dir)}
	case "kubernetes":
		return Packaging{Kind: "k8s-image", How: fmt.Sprintf(`Nimbo builds images whose repository (no registry host) names a folder with a Dockerfile and loads them into the local k3s node: image %[1]s:dev builds %[2]s. In the Deployment:
containers:
  - name: %[1]s
    image: %[1]s:dev
    ports: [{containerPort: 8080}]
    readinessProbe: {httpGet: {path: /health, port: 8080}}
    envFrom: [{secretRef: {name: %[1]s-env}}]`, svc, dir)}
	case "azure":
		return Packaging{Kind: "image", How: imageHow + " Locally the Container App deploys but its container does not run (control plane only): test the code with the docker platform or Cloud Run/ECS, or export.", Terraform: fmt.Sprintf(`resource "azurerm_container_app" "%[1]s" {
  name                         = "%[1]s"
  container_app_environment_id = azurerm_container_app_environment.main.id
  resource_group_name          = azurerm_resource_group.main.name
  revision_mode                = "Single"
  template {
    min_replicas = 0
    max_replicas = 5
    container {
      name   = "%[1]s"
      image  = var.%[1]s_image
      cpu    = 0.5
      memory = "1Gi"
    }
  }
  ingress {
    external_enabled = true
    target_port      = 8080
    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }
}
`, svc)}
	case "gcp":
		return Packaging{Kind: "image", How: imageHow + " Cloud Run runs the local image in Nimbo's sandbox.", Terraform: fmt.Sprintf(`resource "google_cloud_run_v2_service" "%[1]s" {
  name     = "%[1]s"
  location = var.gcp_region
  template {
    containers {
      image = var.%[1]s_image
      ports {
        container_port = 8080
      }
    }
    scaling {
      max_instance_count = 5
    }
  }
}
`, svc)}
	case "oci":
		return Packaging{Kind: "image", How: imageHow + " OCI Container Instances are export only in Nimbo's sandbox.", Terraform: fmt.Sprintf(`resource "oci_container_instances_container_instance" "%[1]s" {
  compartment_id      = var.compartment_ocid
  availability_domain = data.oci_identity_availability_domains.ads.availability_domains[0].name
  display_name        = "%[1]s"
  shape               = "CI.Standard.E4.Flex"
  shape_config {
    ocpus         = 1
    memory_in_gbs = 4
  }
  containers {
    image_url    = var.%[1]s_image
    display_name = "%[1]s"
  }
  vnics {
    subnet_id = oci_core_subnet.private.id
  }
}
`, svc)}
	}
	return Packaging{Kind: "image", How: imageHow + " ECS tasks run the local image in Nimbo's sandbox (the first start takes about a minute). Put an Application Load Balancer target group (port 8080, health check /health) in front for HTTP.", Terraform: fmt.Sprintf(`resource "aws_ecs_task_definition" "%[1]s" {
  family                   = "%[1]s"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.%[1]s_execution.arn
  task_role_arn            = aws_iam_role.%[1]s_task.arn
  container_definitions = jsonencode([{
    name         = "%[1]s"
    image        = var.%[1]s_image
    essential    = true
    portMappings = [{ containerPort = 8080 }]
    environment  = [{ name = "TABLE_NAME", value = aws_dynamodb_table.items.name }]
  }])
}

resource "aws_ecs_service" "%[1]s" {
  name            = "%[1]s"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.%[1]s.arn
  desired_count   = 2
  launch_type     = "FARGATE"
  network_configuration {
    subnets         = aws_subnet.private[*].id
    security_groups = [aws_security_group.%[1]s.id]
  }
}
`, svc)}
}

func vmPackaging(o Options, svc, dir string) Packaging {
	tpl := fmt.Sprintf(`templatefile("${path.module}/%s/deploy/cloud-init.yaml.tftpl", {
    image          = var.%s_image
    registry_login = var.registry_login
    env            = { PORT = "8080" }
  })`, dir, svc)
	how := "The VM runs the service's container image under systemd: cloud-init (" + dir + "/deploy/cloud-init.yaml.tftpl) installs Docker, logs in to the registry (registry_login: a command using the instance identity) and starts it. " +
		imageHow + " Scale with the instance group and health-check /health behind the load balancer."
	var tf string
	switch o.Platform {
	case "azure":
		tf = fmt.Sprintf(`resource "azurerm_linux_virtual_machine_scale_set" "%[1]s" {
  name                = "%[1]s"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  sku                 = "Standard_B2s"
  instances           = 2
  admin_username      = "azureuser"
  custom_data         = base64encode(%[2]s)
  admin_ssh_key {
    username   = "azureuser"
    public_key = var.ssh_public_key
  }
  source_image_reference {
    publisher = "Canonical"
    offer     = "ubuntu-24_04-lts"
    sku       = "server"
    version   = "latest"
  }
  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "Standard_LRS"
  }
  network_interface {
    name    = "%[1]s"
    primary = true
    ip_configuration {
      name                                   = "internal"
      primary                                = true
      subnet_id                              = azurerm_subnet.app.id
      load_balancer_backend_address_pool_ids = [azurerm_lb_backend_address_pool.%[1]s.id]
    }
  }
}
`, svc, tpl)
	case "gcp":
		tf = fmt.Sprintf(`resource "google_compute_instance_template" "%[1]s" {
  name_prefix  = "%[1]s-"
  machine_type = "e2-small"
  disk {
    source_image = "debian-cloud/debian-12"
    boot         = true
  }
  network_interface {
    subnetwork = google_compute_subnetwork.app.id
  }
  metadata = {
    user-data = %[2]s
  }
  lifecycle {
    create_before_destroy = true
  }
}

resource "google_compute_region_instance_group_manager" "%[1]s" {
  name               = "%[1]s"
  region             = var.gcp_region
  base_instance_name = "%[1]s"
  target_size        = 2
  version {
    instance_template = google_compute_instance_template.%[1]s.id
  }
  named_port {
    name = "http"
    port = 80
  }
}
`, svc, tpl)
	case "oci":
		tf = fmt.Sprintf(`resource "oci_core_instance_configuration" "%[1]s" {
  compartment_id = var.compartment_ocid
  display_name   = "%[1]s"
  instance_details {
    instance_type = "compute"
    launch_details {
      compartment_id = var.compartment_ocid
      shape          = "VM.Standard.E4.Flex"
      shape_config {
        ocpus         = 1
        memory_in_gbs = 4
      }
      source_details {
        source_type = "image"
        image_id    = var.image_ocid
      }
      create_vnic_details {
        subnet_id = oci_core_subnet.app.id
      }
      metadata = {
        user_data = base64encode(%[2]s)
      }
    }
  }
}

resource "oci_core_instance_pool" "%[1]s" {
  compartment_id            = var.compartment_ocid
  instance_configuration_id = oci_core_instance_configuration.%[1]s.id
  size                      = 2
  placement_configurations {
    availability_domain = data.oci_identity_availability_domains.ads.availability_domains[0].name
    primary_subnet_id   = oci_core_subnet.app.id
  }
}
`, svc, tpl)
	default:
		tf = fmt.Sprintf(`resource "aws_launch_template" "%[1]s" {
  name_prefix            = "%[1]s-"
  image_id               = data.aws_ssm_parameter.al2023.value
  instance_type          = "t3.small"
  vpc_security_group_ids = [aws_security_group.%[1]s.id]
  user_data              = base64encode(%[2]s)
  iam_instance_profile {
    name = aws_iam_instance_profile.%[1]s.name
  }
  metadata_options {
    http_tokens = "required"
  }
}

resource "aws_autoscaling_group" "%[1]s" {
  name                = "%[1]s"
  min_size            = 2
  max_size            = 4
  vpc_zone_identifier = aws_subnet.private[*].id
  target_group_arns   = [aws_lb_target_group.%[1]s.arn]
  health_check_type   = "ELB"
  launch_template {
    id      = aws_launch_template.%[1]s.id
    version = "$Latest"
  }
}
`, svc, tpl)
	}
	return Packaging{Kind: "vm-image", How: how, Terraform: strings.TrimLeft(tf, "\n")}
}

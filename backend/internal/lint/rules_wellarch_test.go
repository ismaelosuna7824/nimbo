package lint

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// rds renders an aws_db_instance "db" with extra attributes.
func rds(extra string) string {
	return `resource "aws_db_instance" "db" {
  identifier        = "app"
  engine            = "postgres"
  instance_class    = "db.t3.micro"
  allocated_storage = 20
` + extra + `
}
`
}

// sqsWorker renders a queue consumed by a Lambda function.
func sqsWorker(queue string) string {
	return `resource "aws_sqs_queue" "jobs" {
  name = "jobs"
` + queue + `
}
resource "aws_lambda_function" "worker" {
  function_name = "worker"
  role          = "arn"
  runtime       = "nodejs22.x"
  handler       = "index.handler"
}
resource "aws_lambda_event_source_mapping" "jobs" {
  event_source_arn = aws_sqs_queue.jobs.arn
  function_name    = aws_lambda_function.worker.arn
}
`
}

// snsLambda renders a function subscribed to an SNS topic.
const snsLambda = `resource "aws_sns_topic" "events" { name = "events" }
resource "aws_lambda_function" "f" {
  function_name = "f"
  role          = "arn"
  runtime       = "nodejs22.x"
  handler       = "index.handler"
}
resource "aws_sns_topic_subscription" "f" {
  topic_arn = aws_sns_topic.events.arn
  protocol  = "lambda"
  endpoint  = aws_lambda_function.f.arn
}
`

const ecsService = `resource "aws_ecs_cluster" "c" { name = "c" }
resource "aws_ecs_service" "s" {
  name            = "web"
  cluster         = aws_ecs_cluster.c.id
  task_definition = "arn"
  desired_count   = 2
}
`

const awsAppSubnets = `resource "aws_subnet" "a" {
  vpc_id     = "vpc-1"
  cidr_block = "10.0.1.0/24"
}
resource "aws_subnet" "b" {
  vpc_id     = "vpc-1"
  cidr_block = "10.0.2.0/24"
}
`

func asg(extra string) string {
	return awsAppSubnets + `resource "aws_autoscaling_group" "app" {
  min_size = 1
` + extra + `
}
`
}

func dynamo(extra string) string {
	return `resource "aws_dynamodb_table" "t" {
  name     = "orders"
  hash_key = "id"
  attribute {
    name = "id"
    type = "S"
  }
` + extra + `
}
`
}

func azFlexible(extra string) string {
	return `resource "azurerm_postgresql_flexible_server" "db" {
  name                = "db"
  resource_group_name = "rg"
  location            = "westeurope"
` + extra + `
}
`
}

func azMSSQL(extra string) string {
	return `resource "azurerm_mssql_database" "db" {
  name      = "db"
  server_id = "id"
` + extra + `
}
`
}

func azPlan(sku, extra string) string {
	return `resource "azurerm_service_plan" "plan" {
  name                = "plan"
  resource_group_name = "rg"
  location            = "westeurope"
  os_type             = "Linux"
  sku_name            = ` + sku + `
` + extra + `
}
`
}

const azAutoscaleSetting = `resource "azurerm_monitor_autoscale_setting" "a" {
  name                = "a"
  resource_group_name = "rg"
  location            = "westeurope"
  target_resource_id  = %s
  profile {
    name = "default"
    capacity {
      default = 2
      minimum = 2
      maximum = 5
    }
  }
}
`

func gcpSQLHA(settings string) string {
	return `resource "google_sql_database_instance" "db" {
  name             = "db"
  database_version = "POSTGRES_16"
  settings {
    tier = "db-custom-1-3840"
` + settings + `
  }
}
`
}

func gcpMIG(autoscaler string) string {
	return `resource "google_compute_region_instance_group_manager" "app" {
  name               = "app"
  base_instance_name = "app"
  region             = "us-central1"
  target_size        = 2
  version { instance_template = "tpl" }
}
` + autoscaler
}

func cloudRun(template string) string {
	return `resource "google_cloud_run_v2_service" "s" {
  name     = "s"
  location = "us-central1"
  template {
    containers { image = "us-docker.pkg.dev/cloudrun/container/hello" }
` + template + `
  }
}
`
}

func ociDBSystem(database string) string {
	return `resource "oci_database_db_system" "db" {
  availability_domain = "AD-1"
  compartment_id      = "ocid"
  shape               = "VM.Standard2.1"
  subnet_id           = "ocid"
  ssh_public_keys     = ["key"]
  hostname            = "db"
  db_home {
    database {
      admin_password = "pw"
` + database + `
    }
  }
}
`
}

const ociPool = `resource "oci_core_instance_pool" "pool" {
  compartment_id            = "ocid"
  instance_configuration_id = "ocid"
  size                      = 2
  placement_configurations {
    availability_domain = "AD-1"
    primary_subnet_id   = "ocid"
  }
}
`

// wellArchCases run with the other rule cases (TestRules).
var wellArchCases = map[string][]structureCase{
	"bp_rds_backup_retention": {
		{name: "backups off", src: rds(`  backup_retention_period = 0`), want: 1},
		{name: "default retention", src: rds(""), want: 0},
		{name: "seven days", src: rds(`  backup_retention_period = 7`), want: 0},
		{name: "expression", src: rds(`  backup_retention_period = var.backup_days`), want: 0},
		{name: "read replica", src: rds(`  replicate_source_db     = "primary"
  backup_retention_period = 0`), want: 0},
	},
	"bp_rds_deletion_protection": {
		{name: "unset", src: rds(""), want: 1},
		{name: "off", src: rds(`  deletion_protection = false`), want: 1},
		{name: "on", src: rds(`  deletion_protection = true`), want: 0},
		{name: "expression", src: rds(`  deletion_protection = var.protect`), want: 0},
		{name: "aurora cluster", src: `resource "aws_rds_cluster" "c" { engine = "aurora-postgresql" }`, want: 1},
		{name: "aurora member instance", src: `resource "aws_db_instance" "m" {
  engine             = "aurora-postgresql"
  instance_class     = "db.r6g.large"
  cluster_identifier = "c"
}`, want: 0},
	},
	"bp_rds_storage_autoscaling": {
		{name: "fixed", src: rds(""), want: 1},
		{name: "zero", src: rds(`  max_allocated_storage = 0`), want: 1},
		{name: "autoscaling", src: rds(`  max_allocated_storage = 100`), want: 0},
		{name: "expression", src: rds(`  max_allocated_storage = var.max_storage`), want: 0},
	},
	"bp_dynamodb_pitr": {
		{name: "none", src: dynamo(""), want: 1},
		{name: "disabled", src: dynamo(`  point_in_time_recovery { enabled = false }`), want: 1},
		{name: "enabled", src: dynamo(`  point_in_time_recovery { enabled = true }`), want: 0},
		{name: "expression", src: dynamo(`  point_in_time_recovery { enabled = var.pitr }`), want: 0},
	},
	"bp_s3_versioning": {
		{name: "no versioning", src: `resource "aws_s3_bucket" "b" { bucket = "data" }`, want: 1},
		{name: "enabled", src: `resource "aws_s3_bucket" "b" { bucket = "data" }
resource "aws_s3_bucket_versioning" "b" {
  bucket = aws_s3_bucket.b.id
  versioning_configuration { status = "Enabled" }
}`, want: 0},
		{name: "suspended", src: `resource "aws_s3_bucket" "b" { bucket = "data" }
resource "aws_s3_bucket_versioning" "b" {
  bucket = aws_s3_bucket.b.id
  versioning_configuration { status = "Suspended" }
}`, want: 1},
		{name: "status expression", src: `resource "aws_s3_bucket" "b" { bucket = "data" }
resource "aws_s3_bucket_versioning" "b" {
  bucket = aws_s3_bucket.b.id
  versioning_configuration { status = var.versioning }
}`, want: 0},
		{name: "versioning of an unknown bucket", src: `resource "aws_s3_bucket" "b" { bucket = "data" }
resource "aws_s3_bucket_versioning" "b" {
  bucket = var.bucket
  versioning_configuration { status = "Enabled" }
}`, want: 0},
		{name: "versioning of another bucket", src: `resource "aws_s3_bucket" "b" { bucket = "data" }
resource "aws_s3_bucket" "logs" { bucket = "logs" }
resource "aws_s3_bucket_versioning" "logs" {
  bucket = aws_s3_bucket.logs.id
  versioning_configuration { status = "Enabled" }
}`, want: 1},
		{name: "website", src: `resource "aws_s3_bucket" "b" { bucket = "site" }
resource "aws_s3_bucket_website_configuration" "b" {
  bucket = aws_s3_bucket.b.id
  index_document { suffix = "index.html" }
}`, want: 0},
	},
	"bp_elasticache_failover": {
		{name: "defaults", src: `resource "aws_elasticache_replication_group" "r" {
  replication_group_id = "r"
  description          = "r"
}`, want: 1},
		{name: "failover only", src: `resource "aws_elasticache_replication_group" "r" {
  replication_group_id       = "r"
  description                = "r"
  automatic_failover_enabled = true
}`, want: 1},
		{name: "multi-AZ failover", src: `resource "aws_elasticache_replication_group" "r" {
  replication_group_id       = "r"
  description                = "r"
  num_cache_clusters         = 2
  automatic_failover_enabled = true
  multi_az_enabled           = true
}`, want: 0},
		{name: "expressions", src: `resource "aws_elasticache_replication_group" "r" {
  replication_group_id       = "r"
  description                = "r"
  automatic_failover_enabled = var.ha
  multi_az_enabled           = var.ha
}`, want: 0},
	},
	"bp_sqs_dlq": {
		{name: "no DLQ", src: sqsWorker(""), want: 1},
		{name: "redrive policy", src: sqsWorker(`  redrive_policy = jsonencode({ deadLetterTargetArn = "arn", maxReceiveCount = 5 })`), want: 0},
		{name: "redrive policy resource", src: sqsWorker("") + `resource "aws_sqs_queue_redrive_policy" "jobs" {
  queue_url      = aws_sqs_queue.jobs.id
  redrive_policy = jsonencode({ deadLetterTargetArn = "arn", maxReceiveCount = 5 })
}`, want: 0},
		{name: "redrive policy resource for an unknown queue", src: sqsWorker("") + `resource "aws_sqs_queue_redrive_policy" "jobs" {
  queue_url      = var.queue_url
  redrive_policy = "{}"
}`, want: 0},
		{name: "queue without consumer", src: `resource "aws_sqs_queue" "jobs" { name = "jobs" }`, want: 0},
	},
	"bp_lambda_async_failure": {
		{name: "SNS subscription", src: snsLambda, want: 1},
		{name: "dead letter config", src: strings.Replace(snsLambda, `handler       = "index.handler"`, `handler       = "index.handler"
  dead_letter_config { target_arn = "arn" }`, 1), want: 0},
		{name: "on-failure destination", src: snsLambda + `resource "aws_lambda_function_event_invoke_config" "f" {
  function_name = aws_lambda_function.f.function_name
  destination_config {
    on_failure { destination = "arn" }
  }
}`, want: 0},
		{name: "invoke config without on-failure", src: snsLambda + `resource "aws_lambda_function_event_invoke_config" "f" {
  function_name          = aws_lambda_function.f.function_name
  maximum_retry_attempts = 0
}`, want: 1},
		{name: "invoke config for an unknown function", src: snsLambda + `resource "aws_lambda_function_event_invoke_config" "f" {
  function_name = var.function
}`, want: 0},
		{name: "EventBridge target", src: `resource "aws_lambda_function" "f" {
  function_name = "f"
  role          = "arn"
}
resource "aws_cloudwatch_event_target" "t" {
  rule = "r"
  arn  = aws_lambda_function.f.arn
}`, want: 1},
		{name: "SQS subscription", src: strings.Replace(snsLambda, `protocol  = "lambda"`, `protocol  = "sqs"`, 1), want: 0},
	},
	"bp_lambda_stream_on_failure": {
		{name: "kinesis without destination", src: `resource "aws_kinesis_stream" "s" { name = "s" }
resource "aws_lambda_event_source_mapping" "m" {
  event_source_arn  = aws_kinesis_stream.s.arn
  function_name     = "f"
  starting_position = "LATEST"
}`, want: 1},
		{name: "on-failure destination", src: `resource "aws_lambda_event_source_mapping" "m" {
  event_source_arn  = var.stream_arn
  function_name     = "f"
  starting_position = "LATEST"
  destination_config {
    on_failure { destination_arn = "arn" }
  }
}`, want: 0},
		{name: "unknown source with a starting position", src: `resource "aws_lambda_event_source_mapping" "m" {
  event_source_arn  = var.stream_arn
  function_name     = "f"
  starting_position = "TRIM_HORIZON"
}`, want: 1},
		{name: "SQS source", src: sqsWorker(""), want: 0},
	},
	"bp_asg_single_az": {
		{name: "one subnet", src: asg(`  max_size = 2
  vpc_zone_identifier = [aws_subnet.a.id]`), want: 1},
		{name: "two subnets", src: asg(`  max_size = 2
  vpc_zone_identifier = [aws_subnet.a.id, aws_subnet.b.id]`), want: 0},
		{name: "one availability zone", src: asg(`  max_size = 2
  availability_zones = ["us-east-1a"]`), want: 1},
		{name: "expression", src: asg(`  max_size = 2
  vpc_zone_identifier = var.subnet_ids`), want: 0},
		{name: "splat", src: asg(`  max_size = 2
  vpc_zone_identifier = [aws_subnet.a[*].id]`), want: 0},
	},
	"bp_asg_single_instance": {
		{name: "one instance behind a load balancer", src: asg(`  max_size = 1
  target_group_arns = ["arn"]`), want: 1},
		{name: "scales out", src: asg(`  max_size = 4
  target_group_arns = ["arn"]`), want: 0},
		{name: "worker without a load balancer", src: asg(`  max_size = 1`), want: 0},
		{name: "expression", src: asg(`  max_size = var.max
  target_group_arns = ["arn"]`), want: 0},
	},
	"bp_ecs_circuit_breaker": {
		{name: "none", src: ecsService, want: 1},
		{name: "circuit breaker", src: strings.Replace(ecsService, "desired_count   = 2", `desired_count   = 2
  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }`, 1), want: 0},
		{name: "no rollback", src: strings.Replace(ecsService, "desired_count   = 2", `desired_count   = 2
  deployment_circuit_breaker {
    enable   = true
    rollback = false
  }`, 1), want: 1},
		{name: "CodeDeploy", src: strings.Replace(ecsService, "desired_count   = 2", `desired_count   = 2
  deployment_controller { type = "CODE_DEPLOY" }`, 1), want: 0},
		{name: "ECS controller by default", src: strings.Replace(ecsService, "desired_count   = 2", `desired_count   = 2
  deployment_controller {}`, 1), want: 1},
	},
	"bp_ecs_autoscaling": {
		{name: "fixed", src: ecsService, want: 1},
		{name: "scaling target", src: ecsService + `resource "aws_appautoscaling_target" "s" {
  service_namespace  = "ecs"
  scalable_dimension = "ecs:service:DesiredCount"
  resource_id        = "service/${aws_ecs_cluster.c.name}/${aws_ecs_service.s.name}"
  min_capacity       = 2
  max_capacity       = 6
}`, want: 0},
		{name: "literal resource id", src: ecsService + `resource "aws_appautoscaling_target" "s" {
  service_namespace  = "ecs"
  scalable_dimension = "ecs:service:DesiredCount"
  resource_id        = "service/c/web"
  min_capacity       = 2
  max_capacity       = 6
}`, want: 0},
		{name: "target of another service", src: ecsService + `resource "aws_ecs_service" "other" {
  name    = "other"
  cluster = aws_ecs_cluster.c.id
}
resource "aws_appautoscaling_target" "s" {
  service_namespace  = "ecs"
  scalable_dimension = "ecs:service:DesiredCount"
  resource_id        = "service/${aws_ecs_cluster.c.name}/${aws_ecs_service.other.name}"
  min_capacity       = 2
  max_capacity       = 6
}`, want: 1},
		{name: "unknown target", src: ecsService + `resource "aws_appautoscaling_target" "s" {
  service_namespace  = "ecs"
  scalable_dimension = "ecs:service:DesiredCount"
  resource_id        = var.resource_id
  min_capacity       = 2
  max_capacity       = 6
}`, want: 0},
		{name: "daemon", src: strings.Replace(ecsService, "desired_count   = 2", `scheduling_strategy = "DAEMON"`, 1), want: 0},
	},
	"bp_dynamodb_autoscaling": {
		{name: "provisioned", src: dynamo(`  billing_mode   = "PROVISIONED"
  read_capacity  = 5
  write_capacity = 5`), want: 1},
		{name: "on demand", src: dynamo(`  billing_mode = "PAY_PER_REQUEST"`), want: 0},
		{name: "default billing", src: dynamo(""), want: 0},
		{name: "scaled", src: dynamo(`  billing_mode   = "PROVISIONED"
  read_capacity  = 5
  write_capacity = 5`) + `resource "aws_appautoscaling_target" "read" {
  service_namespace  = "dynamodb"
  scalable_dimension = "dynamodb:table:ReadCapacityUnits"
  resource_id        = "table/${aws_dynamodb_table.t.name}"
  min_capacity       = 5
  max_capacity       = 50
}`, want: 0},
		{name: "billing mode expression", src: dynamo(`  billing_mode = var.billing_mode`), want: 0},
	},
	"bp_ebs_gp3": {
		{name: "gp2 volume", src: `resource "aws_ebs_volume" "v" {
  availability_zone = "us-east-1a"
  size              = 20
  type              = "gp2"
}`, want: 1},
		{name: "gp3 volume", src: `resource "aws_ebs_volume" "v" {
  availability_zone = "us-east-1a"
  size              = 20
  type              = "gp3"
}`, want: 0},
		{name: "default volume type", src: `resource "aws_ebs_volume" "v" {
  availability_zone = "us-east-1a"
  size              = 20
}`, want: 0},
		{name: "gp2 root device and RDS storage", src: rds(`  storage_type = "gp2"`) + `resource "aws_instance" "i" {
  ami           = "ami-1"
  instance_type = "t3.micro"
  root_block_device { volume_type = "gp2" }
}`, want: 2},
		{name: "launch template", src: `resource "aws_launch_template" "t" {
  block_device_mappings {
    device_name = "/dev/xvda"
    ebs { volume_type = "gp2" }
  }
}`, want: 1},
		{name: "expression", src: `resource "aws_ebs_volume" "v" {
  availability_zone = "us-east-1a"
  type              = var.volume_type
}`, want: 0},
	},
	"bp_cloudwatch_alarms": {
		{name: "no alarm", src: rds(""), want: 1},
		{name: "alarm on the instance", src: rds("") + `resource "aws_cloudwatch_metric_alarm" "cpu" {
  alarm_name          = "cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "CPUUtilization"
  namespace           = "AWS/RDS"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.db.identifier }
}`, want: 0},
		{name: "alarm with a literal identifier", src: rds("") + `resource "aws_cloudwatch_metric_alarm" "cpu" {
  alarm_name          = "cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  dimensions          = { DBInstanceIdentifier = "app" }
}`, want: 0},
		{name: "alarm with unknown dimensions", src: rds("") + `resource "aws_cloudwatch_metric_alarm" "cpu" {
  alarm_name          = "cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  dimensions          = var.dimensions
}`, want: 0},
		{name: "alarm on another function", src: snsLambda + `resource "aws_lambda_function" "g" {
  function_name = "g"
  role          = "arn"
}
resource "aws_cloudwatch_metric_alarm" "errors" {
  alarm_name          = "errors"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  dimensions          = { FunctionName = aws_lambda_function.g.function_name }
}`, want: 1},
	},
	"bp_ecs_container_insights": {
		{name: "off", src: ecsService, want: 1},
		{name: "enabled", src: `resource "aws_ecs_cluster" "c" {
  name = "c"
  setting {
    name  = "containerInsights"
    value = "enhanced"
  }
}`, want: 0},
		{name: "disabled", src: `resource "aws_ecs_cluster" "c" {
  name = "c"
  setting {
    name  = "containerInsights"
    value = "disabled"
  }
}`, want: 1},
		{name: "account default", src: ecsService + `resource "aws_ecs_account_setting_default" "ci" {
  name  = "containerInsights"
  value = "enabled"
}`, want: 0},
		{name: "expression", src: `resource "aws_ecs_cluster" "c" {
  name = "c"
  setting {
    name  = "containerInsights"
    value = var.insights
  }
}`, want: 0},
	},
	"bp_azure_db_ha": {
		{name: "no HA", src: azFlexible(""), want: 1},
		{name: "same zone", src: azFlexible(`  high_availability { mode = "SameZone" }`), want: 1},
		{name: "zone redundant", src: azFlexible(`  high_availability { mode = "ZoneRedundant" }`), want: 0},
		{name: "mode expression", src: azFlexible(`  high_availability { mode = var.ha_mode }`), want: 0},
		{name: "replica", src: azFlexible(`  create_mode      = "Replica"
  source_server_id = "id"`), want: 0},
		{name: "SQL database", src: azMSSQL(`  sku_name = "GP_Gen5_2"`), want: 1},
		{name: "zone-redundant SQL database", src: azMSSQL(`  zone_redundant = true`), want: 0},
		{name: "standard DTU SQL database", src: azMSSQL(`  sku_name = "S1"`), want: 0},
	},
	"bp_azure_db_geo_backup": {
		{name: "local backups", src: azFlexible(""), want: 1},
		{name: "geo backups", src: azFlexible(`  geo_redundant_backup_enabled = true`), want: 0},
		{name: "expression", src: azFlexible(`  geo_redundant_backup_enabled = var.geo`), want: 0},
		{name: "SQL database with local backups", src: azMSSQL(`  storage_account_type = "Local"`), want: 1},
		{name: "SQL database default", src: azMSSQL(""), want: 0},
	},
	"bp_azure_storage_replication": {
		{name: "LRS", src: `resource "azurerm_storage_account" "s" {
  name                     = "s"
  resource_group_name      = "rg"
  location                 = "westeurope"
  account_tier             = "Standard"
  account_replication_type = "LRS"
}`, want: 1},
		{name: "ZRS", src: `resource "azurerm_storage_account" "s" {
  name                     = "s"
  resource_group_name      = "rg"
  location                 = "westeurope"
  account_tier             = "Standard"
  account_replication_type = "ZRS"
}`, want: 0},
		{name: "expression", src: `resource "azurerm_storage_account" "s" {
  name                     = "s"
  resource_group_name      = "rg"
  location                 = "westeurope"
  account_tier             = "Standard"
  account_replication_type = var.replication
}`, want: 0},
	},
	"bp_azure_zones": {
		{name: "scale set without zones", src: `resource "azurerm_linux_virtual_machine_scale_set" "v" {
  name                = "v"
  resource_group_name = "rg"
  location            = "westeurope"
  sku                 = "Standard_B2s"
  admin_username      = "azureuser"
}`, want: 1},
		{name: "scale set in three zones", src: `resource "azurerm_linux_virtual_machine_scale_set" "v" {
  name                = "v"
  resource_group_name = "rg"
  location            = "westeurope"
  sku                 = "Standard_B2s"
  admin_username      = "azureuser"
  zones               = ["1", "2", "3"]
}`, want: 0},
		{name: "scale set zones expression", src: `resource "azurerm_linux_virtual_machine_scale_set" "v" {
  name                = "v"
  resource_group_name = "rg"
  location            = "westeurope"
  sku                 = "Standard_B2s"
  admin_username      = "azureuser"
  zones               = var.zones
}`, want: 0},
		{name: "AKS default pool in one zone", src: `resource "azurerm_kubernetes_cluster" "k" {
  name                = "k"
  resource_group_name = "rg"
  location            = "westeurope"
  dns_prefix          = "k"
  default_node_pool {
    name    = "default"
    vm_size = "Standard_D2s_v5"
    zones   = ["1"]
  }
}`, want: 1},
	},
	"bp_azure_autoscale": {
		{name: "standard plan", src: azPlan(`"S1"`, ""), want: 1},
		{name: "standard plan with autoscale", src: azPlan(`"S1"`, "") + strings.Replace(azAutoscaleSetting, "%s", "azurerm_service_plan.plan.id", 1), want: 0},
		{name: "autoscale of an unknown resource", src: azPlan(`"P1v3"`, "") + strings.Replace(azAutoscaleSetting, "%s", "var.target", 1), want: 0},
		{name: "premium automatic scaling", src: azPlan(`"P1v3"`, `  premium_plan_auto_scale_enabled = true`), want: 0},
		{name: "consumption plan", src: azPlan(`"Y1"`, ""), want: 0},
		{name: "basic plan", src: azPlan(`"B1"`, ""), want: 0},
		{name: "SKU expression", src: azPlan(`var.sku`, ""), want: 0},
		{name: "AKS pool", src: `resource "azurerm_kubernetes_cluster_node_pool" "p" {
  name                  = "p"
  kubernetes_cluster_id = "id"
  vm_size               = "Standard_D2s_v5"
  zones                 = ["1", "2"]
}`, want: 1},
		{name: "AKS pool autoscaling", src: `resource "azurerm_kubernetes_cluster_node_pool" "p" {
  name                  = "p"
  kubernetes_cluster_id = "id"
  vm_size               = "Standard_D2s_v5"
  zones                 = ["1", "2"]
  auto_scaling_enabled  = true
  min_count             = 1
  max_count             = 5
}`, want: 0},
	},
	"bp_gcp_sql_ha": {
		{name: "default zonal", src: gcpSQLHA(""), want: 1},
		{name: "zonal", src: gcpSQLHA(`    availability_type = "ZONAL"`), want: 1},
		{name: "regional", src: gcpSQLHA(`    availability_type = "REGIONAL"`), want: 0},
		{name: "expression", src: gcpSQLHA(`    availability_type = var.availability`), want: 0},
	},
	"bp_gcp_gke_regional": {
		{name: "zonal", src: `resource "google_container_cluster" "k" {
  name               = "k"
  location           = "us-central1-a"
  initial_node_count = 1
}`, want: 1},
		{name: "regional", src: `resource "google_container_cluster" "k" {
  name               = "k"
  location           = "us-central1"
  initial_node_count = 1
}`, want: 0},
		{name: "expression", src: `resource "google_container_cluster" "k" {
  name               = "k"
  location           = var.location
  initial_node_count = 1
}`, want: 0},
	},
	"bp_gcp_gke_autoscaling": {
		{name: "fixed node pool", src: `resource "google_container_node_pool" "p" {
  name       = "p"
  cluster    = "k"
  node_count = 3
}`, want: 1},
		{name: "autoscaling node pool", src: `resource "google_container_node_pool" "p" {
  name    = "p"
  cluster = "k"
  autoscaling {
    min_node_count = 1
    max_node_count = 5
  }
}`, want: 0},
		{name: "fixed inline pool", src: `resource "google_container_cluster" "k" {
  name     = "k"
  location = "us-central1"
  node_pool {
    name       = "default"
    node_count = 1
  }
}`, want: 1},
		{name: "autopilot", src: `resource "google_container_cluster" "k" {
  name             = "k"
  location         = "us-central1"
  enable_autopilot = true
}`, want: 0},
	},
	"bp_gcp_mig_autoscaling": {
		{name: "no autoscaler", src: gcpMIG(""), want: 1},
		{name: "autoscaler", src: gcpMIG(`resource "google_compute_region_autoscaler" "app" {
  name   = "app"
  region = "us-central1"
  target = google_compute_region_instance_group_manager.app.id
  autoscaling_policy {
    min_replicas = 2
    max_replicas = 5
  }
}`), want: 0},
		{name: "autoscaler of an unknown group", src: gcpMIG(`resource "google_compute_region_autoscaler" "app" {
  name   = "app"
  region = "us-central1"
  target = var.group
  autoscaling_policy {
    min_replicas = 2
    max_replicas = 5
  }
}`), want: 0},
		{name: "zonal autoscaler doesn't scale a regional group", src: gcpMIG(`resource "google_compute_autoscaler" "app" {
  name   = "app"
  zone   = "us-central1-a"
  target = google_compute_region_instance_group_manager.app.id
  autoscaling_policy {
    min_replicas = 2
    max_replicas = 5
  }
}`), want: 1},
	},
	"bp_gcp_cloud_run_max_instances": {
		{name: "uncapped", src: cloudRun(""), want: 1},
		{name: "capped", src: cloudRun(`    scaling { max_instance_count = 5 }`), want: 0},
		{name: "capped by expression", src: cloudRun(`    scaling { max_instance_count = var.max_instances }`), want: 0},
		{name: "min only", src: cloudRun(`    scaling { min_instance_count = 1 }`), want: 1},
	},
	"bp_oci_db_backups": {
		{name: "no backup config", src: ociDBSystem(""), want: 1},
		{name: "auto backups off", src: ociDBSystem(`      db_backup_config { auto_backup_enabled = false }`), want: 1},
		{name: "auto backups", src: ociDBSystem(`      db_backup_config { auto_backup_enabled = true }`), want: 0},
		{name: "expression", src: ociDBSystem(`      db_backup_config { auto_backup_enabled = var.backups }`), want: 0},
		{name: "MySQL backups off", src: `resource "oci_mysql_mysql_db_system" "m" {
  availability_domain = "AD-1"
  compartment_id      = "ocid"
  shape_name          = "MySQL.2"
  subnet_id           = "ocid"
  backup_policy { is_enabled = false }
}`, want: 1},
		{name: "MySQL default", src: `resource "oci_mysql_mysql_db_system" "m" {
  availability_domain = "AD-1"
  compartment_id      = "ocid"
  shape_name          = "MySQL.2"
  subnet_id           = "ocid"
}`, want: 0},
	},
	"bp_oci_instance_pool_autoscaling": {
		{name: "fixed pool", src: ociPool, want: 1},
		{name: "autoscaled pool", src: ociPool + `resource "oci_autoscaling_auto_scaling_configuration" "a" {
  compartment_id = "ocid"
  auto_scaling_resources {
    id   = oci_core_instance_pool.pool.id
    type = "instancePool"
  }
  policies {
    policy_type = "threshold"
    capacity {
      initial = 2
      max     = 6
      min     = 2
    }
  }
}`, want: 0},
		{name: "autoscaling of an unknown pool", src: ociPool + `resource "oci_autoscaling_auto_scaling_configuration" "a" {
  compartment_id = "ocid"
  auto_scaling_resources {
    id   = var.pool_id
    type = "instancePool"
  }
  policies { policy_type = "threshold" }
}`, want: 0},
	},
}

func init() {
	for code, list := range wellArchCases {
		structureCases[code] = append(structureCases[code], list...)
	}
}

// Every rule documents its practice and names the Well-Architected pillars
// it serves.
func TestRulesHavePillarsAndDocs(t *testing.T) {
	codes := map[string]bool{}
	for _, r := range Rules() {
		codes[r.Code] = true
		if !strings.HasPrefix(r.Docs, "https://") {
			t.Errorf("%s: docs link required (got %q)", r.Code, r.Docs)
		}
		if len(r.Pillars) == 0 {
			t.Errorf("%s: no pillars", r.Code)
		}
		seen := map[model.Pillar]bool{}
		for _, p := range r.Pillars {
			if !slices.Contains(model.Pillars, p) || seen[p] {
				t.Errorf("%s: unknown or repeated pillar %q", r.Code, p)
			}
			seen[p] = true
		}
	}
	for code := range rulePillars {
		if !codes[code] {
			t.Errorf("rulePillars lists unknown rule %s", code)
		}
	}
	if len(codes) != len(Rules()) {
		t.Errorf("duplicate rule codes")
	}
}

// The new rules match their severity policy: warnings only for data loss,
// production-only practices opt-in.
func TestWellArchitectedRulesMetadata(t *testing.T) {
	byCode := map[string]model.BestPracticeRule{}
	for _, r := range Rules() {
		byCode[r.Code] = r
	}
	for code := range wellArchCases {
		r, ok := byCode[code]
		if !ok {
			t.Errorf("%s: not a rule", code)
			continue
		}
		if r.Title == "" || r.Description == "" {
			t.Errorf("%s: title and description required", code)
		}
		if r.Severity == model.SeverityWarning && code != "bp_rds_backup_retention" {
			t.Errorf("%s: only data-loss rules warn", code)
		}
	}
	for _, code := range []string{"bp_rds_multi_az", "bp_rds_deletion_protection", "bp_dynamodb_pitr", "bp_s3_versioning", "bp_ecs_autoscaling", "bp_cloudwatch_alarms", "bp_azure_storage_replication", "bp_gcp_sql_ha"} {
		if !byCode[code].DefaultOff {
			t.Errorf("%s: production-only practice should be opt-in", code)
		}
	}
	if got := byCode["bp_rds_backup_retention"].Pillars; len(got) == 0 || got[0] != model.PillarReliability {
		t.Errorf("bp_rds_backup_retention pillars = %v", got)
	}
}

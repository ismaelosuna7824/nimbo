package lint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Rules for the reliability, performance, cost and operations practices of
// the cloud Well-Architected frameworks (AWS Well-Architected, Azure
// Well-Architected, Google Cloud Architecture Framework, OCI) that the
// security and CIS rules don't cover. Practices that only matter in
// production (Multi-AZ, deletion protection, autoscaling, alarms) are
// opt-in, so a sandbox design stays quiet; the ones that flag a choice that
// loses data or work (backups set to 0, consumers without a dead-letter
// queue, provisioned capacity without scaling, gp2) are on.
func init() {
	rules = append(rules,
		// AWS reliability.
		waRule(warning("bp_rds_backup_retention", "Databases keep automated backups", CategoryReliability,
			"RDS DB instances don't set backup_retention_period = 0, which turns automated backups and point-in-time restore off."),
			"https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_WorkingWithAutomatedBackups.html", false, checkRDSBackupRetention,
			model.PillarReliability, model.PillarSecurity),
		waRule(info("bp_rds_deletion_protection", "Databases have deletion protection", CategoryReliability,
			"RDS DB instances and Aurora clusters set deletion_protection = true, so a destroy or a replacing change can't delete them."),
			"https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_DeleteInstance.html#USER_DeleteInstance.DeletionProtection", true, checkRDSDeletionProtection,
			model.PillarReliability),
		waRule(info("bp_rds_storage_autoscaling", "Database storage grows automatically", CategoryPerformance,
			"RDS DB instances set max_allocated_storage, so storage scales up before it fills and stops the database."),
			"https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_PIOPS.Autoscaling.html", true, checkRDSStorageAutoscaling,
			model.PillarPerformance, model.PillarReliability),
		waRule(info("bp_dynamodb_pitr", "DynamoDB tables have point-in-time recovery", CategoryReliability,
			"DynamoDB tables set point_in_time_recovery { enabled = true }, so they can be restored to any second of the last 35 days."),
			"https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/Point-in-time-recovery.html", true, checkDynamoPITR,
			model.PillarReliability, model.PillarSecurity),
		waRule(info("bp_s3_versioning", "Buckets keep object versions", CategoryReliability,
			"S3 buckets that aren't static websites have an aws_s3_bucket_versioning with status = \"Enabled\", so overwritten or deleted objects can be recovered."),
			"https://docs.aws.amazon.com/AmazonS3/latest/userguide/Versioning.html", true, checkS3Versioning,
			model.PillarReliability, model.PillarSecurity),
		waRule(info("bp_elasticache_failover", "Caches fail over automatically", CategoryReliability,
			"ElastiCache replication groups set automatic_failover_enabled = true and multi_az_enabled = true, so a replica in another AZ takes over when the primary fails."),
			"https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/AutoFailover.html", true, checkElastiCacheFailover,
			model.PillarReliability),
		waRule(info("bp_sqs_dlq", "Queues consumed by Lambda have a dead-letter queue", CategoryReliability,
			"SQS queues that trigger a Lambda function set a redrive_policy (or aws_sqs_queue_redrive_policy), so messages that keep failing move to a dead-letter queue."),
			"https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html", false, checkSQSDLQ,
			model.PillarReliability, model.PillarOperations),
		waRule(info("bp_lambda_async_failure", "Asynchronously invoked functions keep failed events", CategoryReliability,
			"Lambda functions invoked by S3, SNS, EventBridge or EventBridge Scheduler have an on-failure destination (aws_lambda_function_event_invoke_config) or a dead_letter_config."),
			"https://docs.aws.amazon.com/lambda/latest/dg/invocation-async-retain-records.html", true, checkLambdaAsyncFailure,
			model.PillarReliability, model.PillarOperations),
		waRule(info("bp_lambda_stream_on_failure", "Stream consumers keep records that keep failing", CategoryReliability,
			"Lambda event source mappings that read a stream (Kinesis, DynamoDB Streams, Kafka) set destination_config { on_failure { … } }."),
			"https://docs.aws.amazon.com/lambda/latest/dg/kinesis-on-failure-destination.html", false, checkLambdaStreamOnFailure,
			model.PillarReliability, model.PillarOperations),
		waRule(info("bp_asg_single_az", "Auto Scaling groups span two or more AZs", CategoryReliability,
			"Auto Scaling groups list subnets (vpc_zone_identifier) or availability_zones in at least two Availability Zones."),
			"https://docs.aws.amazon.com/autoscaling/ec2/userguide/ec2-auto-scaling-availability-zone-balanced.html", true, checkASGSingleAZ,
			model.PillarReliability),
		waRule(info("bp_asg_single_instance", "Load-balanced groups run two or more instances", CategoryReliability,
			"Auto Scaling groups behind a load balancer allow more than one instance (max_size > 1), so they survive an instance failure and can scale out."),
			"https://docs.aws.amazon.com/wellarchitected/latest/reliability-pillar/rel_fault_isolation_multiaz_region_system.html", true, checkASGSingleInstance,
			model.PillarReliability, model.PillarPerformance),
		waRule(info("bp_ecs_circuit_breaker", "ECS deployments roll back on failure", CategoryReliability,
			"ECS services set deployment_circuit_breaker { enable = true, rollback = true }, so a deployment whose tasks keep failing returns to the last working revision."),
			"https://docs.aws.amazon.com/AmazonECS/latest/developerguide/deployment-circuit-breaker.html", true, checkECSCircuitBreaker,
			model.PillarReliability, model.PillarOperations),

		// AWS performance and cost.
		waRule(info("bp_ecs_autoscaling", "ECS services scale automatically", CategoryPerformance,
			"ECS services (replica scheduling) have an aws_appautoscaling_target for their desired count, with a scaling policy."),
			"https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service-auto-scaling.html", true, checkECSAutoscaling,
			model.PillarPerformance, model.PillarCost, model.PillarReliability),
		waRule(info("bp_dynamodb_autoscaling", "Provisioned DynamoDB capacity scales automatically", CategoryPerformance,
			"DynamoDB tables with billing_mode = \"PROVISIONED\" have an aws_appautoscaling_target for their capacity (or use PAY_PER_REQUEST)."),
			"https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/AutoScaling.html", false, checkDynamoAutoscaling,
			model.PillarPerformance, model.PillarCost),
		waRule(info("bp_ebs_gp3", "Block storage uses gp3, not gp2", CategoryCost,
			"EBS volumes, instance and launch template block devices and RDS storage don't choose gp2: gp3 is cheaper with a size-independent baseline."),
			"https://docs.aws.amazon.com/ebs/latest/userguide/general-purpose.html", false, checkEBSGP3,
			model.PillarCost, model.PillarPerformance),

		// AWS operations.
		waRule(info("bp_cloudwatch_alarms", "Functions, databases and load balancers have alarms", CategoryOperations,
			"Each Lambda function, RDS instance or cluster and load balancer is watched by an aws_cloudwatch_metric_alarm whose dimensions refer to it."),
			"https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/AlarmThatSendsEmail.html", true, checkCloudWatchAlarms,
			model.PillarOperations, model.PillarReliability),
		waRule(info("bp_ecs_container_insights", "ECS clusters collect Container Insights", CategoryOperations,
			"ECS clusters set setting { name = \"containerInsights\", value = \"enabled\" } (or \"enhanced\"), or the account default turns it on."),
			"https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/ContainerInsights.html", true, checkECSContainerInsights,
			model.PillarOperations),

		// Azure.
		waRule(info("bp_azure_db_ha", "Azure: databases are zone-redundant", CategoryReliability,
			"PostgreSQL and MySQL flexible servers have high_availability { mode = \"ZoneRedundant\" } and SQL databases set zone_redundant = true."),
			"https://learn.microsoft.com/azure/postgresql/high-availability/concepts-high-availability", true, checkAzDBHA,
			model.PillarReliability),
		waRule(info("bp_azure_db_geo_backup", "Azure: database backups are geo-redundant", CategoryReliability,
			"PostgreSQL and MySQL flexible servers set geo_redundant_backup_enabled = true and SQL databases keep geo-redundant backup storage."),
			"https://learn.microsoft.com/azure/postgresql/backup-restore/concepts-backup-restore", true, checkAzDBGeoBackup,
			model.PillarReliability),
		waRule(info("bp_azure_storage_replication", "Azure: storage replicates beyond one datacenter", CategoryReliability,
			"Storage accounts don't use account_replication_type = \"LRS\"; ZRS, GZRS or RA-GZRS survive a zone (or region) outage."),
			"https://learn.microsoft.com/azure/storage/common/storage-redundancy", true, checkAzStorageReplication,
			model.PillarReliability),
		waRule(info("bp_azure_zones", "Azure: scale sets and AKS node pools span availability zones", CategoryReliability,
			"Virtual machine scale sets and AKS node pools list two or more zones."),
			"https://learn.microsoft.com/azure/reliability/availability-zones-overview", true, checkAzZones,
			model.PillarReliability),
		waRule(info("bp_azure_autoscale", "Azure: App Service plans, scale sets and AKS node pools scale automatically", CategoryPerformance,
			"Standard and Premium App Service plans and virtual machine scale sets have an azurerm_monitor_autoscale_setting; AKS node pools set auto_scaling_enabled = true."),
			"https://learn.microsoft.com/azure/azure-monitor/autoscale/autoscale-overview", true, checkAzAutoscale,
			model.PillarPerformance, model.PillarCost, model.PillarReliability),

		// Google Cloud.
		waRule(info("bp_gcp_sql_ha", "GCP: Cloud SQL is highly available", CategoryReliability,
			"Primary Cloud SQL instances set settings.availability_type = \"REGIONAL\", with a standby in another zone."),
			"https://cloud.google.com/sql/docs/postgres/high-availability", true, checkGCPSQLHA,
			model.PillarReliability),
		waRule(info("bp_gcp_gke_regional", "GCP: GKE clusters are regional", CategoryReliability,
			"GKE clusters use a region as location, so the control plane and nodes are replicated across zones."),
			"https://cloud.google.com/kubernetes-engine/docs/concepts/regional-clusters", true, checkGCPGKERegional,
			model.PillarReliability),
		waRule(info("bp_gcp_gke_autoscaling", "GCP: GKE node pools scale automatically", CategoryPerformance,
			"GKE node pools (standalone or inline) have an autoscaling block; Autopilot clusters scale on their own."),
			"https://cloud.google.com/kubernetes-engine/docs/concepts/cluster-autoscaler", true, checkGCPGKEAutoscaling,
			model.PillarPerformance, model.PillarCost),
		waRule(info("bp_gcp_mig_autoscaling", "GCP: managed instance groups scale automatically", CategoryPerformance,
			"Managed instance groups have a google_compute_autoscaler (zonal) or google_compute_region_autoscaler (regional) targeting them."),
			"https://cloud.google.com/compute/docs/autoscaler", true, checkGCPMIGAutoscaling,
			model.PillarPerformance, model.PillarCost, model.PillarReliability),
		waRule(info("bp_gcp_cloud_run_max_instances", "GCP: Cloud Run services cap their instances", CategoryCost,
			"Cloud Run services set scaling.max_instance_count (in template or at service level), so a spike can't scale them without bound."),
			"https://cloud.google.com/run/docs/configuring/max-instances", true, checkGCPCloudRunMaxInstances,
			model.PillarCost, model.PillarPerformance),

		// OCI.
		waRule(info("bp_oci_db_backups", "OCI: databases have automatic backups", CategoryReliability,
			"DB systems set db_backup_config { auto_backup_enabled = true } on their database and MySQL DB systems keep backup_policy enabled."),
			"https://docs.oracle.com/en-us/iaas/Content/Database/Tasks/backingupOS.htm", true, checkOCIDBBackups,
			model.PillarReliability, model.PillarSecurity),
		waRule(info("bp_oci_instance_pool_autoscaling", "OCI: instance pools scale automatically", CategoryPerformance,
			"Instance pools have an oci_autoscaling_auto_scaling_configuration targeting them."),
			"https://docs.oracle.com/en-us/iaas/Content/Compute/Tasks/autoscalinginstancepools.htm", true, checkOCIInstancePoolAutoscaling,
			model.PillarPerformance, model.PillarCost),
	)
}

// waRule is a documented rule serving pillars (most relevant first).
func waRule(r model.BestPracticeRule, docs string, optIn bool, check func(*pass), pillars ...model.Pillar) rule {
	r.Docs = docs
	r.DefaultOff = optIn
	r.Pillars = pillars
	return rule{r, check}
}

// ---------------------------------------------------------------------------
// Helpers

// referrers returns the resources of types with an attribute at one of
// paths (dotted, without indexes: "metric_query.metric.dimensions") that
// points at n (see pointsAt). unknown reports an attribute at paths that may
// point at n, so callers stay quiet.
func (p *pass) referrers(n *model.Node, name string, paths []string, types ...string) (out []*model.Node, unknown bool) {
	for _, o := range p.ofType(types...) {
		found := false
		walkAttrs(&o.Body, "", func(path string, a *model.Attr) {
			if found || !slices.Contains(paths, azIndex.ReplaceAllString(path, "")) {
				return
			}
			hit, maybe := p.pointsAt(a.Value, n, name)
			found = hit
			unknown = unknown || maybe
		})
		if found {
			out = append(out, o)
		}
	}
	return out, unknown
}

// pointsAt reports whether v refers to n or is a constant containing name
// (n's literal name; "" when unknown). maybe reports a value Nimbo can't
// resolve (a variable, local, data source or module output, or a constant
// when name is ""): it may point at n.
func (p *pass) pointsAt(v model.Value, n *model.Node, name string) (hit, maybe bool) {
	switch {
	case referencesNode(v, n):
		return true, false
	case p.refersToResource(v):
		return false, false
	case len(references(v)) > 0, name == "":
		return false, true
	}
	return strings.Contains(source(v), name), false
}

// targeted: a resource of types points at n (or may, see referrers).
func (p *pass) targeted(n *model.Node, name string, paths []string, types ...string) bool {
	refs, unknown := p.referrers(n, name, paths, types...)
	return len(refs) > 0 || unknown
}

// refersToResource: every reference of v resolves to a managed resource of
// the design (none is a variable, local, data source or module output), and
// there is at least one.
func (p *pass) refersToResource(v model.Value) bool {
	refs := references(v)
	for _, r := range refs {
		parts := strings.Split(r, ".")
		var hit *model.Node
		for k := len(parts); k >= 2 && hit == nil; k-- {
			hit = p.byAddr[strings.Join(parts[:k], ".")]
		}
		if hit == nil || hit.Kind != model.KindResource {
			return false
		}
	}
	return len(refs) > 0
}

// nameOf returns the literal value of n's name attribute ("" when unset or
// an expression).
func nameOf(n *model.Node, attr string) string {
	s, _ := attrString(&n.Body, attr)
	return s
}

// listLen returns the number of items of the list attribute name of b: a
// constant list or a [...] expression without splat or for items. known is
// false when it is unset or can't be counted.
func listLen(b *model.Body, name string) (n int, known bool) {
	a := getAttr(b, name)
	if a == nil {
		return 0, false
	}
	if l, ok := literal(a.Value).([]any); ok {
		return len(l), true
	}
	t, ok := parse(a.Value).(*hclsyntax.TupleConsExpr)
	if !ok {
		return 0, false
	}
	for _, e := range t.Exprs {
		switch e.(type) {
		case *hclsyntax.SplatExpr, *hclsyntax.ForExpr:
			return 0, false
		}
	}
	return len(t.Exprs), true
}

// awsDBPrimary: an RDS DB instance that manages its own storage and backups
// (not a read replica, not an Aurora cluster member).
func awsDBPrimary(n *model.Node) bool {
	engine, _ := attrString(&n.Body, "engine")
	return getAttr(&n.Body, "replicate_source_db") == nil && !strings.HasPrefix(engine, "aurora")
}

// ---------------------------------------------------------------------------
// AWS reliability

func checkRDSBackupRetention(p *pass) {
	for _, n := range p.ofType("aws_db_instance") {
		if v, ok := attrNumber(&n.Body, "backup_retention_period"); !ok || v != 0 || !awsDBPrimary(n) {
			continue
		}
		p.report(finding{
			node: n, attribute: "backup_retention_period",
			message: fmt.Sprintf("%s turns automated backups off (backup_retention_period = 0)", addr(n)),
			why:     "Without automated backups there is no point-in-time restore: a bad migration, a dropped table or a deleted instance loses the data for good.",
			fix:     "Set backup_retention_period to 7 or more (up to 35 days); keep 0 only for throwaway sandboxes.",
		})
	}
}

func checkRDSDeletionProtection(p *pass) {
	for _, n := range p.ofType("aws_db_instance", "aws_rds_cluster") {
		if n.Type == "aws_db_instance" && !awsDBPrimary(n) || !missingOrFalse(&n.Body, "deletion_protection") {
			continue
		}
		p.report(finding{
			node: n, attribute: "deletion_protection",
			message: fmt.Sprintf("%s has no deletion protection", addr(n)),
			why:     "Without deletion protection a terraform destroy, a replacing change (engine, identifier, encryption) or a mistaken console click deletes the database and its automated backups.",
			fix:     "Set deletion_protection = true (turn it off deliberately before a planned delete) and keep skip_final_snapshot = false.",
		})
	}
}

func checkRDSStorageAutoscaling(p *pass) {
	for _, n := range p.ofType("aws_db_instance") {
		if !awsDBPrimary(n) {
			continue
		}
		if a := getAttr(&n.Body, "max_allocated_storage"); a != nil {
			if v, ok := attrNumber(&n.Body, "max_allocated_storage"); !ok || v > 0 {
				continue
			}
		}
		p.report(finding{
			node: n, attribute: "max_allocated_storage",
			message: fmt.Sprintf("%s has a fixed storage size (max_allocated_storage is unset)", addr(n)),
			why:     "A database whose storage fills up stops accepting writes; growing it by hand takes a change at the worst possible moment.",
			fix:     "Set max_allocated_storage above allocated_storage (e.g. 2-5 times) so RDS grows the storage before it fills up.",
		})
	}
}

func checkDynamoPITR(p *pass) {
	for _, n := range p.ofType("aws_dynamodb_table") {
		if slices.ContainsFunc(blocks(&n.Body, "point_in_time_recovery"), func(b *model.Block) bool { return attrNotFalse(&b.Body, "enabled") }) ||
			azHasDynamic(&n.Body, "point_in_time_recovery") {
			continue
		}
		p.report(finding{
			node: n, attribute: "point_in_time_recovery",
			message: fmt.Sprintf("%s has no point-in-time recovery", addr(n)),
			why:     "Without continuous backups a buggy deploy that overwrites items or a deleted table can't be rolled back.",
			fix:     "Add point_in_time_recovery { enabled = true } (restores to any second of the last 35 days).",
		})
	}
}

func checkS3Versioning(p *pass) {
	for _, n := range p.ofType("aws_s3_bucket") {
		if p.publicIntent(n) || slices.ContainsFunc(blocks(&n.Body, "versioning"), func(b *model.Block) bool { return attrNotFalse(&b.Body, "enabled") }) {
			continue
		}
		refs, unknown := p.referrers(n, nameOf(n, "bucket"), []string{"bucket"}, "aws_s3_bucket_versioning")
		if unknown {
			continue
		}
		if len(refs) == 0 {
			p.report(finding{
				node:    n,
				message: fmt.Sprintf("%s doesn't keep object versions", addr(n)),
				why:     "Without versioning an overwritten or deleted object (a bad deploy, a bug, ransomware) is gone for good.",
				fix:     fmt.Sprintf("Add aws_s3_bucket_versioning with bucket = %s.id and versioning_configuration { status = \"Enabled\" }; a lifecycle rule expiring noncurrent versions caps the cost.", addr(n)),
			})
			continue
		}
		for _, v := range refs {
			cfg := azNested(&v.Body, "versioning_configuration")
			if cfg == nil {
				continue
			}
			if status, ok := attrString(cfg, "status"); ok && status != "Enabled" {
				p.report(finding{
					node: v, attribute: "versioning_configuration[0].status",
					message: fmt.Sprintf("%s sets versioning of %s to %s", addr(v), addr(n), status),
					why:     "Without versioning an overwritten or deleted object (a bad deploy, a bug, ransomware) is gone for good.",
					fix:     "Set versioning_configuration { status = \"Enabled\" }.",
				})
			}
		}
	}
}

func checkElastiCacheFailover(p *pass) {
	for _, n := range p.ofType("aws_elasticache_replication_group") {
		var off []string
		for _, attr := range []string{"automatic_failover_enabled", "multi_az_enabled"} {
			if missingOrFalse(&n.Body, attr) {
				off = append(off, attr)
			}
		}
		if len(off) == 0 {
			continue
		}
		p.report(finding{
			node: n, attribute: off[0],
			message: fmt.Sprintf("%s doesn't fail over automatically (%s off)", addr(n), strings.Join(off, " and ")),
			why:     "When the primary node or its Availability Zone fails, the cache stays down until someone promotes a replica by hand.",
			fix:     "Set automatic_failover_enabled = true and multi_az_enabled = true with at least one replica (num_cache_clusters >= 2 or replicas_per_node_group >= 1) in a subnet group spanning two AZs.",
		})
	}
}

func checkSQSDLQ(p *pass) {
	seen := map[*model.Node]bool{}
	for _, m := range p.ofType("aws_lambda_event_source_mapping") {
		q := p.referencedNode(&m.Body, "event_source_arn")
		if q == nil || q.Type != "aws_sqs_queue" || seen[q] {
			continue
		}
		seen[q] = true
		if getAttr(&q.Body, "redrive_policy") != nil || p.targeted(q, "", []string{"queue_url"}, "aws_sqs_queue_redrive_policy") {
			continue
		}
		fn := p.referencedNode(&m.Body, "function_name")
		consumer := "a Lambda function"
		if fn != nil {
			consumer = addr(fn)
		}
		p.report(finding{
			node: q, attribute: "redrive_policy",
			message: fmt.Sprintf("%s feeds %s but has no dead-letter queue", addr(q), consumer),
			why:     "A message the function keeps failing on (a poison message) is retried until it expires: it blocks batches, burns invocations and is then lost without a trace.",
			fix:     fmt.Sprintf("Add an aws_sqs_queue for failed messages and set redrive_policy = jsonencode({ deadLetterTargetArn = aws_sqs_queue.<dlq>.arn, maxReceiveCount = 5 }) on %s.", addr(q)),
		})
	}
}

// asyncTriggers returns the Lambda functions invoked asynchronously (S3
// notifications, SNS subscriptions, EventBridge rules and schedules) with a
// trigger that invokes each.
func (p *pass) asyncTriggers() (fns []*model.Node, by map[*model.Node]*model.Node) {
	by = map[*model.Node]*model.Node{}
	add := func(trigger *model.Node, b *model.Body, attr string) {
		if fn := p.referencedNode(b, attr); fn != nil && fn.Type == "aws_lambda_function" && by[fn] == nil {
			by[fn] = trigger
			fns = append(fns, fn)
		}
	}
	for _, n := range p.resources() {
		switch n.Type {
		case "aws_s3_bucket_notification":
			for _, blk := range blocks(&n.Body, "lambda_function") {
				add(n, &blk.Body, "lambda_function_arn")
			}
		case "aws_sns_topic_subscription":
			if proto, _ := attrString(&n.Body, "protocol"); proto == "lambda" {
				add(n, &n.Body, "endpoint")
			}
		case "aws_cloudwatch_event_target":
			add(n, &n.Body, "arn")
		case "aws_scheduler_schedule":
			for _, blk := range blocks(&n.Body, "target") {
				add(n, &blk.Body, "arn")
			}
		}
	}
	return fns, by
}

func checkLambdaAsyncFailure(p *pass) {
	fns, by := p.asyncTriggers()
	for _, fn := range fns {
		if slices.ContainsFunc(blocks(&fn.Body, "dead_letter_config"), func(b *model.Block) bool { return getAttr(&b.Body, "target_arn") != nil }) {
			continue
		}
		cfgs, unknown := p.referrers(fn, nameOf(fn, "function_name"), []string{"function_name"}, "aws_lambda_function_event_invoke_config")
		if unknown || slices.ContainsFunc(cfgs, func(c *model.Node) bool {
			return azNested(&c.Body, "destination_config", "on_failure") != nil || azHasDynamic(&c.Body, "destination_config")
		}) {
			continue
		}
		p.report(finding{
			node: fn, attribute: "dead_letter_config",
			message: fmt.Sprintf("%s is invoked asynchronously by %s but drops events that fail", addr(fn), addr(by[fn])),
			why:     "Asynchronous invocations are retried twice and then discarded: without a failure destination the event is lost with no trace.",
			fix:     "Add aws_lambda_function_event_invoke_config for the function with destination_config { on_failure { destination = <SQS queue, SNS topic or bucket ARN> } }, or dead_letter_config { target_arn = <SQS queue or SNS topic ARN> } on the function.",
		})
	}
}

func checkLambdaStreamOnFailure(p *pass) {
	for _, m := range p.ofType("aws_lambda_event_source_mapping") {
		src := p.referencedNode(&m.Body, "event_source_arn")
		stream := getAttr(&m.Body, "starting_position") != nil ||
			src != nil && (src.Type == "aws_kinesis_stream" || src.Type == "aws_dynamodb_table" || src.Type == "aws_msk_cluster")
		if !stream || src != nil && src.Type == "aws_sqs_queue" {
			continue
		}
		if azNested(&m.Body, "destination_config", "on_failure") != nil || azHasDynamic(&m.Body, "destination_config") {
			continue
		}
		p.report(finding{
			node: m, attribute: "destination_config",
			message: fmt.Sprintf("%s has no on-failure destination for records that keep failing", addr(m)),
			why:     "A stream batch that keeps failing is retried until its records expire, blocking the shard or partition; then the records are dropped without a trace.",
			fix:     "Add destination_config { on_failure { destination_arn = <SQS queue or SNS topic ARN> } } and set maximum_retry_attempts and bisect_batch_on_function_error = true.",
		})
	}
}

func checkASGSingleAZ(p *pass) {
	for _, n := range p.ofType("aws_autoscaling_group") {
		attr := "vpc_zone_identifier"
		if getAttr(&n.Body, attr) == nil {
			attr = "availability_zones"
		}
		if count, known := listLen(&n.Body, attr); !known || count != 1 {
			continue
		}
		p.report(finding{
			node: n, attribute: attr,
			message: fmt.Sprintf("%s launches its instances in a single Availability Zone (%s)", addr(n), attr),
			why:     "When that zone has an outage every instance of the group goes down with it; spread across zones the group keeps serving and replaces capacity elsewhere.",
			fix:     "List subnets in at least two Availability Zones in vpc_zone_identifier (e.g. one private subnet per AZ).",
		})
	}
}

func checkASGSingleInstance(p *pass) {
	for _, n := range p.ofType("aws_autoscaling_group") {
		if !nonEmpty(&n.Body, "target_group_arns") && !nonEmpty(&n.Body, "load_balancers") && len(blocks(&n.Body, "traffic_source")) == 0 {
			continue
		}
		if v, ok := attrNumber(&n.Body, "max_size"); !ok || v > 1 {
			continue
		}
		p.report(finding{
			node: n, attribute: "max_size",
			message: fmt.Sprintf("%s serves load-balanced traffic with at most one instance", addr(n)),
			why:     "With a single instance every failure, deployment or instance refresh is downtime, and the group can't scale out under load.",
			fix:     "Set min_size = 2 (one instance per AZ) and max_size above it, with a scaling policy (aws_autoscaling_policy).",
		})
	}
}

func checkECSCircuitBreaker(p *pass) {
	for _, n := range p.ofType("aws_ecs_service") {
		if dc := azNested(&n.Body, "deployment_controller"); dc != nil && getAttr(dc, "type") != nil {
			if t, ok := attrString(dc, "type"); !ok || t != "ECS" {
				continue // CodeDeploy and external controllers roll back themselves
			}
		}
		on := func(b *model.Body) bool { return b != nil && attrNotFalse(b, "enable") && attrNotFalse(b, "rollback") }
		if on(azNested(&n.Body, "deployment_circuit_breaker")) || on(azNested(&n.Body, "alarms")) {
			continue
		}
		p.report(finding{
			node: n, attribute: "deployment_circuit_breaker",
			message: fmt.Sprintf("%s doesn't roll back failed deployments", addr(n)),
			why:     "Without the circuit breaker a revision whose tasks keep crashing is retried forever: the deployment never finishes and capacity drops while it churns.",
			fix:     "Add deployment_circuit_breaker { enable = true, rollback = true }.",
		})
	}
}

// ---------------------------------------------------------------------------
// AWS performance and cost

func checkECSAutoscaling(p *pass) {
	for _, n := range p.ofType("aws_ecs_service") {
		if s, _ := attrString(&n.Body, "scheduling_strategy"); s == "DAEMON" {
			continue
		}
		if p.targeted(n, nameOf(n, "name"), []string{"resource_id"}, "aws_appautoscaling_target") {
			continue
		}
		p.report(finding{
			node: n, attribute: "desired_count",
			message: fmt.Sprintf("%s runs a fixed number of tasks (no Application Auto Scaling target)", addr(n)),
			why:     "A fixed task count is either too small for a traffic peak (slow responses, errors) or paid for while idle.",
			fix:     fmt.Sprintf("Add aws_appautoscaling_target (service_namespace = \"ecs\", scalable_dimension = \"ecs:service:DesiredCount\", resource_id = \"service/${<cluster>.name}/${%s.name}\", min_capacity = 2) with a target-tracking aws_appautoscaling_policy on CPU or requests, and lifecycle { ignore_changes = [desired_count] } on the service.", addr(n)),
		})
	}
}

func checkDynamoAutoscaling(p *pass) {
	for _, n := range p.ofType("aws_dynamodb_table") {
		if mode, _ := attrString(&n.Body, "billing_mode"); mode != "PROVISIONED" {
			continue
		}
		if p.targeted(n, nameOf(n, "name"), []string{"resource_id"}, "aws_appautoscaling_target") {
			continue
		}
		p.report(finding{
			node: n, attribute: "billing_mode",
			message: fmt.Sprintf("%s uses PROVISIONED capacity without auto scaling", addr(n)),
			why:     "Fixed read/write capacity throttles requests above it and is paid for in full while traffic is low.",
			fix:     fmt.Sprintf("Add aws_appautoscaling_target resources (service_namespace = \"dynamodb\", resource_id = \"table/${%s.name}\", scalable_dimension = \"dynamodb:table:ReadCapacityUnits\" and \"dynamodb:table:WriteCapacityUnits\") with target-tracking aws_appautoscaling_policy, or set billing_mode = \"PAY_PER_REQUEST\".", addr(n)),
		})
	}
}

func checkEBSGP3(p *pass) {
	report := func(n *model.Node, attr string) {
		p.report(finding{
			node: n, attribute: attr,
			message: fmt.Sprintf("%s uses gp2 storage (%s)", addr(n), attr),
			why:     "gp3 costs about 20% less per GB than gp2 and has a 3,000 IOPS / 125 MB/s baseline at any size, where gp2 performance grows only with the volume size.",
			fix:     fmt.Sprintf("Set %s = \"gp3\" (an in-place change without downtime).", lastSegment(attr)),
		})
	}
	gp2 := func(b *model.Body, attr string) bool { s, _ := attrString(b, attr); return s == "gp2" }
	for _, n := range p.resources() {
		switch n.Type {
		case "aws_ebs_volume":
			if gp2(&n.Body, "type") {
				report(n, "type")
			}
		case "aws_db_instance":
			if gp2(&n.Body, "storage_type") {
				report(n, "storage_type")
			}
		case "aws_instance":
			for _, bt := range []string{"root_block_device", "ebs_block_device"} {
				for i, blk := range blocks(&n.Body, bt) {
					if gp2(&blk.Body, "volume_type") {
						report(n, fmt.Sprintf("%s[%d].volume_type", bt, i))
					}
				}
			}
		case "aws_launch_template":
			for i, m := range blocks(&n.Body, "block_device_mappings") {
				for j, ebs := range blocks(&m.Body, "ebs") {
					if gp2(&ebs.Body, "volume_type") {
						report(n, fmt.Sprintf("block_device_mappings[%d].ebs[%d].volume_type", i, j))
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// AWS operations

// alarmTargets are the resources bp_cloudwatch_alarms expects an alarm for,
// with their name attribute and the alarm that fits them.
var alarmTargets = map[string]struct{ name, fix string }{
	"aws_lambda_function": {"function_name", "on AWS/Lambda Errors and Throttles with dimensions = { FunctionName = %s.function_name }"},
	"aws_db_instance":     {"identifier", "on AWS/RDS CPUUtilization and FreeStorageSpace with dimensions = { DBInstanceIdentifier = %s.identifier }"},
	"aws_rds_cluster":     {"cluster_identifier", "on AWS/RDS CPUUtilization and FreeableMemory with dimensions = { DBClusterIdentifier = %s.cluster_identifier }"},
	"aws_lb":              {"name", "on HTTPCode_ELB_5XX_Count / UnHealthyHostCount (AWS/ApplicationELB or AWS/NetworkELB) with dimensions = { LoadBalancer = %s.arn_suffix }"},
	"aws_alb":             {"name", "on AWS/ApplicationELB HTTPCode_ELB_5XX_Count with dimensions = { LoadBalancer = %s.arn_suffix }"},
}

func checkCloudWatchAlarms(p *pass) {
	for _, n := range p.resources() {
		t, ok := alarmTargets[n.Type]
		if !ok || p.targeted(n, nameOf(n, t.name), []string{"dimensions", "metric_query.metric.dimensions"}, "aws_cloudwatch_metric_alarm") {
			continue
		}
		p.report(finding{
			node:    n,
			message: fmt.Sprintf("No CloudWatch alarm watches %s", addr(n)),
			why:     "Without alarms failures are found by users: errors, throttling or a full disk go unnoticed until something breaks visibly.",
			fix:     "Add an aws_cloudwatch_metric_alarm " + fmt.Sprintf(t.fix, addr(n)) + ", with alarm_actions pointing to an SNS topic someone is subscribed to.",
		})
	}
}

func checkECSContainerInsights(p *pass) {
	for _, d := range p.ofType("aws_ecs_account_setting_default") {
		if name, _ := attrString(&d.Body, "name"); name == "containerInsights" {
			if v, ok := attrString(&d.Body, "value"); !ok || v != "disabled" {
				return
			}
		}
	}
	for _, n := range p.ofType("aws_ecs_cluster") {
		if azHasDynamic(&n.Body, "setting") || slices.ContainsFunc(blocks(&n.Body, "setting"), func(b *model.Block) bool {
			name, _ := attrString(&b.Body, "name")
			v, ok := attrString(&b.Body, "value")
			return name == "containerInsights" && (!ok || v != "disabled")
		}) {
			continue
		}
		p.report(finding{
			node: n, attribute: "setting",
			message: fmt.Sprintf("%s doesn't collect Container Insights metrics", addr(n)),
			why:     "Without Container Insights there are no per-service and per-task CPU, memory and restart metrics to alarm on or to debug a degraded service with.",
			fix:     "Add setting { name = \"containerInsights\", value = \"enabled\" } (or \"enhanced\") to the cluster.",
		})
	}
}

// ---------------------------------------------------------------------------
// Azure

// azFlexibleServers are the flexible database servers with high
// availability and geo-redundant backup settings.
var azFlexibleServers = []string{"azurerm_postgresql_flexible_server", "azurerm_mysql_flexible_server"}

// azReplica: a flexible server created as a replica (it follows its source).
func azReplica(n *model.Node) bool {
	mode, _ := attrString(&n.Body, "create_mode")
	return getAttr(&n.Body, "source_server_id") != nil || strings.EqualFold(mode, "Replica")
}

var azDTUStandard = regexp.MustCompile(`^S\d+$`)

// azSQLZoneRedundantSKU: SQL Database SKUs supporting zone redundancy (not
// Basic or Standard DTU tiers); unknown SKUs count as supporting it.
func azSQLZoneRedundantSKU(n *model.Node) bool {
	sku, ok := attrString(&n.Body, "sku_name")
	return !ok || sku != "Basic" && !azDTUStandard.MatchString(sku)
}

func checkAzDBHA(p *pass) {
	why := "A database without zone-redundant high availability is down for as long as its zone (or the host) is; with it a standby in another zone takes over within minutes."
	for _, n := range p.ofType(azFlexibleServers...) {
		if azReplica(n) || azHasDynamic(&n.Body, "high_availability") {
			continue
		}
		ha := azNested(&n.Body, "high_availability")
		if ha != nil {
			if mode, ok := attrString(ha, "mode"); !ok || mode == "ZoneRedundant" {
				continue
			}
		}
		p.report(finding{
			node: n, attribute: "high_availability",
			message: fmt.Sprintf("%s has no zone-redundant high availability", addr(n)),
			why:     why,
			fix:     "Add high_availability { mode = \"ZoneRedundant\" } (General Purpose or Business Critical tier; Burstable SKUs don't support it).",
		})
	}
	for _, n := range p.ofType("azurerm_mssql_database") {
		if getAttr(&n.Body, "elastic_pool_id") != nil || !azSQLZoneRedundantSKU(n) || !missingOrFalse(&n.Body, "zone_redundant") {
			continue
		}
		p.report(finding{
			node: n, attribute: "zone_redundant",
			message: fmt.Sprintf("%s is not zone-redundant", addr(n)),
			why:     why,
			fix:     "Set zone_redundant = true (General Purpose, Business Critical, Hyperscale or Premium tier).",
		})
	}
}

func checkAzDBGeoBackup(p *pass) {
	why := "Backups kept in the server's region are lost with it in a regional outage; geo-redundant backups can be restored in the paired region."
	for _, n := range p.ofType(azFlexibleServers...) {
		if azReplica(n) || !missingOrFalse(&n.Body, "geo_redundant_backup_enabled") {
			continue
		}
		p.report(finding{
			node: n, attribute: "geo_redundant_backup_enabled",
			message: fmt.Sprintf("%s keeps its backups in one region", addr(n)),
			why:     why,
			fix:     "Set geo_redundant_backup_enabled = true (it can only be set when the server is created).",
		})
	}
	for _, n := range p.ofType("azurerm_mssql_database") {
		if t, ok := attrString(&n.Body, "storage_account_type"); !ok || t != "Local" && t != "Zone" {
			continue
		}
		p.report(finding{
			node: n, attribute: "storage_account_type",
			message: fmt.Sprintf("%s keeps its backups in one region (storage_account_type)", addr(n)),
			why:     why,
			fix:     "Set storage_account_type = \"Geo\" (or \"GeoZone\").",
		})
	}
}

func checkAzStorageReplication(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		if t, _ := attrString(&n.Body, "account_replication_type"); t != "LRS" {
			continue
		}
		p.report(finding{
			node: n, attribute: "account_replication_type",
			message: fmt.Sprintf("%s keeps its data in a single datacenter (account_replication_type = \"LRS\")", addr(n)),
			why:     "Locally redundant storage keeps three copies in one datacenter: a zone outage makes the data unavailable, and a disaster can lose it.",
			fix:     "Use account_replication_type = \"ZRS\" (survives a zone outage) or \"GZRS\"/\"RAGZRS\" (also a regional one) for production data.",
		})
	}
}

func checkAzZones(p *pass) {
	report := func(n *model.Node, attr string) {
		p.report(finding{
			node: n, attribute: attr,
			message: fmt.Sprintf("%s doesn't spread across availability zones (%s)", addr(n), attr),
			why:     "Instances in one zone (or with no zone) all go down in a zone outage; spread across zones the workload keeps serving.",
			fix:     fmt.Sprintf("Set %s = [\"1\", \"2\", \"3\"] in a region with availability zones (zone_balance = true for scale sets).", lastSegment(attr)),
		})
	}
	single := func(b *model.Body) bool {
		if getAttr(b, "zones") == nil {
			return true
		}
		count, known := listLen(b, "zones")
		return known && count < 2
	}
	for _, n := range p.resources() {
		switch n.Type {
		case "azurerm_linux_virtual_machine_scale_set", "azurerm_windows_virtual_machine_scale_set", "azurerm_orchestrated_virtual_machine_scale_set", "azurerm_kubernetes_cluster_node_pool":
			if single(&n.Body) {
				report(n, "zones")
			}
		case "azurerm_kubernetes_cluster":
			if pool := azNested(&n.Body, "default_node_pool"); pool != nil && single(pool) {
				report(n, "default_node_pool[0].zones")
			}
		}
	}
}

var azAutoscalingSKU = regexp.MustCompile(`^(S\d|P\d|I\d)`)

// azAutoscalingPlan: an App Service plan SKU that scales through autoscale
// settings (Standard, Premium, Isolated). Free, Shared and Basic plans don't
// autoscale, and Consumption, Flex Consumption, Elastic Premium and Workflow
// Standard plans scale on their own.
func azAutoscalingPlan(sku string) bool { return azAutoscalingSKU.MatchString(sku) }

func checkAzAutoscale(p *pass) {
	report := func(n *model.Node, attr, msg, fix string) {
		p.report(finding{
			node: n, attribute: attr, message: msg,
			why: "A fixed instance count is either too small for a traffic peak (slow responses, errors) or paid for while idle.",
			fix: fix,
		})
	}
	settingFix := func(n *model.Node) string {
		return fmt.Sprintf("Add azurerm_monitor_autoscale_setting with target_resource_id = %s.id and a profile with capacity { minimum = 2, default = 2, maximum = <n> } and CPU-based scale-out and scale-in rules.", addr(n))
	}
	autoscaled := func(n *model.Node) bool {
		return p.targeted(n, "", []string{"target_resource_id"}, "azurerm_monitor_autoscale_setting")
	}
	poolFix := "Set auto_scaling_enabled = true with min_count and max_count, so the cluster autoscaler adds nodes for pending pods and removes idle ones."
	for _, n := range p.resources() {
		switch n.Type {
		case "azurerm_service_plan":
			if sku, ok := attrString(&n.Body, "sku_name"); !ok || !azAutoscalingPlan(sku) || attrIsTrue(&n.Body, "premium_plan_auto_scale_enabled") || autoscaled(n) {
				continue
			}
			report(n, "sku_name", fmt.Sprintf("%s runs a fixed number of workers (no autoscale setting)", addr(n)), settingFix(n))
		case "azurerm_linux_virtual_machine_scale_set", "azurerm_windows_virtual_machine_scale_set", "azurerm_orchestrated_virtual_machine_scale_set":
			if autoscaled(n) {
				continue
			}
			report(n, "instances", fmt.Sprintf("%s runs a fixed number of instances (no autoscale setting)", addr(n)), settingFix(n))
		case "azurerm_kubernetes_cluster_node_pool":
			if missingOrFalse(&n.Body, "auto_scaling_enabled") {
				report(n, "auto_scaling_enabled", fmt.Sprintf("%s has a fixed node count", addr(n)), poolFix)
			}
		case "azurerm_kubernetes_cluster":
			if pool := azNested(&n.Body, "default_node_pool"); pool != nil && missingOrFalse(pool, "auto_scaling_enabled") {
				report(n, "default_node_pool[0].auto_scaling_enabled", fmt.Sprintf("%s has a default node pool with a fixed node count", addr(n)), poolFix)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Google Cloud

func checkGCPSQLHA(p *pass) {
	for _, n := range p.ofType("google_sql_database_instance") {
		settings, _ := gcpSQLSettings(n)
		if settings == nil || gcpSQLReplica(n) {
			continue
		}
		if t, ok := attrString(settings, "availability_type"); getAttr(settings, "availability_type") != nil && (!ok || t != "ZONAL") {
			continue
		}
		p.report(finding{
			node: n, attribute: "settings[0].availability_type",
			message: fmt.Sprintf("%s runs in a single zone (availability_type is ZONAL)", addr(n)),
			why:     "A zonal instance is down for as long as its zone is; a regional (HA) instance keeps a synchronous standby in another zone that takes over automatically.",
			fix:     "Set settings { availability_type = \"REGIONAL\" } (it needs automated backups, and binary logging on MySQL).",
		})
	}
}

func checkGCPGKERegional(p *pass) {
	for _, n := range p.ofType("google_container_cluster") {
		loc, _ := attrString(&n.Body, "location")
		if !gcpZone.MatchString(loc) {
			continue
		}
		p.report(finding{
			node: n, attribute: "location",
			message: fmt.Sprintf("%s is a zonal cluster (%s)", addr(n), loc),
			why:     "A zonal cluster has one control plane in one zone: a zone outage or a control plane upgrade stops deployments and autoscaling, and its nodes go down with the zone.",
			fix:     fmt.Sprintf("Set location to the region (%q) for a control plane replicated across three zones; node pools then spread across them.", loc[:strings.LastIndex(loc, "-")]),
		})
	}
}

func checkGCPGKEAutoscaling(p *pass) {
	fix := "Add autoscaling { min_node_count = 1, max_node_count = <n> } (or total_min_node_count/total_max_node_count), so the cluster autoscaler adds nodes for pending pods and removes idle ones."
	report := func(n *model.Node, attr, msg string) {
		p.report(finding{
			node: n, attribute: attr, message: msg,
			why: "A fixed-size node pool can't fit new pods during a peak (they stay pending) and pays for idle nodes the rest of the time.",
			fix: fix,
		})
	}
	fixed := func(b *model.Body) bool { return len(blocks(b, "autoscaling")) == 0 && !azHasDynamic(b, "autoscaling") }
	for _, n := range p.ofType("google_container_node_pool") {
		if fixed(&n.Body) {
			report(n, "autoscaling", fmt.Sprintf("%s has a fixed node count (no autoscaling block)", addr(n)))
		}
	}
	for _, n := range p.ofType("google_container_cluster") {
		if attrIsTrue(&n.Body, "enable_autopilot") {
			continue
		}
		for i, pool := range blocks(&n.Body, "node_pool") {
			if fixed(&pool.Body) {
				report(n, fmt.Sprintf("node_pool[%d].autoscaling", i), fmt.Sprintf("%s has a node pool with a fixed node count (node_pool[%d])", addr(n), i))
			}
		}
	}
}

func checkGCPMIGAutoscaling(p *pass) {
	autoscalers := map[string]string{
		"google_compute_instance_group_manager":        "google_compute_autoscaler",
		"google_compute_region_instance_group_manager": "google_compute_region_autoscaler",
	}
	for _, n := range p.resources() {
		as, ok := autoscalers[n.Type]
		if !ok || p.targeted(n, nameOf(n, "name"), []string{"target"}, as) {
			continue
		}
		p.report(finding{
			node: n, attribute: "target_size",
			message: fmt.Sprintf("%s keeps a fixed number of instances (no autoscaler)", addr(n)),
			why:     "A fixed group is either too small for a traffic peak (slow responses, errors) or paid for while idle.",
			fix:     fmt.Sprintf("Add a %s with target = %s.id and autoscaling_policy { min_num_replicas = 2, max_replicas = <n>, cpu_utilization { target = 0.6 } }, and drop target_size from the group.", as, addr(n)),
		})
	}
}

func checkGCPCloudRunMaxInstances(p *pass) {
	capped := func(b *model.Body) bool {
		s := azNested(b, "scaling")
		return s != nil && getAttr(s, "max_instance_count") != nil
	}
	for _, n := range p.ofType("google_cloud_run_v2_service") {
		tpl := azNested(&n.Body, "template")
		if capped(&n.Body) || tpl == nil || capped(tpl) {
			continue
		}
		p.report(finding{
			node: n, attribute: "template[0].scaling[0].max_instance_count",
			message: fmt.Sprintf("%s doesn't cap its instance count", addr(n)),
			why:     "Cloud Run scales out to 100 instances by default: a traffic spike, a retry storm or an attack multiplies the bill and can exhaust the connections of the database behind it.",
			fix:     "Set template { scaling { max_instance_count = <n> } } to what the service and its dependencies can handle.",
		})
	}
}

// ---------------------------------------------------------------------------
// OCI

func checkOCIDBBackups(p *pass) {
	why := "Without automatic backups a deleted or corrupted database can't be restored."
	for _, n := range p.ofType("oci_database_db_system") {
		db := azNested(&n.Body, "db_home", "database")
		if db == nil {
			continue
		}
		if cfg := azNested(db, "db_backup_config"); cfg != nil && attrNotFalse(cfg, "auto_backup_enabled") {
			continue
		}
		p.report(finding{
			node: n, attribute: "db_home[0].database[0].db_backup_config[0].auto_backup_enabled",
			message: fmt.Sprintf("%s has no automatic database backups", addr(n)),
			why:     why,
			fix:     "Add db_backup_config { auto_backup_enabled = true, recovery_window_in_days = 30 } to db_home.database.",
		})
	}
	for _, n := range p.ofType("oci_mysql_mysql_db_system") {
		if bp := azNested(&n.Body, "backup_policy"); bp == nil || !hasLiteralFalse(bp, "is_enabled") {
			continue
		}
		p.report(finding{
			node: n, attribute: "backup_policy[0].is_enabled",
			message: fmt.Sprintf("%s turns automatic backups off", addr(n)),
			why:     why,
			fix:     "Set backup_policy { is_enabled = true, retention_in_days = 7 } (or more).",
		})
	}
}

func checkOCIInstancePoolAutoscaling(p *pass) {
	for _, n := range p.ofType("oci_core_instance_pool") {
		if p.targeted(n, "", []string{"auto_scaling_resources.id"}, "oci_autoscaling_auto_scaling_configuration") {
			continue
		}
		p.report(finding{
			node: n, attribute: "size",
			message: fmt.Sprintf("%s keeps a fixed size (no autoscaling configuration)", addr(n)),
			why:     "A fixed pool is either too small for a traffic peak (slow responses, errors) or paid for while idle.",
			fix:     fmt.Sprintf("Add oci_autoscaling_auto_scaling_configuration with auto_scaling_resources { id = %s.id, type = \"instancePool\" } and a threshold policy on CPU (min, initial and max capacity).", addr(n)),
		})
	}
}

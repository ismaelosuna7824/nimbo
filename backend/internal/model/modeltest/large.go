// Package modeltest builds realistic workflows for tests and benchmarks.
package modeltest

import (
	"fmt"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// CellNodes is how many nodes one network cell of LargeWorkflow holds.
const CellNodes = 10

// LargeWorkflow returns an AWS workflow of about nodes nodes (rounded up to
// whole cells of CellNodes). Every cell is a VPC with two subnets, a
// security group, an instance, a bucket, an IAM role, a Lambda in the VPC,
// an SQS queue and its event source mapping, wired by 12 references (about
// 1.2 edges per node). Values carry only Expr, as clients may send them;
// hclcodec.Normalize derives literals and edges.
func LargeWorkflow(nodes int) *model.Workflow {
	wf := &model.Workflow{
		ID:   "bench",
		Name: "Bench",
		Variables: []model.NamedBlock{
			{Name: "env", Body: body(attrs("type", "string", "default", `"dev"`))},
			{Name: "ami", Body: body(attrs("type", "string", "default", `"ami-0123456789abcdef0"`))},
		},
		Providers: []model.NamedBlock{{Name: "aws", Body: body(attrs("region", `"us-east-1"`))}},
		Terraform: &model.Body{Blocks: []model.Block{{
			Type: "required_providers",
			Body: body(attrs("aws", `{ source = "hashicorp/aws", version = "~> 5.0" }`)),
		}}},
	}
	for c := 0; c*CellNodes < nodes; c++ {
		wf.Nodes = append(wf.Nodes, cell(c)...)
	}
	return wf
}

func cell(c int) []model.Node {
	n := func(typ, name string) string { return fmt.Sprintf("%s.%s%d", typ, name, c) }
	vpc, subA, subB := n("aws_vpc", "main"), n("aws_subnet", "a"), n("aws_subnet", "b")
	sg, role, bucket := n("aws_security_group", "app"), n("aws_iam_role", "fn"), n("aws_s3_bucket", "data")
	fn, queue := n("aws_lambda_function", "worker"), n("aws_sqs_queue", "jobs")
	tags := fmt.Sprintf(`{ Name = "cell-%d", Env = var.env, Team = "platform" }`, c)
	x := float64(c%10) * 1600
	y := float64(c/10) * 900

	node := func(typ, name string, i int, b model.Body) model.Node {
		return model.Node{
			Kind:       model.KindResource,
			Type:       typ,
			Name:       fmt.Sprintf("%s%d", name, c),
			Position:   model.Position{X: x + float64(i%5)*300, Y: y + float64(i/5)*400},
			Positioned: true,
			Body:       b,
		}
	}
	sgBody := body(attrs(
		"name", fmt.Sprintf(`"app-%d"`, c),
		"description", `"Application traffic"`,
		"vpc_id", vpc+".id",
		"tags", tags,
	))
	sgBody.Blocks = []model.Block{
		{Type: "ingress", Body: body(attrs("from_port", "443", "to_port", "443", "protocol", `"tcp"`, "cidr_blocks", `["0.0.0.0/0"]`))},
		{Type: "egress", Body: body(attrs("from_port", "0", "to_port", "0", "protocol", `"-1"`, "cidr_blocks", `["0.0.0.0/0"]`))},
	}
	fnBody := body(attrs(
		"function_name", fmt.Sprintf(`"worker-%d-${var.env}"`, c),
		"role", role+".arn",
		"handler", `"index.handler"`,
		"runtime", `"nodejs20.x"`,
		"filename", `"lambda.zip"`,
		"memory_size", "256",
		"timeout", "30",
		"tags", tags,
	))
	fnBody.Blocks = []model.Block{
		{Type: "environment", Body: body(attrs("variables", fmt.Sprintf(`{ BUCKET = %s.bucket, STAGE = var.env }`, bucket)))},
		{Type: "vpc_config", Body: body(attrs(
			"subnet_ids", fmt.Sprintf("[%s.id, %s.id]", subA, subB),
			"security_group_ids", fmt.Sprintf("[%s.id]", sg),
		))},
	}
	return []model.Node{
		node("aws_vpc", "main", 0, body(attrs(
			"cidr_block", fmt.Sprintf(`"10.%d.0.0/16"`, c%256),
			"enable_dns_hostnames", "true",
			"enable_dns_support", "true",
			"tags", tags,
		))),
		node("aws_subnet", "a", 1, body(attrs(
			"vpc_id", vpc+".id",
			"cidr_block", fmt.Sprintf(`"10.%d.1.0/24"`, c%256),
			"availability_zone", `"us-east-1a"`,
			"map_public_ip_on_launch", "false",
			"tags", tags,
		))),
		node("aws_subnet", "b", 2, body(attrs(
			"vpc_id", vpc+".id",
			"cidr_block", fmt.Sprintf(`"10.%d.2.0/24"`, c%256),
			"availability_zone", `"us-east-1b"`,
			"tags", tags,
		))),
		node("aws_security_group", "app", 3, sgBody),
		node("aws_instance", "web", 4, body(attrs(
			"ami", "var.ami",
			"instance_type", `"t3.micro"`,
			"subnet_id", subA+".id",
			"vpc_security_group_ids", fmt.Sprintf("[%s.id]", sg),
			"user_data", `<<-EOT
#!/bin/bash
echo "hello from ${var.env}" > /tmp/hello
EOT`,
			"tags", tags,
		))),
		node("aws_s3_bucket", "data", 5, body(attrs(
			"bucket", fmt.Sprintf(`"nimbo-data-%d-${var.env}"`, c),
			"force_destroy", "true",
			"tags", tags,
		))),
		node("aws_iam_role", "fn", 6, body(attrs(
			"name", fmt.Sprintf(`"worker-role-%d"`, c),
			"assume_role_policy", `jsonencode({
  Version = "2012-10-17"
  Statement = [{ Action = "sts:AssumeRole", Effect = "Allow", Principal = { Service = "lambda.amazonaws.com" } }]
})`,
			"tags", tags,
		))),
		node("aws_lambda_function", "worker", 7, fnBody),
		node("aws_sqs_queue", "jobs", 8, body(attrs(
			"name", fmt.Sprintf(`"jobs-%d"`, c),
			"visibility_timeout_seconds", "60",
			"message_retention_seconds", "86400",
			"tags", tags,
		))),
		node("aws_lambda_event_source_mapping", "jobs", 9, body(attrs(
			"event_source_arn", queue+".arn",
			"function_name", fn+".arn",
			"batch_size", "10",
		))),
	}
}

func body(a []model.Attr) model.Body { return model.Body{Attrs: a} }

// attrs builds attributes from name, expression pairs.
func attrs(kv ...string) []model.Attr {
	out := make([]model.Attr, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, model.Attr{Name: kv[i], Value: model.Value{Expr: kv[i+1]}})
	}
	return out
}

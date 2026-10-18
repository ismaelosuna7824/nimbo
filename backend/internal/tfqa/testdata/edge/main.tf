# ---------------------------------------------------------------------------
# Queues

locals {
  name = "edge-${var.stage}" # interpolation

  # for expressions
  queue_names = [for k, q in var.queues : "${local.name}-${k}" if !q.fifo]
  by_delay    = { for k, q in var.queues : k => q.delay... }

  # provider-defined function (Terraform 1.8+)
  role_arn   = provider::aws::arn_parse("arn:aws:iam::444455556666:role/example")
  greeting   = "Hola, señor — ¿qué tal? 日本語 🚀"
  tabbed     = "a\tb\\tc"
  percent    = "100%%{literal} and $${literal}"
  conditional = var.stage == "prod" ? 3 : 1
}

resource "aws_sqs_queue" "queues" {
  for_each = var.queues

  name                       = "${local.name}-${each.key}${each.value.fifo ? ".fifo" : ""}"
  fifo_queue                 = each.value.fifo
  delay_seconds              = each.value.delay
  visibility_timeout_seconds = 30 /* inline block comment */

  tags = {
    "ñandú"  = "sí"
    Comment  = coalesce(each.value.comment, "none")
    "a.b/c"  = "quoted key"
  }

  lifecycle {
    create_before_destroy = true
    ignore_changes        = [tags["Comment"], delay_seconds]
    replace_triggered_by  = [terraform_data.revision]

    precondition {
      condition     = length(each.key) > 0
      error_message = "Queue keys must not be empty."
    }

    postcondition {
      condition     = self.arn != ""
      error_message = "The queue has no ARN."
    }
  }
}

resource "aws_sqs_queue" "imported" {
  for_each = var.existing_queues
  name     = each.key
}

resource "aws_sqs_queue" "replica" {
  provider = aws.west
  count    = local.conditional
  name     = "${local.name}-replica-${count.index}"

  # Commented-out settings stay in the export:
  # kms_master_key_id = "alias/aws/sqs"
  # sqs_managed_sse_enabled = false
}

# ---------------------------------------------------------------------------
# Security group with dynamic blocks

resource "aws_security_group" "web" {
  name        = "${local.name}-web"
  description = "Web — ingress ${join(", ", [for r in var.ingress : r.port])}"

  dynamic "ingress" {
    for_each = var.ingress
    iterator = rule
    content {
      from_port   = rule.value.port
      to_port     = rule.value.port
      protocol    = "tcp"
      cidr_blocks = [rule.value.cidr]
    }
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# ---------------------------------------------------------------------------
# Lambda from a local source directory

data "archive_file" "handler" {
  type        = "zip"
  source_dir  = "${path.module}/lambda/src"
  output_path = "${path.module}/build/handler.zip"
}

data "aws_iam_policy_document" "assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "handler" {
  name               = "${local.name}-handler"
  assume_role_policy = data.aws_iam_policy_document.assume.json
}

resource "aws_iam_role_policy" "handler" {
  name   = "inline"
  role   = aws_iam_role.handler.id
  policy = file("${path.module}/files/policy.json")
}

resource "aws_lambda_function" "handler" {
  function_name    = "${local.name}-handler"
  role             = aws_iam_role.handler.arn
  runtime          = "nodejs20.x"
  handler          = "index.handler"
  filename         = data.archive_file.handler.output_path
  source_code_hash = data.archive_file.handler.output_base64sha256

  environment {
    variables = {
      QUEUE_URLS = jsonencode(values(aws_sqs_queue.queues)[*].url)
      FIRST_ARN  = aws_sqs_queue.replica[0].arn
      ALL_ARNS   = join(",", aws_sqs_queue.replica.*.arn)
      MOTD       = <<EOT
Welcome to ${local.name}!
%{if var.stage == "prod"~}
Production: be careful.
%{else~}
Stage: ${var.stage}
%{endif~}
%{for q in local.queue_names~}
- ${q}
%{endfor~}
EOT
    }
  }
}

resource "aws_lambda_function" "prebuilt" {
  function_name    = "${local.name}-prebuilt"
  role             = aws_iam_role.handler.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  filename         = "${path.module}/artifacts/bootstrap.zip"
  source_code_hash = filebase64sha256("${path.module}/artifacts/bootstrap.zip")
}

# ---------------------------------------------------------------------------
# Instances with provisioners

data "aws_ami" "al2023" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-*-x86_64"]
  }
}

resource "aws_instance" "bastion" {
  ami           = data.aws_ami.al2023.id
  instance_type = "t3.micro"
  user_data = templatefile("${path.module}/templates/user_data.sh.tftpl", {
    stage  = var.stage
    queues = local.queue_names
  })
  vpc_security_group_ids = [aws_security_group.web.id]

  connection {
    type        = "ssh"
    user        = "ec2-user"
    host        = self.public_ip
    private_key = file("${path.module}/files/id_ed25519.example")
  }

  provisioner "file" {
    source      = "${path.module}/files/motd"
    destination = "/tmp/motd"
  }

  provisioner "remote-exec" {
    inline = [
      "sudo cp /tmp/motd /etc/motd",
      "echo '${local.greeting}'",
    ]
    on_failure = continue
  }

  provisioner "local-exec" {
    when    = destroy
    command = "echo destroying ${self.id}"
  }
}

resource "null_resource" "notify" {
  triggers = {
    queues = join(",", local.queue_names)
  }

  provisioner "local-exec" {
    interpreter = ["/bin/sh", "-c"]
    command     = <<-EOT
      echo "queues changed"
      echo "${join(" ", local.queue_names)}"
    EOT
    environment = {
      STAGE = var.stage
    }
  }

  depends_on = [aws_sqs_queue.queues]
}

resource "terraform_data" "revision" {
  input            = var.stage
  triggers_replace = [local.name, sha1(jsonencode(var.queues))]
}

# ---------------------------------------------------------------------------
# Child module with a provider mapping

module "app" {
  source = "./modules/app"

  name   = local.name
  queues = { for k, q in aws_sqs_queue.queues : k => q.arn }

  providers = {
    aws        = aws
    aws.replica = aws.west
  }

  depends_on = [terraform_data.revision]
}

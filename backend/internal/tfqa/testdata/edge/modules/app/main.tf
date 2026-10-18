# Module copied verbatim by the import; its nested local module call is
# rewritten to the flattened modules/ layout.
module "labels" {
  source = "./modules/labels"
  name   = var.name
}

resource "aws_sns_topic" "events" {
  name   = module.labels.id
  policy = templatefile("${path.module}/templates/topic-policy.json.tftpl", { queues = values(var.queues) })
}

resource "aws_sns_topic" "replica" {
  provider = aws.replica
  name     = "${module.labels.id}-replica"
}

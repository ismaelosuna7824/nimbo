locals {
  name_prefix = "${var.project}-${var.environment}"
  common_tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "nimbo"
  }

  # The services behind the gateway: each gets GET /<name>.
  services = {
    orders = [{ id = "o-1001", item = "keyboard", total = 49.9 }, { id = "o-1002", item = "monitor", total = 189 }]
    users  = [{ id = "u-1", name = "Ada" }, { id = "u-2", name = "Linus" }]
  }

  # Every function the API invokes, for the invoke permissions.
  invoked_functions = merge(
    { for name, fn in aws_lambda_function.service : name => fn.function_name },
    { authorizer = aws_lambda_function.authorizer.function_name },
  )
}

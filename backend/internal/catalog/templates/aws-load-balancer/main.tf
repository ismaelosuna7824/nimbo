resource "aws_lb" "app" {
  name                       = local.name_prefix
  load_balancer_type         = "application"
  internal                   = false
  security_groups            = [aws_security_group.alb.id]
  subnets                    = local.subnet_ids
  drop_invalid_header_fields = true
}

# Requests go only to targets that answer /health with 200.
resource "aws_lb_target_group" "app" {
  name                 = local.name_prefix
  port                 = 8080
  protocol             = "HTTP"
  target_type          = "instance"
  vpc_id               = data.aws_vpc.this.id
  deregistration_delay = 30

  health_check {
    path                = "/health"
    matcher             = "200"
    interval            = 10
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 2
  }
}

# HTTP serves the app, or redirects to HTTPS when a certificate is given.
resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.app.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = local.https ? "redirect" : "forward"
    target_group_arn = local.https ? null : aws_lb_target_group.app.arn

    dynamic "redirect" {
      for_each = local.https ? [1] : []
      content {
        port        = "443"
        protocol    = "HTTPS"
        status_code = "HTTP_301"
      }
    }
  }
}

resource "aws_lb_listener" "https" {
  count             = local.https ? 1 : 0
  load_balancer_arn = aws_lb.app.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}

# A listener rule the load balancer answers itself: /admin/* never reaches
# the servers.
resource "aws_lb_listener_rule" "block_admin" {
  listener_arn = local.https ? aws_lb_listener.https[0].arn : aws_lb_listener.http.arn
  priority     = 10

  action {
    type = "fixed-response"

    fixed_response {
      content_type = "application/json"
      message_body = jsonencode({ message = "Forbidden" })
      status_code  = "403"
    }
  }

  condition {
    path_pattern {
      values = ["/admin/*"]
    }
  }
}

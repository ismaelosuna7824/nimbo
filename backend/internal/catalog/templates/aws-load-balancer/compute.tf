# Amazon Linux 2023, the latest image AWS publishes for the region.
data "aws_ssm_parameter" "al2023" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}

# Identical app servers: a tiny Python HTTP service on port 8080 that
# answers with the instance's host name. Root volumes follow the account's
# EBS encryption-by-default setting.
resource "aws_launch_template" "app" {
  name_prefix            = "${local.name_prefix}-app-"
  image_id               = data.aws_ssm_parameter.al2023.value
  instance_type          = var.instance_type
  vpc_security_group_ids = [aws_security_group.app.id]

  metadata_options {
    http_endpoint = "enabled"
    http_tokens   = "required"
  }

  tag_specifications {
    resource_type = "instance"
    tags          = merge(local.common_tags, { Name = "${local.name_prefix}-app" })
  }

  user_data = base64encode(<<-EOT
    #!/bin/bash
    set -euo pipefail
    mkdir -p /opt/app
    cat > /opt/app/server.py <<'PY'
    import json, socket
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            body = {"status": "ok"} if self.path == "/health" else {"server": socket.gethostname(), "path": self.path}
            payload = json.dumps(body).encode()
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
    PY
    cat > /etc/systemd/system/app.service <<'UNIT'
    [Unit]
    Description=App server
    After=network-online.target

    [Service]
    ExecStart=/usr/bin/python3 /opt/app/server.py
    Restart=always
    DynamicUser=yes

    [Install]
    WantedBy=multi-user.target
    UNIT
    # Without systemd (the local sandbox) the server runs in the background.
    if ! systemctl enable --now app.service 2>/dev/null; then
      nohup python3 /opt/app/server.py >/var/log/app.log 2>&1 &
    fi
  EOT
  )
}

# Keeps instance_count servers in the app subnets, registers them with the
# target group and replaces any the load balancer reports unhealthy. Only
# the load balancer's security group reaches them.
resource "aws_autoscaling_group" "app" {
  name                      = local.name_prefix
  min_size                  = var.instance_count
  max_size                  = var.instance_count * 2
  desired_capacity          = var.instance_count
  vpc_zone_identifier       = local.app_subnet_ids
  target_group_arns         = [aws_lb_target_group.app.arn]
  health_check_type         = "ELB"
  health_check_grace_period = 120

  launch_template {
    id      = aws_launch_template.app.id
    version = aws_launch_template.app.latest_version
  }
}

# Amazon Linux 2023, the latest image AWS publishes for the region.
data "aws_ssm_parameter" "al2023" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}

resource "aws_security_group" "proxy" {
  name        = "${local.name_prefix}-proxy"
  description = "Reverse proxy: HTTP from anywhere"
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "HTTP"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description = "Package downloads and the app servers"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "app" {
  name        = "${local.name_prefix}-app"
  description = "App servers: port 8080 from the proxy only"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "App traffic from the proxy"
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.proxy.id]
  }
}

# Two internal app servers (a tiny Python HTTP service that answers with
# its name and the path it received).
resource "aws_instance" "app" {
  for_each               = local.apps
  ami                    = data.aws_ssm_parameter.al2023.value
  instance_type          = var.instance_type
  subnet_id              = aws_subnet.private.id
  vpc_security_group_ids = [aws_security_group.app.id]
  tags                   = { Name = "${local.name_prefix}-${each.key}" }

  metadata_options {
    http_tokens = "required"
  }

  user_data = <<-EOT
    #!/bin/bash
    set -euo pipefail
    mkdir -p /opt/app
    cat > /opt/app/server.py <<'PY'
    import json, socket
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            body = json.dumps({"server": "${each.key}", "host": socket.gethostname(), "path": self.path}).encode()
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
    PY
    cat > /etc/systemd/system/app.service <<'UNIT'
    [Unit]
    Description=${each.key} app server
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
}

# NGINX reverse proxy: the single public entry; clients never see the app
# servers' addresses.
resource "aws_instance" "proxy" {
  ami                    = data.aws_ssm_parameter.al2023.value
  instance_type          = var.instance_type
  subnet_id              = aws_subnet.public.id
  vpc_security_group_ids = [aws_security_group.proxy.id]
  tags                   = { Name = "${local.name_prefix}-proxy" }

  metadata_options {
    http_tokens = "required"
  }

  user_data_replace_on_change = true
  user_data                   = <<-EOT
    #!/bin/bash
    set -euo pipefail
    dnf install -y nginx
    # Listen on the instance's own address (works wherever a metadata proxy
    # holds 169.254.169.254:80, as in the local sandbox).
    token=$(curl -sf -X PUT http://169.254.169.254/latest/api/token -H "X-aws-ec2-metadata-token-ttl-seconds: 60")
    ip=$(curl -sf -H "X-aws-ec2-metadata-token: $token" http://169.254.169.254/latest/meta-data/local-ipv4)
    cat > /etc/nginx/nginx.conf <<'CONF'
    user nginx;
    worker_processes auto;
    error_log /var/log/nginx/error.log;
    pid /run/nginx.pid;

    events {
      worker_connections 1024;
    }

    http {
      server_tokens off;
      access_log /var/log/nginx/access.log;

      upstream web {
        server ${aws_instance.app["web"].private_ip}:8080;
      }

      upstream api {
        server ${aws_instance.app["api"].private_ip}:8080;
      }

      server {
        listen LISTEN_IP:80 default_server;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        location = /healthz {
          return 200 "ok";
        }

        location /api/ {
          proxy_pass http://api;
        }

        location / {
          proxy_pass http://web;
        }
      }
    }
    CONF
    sed -i "s/LISTEN_IP/$ip/" /etc/nginx/nginx.conf
    nginx -t
    if ! systemctl enable --now nginx 2>/dev/null; then
      nginx
    fi
  EOT
}

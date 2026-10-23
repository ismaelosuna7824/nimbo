# Oracle Linux 9, the newest image for the shape.
data "oci_core_images" "ol9" {
  compartment_id           = var.compartment_ocid
  operating_system         = "Oracle Linux"
  operating_system_version = "9"
  shape                    = var.instance_shape
  sort_by                  = "TIMECREATED"
  sort_order               = "DESC"
}

# The internal servers: a tiny Python HTTP service on port 8080 that
# answers with its name and the path it received.
resource "oci_core_instance" "app" {
  for_each            = local.apps
  compartment_id      = var.compartment_ocid
  availability_domain = data.oci_identity_availability_domains.ads.availability_domains[0].name
  display_name        = "${local.name_prefix}-${each.key}"
  shape               = var.instance_shape
  freeform_tags       = local.common_tags

  shape_config {
    ocpus         = var.instance_ocpus
    memory_in_gbs = var.instance_memory_gbs
  }

  source_details {
    source_type = "image"
    source_id   = data.oci_core_images.ol9.images[0].id
  }

  create_vnic_details {
    subnet_id        = oci_core_subnet.tier["private"].id
    assign_public_ip = false
  }

  metadata = {
    user_data = base64encode(<<-EOT
      #!/bin/bash
      cat > /opt/app.py <<'PY'
      import json, socket
      from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

      class Handler(BaseHTTPRequestHandler):
          def do_GET(self):
              body = {"status": "ok"} if self.path == "/health" else {"server": "${each.key}", "host": socket.gethostname(), "path": self.path}
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
      ExecStart=/usr/bin/python3 /opt/app.py
      Restart=always
      DynamicUser=yes

      [Install]
      WantedBy=multi-user.target
      UNIT
      firewall-cmd --permanent --add-port=8080/tcp
      firewall-cmd --reload
      systemctl enable --now app.service
    EOT
    )
  }
}

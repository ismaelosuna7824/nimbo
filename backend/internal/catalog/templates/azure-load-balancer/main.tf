resource "azurerm_resource_group" "main" {
  name     = "${local.name_prefix}-rg"
  location = var.location
  tags     = local.common_tags
}

# One public IP for every instance of the scale set.
resource "azurerm_lb" "main" {
  name                = "${local.name_prefix}-lb"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  sku                 = "Standard"
  tags                = local.common_tags

  frontend_ip_configuration {
    name                 = "public"
    public_ip_address_id = azurerm_public_ip.lb.id
  }
}

resource "azurerm_lb_backend_address_pool" "app" {
  name            = "app"
  loadbalancer_id = azurerm_lb.main.id
}

# Instances that stop answering /health with 200 get no new connections.
resource "azurerm_lb_probe" "http" {
  name                = "http-health"
  loadbalancer_id     = azurerm_lb.main.id
  protocol            = "Http"
  port                = 80
  request_path        = "/health"
  interval_in_seconds = 5
  number_of_probes    = 2
}

resource "azurerm_lb_rule" "http" {
  name                           = "http"
  loadbalancer_id                = azurerm_lb.main.id
  protocol                       = "Tcp"
  frontend_port                  = 80
  backend_port                   = 80
  frontend_ip_configuration_name = "public"
  backend_address_pool_ids       = [azurerm_lb_backend_address_pool.app.id]
  probe_id                       = azurerm_lb_probe.http.id
  disable_outbound_snat          = true
  idle_timeout_in_minutes        = 4
}

# Identical VMs; the scale set replaces instances the probe reports
# unhealthy.
resource "azurerm_linux_virtual_machine_scale_set" "app" {
  name                            = "${local.name_prefix}-vmss"
  resource_group_name             = azurerm_resource_group.main.name
  location                        = azurerm_resource_group.main.location
  sku                             = var.vm_size
  instances                       = var.instance_count
  admin_username                  = "azureuser"
  disable_password_authentication = true
  upgrade_mode                    = "Manual"
  health_probe_id                 = azurerm_lb_probe.http.id
  tags                            = local.common_tags

  admin_ssh_key {
    username   = "azureuser"
    public_key = var.admin_ssh_public_key
  }

  source_image_reference {
    publisher = "Canonical"
    offer     = "ubuntu-24_04-lts"
    sku       = "server"
    version   = "latest"
  }

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "Standard_LRS"
  }

  network_interface {
    name    = "app"
    primary = true

    ip_configuration {
      name                                   = "app"
      primary                                = true
      subnet_id                              = azurerm_subnet.app.id
      load_balancer_backend_address_pool_ids = [azurerm_lb_backend_address_pool.app.id]
    }
  }

  automatic_instance_repair {
    enabled      = true
    grace_period = "PT10M"
  }

  # A tiny Python HTTP service on port 80 that answers with the VM's name.
  custom_data = base64encode(<<-EOT
    #!/bin/bash
    cat > /opt/app.py <<'PY'
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

    ThreadingHTTPServer(("0.0.0.0", 80), Handler).serve_forever()
    PY
    cat > /etc/systemd/system/app.service <<'UNIT'
    [Unit]
    Description=App server
    After=network-online.target

    [Service]
    ExecStart=/usr/bin/python3 /opt/app.py
    Restart=always
    AmbientCapabilities=CAP_NET_BIND_SERVICE
    DynamicUser=yes

    [Install]
    WantedBy=multi-user.target
    UNIT
    systemctl enable --now app.service
  EOT
  )
}

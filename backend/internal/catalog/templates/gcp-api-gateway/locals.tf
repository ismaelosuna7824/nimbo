locals {
  name_prefix = "${var.project}-${var.environment}"
  # GCP label keys and values must be lowercase.
  common_labels = {
    project     = var.project
    environment = var.environment
    managed_by  = "nimbo"
  }

  # The backend services: the proxy routes /v1/<name>/** to each.
  services = toset(["orders", "users"])

  # The Apigee proxy bundle: API key check, routing, one target per service
  # called with a Google-signed identity token.
  proxy_files = merge(
    {
      "apiproxy/${local.name_prefix}.xml"    = <<-XML
        <APIProxy name="${local.name_prefix}">
          <Description>Single entry for the orders and users services.</Description>
        </APIProxy>
      XML
      "apiproxy/policies/verify-api-key.xml" = <<-XML
        <VerifyAPIKey name="verify-api-key">
          <APIKey ref="request.header.x-api-key"/>
        </VerifyAPIKey>
      XML
      "apiproxy/policies/remove-api-key.xml" = <<-XML
        <AssignMessage name="remove-api-key">
          <AssignTo createNew="false" transport="http" type="request"/>
          <Remove>
            <Headers>
              <Header name="x-api-key"/>
            </Headers>
          </Remove>
        </AssignMessage>
      XML
      "apiproxy/policies/not-found.xml"      = <<-XML
        <RaiseFault name="not-found">
          <FaultResponse>
            <Set>
              <StatusCode>404</StatusCode>
              <Payload contentType="application/json">{"message": "Not found"}</Payload>
            </Set>
          </FaultResponse>
        </RaiseFault>
      XML
      "apiproxy/proxies/default.xml"         = <<-XML
        <ProxyEndpoint name="default">
          <PreFlow name="PreFlow">
            <Request>
              <Step><Name>verify-api-key</Name></Step>
              <Step><Name>remove-api-key</Name></Step>
            </Request>
          </PreFlow>
          <Flows>
            <Flow name="unknown-path">
              <Condition>${join(" and ", [for s in local.services : "!(proxy.pathsuffix MatchesPath \"/${s}/**\" or proxy.pathsuffix = \"/${s}\")"])}</Condition>
              <Request>
                <Step><Name>not-found</Name></Step>
              </Request>
            </Flow>
          </Flows>
          <HTTPProxyConnection>
            <BasePath>/v1</BasePath>
          </HTTPProxyConnection>
          %{~for s in local.services}
          <RouteRule name="${s}">
            <Condition>proxy.pathsuffix MatchesPath "/${s}/**" or proxy.pathsuffix = "/${s}"</Condition>
            <TargetEndpoint>${s}</TargetEndpoint>
          </RouteRule>
          %{~endfor}
        </ProxyEndpoint>
      XML
    },
    {
      for name, svc in google_cloud_run_v2_service.service : "apiproxy/targets/${name}.xml" => <<-XML
        <TargetEndpoint name="${name}">
          <HTTPTargetConnection>
            <Authentication>
              <GoogleIDToken>
                <Audience>${svc.uri}</Audience>
              </GoogleIDToken>
            </Authentication>
            <URL>${svc.uri}</URL>
          </HTTPTargetConnection>
        </TargetEndpoint>
      XML
    },
  )
}

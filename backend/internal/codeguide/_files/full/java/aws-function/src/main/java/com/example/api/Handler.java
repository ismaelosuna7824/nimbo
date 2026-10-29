package com.example.api;

import com.amazonaws.services.lambda.runtime.Context;
import com.amazonaws.services.lambda.runtime.RequestHandler;
import com.amazonaws.services.lambda.runtime.events.APIGatewayV2HTTPEvent;
import com.amazonaws.services.lambda.runtime.events.APIGatewayV2HTTPResponse;
import com.example.api.adapters.inbound.http.Routes;
import com.example.api.adapters.outbound.store.Repositories;
import com.example.api.application.Items;
import java.time.Clock;
import java.util.Map;
import java.util.UUID;

/** Composition root + Lambda driving adapter (API Gateway HTTP API, payload 2.0). */
public final class Handler implements RequestHandler<APIGatewayV2HTTPEvent, APIGatewayV2HTTPResponse> {
  // Built once per execution environment (init phase), reused across invocations.
  private static final Routes ROUTES =
      new Routes(new Items(Repositories.create(System.getenv()), () -> UUID.randomUUID().toString(), Clock.systemUTC()));

  @Override
  public APIGatewayV2HTTPResponse handleRequest(APIGatewayV2HTTPEvent event, Context context) {
    String method = event.getRequestContext().getHttp().getMethod();
    Routes.Response res = ROUTES.handle(method, event.getRawPath(), event.getBody());
    context.getLogger().log("{\"level\":\"info\",\"msg\":\"request\",\"requestId\":\"" + context.getAwsRequestId()
        + "\",\"status\":" + res.status() + "}\n");
    return APIGatewayV2HTTPResponse.builder()
        .withStatusCode(res.status())
        .withHeaders(Map.of("content-type", "application/json"))
        .withBody(res.body())
        .build();
  }
}

# Install the gems (cached until Gemfile/Gemfile.lock change) in a stage
# with compilers for native extensions, then copy them into a slim runtime
# image that runs as a non-root user.
FROM ruby:3.3-slim AS build
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential \
    && rm -rf /var/lib/apt/lists/*
ENV BUNDLE_DEPLOYMENT=1 BUNDLE_WITHOUT=development:test BUNDLE_PATH=/usr/local/bundle
WORKDIR /app
COPY Gemfile Gemfile.lock ./
RUN bundle install --jobs 4 && rm -rf /usr/local/bundle/ruby/*/cache

FROM ruby:3.3-slim
ENV BUNDLE_DEPLOYMENT=1 BUNDLE_WITHOUT=development:test BUNDLE_PATH=/usr/local/bundle
RUN useradd --create-home --uid 10001 app
WORKDIR /app
COPY --from=build /usr/local/bundle /usr/local/bundle
COPY --chown=app:app . .
USER app
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
  CMD ["ruby", "-rnet/http", "-e", "exit Net::HTTP.get_response(URI('http://127.0.0.1:8080/')).is_a?(Net::HTTPSuccess)"]
# Replace with your server (e.g. bundle exec puma -b tcp://0.0.0.0:8080).
CMD ["bundle", "exec", "ruby", "app.rb"]

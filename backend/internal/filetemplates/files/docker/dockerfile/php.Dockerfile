# Install the Composer packages (cached until composer.json/composer.lock
# change) in a Composer stage, then serve the app with Apache + mod_php as
# www-data. Apache listens on 8080: ports below 1024 need root.
FROM composer:2.8 AS deps
WORKDIR /app
COPY composer.json composer.lock* ./
RUN --mount=type=cache,target=/tmp/cache \
    composer install --no-dev --no-interaction --no-progress --prefer-dist --no-scripts --no-autoloader
COPY . .
RUN composer dump-autoload --no-dev --optimize

FROM php:8.3-apache
RUN sed -i 's/^Listen 80$/Listen 8080/' /etc/apache2/ports.conf \
    && sed -i 's/<VirtualHost \*:80>/<VirtualHost *:8080>/' /etc/apache2/sites-available/000-default.conf
WORKDIR /var/www/html
COPY --from=deps --chown=www-data:www-data /app ./
USER www-data
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8080/ >/dev/null || exit 1
CMD ["apache2-foreground"]
